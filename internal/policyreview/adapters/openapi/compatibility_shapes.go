package openapi

import (
	"encoding/json"
	"strconv"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

type parameterKey struct{ location, name string }

func effectiveParameters(inherited, local any) (map[parameterKey]object, bool) {
	effective := map[parameterKey]object{}
	for _, group := range []any{inherited, local} {
		list, _ := group.([]any)
		seen := map[parameterKey]bool{}
		for _, item := range list {
			parameter, _ := item.(object)
			location, hasLocation := parameter["in"].(string)
			name, hasName := parameter["name"].(string)
			if !hasLocation || !hasName || parameter["$ref"] != nil {
				return nil, false
			}
			if location == "header" {
				name = strings.ToLower(name)
			}
			key := parameterKey{location, name}
			if seen[key] {
				return nil, false
			}
			seen[key] = true
			effective[key] = parameter
		}
	}
	return effective, true
}

func required(value object) bool {
	flag, _ := value["required"].(bool)
	return flag
}

func (d *comparison) parameters(oldInherited, oldLocal, newInherited, newLocal any, pointer string) {
	before, beforeOK := effectiveParameters(oldInherited, oldLocal)
	after, afterOK := effectiveParameters(newInherited, newLocal)
	if !beforeOK || !afterOK {
		d.add(domain.Unknown, "PARAMETER_REFERENCE_OR_DUPLICATE", pointer)
		return
	}
	var tightened, loosened, other bool
	for key, next := range after {
		previous := before[key]
		tightened = tightened || (required(next) && !required(previous))
		loosened = loosened || (!required(next) && (previous == nil || required(previous)))
		other = other || (previous != nil && !equalExcept(previous, next, "required", "description"))
	}
	for key := range before {
		if after[key] == nil {
			other = true
		}
	}
	if tightened {
		d.add(domain.Breaking, "PARAMETER_REQUIRED", pointer)
	}
	if loosened {
		d.add(domain.Compatible, "OPTIONAL_PARAMETER", pointer)
	}
	if other {
		d.add(domain.Unknown, "PARAMETER_CHANGED", pointer)
	}
}

func (d *comparison) requestBody(before, after object, pointer string) {
	switch {
	case after == nil || before["$ref"] != nil || after["$ref"] != nil:
		d.add(domain.Unknown, "REQUEST_BODY_CHANGED", pointer)
		return
	case required(after) && !required(before):
		d.add(domain.Breaking, "REQUEST_BODY_REQUIRED", pointer)
	case before == nil || (required(before) && !required(after)):
		d.add(domain.Compatible, "OPTIONAL_REQUEST_BODY", pointer)
	}
	if before == nil {
		return
	}
	if !equalExcept(before, after, "description", "required", "content") {
		d.add(domain.Unknown, "REQUEST_BODY_CHANGED", pointer)
	}
	oldContent, newContent := objectAt(before, "content"), objectAt(after, "content")
	for _, key := range differentKeys(oldContent, newContent) {
		a, b := changedSchema(oldContent, newContent, key)
		location := pointer + "/content/" + escapePointer.Replace(key)
		if requestTightened(a, b) {
			d.add(domain.Breaking, "REQUEST_SCHEMA_TIGHTENED", location+"/schema")
		} else {
			d.add(domain.Unknown, "REQUEST_CONTENT_CHANGED", location)
		}
	}
}

func requestTightened(before, after object) bool {
	if before == nil || after == nil || before["$ref"] != nil || after["$ref"] != nil {
		return false
	}
	switch before["type"] {
	case "object":
		return equalExcept(before, after, "required") && addsMember(stringsIn(after["required"]), stringsIn(before["required"]))
	case "string":
		if changed(before, after, "enum") && equalExcept(before, after, "enum") {
			return strictlyIncludes(stringsIn(before["enum"]), stringsIn(after["enum"]))
		}
		if changed(before, after, "maxLength") && equalExcept(before, after, "maxLength") {
			oldLimit, hadLimit := lengthLimit(before)
			newLimit, hasLimit := lengthLimit(after)
			return hasLimit && (before["maxLength"] == nil || (hadLimit && newLimit < oldLimit))
		}
	}
	return false
}

func lengthLimit(schema object) (uint64, bool) {
	number, ok := schema["maxLength"].(json.Number)
	if !ok {
		return 0, false
	}
	limit, err := strconv.ParseUint(number.String(), 10, 64)
	return limit, err == nil
}

func stringsIn(value any) map[string]bool {
	list, _ := value.([]any)
	if list == nil {
		return nil
	}
	values := make(map[string]bool, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		values[text] = true
	}
	return values
}

func addsMember(values, baseline map[string]bool) bool {
	for value := range values {
		if !baseline[value] {
			return true
		}
	}
	return false
}

func strictlyIncludes(values, subset map[string]bool) bool {
	return subset != nil && len(values) > len(subset) && !addsMember(subset, values)
}

func changedSchema(before, after object, mediaType string) (object, object) {
	oldMedia, newMedia := objectAt(before, mediaType), objectAt(after, mediaType)
	if !equalExcept(oldMedia, newMedia, "schema") {
		return nil, nil
	}
	return objectAt(oldMedia, "schema"), objectAt(newMedia, "schema")
}

func (d *comparison) responses(before, after object, pointer string) {
	for _, key := range differentKeys(before, after) {
		a, b := objectAt(before, key), objectAt(after, key)
		location := pointer + "/" + escapePointer.Replace(key)
		switch {
		case a != nil && b != nil && equalExcept(a, b, "description"):
		case key == "default" || strings.HasPrefix(key, "4") || strings.HasPrefix(key, "5"):
			d.add(domain.Risky, "ERROR_RESPONSE_CHANGED", location)
		case responseEnumExpanded(a, b):
			d.add(domain.Risky, "RESPONSE_ENUM_EXPANDED", location)
		default:
			d.add(domain.Unknown, "RESPONSE_CHANGED", location)
		}
	}
}

func responseEnumExpanded(before, after object) bool {
	if before == nil || after == nil || !equalExcept(before, after, "description", "content") {
		return false
	}
	oldContent, newContent := objectAt(before, "content"), objectAt(after, "content")
	keys := differentKeys(oldContent, newContent)
	for _, key := range keys {
		a, b := changedSchema(oldContent, newContent, key)
		if a == nil || b == nil || a["type"] != "string" || a["$ref"] != nil || !equalExcept(a, b, "enum") ||
			!strictlyIncludes(stringsIn(b["enum"]), stringsIn(a["enum"])) {
			return false
		}
	}
	return len(keys) > 0
}
