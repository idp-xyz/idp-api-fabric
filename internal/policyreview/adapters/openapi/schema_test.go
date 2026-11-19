package openapi_test

import (
	"slices"
	"testing"
)

func TestOfficialOpenAPISchemaValidatesInlineSchemasOffline(t *testing.T) {
	const header = "openapi: 3.1.2\ninfo: {title: Shipments, version: '1'}\n"
	for _, tc := range []struct {
		name      string
		content   string
		invalid   bool
		undecided bool
	}{
		{name: "valid", content: "components: {schemas: {Shipment: {type: object}}}"},
		{name: "standard dialect alias", content: "jsonSchemaDialect: https://spec.openapis.org/oas/3.1/dialect/base\npaths: {}"},
		{name: "invalid paths", content: "paths: []", invalid: true},
		{name: "invalid inline type", content: "components: {schemas: {Shipment: {type: imaginary}}}", invalid: true},
		{name: "unsupported document dialect", content: "jsonSchemaDialect: https://example.test/custom-dialect\npaths: {}", undecided: true},
		{name: "unsupported inline dialect", content: "components: {schemas: {Shipment: {$schema: 'https://example.test/custom-dialect'}}}", undecided: true},
		{name: "numeric bound", content: "components: {schemas: {Shipment: {maximum: !!float 1e1001}}}", undecided: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said := evaluate(t, file("openapi.yaml", header+tc.content+"\n"))
			if findings, undecided := said.findings["011"], slices.Contains(said.undecided, "011"); (len(findings) > 0) != tc.invalid || undecided != tc.undecided {
				t.Fatalf("official schema reported findings %v, undecided %t", findings, undecided)
			}
		})
	}

	source := `{"openapi":"3.1.2","info":{"title":"Shipments","version":"1"},"components":{"schemas":{"Shipment":{"maximum":1e1001}}}}`
	if said := evaluate(t, file("openapi.json", source)); !slices.Contains(said.undecided, "011") {
		t.Fatalf("the JSON number must have the same bound, got %+v", said)
	}
}
