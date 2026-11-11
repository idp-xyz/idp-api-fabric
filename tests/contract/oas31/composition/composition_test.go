// What the generated code makes of composed schemas (F07 §4.2 "组合 Schema"):
// oneOf with a discriminator, allOf, const, the two spellings of an
// enumeration and the three forms of additionalProperties.
package composition_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/composition"
	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/oastest"
)

// documents keeps what it was sent for the test.
type documents struct {
	variant    any
	variantErr error
	document   *composition.Document
}

func (s *documents) RecordEvent(_ context.Context, request composition.RecordEventRequestObject) (composition.RecordEventResponseObject, error) {
	s.variant, s.variantErr = request.Body.ValueByDiscriminator()
	kind, _ := request.Body.Discriminator()
	return composition.RecordEvent200JSONResponse{Kind: kind}, nil
}

func (s *documents) StoreDocument(_ context.Context, request composition.StoreDocumentRequestObject) (composition.StoreDocumentResponseObject, error) {
	s.document = request.Body
	return composition.StoreDocument200JSONResponse(*request.Body), nil
}

func servers(t *testing.T) (operations *documents, generated, validated http.Handler) {
	t.Helper()

	operations = &documents{}
	generated = composition.HandlerFromMux(composition.NewStrictHandler(operations, nil), http.NewServeMux())
	return operations, generated, oastest.Validated(t, "composition", generated, nil)
}

func post(target, body string) oastest.Request {
	return oastest.Request{Method: http.MethodPost, Target: target, Body: body}
}

// document is a valid document with the given members replaced.
func document(t *testing.T, replaced map[string]any) string {
	t.Helper()

	members := map[string]any{
		"spec_version": "3.1.2",
		"audited":      map[string]any{"id": "i", "actor": "a"},
		"labels":       map[string]any{"k": "v"},
		"closed":       map[string]any{"known": "k"},
		"open":         map[string]any{"known": "k", "extra": 1},
		"state":        "DRAFT",
		"review_state": "REJECTED",
	}
	for name, value := range replaced {
		members[name] = value
	}
	encoded, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// oneOf with a discriminator becomes a union the operation resolves itself:
// ValueByDiscriminator returns the variant the discriminator names.
func TestADiscriminatedUnionYieldsTheVariantItsDiscriminatorNames(t *testing.T) {
	operations, generated, _ := servers(t)

	response := oastest.Do(t, generated, post("/events", `{"kind":"revoked","reason":"superseded"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	want := composition.Revoked{Kind: composition.RevokedKindRevoked, Reason: "superseded"}
	if operations.variantErr != nil || operations.variant != want {
		t.Errorf("the union yields %#v, %v, want %#v", operations.variant, operations.variantErr, want)
	}
}

// allOf is flattened into one struct, a map-valued additionalProperties is a
// Go map, an open object keeps what the contract does not name, and both
// spellings of an enumeration become a named string type with its values.
func TestComposedSchemasBecomePlainGoTypes(t *testing.T) {
	operations, generated, _ := servers(t)

	sent := document(t, nil)
	response := oastest.Do(t, generated, post("/documents", sent))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}

	known := "k"
	want := composition.Document{
		SpecVersion: composition.DocumentSpecVersionN312,
		Audited:     composition.Audited{ID: "i", Actor: "a"},
		Labels:      composition.Labels{"k": "v"},
		Closed:      composition.Closed{Known: "k"},
		Open:        composition.Open{Known: &known, AdditionalProperties: map[string]any{"extra": float64(1)}},
		State:       composition.StateDraft,
		ReviewState: composition.ReviewStateREJECTED,
	}
	if got := *operations.document; !reflect.DeepEqual(got, want) {
		t.Errorf("the document was read as\n %+v\nwant\n %+v", got, want)
	}

	var written, original map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &written); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(sent), &original); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, original) {
		t.Errorf("the document was written back as %v, want %v", written, original)
	}
}

// const and both spellings of an enumeration get a Valid method. Nothing
// generated calls it: an operation that wants the check has to.
func TestEnumerationsCarryACheckTheGeneratedCodeDoesNotMake(t *testing.T) {
	if !composition.StateDraft.Valid() || composition.State("NOPE").Valid() {
		t.Error("State.Valid must hold for DRAFT and fail for NOPE")
	}
	if !composition.ReviewStateREJECTED.Valid() || composition.ReviewState("NOPE").Valid() {
		t.Error("ReviewState.Valid must hold for REJECTED and fail for NOPE")
	}
	if !composition.DocumentSpecVersionN312.Valid() || composition.DocumentSpecVersion("3.0.0").Valid() {
		t.Error("DocumentSpecVersion.Valid must hold for 3.1.2 and fail for 3.0.0")
	}
}

// What the contract forbids. The generated code alone accepts all of it but a
// value of the wrong JSON type; only a validator refuses the rest.
func TestWhatTheContractForbids(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		body      string
		generated int    // the status from the generated code alone
		reason    string // why the validator refuses the request
	}{
		{"a discriminator value no variant has", "/events",
			`{"kind":"other","release_id":"r1"}`,
			http.StatusOK, "value must be 'published'"},
		{"a variant without a property it requires", "/events",
			`{"kind":"revoked"}`,
			http.StatusOK, "missing property 'reason'"},
		{"another value than the const", "/documents",
			document(t, map[string]any{"spec_version": "3.0.0"}),
			http.StatusOK, "value must be '3.1.2'"},
		{"a value outside an enumeration written as oneOf + const", "/documents",
			document(t, map[string]any{"state": "NOPE"}),
			http.StatusOK, "value must be 'DRAFT'"},
		{"a value outside an enumeration written with enum", "/documents",
			document(t, map[string]any{"review_state": "NOPE"}),
			http.StatusOK, "value must be one of 'IN_REVIEW', 'REJECTED'"},
		{"a member a closed object does not declare", "/documents",
			document(t, map[string]any{"closed": map[string]any{"known": "k", "extra": 1}}),
			http.StatusOK, "additional properties 'extra' not allowed"},
		{"a member only one part of an allOf requires", "/documents",
			document(t, map[string]any{"audited": map[string]any{"id": "i"}}),
			http.StatusOK, "missing property 'actor'"},
		{"a value of another type under additionalProperties", "/documents",
			document(t, map[string]any{"labels": map[string]any{"k": 1}}),
			http.StatusBadRequest, "got number, want string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, generated, validated := servers(t)

			if got := oastest.Do(t, generated, post(c.target, c.body)); got.Code != c.generated {
				t.Errorf("the generated code alone answers %d, want %d: %s", got.Code, c.generated, got.Body)
			}
			got := oastest.Do(t, validated, post(c.target, c.body))
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), c.reason) {
				t.Errorf("behind the validator: %d %s\nwant 400 with %q", got.Code, got.Body, c.reason)
			}
		})
	}
}

// A hazard for operations: for a discriminator value no variant has, the
// union reaches the operation and only fails when it is resolved.
func TestAnUnknownDiscriminatorReachesTheOperation(t *testing.T) {
	operations, generated, _ := servers(t)

	response := oastest.Do(t, generated, post("/events", `{"kind":"other"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	if operations.variantErr == nil || !strings.Contains(operations.variantErr.Error(), "unknown discriminator value: other") {
		t.Errorf("resolving the union yields %v, %v, want an unknown discriminator error", operations.variant, operations.variantErr)
	}
}
