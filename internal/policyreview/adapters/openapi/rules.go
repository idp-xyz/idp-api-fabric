package openapi

import (
	"regexp"
	"strconv"
	"strings"
)

// supportedVersion is the one OpenAPI version the rules are written for
// (F04 §1.2).
const supportedVersion = "3.1.2"

// judgment is what a rule comes to on one source.
type judgment struct {
	findings []found
	// undecided says that the rule could not be judged in full: what it
	// found stands, and what it did not find proves nothing.
	undecided bool
}

// methods are the operations a Path Item Object holds (OpenAPI 3.1.2 §4.8.9).
var methods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// operation is one operation of the API the entrypoint describes.
type operation struct {
	node *node
	// webhook says that the operation is one the API calls, listed under
	// webhooks, and not one it serves under paths.
	webhook bool
}

// surface is the operations of the API, in the order they are written.
type surface struct {
	operations []operation
	// unclear says that something between the entrypoint and an operation
	// is not what OpenAPI says it is, so that operations may be missing
	// from the list. A rule about every operation is undecided then: an
	// operation that was not seen was not judged.
	unclear bool
}

// surface lists the operations under paths and under webhooks. A path item
// may be a reference, alone or beside operations of its own.
func (b *bundle) surface() surface {
	var listed surface
	for _, section := range []struct {
		key     string
		webhook bool
	}{{"paths", false}, {"webhooks", true}} {
		items := b.entrypoint().member(section.key)
		if items == nil {
			continue
		}
		if items.kind != mapping {
			listed.unclear = true
			continue
		}
		for _, name := range items.keys {
			// A key that begins with x- is an extension, not a path.
			if strings.HasPrefix(name, "x-") {
				continue
			}
			b.pathItem(items.members[name], section.webhook, 0, &listed)
		}
	}
	return listed
}

func (b *bundle) pathItem(item *node, webhook bool, hops int, listed *surface) {
	if item.kind != mapping || hops > maxReferenceDepth {
		listed.unclear = true
		return
	}
	if reference := item.members["$ref"]; reference != nil {
		value, _ := reference.text()
		if referenced, reached := b.target(item.file, value); reached == resolved {
			b.pathItem(referenced, webhook, hops+1, listed)
		} else {
			listed.unclear = true
		}
	}
	for _, key := range item.keys {
		if !methods[key] {
			continue
		}
		if described := item.members[key]; described.kind == mapping {
			listed.operations = append(listed.operations, operation{node: described, webhook: webhook})
		} else {
			listed.unclear = true
		}
	}
}

// F04-GOV-001: the entrypoint declares the one supported OpenAPI version.
func (b *bundle) specVersion() judgment {
	entry := b.entrypoint()
	if version, isText := entry.member("openapi").text(); !isText || version != supportedVersion {
		return judgment{findings: []found{{
			under(entry, "openapi"),
			"the entrypoint does not declare OpenAPI " + supportedVersion + ", the one version that is checked; write openapi: " + supportedVersion,
		}}}
	}
	return judgment{}
}

// F04-GOV-002: the document names the API and its version.
func (b *bundle) infoRequired() judgment {
	entry := b.entrypoint()
	info := entry.member("info")
	if info == nil || info.kind != mapping {
		return judgment{findings: []found{{under(entry, "info"), "the entrypoint has no info object; give the API a title and a version there"}}}
	}
	var judged judgment
	for _, key := range []string{"title", "version"} {
		if value, isText := info.member(key).text(); !isText || strings.TrimSpace(value) == "" {
			judged.findings = append(judged.findings, found{
				under(info, key), "info." + key + " is missing or is not a text; write it as a quoted text that is not empty",
			})
		}
	}
	return judged
}

// F04-GOV-003: every operation under paths has an operationId.
func (b *bundle) operationIDRequired() judgment {
	listed := b.surface()
	judged := judgment{undecided: listed.unclear}
	for _, operation := range listed.operations {
		if operation.webhook {
			continue
		}
		if id, isText := operation.node.member("operationId").text(); !isText || strings.TrimSpace(id) == "" {
			judged.findings = append(judged.findings, found{
				under(operation.node, "operationId"),
				"the operation has no operationId; give it one, and keep it the same from revision to revision",
			})
		}
	}
	return judged
}

// F04-GOV-008: no two operations of the document share an operationId. The
// operations under webhooks count: OpenAPI asks for an operationId that is
// unique among all operations of the API.
func (b *bundle) operationIDUnique() judgment {
	listed := b.surface()
	judged := judgment{undecided: listed.unclear}
	first := make(map[string]*node)
	for _, operation := range listed.operations {
		id, isText := operation.node.member("operationId").text()
		if !isText || strings.TrimSpace(id) == "" {
			continue
		}
		if earlier, taken := first[id]; taken {
			judged.findings = append(judged.findings, found{
				under(operation.node, "operationId"),
				"the operationId is already used by the operation at line " + strconv.Itoa(at(earlier).line) + " of " + earlier.file +
					"; an operationId names one operation",
			})
			continue
		}
		first[id] = operation.node
	}
	return judged
}

// F04-GOV-006: a security requirement applies to every operation under
// paths, its own or the one of the document. A requirement that is an empty
// list applies none.
func (b *bundle) operationSecurity() judgment {
	listed := b.surface()
	judged := judgment{undecided: listed.unclear}
	global := b.entrypoint().member("security")
	for _, operation := range listed.operations {
		if operation.webhook {
			continue
		}
		effective := operation.node.member("security")
		if effective == nil {
			effective = global
		}
		if effective == nil || effective.kind != sequence || len(effective.items) == 0 {
			judged.findings = append(judged.findings, found{
				under(operation.node, "security"),
				"no security requirement applies to the operation; state one for it or one for the whole document",
			})
		}
	}
	return judged
}

var errorStatus = regexp.MustCompile(`^(default|[45][0-9]{2}|[45][Xx]{2})$`)

const problemMediaType = "application/problem+json"

// F04-GOV-007: the error responses of the operations under paths offer a
// problem document (RFC 9457). An error response is one under a 4xx or 5xx
// status, a 4XX or 5XX range, or default.
func (b *bundle) problemContent() judgment {
	listed := b.surface()
	judged := judgment{undecided: listed.unclear}
	for _, operation := range listed.operations {
		if operation.webhook {
			continue
		}
		responses := operation.node.member("responses")
		if responses == nil {
			continue
		}
		if responses.kind != mapping {
			judged.undecided = true
			continue
		}
		for _, status := range responses.keys {
			if !errorStatus.MatchString(status) {
				continue
			}
			declared := responses.members[status]
			response := b.follow(declared)
			if response == nil {
				judged.undecided = true
				continue
			}
			content := response.member("content")
			if content != nil && content.kind != mapping {
				judged.undecided = true
				continue
			}
			if !offersProblem(content) {
				judged.findings = append(judged.findings, found{
					at(declared), "the error response does not offer " + problemMediaType + " (RFC 9457); describe its body as a problem document",
				})
			}
		}
	}
	return judged
}

func offersProblem(content *node) bool {
	if content == nil {
		return false
	}
	for _, mediaType := range content.keys {
		name, _, _ := strings.Cut(mediaType, ";")
		if strings.EqualFold(strings.TrimSpace(name), problemMediaType) {
			return true
		}
	}
	return false
}

// F04-GOV-005: no reference leads outside the files of the source. What a
// reference names inside them is not this rule's business: a reference that
// stays inside and names nothing is a document that is not valid, not one
// that reaches out.
func (b *bundle) refSafety() judgment {
	var judged judgment
	b.walk(func(n *node) {
		value, isText := n.member("$ref").text()
		if !isText {
			return
		}
		if _, reached := b.target(n.file, value); reached == outside {
			judged.findings = append(judged.findings, found{
				at(n.members["$ref"]),
				"the reference leads outside the files of the source: to an address, an absolute path, a path above the bundle or a file that was not submitted",
			})
		}
	})
	return judged
}

// F04-GOV-009: every file is one document that reads one way.
func (b *bundle) parseSafety() judgment {
	return judgment{findings: b.malformed, undecided: b.overBudget}
}
