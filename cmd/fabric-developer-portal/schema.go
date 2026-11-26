package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// A contract is untrusted input, so a schema is listed only to a bounded depth
// and length; the source bundle download keeps the whole of it.
const (
	schemaDepth  = 6
	schemaFields = 200
	enumValues   = 20
)

type field struct {
	Depth                   int
	Name, Type, Description string
	Note                    string
	Required                bool
}

type schemaWalk struct {
	rows      []field
	open      map[*openapi3.Schema]bool
	truncated bool
}

func schemaFieldsOf(ref *openapi3.SchemaRef) []field {
	if ref == nil || ref.Value == nil {
		return nil
	}
	walk := schemaWalk{open: map[*openapi3.Schema]bool{}}
	walk.field(ref.Value, "", false, 0)
	if walk.truncated {
		walk.rows = append(walk.rows, field{Note: "Further fields are omitted; see the source bundle."})
	}
	return walk.rows
}

func (w *schemaWalk) field(s *openapi3.Schema, name string, required bool, depth int) {
	if len(w.rows) >= schemaFields {
		w.truncated = true
		return
	}
	row := field{Depth: depth, Name: name, Type: schemaType(s), Description: s.Description, Required: required, Note: schemaNote(s)}
	if w.open[s] {
		row.Note = "Repeats an enclosing schema."
		w.rows = append(w.rows, row)
		return
	}
	w.rows = append(w.rows, row)
	w.children(s, depth+1)
}

func (w *schemaWalk) children(s *openapi3.Schema, depth int) {
	if w.open[s] {
		return
	}
	if depth > schemaDepth {
		if len(s.Properties) > 0 || len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) > 0 || s.Items != nil {
			w.rows[len(w.rows)-1].Note = "Nested fields are omitted; see the source bundle."
		}
		return
	}
	w.open[s] = true
	defer delete(w.open, s)
	for _, member := range s.AllOf {
		if member != nil && member.Value != nil {
			w.children(member.Value, depth)
		}
	}
	required := map[string]bool{}
	for _, name := range s.Required {
		required[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		if property := s.Properties[name]; property != nil && property.Value != nil {
			w.field(property.Value, name, required[name], depth)
		}
	}
	if s.Items != nil && s.Items.Value != nil {
		w.children(s.Items.Value, depth)
	}
	for _, choice := range []struct {
		label   string
		members openapi3.SchemaRefs
	}{{"one of", s.OneOf}, {"any of", s.AnyOf}} {
		for i, member := range choice.members {
			if member != nil && member.Value != nil {
				w.field(member.Value, fmt.Sprintf("%s #%d", choice.label, i+1), false, depth)
			}
		}
	}
}

func schemaType(s *openapi3.Schema) string {
	kinds := schemaKinds(s)
	for i, kind := range kinds {
		if kind == "array" && s.Items != nil && s.Items.Value != nil {
			kinds[i] = "array of " + strings.Join(schemaKinds(s.Items.Value), " | ")
		}
	}
	kind := strings.Join(kinds, " | ")
	if s.Format != "" {
		kind += " (" + s.Format + ")"
	}
	return kind
}

func schemaKinds(s *openapi3.Schema) []string {
	var kinds []string
	if s.Type != nil {
		kinds = slices.Clone(s.Type.Slice())
	}
	if len(kinds) == 0 {
		switch {
		case len(s.OneOf) > 0:
			kinds = []string{"one of"}
		case len(s.AnyOf) > 0:
			kinds = []string{"any of"}
		case len(s.Properties) > 0 || len(s.AllOf) > 0:
			kinds = []string{"object"}
		default:
			kinds = []string{"any"}
		}
	}
	if s.Nullable && !slices.Contains(kinds, "null") {
		kinds = append(kinds, "null")
	}
	return kinds
}

func schemaNote(s *openapi3.Schema) string {
	var notes []string
	if s.Const != nil {
		notes = append(notes, fmt.Sprintf("Always %v.", s.Const))
	}
	if len(s.Enum) > 0 {
		values := make([]string, 0, min(len(s.Enum), enumValues))
		for _, value := range s.Enum[:min(len(s.Enum), enumValues)] {
			values = append(values, fmt.Sprint(value))
		}
		more := ""
		if len(s.Enum) > enumValues {
			more = ", …"
		}
		notes = append(notes, "One of: "+strings.Join(values, ", ")+more+".")
	}
	if s.ReadOnly {
		notes = append(notes, "Read-only.")
	}
	if s.WriteOnly {
		notes = append(notes, "Write-only.")
	}
	if s.Deprecated {
		notes = append(notes, "Deprecated.")
	}
	return strings.Join(notes, " ")
}

// sampleValue is the value a request sample sends when the contract gives no
// example: the schema's own example, else the simplest value of its type.
// Read-only properties are left out because a client does not send them.
func sampleValue(s *openapi3.Schema, depth int, open map[*openapi3.Schema]bool, budget *int) any {
	switch {
	case s.Example != nil:
		return s.Example
	case len(s.Examples) > 0:
		return s.Examples[0]
	case s.Const != nil:
		return s.Const
	case len(s.Enum) > 0:
		return s.Enum[0]
	case s.Default != nil:
		return s.Default
	}
	if open[s] || depth > schemaDepth || *budget <= 0 {
		return nil
	}
	*budget--
	open[s] = true
	defer delete(open, s)
	for _, members := range []openapi3.SchemaRefs{s.OneOf, s.AnyOf} {
		if len(members) > 0 && members[0] != nil && members[0].Value != nil {
			return sampleValue(members[0].Value, depth+1, open, budget)
		}
	}
	kinds := schemaKinds(s)
	switch {
	case slices.Contains(kinds, "object"):
		object := map[string]any{}
		for _, member := range s.AllOf {
			if member == nil || member.Value == nil {
				continue
			}
			value := sampleValue(member.Value, depth+1, open, budget)
			if part, ok := value.(map[string]any); ok {
				maps.Copy(object, part)
			} else if len(s.AllOf) == 1 && len(s.Properties) == 0 {
				return value
			}
		}
		for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
			property := s.Properties[name]
			if property == nil || property.Value == nil || property.Value.ReadOnly {
				continue
			}
			object[name] = sampleValue(property.Value, depth+1, open, budget)
		}
		return object
	case slices.Contains(kinds, "array"):
		if s.Items == nil || s.Items.Value == nil {
			return []any{}
		}
		item := sampleValue(s.Items.Value, depth+1, open, budget)
		if item == nil {
			return []any{}
		}
		return []any{item}
	case slices.Contains(kinds, "string"):
		return "string"
	case slices.Contains(kinds, "integer"), slices.Contains(kinds, "number"):
		return 0
	case slices.Contains(kinds, "boolean"):
		return false
	}
	return nil
}
