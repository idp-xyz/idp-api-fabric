// What the generated code makes of HTTP semantics (F07 §4.2 "HTTP 语义"):
// parameters in the path, the query, a header and a cookie, their
// serialization, constraints and defaults, and responses with headers and
// with an application/problem+json body.
package httpsemantics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/httpsemantics"
	"go.idp.xyz/idp-api-fabric/tests/contract/oas31/oastest"
)

// assets keeps the request it was sent and picks its response by asset.
type assets struct {
	received httpsemantics.ReplaceAssetRequestObject
}

func (s *assets) ReplaceAsset(_ context.Context, request httpsemantics.ReplaceAssetRequestObject) (httpsemantics.ReplaceAssetResponseObject, error) {
	s.received = request
	problem := func(status int, code string) httpsemantics.Problem {
		return httpsemantics.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, ErrorCode: code}
	}
	switch request.AssetID {
	case "stale":
		return httpsemantics.ReplaceAsset412ApplicationProblemPlusJSONResponse{
			ProblemApplicationProblemPlusJSONResponse: httpsemantics.ProblemApplicationProblemPlusJSONResponse(
				problem(http.StatusPreconditionFailed, "GENERATION_MISMATCH")),
		}, nil
	case "unconditional":
		return httpsemantics.ReplaceAsset428ApplicationProblemPlusJSONResponse(
			problem(http.StatusPreconditionRequired, "PRECONDITION_REQUIRED")), nil
	case "down":
		return httpsemantics.ReplaceAssetdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusServiceUnavailable,
			Body:       problem(http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"),
		}, nil
	}
	return httpsemantics.ReplaceAsset200JSONResponse{
		Body:    *request.Body,
		Headers: httpsemantics.ReplaceAsset200ResponseHeaders{ETag: `"7"`},
	}, nil
}

func servers(t *testing.T) (operations *assets, generated, validated http.Handler) {
	t.Helper()

	operations = &assets{}
	generated = httpsemantics.HandlerFromMux(httpsemantics.NewStrictHandler(operations, nil), http.NewServeMux())
	return operations, generated, oastest.Validated(t, "httpsemantics", generated, nil)
}

func put(target, body string, header map[string]string) oastest.Request {
	return oastest.Request{Method: http.MethodPut, Target: target, Body: body, Header: header}
}

var ifMatch = map[string]string{"If-Match": `"6"`}

// Parameters are bound from every location the contract names: the path, a
// query array that is not exploded, an integer in the query, a required and
// an optional header, and a cookie.
func TestParametersAreBoundFromEveryLocation(t *testing.T) {
	operations, generated, _ := servers(t)

	response := oastest.Do(t, generated, put("/assets/a1?tags=x,y&limit=5", `{"name":"n"}`, map[string]string{
		"If-Match": `"6"`, "Idempotency-Key": "key-12345", "Cookie": "session=abc",
	}))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}

	tags, limit, key, session := []string{"x", "y"}, 5, "key-12345", "abc"
	want := httpsemantics.ReplaceAssetRequestObject{
		AssetID: "a1",
		Params: httpsemantics.ReplaceAssetParams{
			Tags: &tags, Limit: &limit, IfMatch: `"6"`, IdempotencyKey: &key, Session: &session,
		},
		Body: &httpsemantics.Asset{Name: "n"},
	}
	if got := operations.received; !reflect.DeepEqual(got, want) {
		t.Errorf("the operation received\n %+v\nwant\n %+v", got, want)
	}
}

// A default the contract declares is not applied by the generated code: the
// operation sees an absent parameter. The validator writes the default into
// the request before the generated code reads it.
func TestADefaultIsAppliedByTheValidatorOnly(t *testing.T) {
	operations, generated, validated := servers(t)

	oastest.Do(t, generated, put("/assets/a1", `{"name":"n"}`, ifMatch))
	if got := operations.received.Params.Limit; got != nil {
		t.Errorf("the generated code alone hands the operation limit=%d, want it absent", *got)
	}

	oastest.Do(t, validated, put("/assets/a1", `{"name":"n"}`, ifMatch))
	if got := operations.received.Params.Limit; got == nil || *got != 20 {
		t.Errorf("behind the validator the operation receives limit=%v, want the default 20", got)
	}
}

// What the contract forbids. The generated code alone refuses a missing
// required header and a value it cannot parse, and no constraint.
func TestWhatTheContractForbids(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		body      string
		header    map[string]string
		generated int    // the status from the generated code alone
		reason    string // why the validator refuses the request
	}{
		{"a required header is absent", "/assets/a1", `{"name":"n"}`, nil,
			http.StatusBadRequest, `parameter "If-Match" in header has an error: value is required but missing`},
		{"a query value that is not an integer", "/assets/a1?limit=abc", `{"name":"n"}`, ifMatch,
			http.StatusBadRequest, `parameter "limit" in query has an error`},
		{"a query value above its maximum", "/assets/a1?limit=500", `{"name":"n"}`, ifMatch,
			http.StatusOK, "maximum: got 500, want 100"},
		{"a header shorter than its minLength", "/assets/a1", `{"name":"n"}`, map[string]string{"If-Match": "x"},
			http.StatusOK, "minLength: got 1, want 3"},
		{"a body member shorter than its minLength", "/assets/a1", `{"name":""}`, ifMatch,
			http.StatusOK, "minLength: got 0, want 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, generated, validated := servers(t)

			if got := oastest.Do(t, generated, put(c.target, c.body, c.header)); got.Code != c.generated {
				t.Errorf("the generated code alone answers %d, want %d: %s", got.Code, c.generated, got.Body)
			}
			got := oastest.Do(t, validated, put(c.target, c.body, c.header))
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), c.reason) {
				t.Errorf("behind the validator: %d %s\nwant 400 with %q", got.Code, got.Body, c.reason)
			}
		})
	}
}

// Every declared response has a Go type that fixes its status and its media
// type, and a response header is a field of the response.
//
// The types are uneven: of two statuses that refer to the same shared
// response, the first embeds a type named after the shared response and the
// second is the schema type itself.
func TestResponsesCarryTheirStatusMediaTypeAndHeaders(t *testing.T) {
	cases := []struct {
		asset       string
		status      int
		contentType string
		etag        string
		members     map[string]any
	}{
		{"a1", http.StatusOK, "application/json", `"7"`, map[string]any{"name": "n"}},
		{"stale", http.StatusPreconditionFailed, "application/problem+json", "", map[string]any{
			"type": "about:blank", "title": "Precondition Failed", "status": float64(412), "error_code": "GENERATION_MISMATCH"}},
		{"unconditional", http.StatusPreconditionRequired, "application/problem+json", "", map[string]any{
			"type": "about:blank", "title": "Precondition Required", "status": float64(428), "error_code": "PRECONDITION_REQUIRED"}},
		{"down", http.StatusServiceUnavailable, "application/problem+json", "", map[string]any{
			"type": "about:blank", "title": "Service Unavailable", "status": float64(503), "error_code": "DEPENDENCY_UNAVAILABLE"}},
	}
	for _, c := range cases {
		t.Run(c.asset, func(t *testing.T) {
			_, generated, _ := servers(t)

			response := oastest.Do(t, generated, put("/assets/"+c.asset, `{"name":"n"}`, ifMatch))
			if response.Code != c.status {
				t.Fatalf("status %d, want %d: %s", response.Code, c.status, response.Body)
			}
			if got := response.Header().Get("Content-Type"); got != c.contentType {
				t.Errorf("Content-Type %q, want %q", got, c.contentType)
			}
			if got := response.Header().Get("ETag"); got != c.etag {
				t.Errorf("ETag %q, want %q", got, c.etag)
			}
			var members map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &members); err != nil {
				t.Fatalf("the body is not a JSON object: %v\n%s", err, response.Body)
			}
			if !reflect.DeepEqual(members, c.members) {
				t.Errorf("body %v, want %v", members, c.members)
			}
		})
	}
}

// The generated code answers what it refuses itself in plain text that quotes
// the request, unless its error handlers are replaced. The management API
// replaces them, since every error of that API is a problem.
func TestTheGeneratedCodeAnswersItsOwnRefusalsInPlainText(t *testing.T) {
	_, generated, _ := servers(t)

	response := oastest.Do(t, generated, put("/assets/a1?limit=secret-value", `{"name":"n"}`, ifMatch))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type %q, want text/plain", got)
	}
	if !strings.Contains(response.Body.String(), "secret-value") {
		t.Errorf("the default answer was expected to quote the request, got %q", response.Body)
	}
}
