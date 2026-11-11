// What the generated code makes of security requirements (F07 §4.2
// "安全语义"): an OAuth2 scheme with scopes, a document default, an operation
// that overrides it and an operation that is explicitly anonymous.
//
// These tests are about which requirement reaches the code that must decide
// it. They say nothing about tokens, and a declared scope is not an
// authorization (F04 §3.1).
package security_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"

	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/oastest"
	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/security"
)

// notices records the operations that ran.
type notices struct {
	ran []string
}

func (s *notices) ReadPublicNotice(context.Context, security.ReadPublicNoticeRequestObject) (security.ReadPublicNoticeResponseObject, error) {
	s.ran = append(s.ran, "readPublicNotice")
	return security.ReadPublicNotice200JSONResponse{Text: "public"}, nil
}

func (s *notices) ReadPrivateNotice(context.Context, security.ReadPrivateNoticeRequestObject) (security.ReadPrivateNoticeResponseObject, error) {
	s.ran = append(s.ran, "readPrivateNotice")
	return security.ReadPrivateNotice200JSONResponse{Text: "private"}, nil
}

func (s *notices) ReplacePrivateNotice(context.Context, security.ReplacePrivateNoticeRequestObject) (security.ReplacePrivateNoticeResponseObject, error) {
	s.ran = append(s.ran, "replacePrivateNotice")
	return security.ReplacePrivateNotice204Response{}, nil
}

func generated(operations *notices) http.Handler {
	return security.HandlerFromMux(security.NewStrictHandler(operations, nil), http.NewServeMux())
}

// requirement is one security requirement the validator asked to be decided.
type requirement struct {
	Scheme string
	Scopes []string
}

// authenticator records every requirement it is asked to decide and accepts
// a request that carries any Authorization header.
type authenticator struct {
	asked []requirement
}

func (a *authenticator) authenticate(_ context.Context, input *openapi3filter.AuthenticationInput) error {
	a.asked = append(a.asked, requirement{Scheme: input.SecuritySchemeName, Scopes: input.Scopes})
	if input.RequestValidationInput.Request.Header.Get("Authorization") == "" {
		return errors.New("no credentials")
	}
	return nil
}

var bearer = map[string]string{"Authorization": "Bearer opaque"}

// The generated code enforces no security requirement and does not hand the
// declared scopes to the operation: alone, it serves a private operation to
// a caller without credentials.
func TestTheGeneratedCodeAloneServesAPrivateOperationToAnyone(t *testing.T) {
	operations := &notices{}

	response := oastest.Do(t, generated(operations), oastest.Request{Method: http.MethodGet, Target: "/private/notice"})
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	if want := []string{"readPrivateNotice"}; !reflect.DeepEqual(operations.ran, want) {
		t.Errorf("operations that ran: %v, want %v", operations.ran, want)
	}
}

// Behind the validator each operation's requirement is handed to the
// authenticator, with the scheme and the scopes the contract declares for
// that operation, and the operation runs only if the authenticator accepts.
// An operation declared anonymous never reaches the authenticator.
func TestTheValidatorHandsEachRequirementToTheAuthenticator(t *testing.T) {
	cases := []struct {
		name    string
		request oastest.Request
		status  int
		asked   []requirement
		ran     []string
	}{
		{"an operation declared anonymous",
			oastest.Request{Method: http.MethodGet, Target: "/public/notice"},
			http.StatusOK, nil, []string{"readPublicNotice"}},
		{"the document default, without credentials",
			oastest.Request{Method: http.MethodGet, Target: "/private/notice"},
			http.StatusUnauthorized, []requirement{{"serviceOAuth", []string{"contracts:read"}}}, nil},
		{"the document default, with credentials",
			oastest.Request{Method: http.MethodGet, Target: "/private/notice", Header: bearer},
			http.StatusOK, []requirement{{"serviceOAuth", []string{"contracts:read"}}}, []string{"readPrivateNotice"}},
		{"an operation that overrides the default",
			oastest.Request{Method: http.MethodPut, Target: "/private/notice", Body: `{"text":"t"}`, Header: bearer},
			http.StatusNoContent, []requirement{{"serviceOAuth", []string{"contracts:write"}}}, []string{"replacePrivateNotice"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operations, authenticator := &notices{}, &authenticator{}
			validated := oastest.Validated(t, "security", generated(operations), authenticator.authenticate)

			response := oastest.Do(t, validated, c.request)
			if response.Code != c.status {
				t.Fatalf("status %d, want %d: %s", response.Code, c.status, response.Body)
			}
			if !reflect.DeepEqual(authenticator.asked, c.asked) {
				t.Errorf("the authenticator was asked %v, want %v", authenticator.asked, c.asked)
			}
			if !reflect.DeepEqual(operations.ran, c.ran) {
				t.Errorf("operations that ran: %v, want %v", operations.ran, c.ran)
			}
		})
	}
}

// A validator that was given no authenticator refuses an operation with a
// security requirement instead of serving it.
func TestWithoutAnAuthenticatorAPrivateOperationIsRefused(t *testing.T) {
	operations := &notices{}
	validated := oastest.Validated(t, "security", generated(operations), nil)

	response := oastest.Do(t, validated, oastest.Request{Method: http.MethodGet, Target: "/private/notice", Header: bearer})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", response.Code, response.Body)
	}
	if len(operations.ran) != 0 {
		t.Errorf("operations that ran: %v, want none", operations.ran)
	}
}
