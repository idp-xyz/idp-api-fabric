package openapi

import (
	"cmp"
	"reflect"
	"slices"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

type Comparator struct{}

func (Comparator) Tool() domain.Tool {
	return domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"}
}

// Compare classifies changes to recorded, bounded OpenAPI 3.1.2 inputs.
// A change outside the implemented comparisons remains unknown.
func (Comparator) Compare(baseline, candidate domain.Source) domain.Comparison {
	diff := comparison{Classification: domain.Compatible}
	if baseline.Closure != domain.ClosureVerified || candidate.Closure != domain.ClosureVerified {
		diff.add(domain.Unknown, "REFERENCE_CLOSURE_UNVERIFIED", "")
		return diff.result()
	}
	before, after := comparableDocument(baseline), comparableDocument(candidate)
	if before == nil || after == nil {
		diff.add(domain.Unknown, "DOCUMENT_UNSUPPORTED", "")
		return diff.result()
	}
	if baseline.Entrypoint != candidate.Entrypoint || !sameCompanionFiles(baseline, candidate) {
		diff.add(domain.Unknown, "REFERENCE_FILES_CHANGED", "")
	}

	if changed(before, after, "security") {
		diff.add(domain.Risky, "SECURITY_CHANGED", "/security")
	}
	oldComponents, newComponents := objectAt(before, "components"), objectAt(after, "components")
	if changed(oldComponents, newComponents, "securitySchemes") {
		diff.add(domain.Risky, "SECURITY_CHANGED", "/components/securitySchemes")
	}
	if !equalExcept(oldComponents, newComponents, "securitySchemes") {
		diff.add(domain.Unknown, "COMPONENTS_CHANGED", "/components")
	}
	if !equalExcept(objectAt(before, "info"), objectAt(after, "info"), "title", "summary", "description", "version") ||
		!equalExcept(before, after, "info", "externalDocs", "paths", "security", "components") {
		diff.add(domain.Unknown, "DOCUMENT_CHANGED", "")
	}
	diff.paths(objectAt(before, "paths"), objectAt(after, "paths"))
	return diff.result()
}

type object = map[string]any

func objectAt(value object, key string) object {
	child, _ := value[key].(object)
	return child
}

func comparableDocument(source domain.Source) object {
	parsed := read(source)
	if !parsed.readable() || len(parsed.specVersion().findings) > 0 {
		return nil
	}
	root := parsed.entrypoint()
	document, err := jsonValue(root)
	schema, schemaErr := officialSchema()
	if err != nil || schemaErr != nil || schema.Validate(document) != nil {
		return nil
	}
	return document.(object)
}

func sameCompanionFiles(before, after domain.Source) bool {
	oldFiles := make(map[string]string, len(before.Files))
	for _, file := range before.Files {
		if file.Path != before.Entrypoint {
			oldFiles[file.Path] = file.Content
		}
	}
	for _, file := range after.Files {
		if file.Path == after.Entrypoint {
			continue
		}
		if content, exists := oldFiles[file.Path]; !exists || content != file.Content {
			return false
		}
		delete(oldFiles, file.Path)
	}
	return len(oldFiles) == 0
}

type comparison domain.Comparison

func (d *comparison) add(classification domain.Compatibility, code, pointer string) {
	if !domain.ValidPointer(pointer) {
		pointer = ""
	}
	d.Changes = append(d.Changes, domain.CompatibilityChange{Classification: classification, Code: code, Pointer: pointer})
	if priority(classification) > priority(d.Classification) {
		d.Classification = classification
	}
}

func (d *comparison) result() domain.Comparison {
	if len(d.Changes) > domain.MaxCompatibilityChanges {
		slices.SortStableFunc(d.Changes, func(a, b domain.CompatibilityChange) int {
			return cmp.Compare(priority(b.Classification), priority(a.Classification))
		})
		d.Changes = d.Changes[:domain.MaxCompatibilityChanges]
		d.Truncated = true
	}
	return domain.Comparison(*d)
}

func priority(classification domain.Compatibility) int {
	switch classification {
	case domain.Breaking:
		return 3
	case domain.Unknown:
		return 2
	case domain.Risky:
		return 1
	default:
		return 0
	}
}

func (d *comparison) paths(before, after object) {
	for _, key := range differentKeys(before, after) {
		pointer := "/paths/" + escapePointer.Replace(key)
		if !strings.HasPrefix(key, "/") {
			d.add(domain.Unknown, "PATHS_EXTENSION_CHANGED", "")
			continue
		}
		oldPath, newPath := objectAt(before, key), objectAt(after, key)
		switch {
		case newPath == nil:
			d.add(domain.Breaking, "PATH_REMOVED", pointer)
		case oldPath == nil:
			if routeMayOverlap(key, before) {
				d.add(domain.Risky, "PATH_MAY_OVERLAP", pointer)
			} else {
				d.add(domain.Compatible, "PATH_ADDED", pointer)
			}
		case oldPath["$ref"] != nil || newPath["$ref"] != nil:
			d.add(domain.Unknown, "PATH_REFERENCE_CHANGED", pointer)
		default:
			d.operations(oldPath, newPath, pointer)
		}
	}
}

func routeMayOverlap(path string, before object) bool {
	newParts := strings.Split(path, "/")
	for old := range before {
		if !strings.HasPrefix(old, "/") {
			continue
		}
		oldParts := strings.Split(old, "/")
		if len(oldParts) != len(newParts) {
			continue
		}
		matches := true
		for i, part := range oldParts {
			if part != newParts[i] && !strings.Contains(part, "{") && !strings.Contains(newParts[i], "{") {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func (d *comparison) operations(before, after object, pointer string) {
	keys := differentKeys(before, after)
	if changed(before, after, "parameters") {
		// Inherited parameters also affect unchanged operations.
		for method := range methods {
			if objectAt(before, method) != nil && !changed(before, after, method) {
				keys = append(keys, method)
			}
		}
		slices.Sort(keys)
	}
	for _, key := range keys {
		location := pointer + "/" + escapePointer.Replace(key)
		if !methods[key] {
			switch key {
			case "description", "summary", "parameters":
			default:
				d.add(domain.Unknown, "PATH_SETTINGS_CHANGED", location)
			}
			continue
		}
		oldOperation, newOperation := objectAt(before, key), objectAt(after, key)
		switch {
		case newOperation == nil:
			d.add(domain.Breaking, "OPERATION_REMOVED", location)
		case oldOperation == nil:
			d.add(domain.Compatible, "OPERATION_ADDED", location)
		default:
			d.operation(oldOperation, newOperation, before["parameters"], after["parameters"], location)
		}
	}
}

func (d *comparison) operation(before, after object, oldInherited, newInherited any, pointer string) {
	if changed(before, after, "security") {
		d.add(domain.Risky, "SECURITY_CHANGED", pointer+"/security")
	}
	if changed(before, after, "parameters") || !reflect.DeepEqual(oldInherited, newInherited) {
		d.parameters(oldInherited, before["parameters"], newInherited, after["parameters"], pointer+"/parameters")
	}
	if changed(before, after, "requestBody") {
		d.requestBody(objectAt(before, "requestBody"), objectAt(after, "requestBody"), pointer+"/requestBody")
	}
	d.responses(objectAt(before, "responses"), objectAt(after, "responses"), pointer+"/responses")
	if !equalExcept(before, after, "summary", "description", "externalDocs", "security", "parameters", "requestBody", "responses") {
		d.add(domain.Unknown, "OPERATION_CHANGED", pointer)
	}
}

func differentKeys(before, after object) []string {
	var keys []string
	for key := range before {
		if changed(before, after, key) {
			keys = append(keys, key)
		}
	}
	for key := range after {
		if _, exists := before[key]; !exists {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func changed(before, after object, key string) bool {
	oldValue, hadValue := before[key]
	newValue, hasValue := after[key]
	return hadValue != hasValue || !reflect.DeepEqual(oldValue, newValue)
}

func equalExcept(before, after object, allowed ...string) bool {
	for _, key := range differentKeys(before, after) {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}
