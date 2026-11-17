package http

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// A change of the brand is handed to its use case as the verified caller,
// with the brand the body names and the version If-Match names. The answer
// shows the product as it is now, and carries its version as its ETag.
func TestChangeProductBrandHandsTheRequestToItsUseCase(t *testing.T) {
	s := newService(t)

	changed := s.do(t, call{Method: http.MethodPut, Target: productBrand, Token: "alice", Body: brandChange, Header: fromVersion4})
	if changed.Code != http.StatusOK || !reflect.DeepEqual(members(t, changed), productBody) {
		t.Errorf("PUT brand: %d %s", changed.Code, changed.Body)
	}
	if got := changed.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("PUT brand: Cache-Control %q, want no-store", got)
	}
	if got := changed.Header().Get("ETag"); got != `"4"` {
		t.Errorf("PUT brand: ETag %q, want the version of the product the use case answered, in double quotes", got)
	}

	g := s.governance
	if len(g.brandsChanged) != 1 || g.calls() != 1 {
		t.Fatalf("the use case was called %d times of %d calls, want once and no other", len(g.brandsChanged), g.calls())
	}
	command := g.brandsChanged[0]
	if !uuid.MatchString(command.CorrelationID) {
		t.Errorf("correlation identifier %q, want one the server generated", command.CorrelationID)
	}
	command.CorrelationID = ""
	want := application.ChangeProductBrand{
		// The caller is who the token is about, and the product is the one
		// of the path.
		Caller: domain.Principal(alice), ProductID: productID,
		Brand:   domain.Brand{Name: "CargoPai Developers", LogoURL: brandLogo, PrimaryColor: "#0A7B3C"},
		Version: 4,
	}
	if command != want {
		t.Errorf("ChangeProductBrand %+v, want %+v", command, want)
	}
}

// A brand is replaced as a whole: what the body leaves out, the use case is
// asked to take away. And a product without a logo or a colour is shown
// without one, wherever a product is shown.
func TestABrandWithoutALogoOrAColourHasNeither(t *testing.T) {
	s := newService(t)
	s.governance.product.Brand = domain.Brand{Name: "CargoPai"}

	for _, operation := range []call{
		{Method: http.MethodPost, Target: "/v1/products", Body: registration},
		{Method: http.MethodGet, Target: "/v1/products/" + productID},
		{Method: http.MethodPut, Target: productBrand, Body: `{"name":"CargoPai"}`, Header: fromVersion4},
		{Method: http.MethodGet, Target: "/v1/products"},
	} {
		operation.Token = "alice"
		response := s.do(t, operation)
		if response.Code >= 300 {
			t.Fatalf("%s %s: status %d: %s", operation.Method, operation.Target, response.Code, response.Body)
		}
		body := response.Body.String()
		if !strings.Contains(body, `"brand":{"name":"CargoPai"}`) || strings.Contains(body, "logo_url") || strings.Contains(body, "primary_color") {
			t.Errorf("%s %s: the answer shows the brand otherwise than as a name alone: %s", operation.Method, operation.Target, body)
		}
	}
	if got := s.governance.brandsChanged[0]; got.Brand != (domain.Brand{Name: "CargoPai"}) || got.Version != 4 {
		t.Errorf("ChangeProductBrand %+v, want a change to the name alone from version 4", got)
	}
}

// A change of the brand says which version of the product it was made from,
// as a change of an audience configuration says its own. One that does not
// is refused, not made: it would overwrite whatever changed since the caller
// read. Who sends it is still asked first.
func TestAChangeOfTheBrandNamesTheVersionOfTheProduct(t *testing.T) {
	s := newService(t)
	change := call{Method: http.MethodPut, Target: productBrand, Body: brandChange}

	wantProblem(t, s.do(t, change), http.StatusUnauthorized, CodeCredentialsMissing, "")
	change.Token = "mallory"
	wantProblem(t, s.do(t, change), http.StatusUnauthorized, CodeTokenInvalid, "")
	change.Token = "alice"
	wantProblem(t, s.do(t, change), http.StatusPreconditionRequired, CodePreconditionRequired,
		"A change needs If-Match: the ETag of what it changes, as it was read.")

	// It names one version, as it was handed out: not any, not one that is
	// merely alike, not one of several.
	for _, value := range []string{"", "*", `W/"4"`, `"4", "5"`, `4`, `"0"`, `""`, `"canary"`} {
		change.Header = map[string]string{"If-Match": value}
		response := s.do(t, change)
		wantProblem(t, response, http.StatusBadRequest, CodeRequestInvalid, "The header parameter If-Match does not match the contract.")
		if strings.Contains(response.Body.String(), "canary") || strings.Contains(s.log.String(), "canary") {
			t.Errorf("If-Match %s: the answer or the log repeats what was sent", value)
		}
	}
	if s.governance.calls() != 0 {
		t.Errorf("%d changes that name no single version reached a use case", s.governance.calls())
	}

	for tag, version := range map[string]int64{`"1"`: 1, `"4"`: 4, `"42"`: 42} {
		s.governance.brandsChanged = nil
		change.Header = map[string]string{"If-Match": tag}
		response := s.do(t, change)
		if response.Code != http.StatusOK {
			t.Fatalf("If-Match %s: status %d, want 200: %s", tag, response.Code, response.Body)
		}
		if len(s.governance.brandsChanged) != 1 || s.governance.brandsChanged[0].Version != version {
			t.Errorf("If-Match %s: the use case was asked %+v, want one change from version %d", tag, s.governance.brandsChanged, version)
		}
	}
}

// A list shows a product as a single read does, with its brand. It carries
// no version: a change is made from the product as it was read on its own.
func TestAListShowsAProductWithItsBrand(t *testing.T) {
	s := newService(t)

	response := s.do(t, call{Method: http.MethodGet, Target: "/v1/products", Token: "alice"})
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	items, _ := members(t, response)["items"].([]any)
	if len(items) != 1 || !reflect.DeepEqual(items[0], any(productBody)) {
		t.Errorf("the list holds %v, want the product with its brand", items)
	}
	if got := response.Header().Get("ETag"); got != "" {
		t.Errorf("the list carries the ETag %q, want none", got)
	}
}
