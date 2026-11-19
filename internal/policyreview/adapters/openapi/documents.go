package openapi

import (
	"encoding/json"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	yaml "github.com/oasdiff/yaml3"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// What the evaluator reads of one source. A source that Contract Registry
// recorded is well within these bounds: the registry holds a bundle to the
// same number of nodes and the same nesting before it records it. The
// bounds are here for whatever else may be handed to the evaluator.
const (
	maxNodes          = 8192
	maxNodeDepth      = 64
	maxReferenceDepth = 16
	maxReferenceBytes = 1024
)

type kind int

const (
	scalar kind = iota
	mapping
	sequence
)

// node is one value of a document, with the place it was read at.
type node struct {
	kind kind
	// tag is the short YAML tag of a scalar: !!str, !!int, !!bool, !!null,
	// !!float or !!timestamp.
	tag   string
	value string
	plain bool
	// line is the line the value begins on, and keyLine the line of its key
	// when it is the value of one. A mapping written as a block begins on
	// the line of its first key, which is the line after the key it is the
	// value of: whoever is sent to an operation looks for "get:".
	line    int
	keyLine int

	file   string
	parent *node
	// token is the reference token of the node under its parent: the key
	// of a mapping or the index of a sequence.
	token string

	keys    []string
	members map[string]*node
	items   []*node
}

// text returns the value of a scalar that is a string.
func (n *node) text() (string, bool) {
	if n == nil || n.kind != scalar || n.tag != "!!str" {
		return "", false
	}
	return n.value, true
}

// member returns the value of a key of a mapping, and nil for anything else.
func (n *node) member(key string) *node {
	if n == nil || n.kind != mapping {
		return nil
	}
	return n.members[key]
}

var escapePointer = strings.NewReplacer("~", "~0", "/", "~1")

// pointer is the JSON Pointer of the node in its file. It is empty for the
// root, and for a node whose pointer cannot be kept with a finding: a key
// may hold anything, a control character included, and a finding at such a
// place is known by its line.
func (n *node) pointer() string {
	var tokens []string
	for at := n; at.parent != nil; at = at.parent {
		tokens = append(tokens, escapePointer.Replace(at.token))
	}
	if len(tokens) == 0 {
		return ""
	}
	var built strings.Builder
	for i := len(tokens) - 1; i >= 0; i-- {
		built.WriteByte('/')
		built.WriteString(tokens[i])
	}
	if pointer := built.String(); domain.ValidPointer(pointer) {
		return pointer
	}
	return ""
}

// place is where something was found: a node of a document, or a line of a
// file when there is no node to name.
type place struct {
	file    string
	pointer string
	line    int
}

func at(n *node) place {
	where := place{file: n.file, pointer: n.pointer(), line: n.line}
	if n.keyLine > 0 {
		where.line = n.keyLine
	}
	return where
}

// under is the place of the member key of a mapping when it has that
// member, and of the mapping itself when it has not.
func under(n *node, key string) place {
	if found := n.member(key); found != nil {
		return at(found)
	}
	return at(n)
}

// found is a finding before it is given its rule.
type found struct {
	place   place
	message string
}

// bundle is a source as the evaluator read it.
type bundle struct {
	source domain.Source
	// documents holds the root of every file that is one well-formed
	// document.
	documents map[string]*node
	// malformed is what keeps a file from being such a document.
	malformed []found
	// overBudget says that the files hold more than the evaluator reads.
	overBudget bool
	nodes      int
}

// read parses the files of the source. It reads all of them: a rule about
// one file is not judged on a source that is half read.
func read(source domain.Source) *bundle {
	read := &bundle{source: source, documents: make(map[string]*node, len(source.Files))}
	for _, file := range source.Files {
		root, problems := read.document(file)
		read.malformed = append(read.malformed, problems...)
		if len(problems) == 0 && root != nil {
			read.documents[file.Path] = root
		}
	}
	return read
}

// readable reports whether every file was read in full. The rules that read
// the documents are judged only then.
func (b *bundle) readable() bool {
	return !b.overBudget && len(b.malformed) == 0 && len(b.documents) == len(b.source.Files) && b.entrypoint() != nil
}

// entrypoint is the root of the entrypoint when it is a mapping.
func (b *bundle) entrypoint() *node {
	if root := b.documents[b.source.Entrypoint]; root != nil && root.kind == mapping {
		return root
	}
	return nil
}

func (b *bundle) document(file domain.SourceFile) (*node, []found) {
	// What keeps a file from being parsed is a finding of the file, without
	// a line: the line a parser stops at is where it gave up, which is
	// often not where the mistake is, and a finding names a place only when
	// it is the place.
	whole := place{file: file.Path}
	if path.Ext(file.Path) == ".json" && !json.Valid([]byte(file.Content)) {
		return nil, []found{{whole, "the file is named as JSON and is not valid JSON"}}
	}
	decoder := yaml.NewDecoder(strings.NewReader(file.Content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || len(document.Content) != 1 {
		return nil, []found{{whole, "the file is not one well-formed YAML or JSON document"}}
	}
	if err := decoder.Decode(&yaml.Node{}); err != io.EOF {
		return nil, []found{{whole, "the file holds more than one document; a file of a contract holds one"}}
	}
	var problems []found
	root := b.convert(document.Content[0], file.Path, nil, "", 0, &problems)
	if file.Path == b.source.Entrypoint && root != nil && root.kind != mapping && len(problems) == 0 {
		problems = append(problems, found{at(root), "the entrypoint is not a mapping; an OpenAPI document is one"})
	}
	return root, problems
}

// convert copies a YAML node and what is under it, and notes what makes the
// document ambiguous: a key that is repeated or is no string, an alias, a
// tag that plain JSON has no value for. It stops when the budget is spent.
func (b *bundle) convert(from *yaml.Node, file string, parent *node, token string, depth int, problems *[]found) *node {
	b.nodes++
	if depth > maxNodeDepth || b.nodes > maxNodes {
		b.overBudget = true
		return nil
	}
	converted := &node{file: file, parent: parent, token: token, line: from.Line, plain: from.Style == 0}
	if from.Kind == yaml.AliasNode {
		*problems = append(*problems, found{at(converted), "the document uses a YAML alias; write the value out where it is used"})
		return converted
	}
	switch tag := from.ShortTag(); tag {
	case "!!map", "!!seq", "!!str", "!!bool", "!!null", "!!int", "!!float", "!!timestamp":
		converted.tag = tag
	default:
		*problems = append(*problems, found{at(converted), "the document uses a YAML tag that plain JSON has no value for"})
		return converted
	}
	switch from.Kind {
	case yaml.MappingNode:
		converted.kind = mapping
		converted.members = make(map[string]*node, len(from.Content)/2)
		for i := 0; i+1 < len(from.Content); i += 2 {
			key, value := from.Content[i], from.Content[i+1]
			switch {
			case key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str":
				*problems = append(*problems, found{
					place{file: file, pointer: converted.pointer(), line: key.Line},
					"a key of a mapping is not a string; quote it, as in '200', so that it is read one way",
				})
				continue
			case converted.members[key.Value] != nil:
				*problems = append(*problems, found{
					place{file: file, pointer: converted.pointer(), line: key.Line},
					"a key is repeated in a mapping, so that one of its two values is silently dropped",
				})
				continue
			}
			child := b.convert(value, file, converted, key.Value, depth+1, problems)
			if b.overBudget {
				return nil
			}
			child.keyLine = key.Line
			converted.keys = append(converted.keys, key.Value)
			converted.members[key.Value] = child
		}
	case yaml.SequenceNode:
		converted.kind = sequence
		for i, item := range from.Content {
			child := b.convert(item, file, converted, strconv.Itoa(i), depth+1, problems)
			if b.overBudget {
				return nil
			}
			converted.items = append(converted.items, child)
		}
	default:
		converted.kind = scalar
		converted.value = from.Value
	}
	return converted
}

// walk calls visit for every node of every document that was read, files in
// the order of the source and nodes in the order they are written.
func (b *bundle) walk(visit func(*node)) {
	var descend func(*node)
	descend = func(n *node) {
		visit(n)
		for _, key := range n.keys {
			descend(n.members[key])
		}
		for _, item := range n.items {
			descend(item)
		}
	}
	for _, file := range b.source.Files {
		if root := b.documents[file.Path]; root != nil {
			descend(root)
		}
	}
}

// reach is how far a reference gets.
type reach int

const (
	// resolved: the reference names a node of a file of the source.
	resolved reach = iota
	// dangling: the reference stays inside the source and names nothing
	// there.
	dangling
	// outside: the reference names something the source does not hold: an
	// address, an absolute path, a path above the bundle, or a file that
	// was not submitted.
	outside
)

var (
	invalidPointerEscape = regexp.MustCompile(`~([^01]|$)`)
	unescapePointer      = strings.NewReplacer("~1", "/", "~0", "~")
	sourcePath           = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)*$`)
)

// target resolves a reference written in file. It reads the files of the
// source and nothing else: no reference makes it open a file or an address.
func (b *bundle) target(file, reference string) (*node, reach) {
	if reference == "" || len(reference) > maxReferenceBytes {
		return nil, dangling
	}
	ref, err := url.Parse(reference)
	if err != nil || ref.Scheme != "" || ref.Opaque != "" || ref.Host != "" || ref.User != nil ||
		ref.RawQuery != "" || ref.ForceQuery || path.IsAbs(ref.Path) || strings.Contains(reference, `\`) {
		return nil, outside
	}
	name := file
	if ref.Path != "" {
		name = path.Join(path.Dir(file), ref.Path)
	}
	if !sourcePath.MatchString(name) || strings.HasPrefix(name, "../") || name == ".." {
		return nil, outside
	}
	current, held := b.documents[name]
	if !held {
		for _, submitted := range b.source.Files {
			if submitted.Path == name {
				// The file is there and could not be read.
				return nil, dangling
			}
		}
		return nil, outside
	}
	if ref.Fragment == "" {
		return current, resolved
	}
	pointer, isPointer := strings.CutPrefix(ref.Fragment, "/")
	if !isPointer || invalidPointerEscape.MatchString(pointer) {
		return nil, dangling
	}
	for _, segment := range strings.Split(pointer, "/") {
		token := unescapePointer.Replace(segment)
		switch current.kind {
		case mapping:
			current = current.members[token]
		case sequence:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(current.items) || strconv.Itoa(index) != token {
				return nil, dangling
			}
			current = current.items[index]
		default:
			return nil, dangling
		}
		if current == nil {
			return nil, dangling
		}
	}
	return current, resolved
}

// follow returns the object n stands for: n itself, or what its $ref leads
// to, however many references that takes. It returns nil for a reference
// that does not resolve to an object inside the source, or that leads back
// to itself.
func (b *bundle) follow(n *node) *node {
	for hops := 0; n != nil && n.kind == mapping; hops++ {
		reference := n.members["$ref"]
		if reference == nil {
			return n
		}
		value, isText := reference.text()
		if !isText || hops == maxReferenceDepth {
			return nil
		}
		next, reached := b.target(n.file, value)
		if reached != resolved {
			return nil
		}
		n = next
	}
	return nil
}
