package openapi

import (
	"bytes"
	"errors"
	"strconv"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// Event checks use the shared parse, reference and content safety rules.
// The AsyncAPI evaluator checks the version, metadata, operations, security
// declarations and document schema. RFC 9457 checks apply to HTTP only.

// EventVersion identifies the behavior used to record AsyncAPI check runs.
const EventVersion = "1"

const (
	supportedEventVersion = "3.1.0"
	eventSchemaURL        = "http://asyncapi.com/definitions/3.1.0/asyncapi.json"
)

// EventEvaluator is the built-in tool of a check of an AsyncAPI contract.
type EventEvaluator struct{}

var _ ports.Evaluator = EventEvaluator{}

func (EventEvaluator) Tool() domain.Tool {
	return domain.Tool{Name: "fabric-asyncapi-rules", Version: EventVersion}
}

var eventEvaluators = evaluatorTable{
	"supported_spec_version":  {(*bundle).eventSpecVersion, true},
	"info_required":           {(*bundle).infoRequired, true},
	"operation_id_required":   {(*bundle).eventOperationsNamed, true},
	"operation_id_unique":     {(*bundle).eventOperationsNamed, true},
	"operation_security":      {(*bundle).eventOperationSecurity, true},
	"official_oas_validation": {(*bundle).eventOfficialValidation, true},
	"ref_closure_budget":      evaluators["ref_closure_budget"],
	"ref_safety":              evaluators["ref_safety"],
	"parse_safety":            evaluators["parse_safety"],
	"secret_scan":             evaluators["secret_scan"],
	"internal_url_scan":       evaluators["internal_url_scan"],
}

func (EventEvaluator) Evaluate(source domain.Source, rules []domain.Rule) []domain.Observation {
	return evaluate(eventEvaluators, source, rules)
}

// F04-GOV-001: the entrypoint declares the one supported AsyncAPI version.
func (b *bundle) eventSpecVersion() judgment {
	entry := b.entrypoint()
	if version, isText := entry.member("asyncapi").text(); !isText || version != supportedEventVersion {
		return judgment{findings: []found{{
			under(entry, "asyncapi"),
			"the entrypoint does not declare AsyncAPI " + supportedEventVersion + ", the one version that is checked; write asyncapi: " + supportedEventVersion,
		}}}
	}
	return judgment{}
}

// F04-GOV-003 and F04-GOV-008: an AsyncAPI operation is named by its key
// under operations, which a document holds once, so every operation has an
// identifier and no two share one. What is left to judge is that the
// operations are a map of them.
func (b *bundle) eventOperationsNamed() judgment {
	entry := b.entrypoint()
	if operations := entry.member("operations"); operations != nil && operations.kind != mapping {
		return judgment{findings: []found{{under(entry, "operations"), "operations is not a map from the identifier of an operation to the operation"}}}
	}
	return judgment{}
}

// F04-GOV-006: every operation states its security, or every server it is
// offered on states one. An operation is offered on the servers its channel
// names, or on every server of the document when the channel names none. A
// security list that is empty states none, and an operation offered on no
// server has only its own.
func (b *bundle) eventOperationSecurity() judgment {
	entry := b.entrypoint()
	operations := entry.member("operations")
	if operations == nil || operations.kind != mapping {
		return judgment{}
	}
	var judged judgment
	for _, name := range operations.keys {
		operation := b.follow(operations.members[name])
		if operation == nil {
			judged.undecided = true
			continue
		}
		if secured(operation.member("security")) {
			continue
		}
		servers, clear := b.offeredOn(operation)
		if !clear {
			judged.undecided = true
			continue
		}
		if len(servers) == 0 {
			judged.findings = append(judged.findings, found{
				under(operation, "security"),
				"the operation states no security and is offered on no server; state the security of the operation or of the servers of its channel",
			})
			continue
		}
		for _, server := range servers {
			if !secured(server.member("security")) {
				judged.findings = append(judged.findings, found{
					under(operation, "security"),
					"the operation states no security, and neither does the server at line " + strconv.Itoa(at(server).line) + " of " + server.file +
						" it is offered on; state the security of the operation or of that server",
				})
				break
			}
		}
	}
	return judged
}

// offeredOn returns the servers of the channel of an operation. clear is
// false when a reference among them does not lead to an object.
func (b *bundle) offeredOn(operation *node) (servers []*node, clear bool) {
	channel := b.follow(operation.member("channel"))
	if channel == nil {
		return nil, false
	}
	listed := channel.member("servers")
	if listed == nil {
		every := b.entrypoint().member("servers")
		if every == nil {
			return nil, true
		}
		if every.kind != mapping {
			return nil, false
		}
		for _, name := range every.keys {
			listed := every.members[name]
			if server := b.follow(listed); server != nil {
				servers = append(servers, server)
			} else {
				return nil, false
			}
		}
		return servers, true
	}
	if listed.kind != sequence {
		return nil, false
	}
	for _, item := range listed.items {
		server := b.follow(item)
		if server == nil {
			return nil, false
		}
		servers = append(servers, server)
	}
	return servers, true
}

func secured(security *node) bool {
	return security != nil && security.kind == sequence && len(security.items) > 0
}

var eventSchema = sync.OnceValues(compileEventSchema)

func compileEventSchema() (*jsonschema.Schema, error) {
	content, err := schemaAssets.ReadFile("schemas/asyncapi-3.1.0.json")
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(embeddedOnly{})
	compiler.AssertFormat()
	if err := compiler.AddResource(eventSchemaURL, document); err != nil {
		return nil, err
	}
	return compiler.Compile(eventSchemaURL)
}

// F04-GOV-011: the entry document satisfies the official AsyncAPI 3.1.0
// schema. As for OpenAPI, the files it references are held to the closure
// rules and not validated on their own.
func (b *bundle) eventOfficialValidation() judgment {
	root := b.entrypoint()
	document, err := jsonValue(root)
	if errors.Is(err, errNumberBudget) {
		return judgment{undecided: true}
	}
	if err != nil {
		return judgment{findings: []found{{at(root), "the entrypoint has a value that cannot be validated as JSON"}}}
	}
	schema, err := eventSchema()
	if err != nil {
		return judgment{undecided: true}
	}
	err = schema.Validate(document)
	if err == nil {
		return judgment{}
	}
	var violation *jsonschema.ValidationError
	if !errors.As(err, &violation) {
		return judgment{undecided: true}
	}
	// As for OpenAPI, the place is said and the instance is not echoed.
	return judgment{findings: []found{{at(nodeAt(root, firstInvalidLocation(violation))), "the value does not satisfy the official AsyncAPI 3.1.0 schema"}}}
}
