package openapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"sync"

	yaml "github.com/oasdiff/yaml3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaHost        = "https://spec.openapis.org/oas/3.1/"
	baseSchemaURL     = schemaHost + "schema-base/2026-11-05"
	pinnedDialect     = schemaHost + "dialect/2024-11-10"
	openAPIDialectURL = schemaHost + "dialect/base"
)

//go:embed schemas/*.json
var schemaAssets embed.FS

var officialSchema = sync.OnceValues(compileOfficialSchema)

var errNumberBudget = errors.New("a number exceeds the validation budget")

type embeddedOnly struct{}

func (embeddedOnly) Load(string) (any, error) {
	return nil, errors.New("the requested metamodel is outside the embedded OpenAPI schemas")
}

func compileOfficialSchema() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(embeddedOnly{})
	compiler.AssertFormat()
	for _, name := range []string{
		"schema-base-2026-11-05.json", "schema-2026-11-05.json", "dialect-2024-11-10.json", "meta-2024-11-10.json",
	} {
		content, err := schemaAssets.ReadFile("schemas/" + name)
		if err != nil {
			return nil, err
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
		if err != nil {
			return nil, err
		}
		object, isObject := document.(map[string]any)
		id, hasID := object["$id"].(string)
		if !isObject || !hasID || !strings.HasPrefix(id, schemaHost) {
			return nil, fmt.Errorf("the embedded schema %s has an unexpected identifier", name)
		}
		if err := compiler.AddResource(id, document); err != nil {
			return nil, err
		}
	}
	return compiler.Compile(baseSchemaURL)
}

// officialValidation checks the entry document and its inline Schema Objects
// against the pinned OpenAPI dialect. Reference closure is checked separately.
func (b *bundle) officialValidation() judgment {
	root := b.entrypoint()
	if dialect, ok := root.member("jsonSchemaDialect").text(); ok && dialect != openAPIDialectURL && dialect != pinnedDialect {
		return judgment{undecided: true}
	}
	document, err := jsonValue(root)
	if errors.Is(err, errNumberBudget) {
		return judgment{undecided: true}
	}
	if err != nil {
		return judgment{findings: []found{{at(root), "the entrypoint has a value that cannot be validated as JSON"}}}
	}
	schema, err := officialSchema()
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
	// Keep a stable location without echoing the instance or arbitrary
	// validation messages, which can contain values from the submitted file.
	location := nodeAt(root, firstInvalidLocation(violation))
	if dialect, ok := location.text(); ok && location.token == "$schema" &&
		dialect != openAPIDialectURL && dialect != pinnedDialect {
		return judgment{undecided: true}
	}
	return judgment{findings: []found{{at(location), "the value does not satisfy the official OpenAPI 3.1 schema"}}}
}

// The validator can report failures in map iteration order.
func firstInvalidLocation(violation *jsonschema.ValidationError) []string {
	if len(violation.Causes) == 0 {
		return violation.InstanceLocation
	}
	first := firstInvalidLocation(violation.Causes[0])
	for _, cause := range violation.Causes[1:] {
		if location := firstInvalidLocation(cause); strings.Join(location, "\x00") < strings.Join(first, "\x00") {
			first = location
		}
	}
	return first
}

func nodeAt(root *node, location []string) *node {
	current := root
	for _, token := range location {
		var next *node
		switch current.kind {
		case mapping:
			next = current.member(token)
		case sequence:
			if index, err := strconv.Atoi(token); err == nil && index >= 0 && index < len(current.items) {
				next = current.items[index]
			}
		}
		if next == nil {
			break
		}
		current = next
	}
	return current
}

func jsonValue(n *node) (any, error) {
	switch n.kind {
	case mapping:
		object := make(map[string]any, len(n.keys))
		for _, key := range n.keys {
			value, err := jsonValue(n.members[key])
			if err != nil {
				return nil, err
			}
			// Resolve the official versionless alias in the validation
			// projection while preserving the recorded source bytes.
			if (key == "$schema" || key == "jsonSchemaDialect") && value == openAPIDialectURL {
				value = pinnedDialect
			}
			object[key] = value
		}
		return object, nil
	case sequence:
		list := make([]any, 0, len(n.items))
		for _, item := range n.items {
			value, err := jsonValue(item)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		return list, nil
	default:
		number := n.tag == "!!int" || n.tag == "!!float" || (n.plain && path.Ext(n.file) == ".json")
		if number && len(n.value) > 0 && strings.ContainsRune("-0123456789", rune(n.value[0])) &&
			json.Valid([]byte(n.value)) {
			// The validator uses arbitrary precision arithmetic. Bound both
			// the number of digits and the exponent before passing it through.
			if len(n.value) > 256 {
				return nil, errNumberBudget
			}
			if index := strings.IndexAny(n.value, "eE"); index >= 0 {
				exponent, err := strconv.Atoi(n.value[index+1:])
				if err != nil || exponent < -1000 || exponent > 1000 {
					return nil, errNumberBudget
				}
			}
			return json.Number(n.value), nil
		}
		if n.tag == "!!str" || n.tag == "!!timestamp" {
			return n.value, nil
		}
		var value any
		scalar := yaml.Node{Kind: yaml.ScalarNode, Tag: n.tag, Value: n.value}
		if err := scalar.Decode(&value); err != nil {
			return nil, err
		}
		if number, ok := value.(float64); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
			return nil, errors.New("a nonfinite YAML number has no JSON equivalent")
		}
		return value, nil
	}
}
