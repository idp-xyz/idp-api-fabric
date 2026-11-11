// Package oastest sends requests to the servers generated from the sample
// contracts: to the generated code alone, and to the same code behind a
// validator that checks every request against the contract it came from.
//
// The difference between the two answers is what the generated code does not
// enforce by itself.
package oastest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	middleware "github.com/oapi-codegen/nethttp-middleware"
)

// Request is one request to a generated server. A header named Cookie holds
// one name=value pair.
type Request struct {
	Method string
	Target string
	Body   string
	Header map[string]string
}

// Do sends request to handler and returns what it answered.
func Do(t *testing.T, handler http.Handler, request Request) *httptest.ResponseRecorder {
	t.Helper()

	sent := httptest.NewRequest(request.Method, request.Target, strings.NewReader(request.Body))
	if request.Body != "" {
		sent.Header.Set("Content-Type", "application/json")
	}
	for name, value := range request.Header {
		sent.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, sent)
	return recorder
}

// Contract loads samples/<sample>.yaml, next to the package under test, and
// fails the test unless it is a valid OpenAPI document. References that leave
// the file are not followed.
func Contract(t *testing.T, sample string) *openapi3.T {
	t.Helper()

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Join("..", "samples", sample+".yaml"))
	if err != nil {
		t.Fatalf("load the sample contract: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("the sample contract is not a valid OpenAPI document: %v", err)
	}
	return document
}

// Validated puts handler behind a validator of the sample contract. A request
// the contract does not allow is answered with the status the validator
// chooses and, as the body, its reason. authenticate decides the operations
// that declare a security requirement; it may be nil for a contract that
// declares none.
//
// Echoing the reason is for these tests only: for an OpenAPI 3.1 contract it
// quotes the offending value, which a service must not send back.
func Validated(t *testing.T, sample string, handler http.Handler, authenticate openapi3filter.AuthenticationFunc) http.Handler {
	t.Helper()

	return middleware.OapiRequestValidatorWithOptions(Contract(t, sample), &middleware.Options{
		Options: openapi3filter.Options{AuthenticationFunc: authenticate},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
			http.Error(w, err.Error(), opts.StatusCode)
		},
	})(handler)
}
