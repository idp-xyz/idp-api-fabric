// What the generated code makes of presence and null (F07 §4.2 "OAS3.1 基础",
// EG-09): type: [T, "null"], required against optional, readOnly and
// writeOnly.
package nullability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/nullability"
	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/oastest"
)

// things answers with the thing it was sent and keeps it for the test.
type things struct {
	received *nullability.Thing
}

func (s *things) CreateThing(_ context.Context, request nullability.CreateThingRequestObject) (nullability.CreateThingResponseObject, error) {
	s.received = request.Body
	return nullability.CreateThing200JSONResponse(*request.Body), nil
}

func servers(t *testing.T) (operations *things, generated, validated http.Handler) {
	t.Helper()

	operations = &things{}
	generated = nullability.HandlerFromMux(nullability.NewStrictHandler(operations, nil), http.NewServeMux())
	return operations, generated, oastest.Validated(t, "nullability", generated, nil)
}

func post(body string) oastest.Request {
	return oastest.Request{Method: http.MethodPost, Target: "/things", Body: body}
}

func members(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, body)
	}
	return decoded
}

// A nullable property is a nullable.Nullable[T], which keeps the three states
// of the member apart and writes each back as it was read. That holds for a
// required property and for an optional one, and for the "oneOf: [$ref, null]"
// spelling as well as for "type: [T, null]".
func TestANullableMemberIsAbsentNullOrAValue(t *testing.T) {
	operations, generated, _ := servers(t)

	sent := `{"required_string":"a","required_nullable":null,"required_nullable_count":7,"optional_nullable_ref":null}`
	response := oastest.Do(t, generated, post(sent))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}

	thing := operations.received
	if !thing.RequiredNullable.IsSpecified() || !thing.RequiredNullable.IsNull() {
		t.Errorf("required_nullable was sent as null, got specified=%t null=%t",
			thing.RequiredNullable.IsSpecified(), thing.RequiredNullable.IsNull())
	}
	if count, err := thing.RequiredNullableCount.Get(); err != nil || count != 7 {
		t.Errorf("required_nullable_count was sent as 7, got %d, %v", count, err)
	}
	if thing.OptionalNullable.IsSpecified() {
		t.Errorf("optional_nullable was not sent, got specified with null=%t", thing.OptionalNullable.IsNull())
	}
	if !thing.OptionalNullableRef.IsSpecified() || !thing.OptionalNullableRef.IsNull() {
		t.Errorf("optional_nullable_ref was sent as null, got specified=%t null=%t",
			thing.OptionalNullableRef.IsSpecified(), thing.OptionalNullableRef.IsNull())
	}
	if thing.OptionalString != nil {
		t.Errorf("optional_string was not sent, got %q", *thing.OptionalString)
	}

	if got, want := members(t, response.Body.Bytes()), members(t, []byte(sent)); !reflect.DeepEqual(got, want) {
		t.Errorf("the thing was written back as %v, want %v", got, want)
	}
}

// What the contract forbids. The generated code alone accepts most of it and
// hands the operation a zero value; only a validator refuses it.
func TestWhatTheContractForbids(t *testing.T) {
	const rest = `"required_nullable":"x","required_nullable_count":1`
	cases := []struct {
		name      string
		body      string
		generated int    // the status from the generated code alone
		reason    string // why the validator refuses the request
	}{
		{"a required property is absent",
			`{"required_string":"a","required_nullable_count":1}`,
			http.StatusOK, `property "required_nullable" is missing`},
		{"null for a required property that is not nullable",
			`{"required_string":null,` + rest + `}`,
			http.StatusOK, "Value is not nullable"},
		{"null for an optional property that is not nullable",
			`{"required_string":"a",` + rest + `,"optional_string":null}`,
			http.StatusOK, "Value is not nullable"},
		{"a readOnly property in a request",
			`{"required_string":"a",` + rest + `,"server_assigned":"s"}`,
			http.StatusOK, `readOnly property "server_assigned" in request`},
		{"a value of another type",
			`{"required_string":"a","required_nullable":"x","required_nullable_count":"1"}`,
			http.StatusBadRequest, "value must be one of integer, null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, generated, validated := servers(t)

			if got := oastest.Do(t, generated, post(c.body)); got.Code != c.generated {
				t.Errorf("the generated code alone answers %d, want %d: %s", got.Code, c.generated, got.Body)
			}
			got := oastest.Do(t, validated, post(c.body))
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), c.reason) {
				t.Errorf("behind the validator: %d %s\nwant 400 with %q", got.Code, got.Body, c.reason)
			}
		})
	}
}

// A property the contract does not name is allowed, since the schema does not
// close the object. It is dropped rather than kept.
func TestAnUnknownMemberIsAcceptedAndDropped(t *testing.T) {
	_, generated, validated := servers(t)

	body := `{"required_string":"a","required_nullable":"x","required_nullable_count":1,"unknown":true}`
	for name, handler := range map[string]http.Handler{"generated": generated, "validated": validated} {
		response := oastest.Do(t, handler, post(body))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200: %s", name, response.Code, response.Body)
		}
		if _, kept := members(t, response.Body.Bytes())["unknown"]; kept {
			t.Errorf("%s: the unknown member was written back", name)
		}
	}
}

// A hazard for operations: a required nullable member the code never set is
// written as the zero value of its type, which is neither null nor absent.
func TestAnUnsetRequiredNullableIsWrittenAsItsZeroValue(t *testing.T) {
	written, err := json.Marshal(nullability.Thing{RequiredString: "a"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"required_string": "a", "required_nullable": "", "required_nullable_count": float64(0)}
	if got := members(t, written); !reflect.DeepEqual(got, want) {
		t.Errorf("an unset thing is written as %v, want %v", got, want)
	}
}

// readOnly and writeOnly do not shape the types: one Go type serves the
// request and the response, so a writeOnly member an operation leaves set is
// written back to the caller.
func TestAWriteOnlyMemberIsWrittenInAResponse(t *testing.T) {
	_, generated, _ := servers(t)

	response := oastest.Do(t, generated,
		post(`{"required_string":"a","required_nullable":"x","required_nullable_count":1,"secret":"w"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	if got := members(t, response.Body.Bytes())["secret"]; got != "w" {
		t.Errorf("secret in the response is %v, want it written back as sent", got)
	}
}
