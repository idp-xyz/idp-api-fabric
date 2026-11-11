// What the generated code makes of anyOf over object schemas (F07 §4.2
// "组合 Schema").
package anyof_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/anyof"
	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/oastest"
)

// contacts keeps the contact it was sent for the test.
type contacts struct {
	received *anyof.Contact
}

func (s *contacts) StoreContact(_ context.Context, request anyof.StoreContactRequestObject) (anyof.StoreContactResponseObject, error) {
	s.received = request.Body
	return anyof.StoreContact200JSONResponse(*request.Body), nil
}

func servers(t *testing.T) (operations *contacts, generated, validated http.Handler) {
	t.Helper()

	operations = &contacts{}
	generated = anyof.HandlerFromMux(anyof.NewStrictHandler(operations, nil), http.NewServeMux())
	return operations, generated, oastest.Validated(t, "anyof", generated, nil)
}

func post(body string) oastest.Request {
	return oastest.Request{Method: http.MethodPost, Target: "/contacts", Body: body}
}

// anyOf becomes a union that holds the raw JSON and is read through one
// accessor per variant.
func TestAnyOfIsReadThroughOneAccessorPerVariant(t *testing.T) {
	operations, generated, validated := servers(t)

	for name, handler := range map[string]http.Handler{"generated": generated, "validated": validated} {
		response := oastest.Do(t, handler, post(`{"email":"e@example.org","phone":"1"}`))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200: %s", name, response.Code, response.Body)
		}
		email, errEmail := operations.received.AsByEmail()
		phone, errPhone := operations.received.AsByPhone()
		if errEmail != nil || errPhone != nil || email.Email != "e@example.org" || phone.Phone != "1" {
			t.Errorf("%s: the union reads as %+v (%v) and %+v (%v)", name, email, errEmail, phone, errPhone)
		}
	}
}

// A hazard for operations: an accessor does not tell whether the value is its
// variant. For a value that matches no variant, the generated code alone
// reaches the operation, and every accessor succeeds with a zero value.
func TestAValueThatMatchesNoVariantIsOnlyRefusedByTheValidator(t *testing.T) {
	operations, generated, validated := servers(t)

	if got := oastest.Do(t, generated, post(`{"fax":"1"}`)); got.Code != http.StatusOK {
		t.Fatalf("the generated code alone answers %d, want 200: %s", got.Code, got.Body)
	}
	email, errEmail := operations.received.AsByEmail()
	phone, errPhone := operations.received.AsByPhone()
	if errEmail != nil || errPhone != nil || email != (anyof.ByEmail{}) || phone != (anyof.ByPhone{}) {
		t.Errorf("the accessors yield %+v (%v) and %+v (%v), want two zero values and no error", email, errEmail, phone, errPhone)
	}

	got := oastest.Do(t, validated, post(`{"fax":"1"}`))
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), `doesn't match any schema from "anyOf"`) {
		t.Errorf("behind the validator: %d %s\nwant 400 for matching no variant", got.Code, got.Body)
	}
}
