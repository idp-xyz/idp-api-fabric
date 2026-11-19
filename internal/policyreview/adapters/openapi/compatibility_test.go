package openapi_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/openapi"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

func comparisonSource(content string) domain.Source {
	return domain.Source{
		Entrypoint: "openapi.yaml",
		Files:      []domain.SourceFile{file("openapi.yaml", content)},
		Closure:    domain.ClosureVerified,
	}
}

func TestOpenAPICompatibilityClassifiesRecognizedChangesAndKeepsUnknowns(t *testing.T) {
	const header = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"
	const baseline = header + "paths: {/shipments: {get: {responses: {'200': {description: OK}}}}}\n"
	const request = header + "paths: {/shipments: {post: {requestBody: {content: {'application/json': {schema: %s}}}}}}\n"
	const response = header + "paths: {/shipments: {get: {responses: {'200': {description: OK, content: {'application/json': {schema: %s}}}}}}}\n"
	for _, tc := range []struct {
		name, before, after string
		classification      domain.Compatibility
		code                string
	}{
		{"remove path", baseline, header + "paths: {}\n", domain.Breaking, "PATH_REMOVED"},
		{"remove operation", baseline, header + "paths: {/shipments: {}}\n", domain.Breaking, "OPERATION_REMOVED"},
		{"add path", header + "paths: {}\n", baseline, domain.Compatible, "PATH_ADDED"},
		{"overlapping route", header + "paths: {'/{parcel}': {}}\n", header + "paths: {'/{parcel}': {}, /shipments: {get: {responses: {'200': {description: OK}}}}}\n", domain.Risky, "PATH_MAY_OVERLAP"},
		{"documentation", baseline, strings.ReplaceAll(baseline, "description: OK", "description: Accepted"), domain.Compatible, ""},
		{"required parameter", baseline, strings.ReplaceAll(baseline, "{get:", "{parameters: [{in: query, name: region, required: true, schema: {type: string}}], get:"), domain.Breaking, "PARAMETER_REQUIRED"},
		{"optional parameter", baseline, strings.ReplaceAll(baseline, "{get: {", "{get: {parameters: [{in: query, name: region, schema: {type: string}}], "), domain.Compatible, "OPTIONAL_PARAMETER"},
		{"security", baseline, baseline + "security: []\n", domain.Risky, "SECURITY_CHANGED"},
		{"error response", baseline, strings.ReplaceAll(baseline, "'200':", "'429': {description: Slow down}, '200':"), domain.Risky, "ERROR_RESPONSE_CHANGED"},
		{"response enum expansion", fmt.Sprintf(response, "{type: string, enum: [queued]}"), fmt.Sprintf(response, "{type: string, enum: [queued, shipped]}"), domain.Risky, "RESPONSE_ENUM_EXPANDED"},
		{"request enum narrowing", fmt.Sprintf(request, "{type: string, enum: [a, b]}"), fmt.Sprintf(request, "{type: string, enum: [a]}"), domain.Breaking, "REQUEST_SCHEMA_TIGHTENED"},
		{"request length narrowing", fmt.Sprintf(request, "{type: string, maxLength: 12}"), fmt.Sprintf(request, "{type: string, maxLength: 8}"), domain.Breaking, "REQUEST_SCHEMA_TIGHTENED"},
		{"request property required", fmt.Sprintf(request, "{type: object, properties: {id: {type: string}}}"), fmt.Sprintf(request, "{type: object, properties: {id: {type: string}}, required: [id]}"), domain.Breaking, "REQUEST_SCHEMA_TIGHTENED"},
		{"unclassified request", fmt.Sprintf(request, "{type: string}"), fmt.Sprintf(request, "{oneOf: [{type: number}, {type: boolean}]}"), domain.Unknown, "REQUEST_CONTENT_CHANGED"},
		{"changed components", baseline, baseline + "components: {schemas: {Shipment: {type: string}}}\n", domain.Unknown, "COMPONENTS_CHANGED"},
		{"invalid document", baseline, header + "paths: []\n", domain.Unknown, "DOCUMENT_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := (openapi.Comparator{}).Compare(comparisonSource(tc.before), comparisonSource(tc.after))
			if result.Classification != tc.classification {
				t.Fatalf("got %+v, want %s", result, tc.classification)
			}
			if tc.code != "" && !slices.ContainsFunc(result.Changes, func(c domain.CompatibilityChange) bool { return c.Code == tc.code }) {
				t.Fatalf("got %+v, want change %s", result, tc.code)
			}
		})
	}
}

func TestOpenAPICompatibilityRequiresClosureEvidence(t *testing.T) {
	const specification = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\npaths: {/shipments: {$ref: path.yaml}}\n"
	before, after := comparisonSource(specification), comparisonSource(specification)
	before.Files = append(before.Files, file("path.yaml", "summary: Baseline"))
	after.Files = append(after.Files, file("path.yaml", "summary: Candidate"))
	comparator := openapi.Comparator{}
	if got := comparator.Compare(before, after); got.Classification != domain.Unknown || got.Changes[0].Code != "REFERENCE_FILES_CHANGED" {
		t.Fatalf("changed companion: %+v", got)
	}
	after.Closure = domain.ClosureUndecided
	if got := comparator.Compare(before, after); got.Classification != domain.Unknown || got.Changes[0].Code != "REFERENCE_CLOSURE_UNVERIFIED" {
		t.Fatalf("unverified closure: %+v", got)
	}
}
