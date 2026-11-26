package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The note example carries shell, Go and JavaScript syntax, which every code
// sample has to keep inside a string literal.
const hostileNote = "It's $(id) `fragile` \"${process.exit()}\""

var shipmentContract = map[string]string{
	"openapi.yaml": `openapi: 3.1.2
info:
  title: Shipping & Tracking
  version: 2.0.0
servers:
  - url: https://sandbox.cargopai.example
    description: Sandbox
security:
  - partnerToken: []
paths:
  /shipments/{shipment_id}:
    parameters:
      - name: shipment_id
        in: path
        required: true
        description: Shipment reference
        schema: {type: string, format: uuid}
    put:
      summary: Replace a shipment
      parameters:
        - name: dry_run
          in: query
          required: true
          schema: {type: boolean}
        - name: X-Request-Id
          in: header
          required: true
          schema: {type: string}
        - name: Accept
          in: header
          required: true
          schema: {type: string}
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: './schemas.yaml#/Shipment'}
      responses:
        '200':
          description: Replaced
          content:
            application/json:
              schema: {$ref: './schemas.yaml#/Shipment'}
        '409':
          description: Conflict
          content:
            application/problem+json:
              schema: {$ref: './schemas.yaml#/Problem'}
components:
  securitySchemes:
    partnerToken: {type: http, scheme: bearer}
`,
	"schemas.yaml": `Shipment:
  type: object
  required: [destination, status]
  properties:
    id: {type: string, readOnly: true}
    status:
      type: string
      enum: [draft, dispatched]
    destination:
      type: object
      properties:
        city: {type: string, description: City name}
    parts:
      type: array
      items: {$ref: '#/Part'}
    note:
      type: [string, 'null']
      example: ` + strconv.Quote(hostileNote) + `
Part:
  type: object
  properties:
    label: {type: string}
    weight: {type: number}
Problem:
  type: object
  properties:
    type: {type: string}
    title: {type: string}
    status: {type: integer}
`,
}

func TestReferenceShowsSchemasAndCodeSamples(t *testing.T) {
	payload := bundleArtifact(t, shipmentContract)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Artifact-Digest", digest)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(origin.Close)
	portal, err := newPortal(origin.URL, testAsset)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer test-partner")
	recorder := httptest.NewRecorder()
	portal.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("reference returned %d: %s", recorder.Code, recorder.Body)
	}
	page := html.UnescapeString(recorder.Body.String())
	for _, expected := range []string{
		"https://sandbox.cargopai.example", "Sandbox",
		"shipment_id", "string (uuid)", "dry_run", "boolean", "X-Request-Id",
		"destination", "City name", "array of object", "string | null",
		"One of: draft, dispatched.", "Read-only.",
		"application/problem+json", "RFC 9457 problem details",
		`curl --globoff --request PUT "$API_BASE_URL"'/shipments/{shipment_id}?dry_run={dry_run}'`,
		`--header 'Authorization: Bearer '"$API_ACCESS_TOKEN"`,
		`--header 'X-Request-Id: {X-Request-Id}'`,
		`os.Getenv("API_BASE_URL")+"/shipments/{shipment_id}?dry_run={dry_run}"`,
		`process.env.API_BASE_URL + "/shipments/{shipment_id}?dry_run={dry_run}"`,
		`"Authorization": "Bearer " + process.env.API_ACCESS_TOKEN`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("reference is missing %q", expected)
		}
	}
	if strings.Contains(page, "Accept: {Accept}") {
		t.Error("a sample sent a header parameter that OpenAPI ignores")
	}
}

func TestCodeSamplesKeepContractTextInsideLiterals(t *testing.T) {
	view, err := readReference(artifact{payload: bundleArtifact(t, shipmentContract)}, "")
	if err != nil || len(view.Operations) != 1 {
		t.Fatalf("reference = %+v, %v", view, err)
	}
	samples := map[string]string{}
	for _, s := range view.Operations[0].Samples {
		samples[s.Language] = s.Code
	}

	curl := samples["cURL"]
	start := strings.Index(curl, "--data '") + len("--data '")
	if start < len("--data '") || !strings.HasSuffix(curl, "'") {
		t.Fatalf("cURL sample has no single-quoted body:\n%s", curl)
	}
	checkSampleBody(t, "cURL", strings.ReplaceAll(curl[start:len(curl)-1], `'\''`, `'`))

	file, err := parser.ParseFile(token.NewFileSet(), "sample.go", samples["Go"], 0)
	if err != nil {
		t.Fatalf("Go sample does not parse: %v\n%s", err, samples["Go"])
	}
	var body string
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "NewReader" {
				body, err = strconv.Unquote(call.Args[0].(*ast.BasicLit).Value)
			}
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	checkSampleBody(t, "Go", body)

	typeScript := samples["TypeScript"]
	start = strings.Index(typeScript, "body: JSON.stringify(") + len("body: JSON.stringify(")
	end := strings.LastIndex(typeScript, "),\n});")
	if start < len("body: JSON.stringify(") || end < start {
		t.Fatalf("TypeScript sample has no body:\n%s", typeScript)
	}
	checkSampleBody(t, "TypeScript", typeScript[start:end])
}

func checkSampleBody(t *testing.T, language, text string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("%s sample body is not the JSON it shows: %v\n%s", language, err, text)
	}
	want := map[string]any{
		"status":      "draft",
		"destination": map[string]any{"city": "string"},
		"parts":       []any{map[string]any{"label": "string", "weight": float64(0)}},
		"note":        hostileNote,
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("%s sample body = %v, want %v without the read-only id", language, body, want)
	}
}

func TestSchemaListingIsBounded(t *testing.T) {
	wide := &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{}}
	for i := range 2 * schemaFields {
		wide.Properties[fmt.Sprintf("field%03d", i)] = openapi3.NewStringSchema().NewRef()
	}
	rows := schemaFieldsOf(wide.NewRef())
	if len(rows) != schemaFields+1 || !strings.Contains(rows[len(rows)-1].Note, "omitted") {
		t.Fatalf("a wide schema listed %d rows, want %d and a closing note", len(rows), schemaFields+1)
	}

	deep := openapi3.NewStringSchema()
	for range 2 * schemaDepth {
		deep = &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{"next": deep.NewRef()}}
	}
	rows = schemaFieldsOf(deep.NewRef())
	last := rows[len(rows)-1]
	if len(rows) != schemaDepth+1 || last.Depth != schemaDepth || !strings.Contains(last.Note, "omitted") {
		t.Fatalf("a deep schema listed %d rows ending %+v, want %d ending with a note", len(rows), last, schemaDepth+1)
	}

	// Import refuses cyclic references, but the listing does not rely on it.
	cyclic := &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{}}
	cyclic.Properties["self"] = &openapi3.SchemaRef{Value: cyclic}
	rows = schemaFieldsOf(cyclic.NewRef())
	if len(rows) != 2 || rows[1].Note != "Repeats an enclosing schema." {
		t.Fatalf("a cyclic schema listed %+v", rows)
	}
	budget := schemaFields
	if value := sampleValue(cyclic, 0, map[*openapi3.Schema]bool{}, &budget); !reflect.DeepEqual(value, map[string]any{"self": nil}) {
		t.Fatalf("a cyclic schema sampled as %v", value)
	}
}
