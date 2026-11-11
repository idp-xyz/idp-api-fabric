package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	validator "github.com/oapi-codegen/nethttp-middleware"

	"go.idp.xyz/idp-api-fabric/api"
)

// loadContract reads the contract this build carries. A reference that
// leaves the document is not followed, and a document that is not valid
// OpenAPI keeps the process from starting.
func loadContract() (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	contract, err := loader.LoadFromData(api.Contract())
	if err != nil {
		return nil, fmt.Errorf("load the contract of the API: %w", err)
	}
	if err := contract.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("the contract of the API is not valid: %w", err)
	}
	return contract, nil
}

// validation puts a handler behind the contract. The generated code binds a
// request and enforces close to nothing of what the contract says about it:
// no required member, no pattern, no bound, no security requirement
// (tests/contract/oas31). The validator does, with the same document the
// code was generated from, before the operation runs.
type validation struct {
	logger *slog.Logger
}

// validated returns next behind the validator of contract. A request is
// checked in this order: who sends it, its parameters, its body. A request
// that fails is answered here and does not reach next.
func (v validation) validated(contract *openapi3.T, authenticate openapi3filter.AuthenticationFunc, next http.Handler) (http.Handler, error) {
	// The validator builds this router itself and panics if it cannot. It is
	// built here first, so that a contract it cannot route is an error.
	if _, err := gorillamux.NewRouter(contract); err != nil {
		return nil, fmt.Errorf("the contract of the API cannot be routed: %w", err)
	}
	return validator.OapiRequestValidatorWithOptions(contract, &validator.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authenticate,
			// A body sent to an operation that declares none is refused.
			RejectWhenRequestBodyNotSpecified: true,
		},
		ErrorHandlerWithOpts:  v.refuse,
		SilenceServersWarning: true,
	})(next), nil
}

// refuse answers a request the validator did not let through.
//
// What the validator reports quotes the request: the value of a parameter, a
// member of the body. None of it is sent back or logged. The answer names the
// place and the keyword that failed, both of which come from the contract.
func (v validation) refuse(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts validator.ErrorHandlerOpts) {
	var security *openapi3filter.SecurityRequirementsError
	var invalid *openapi3filter.RequestError
	var tooLarge *http.MaxBytesError

	switch {
	case opts.MatchedRoute == nil:
		// The mux routed what the contract does not, such as a path whose
		// parameter holds an encoded separator.
		answer(w, r, problem(http.StatusNotFound, CodeRouteNotFound, ""))

	case errors.As(err, &security):
		// The validator reads the body before it asks who sends the request,
		// and reports a body it could not read as a failed security
		// requirement. Nobody was asked to authenticate then, and the request
		// is answered for what is wrong with it.
		for _, cause := range security.Errors {
			if errors.As(cause, &tooLarge) {
				v.tooLarge(w, r)
				return
			}
			if errors.As(cause, &invalid) {
				answer(w, r, problem(http.StatusBadRequest, CodeRequestInvalid, "The request body could not be read."))
				return
			}
		}
		v.unauthenticated(w, r)

	case errors.As(err, &tooLarge):
		v.tooLarge(w, r)

	case errors.As(err, &invalid):
		// A body that is there and is of a media type the operation does not
		// declare. The validator looks the type up the same way.
		if body := invalid.RequestBody; body != nil && !errors.Is(invalid.Err, openapi3filter.ErrInvalidRequired) &&
			body.Content.Get(r.Header.Get("Content-Type")) == nil {
			answer(w, r, problem(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
				"The request body is not of a media type the operation accepts."))
			return
		}
		answer(w, r, problem(http.StatusBadRequest, CodeRequestInvalid, describe(invalid)))

	default:
		v.internal(w, r, "the validator failed", nil)
	}
}

func (validation) tooLarge(w http.ResponseWriter, r *http.Request) {
	answer(w, r, problem(http.StatusRequestEntityTooLarge, CodeRequestTooLarge,
		fmt.Sprintf("The request body is larger than %d bytes.", requestBodyMaxBytes)))
}

// unauthenticated answers a request that failed its security requirement.
func (v validation) unauthenticated(w http.ResponseWriter, r *http.Request) {
	refusal := schemeUnknown
	if state := stateOf(r.Context()); state != nil {
		refusal = state.refusal
	}
	switch refusal {
	case credentialsMissing:
		w.Header().Set("WWW-Authenticate", "Bearer")
		answer(w, r, problem(http.StatusUnauthorized, CodeCredentialsMissing, ""))
	case tokenInvalid:
		// RFC 6750 §3.1. Why the token is not accepted is not said.
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		answer(w, r, problem(http.StatusUnauthorized, CodeTokenInvalid, ""))
	case providerUnavailable:
		// The token was not judged. Answering 401 would tell a caller with a
		// good token to get another one.
		answer(w, r, problem(http.StatusServiceUnavailable, CodeIdentityProviderUnavailable,
			"The token could not be verified at the moment. It may be valid."))
	default:
		// A requirement nobody could decide: no authenticator, or a scheme
		// the transport does not implement. The request is refused, and it
		// is the service that is at fault.
		v.internal(w, r, "a security requirement of the contract could not be decided", nil)
	}
}

func (v validation) internal(w http.ResponseWriter, r *http.Request, message string, err error) {
	correlationID := ""
	if state := stateOf(r.Context()); state != nil {
		correlationID = state.correlationID
	}
	v.logger.ErrorContext(r.Context(), message,
		"correlation_id", correlationID, "method", r.Method, "path", r.URL.Path, "error", err)
	answer(w, r, problem(http.StatusInternalServerError, CodeInternalError, ""))
}

// describe says where a request does not match the contract, in words that
// repeat nothing of the request.
func describe(invalid *openapi3filter.RequestError) string {
	switch {
	case invalid.Parameter != nil:
		// The name and the location are those of the contract.
		return fmt.Sprintf("The %s parameter %s does not match the contract.", invalid.Parameter.In, invalid.Parameter.Name)

	case invalid.RequestBody != nil:
		if errors.Is(invalid.Err, openapi3filter.ErrInvalidRequired) {
			return "The operation requires a request body."
		}
		var schema *openapi3.SchemaError
		if !errors.As(invalid.Err, &schema) {
			return "The request body cannot be read as the contract declares it."
		}
		where, keyword := place(schema.JSONPointer()), schema.SchemaField
		if !keywordName.MatchString(keyword) {
			keyword = ""
		}
		switch {
		case where != "" && keyword != "":
			return fmt.Sprintf("The request body does not match the contract at %s (%s).", where, keyword)
		case where != "":
			return fmt.Sprintf("The request body does not match the contract at %s.", where)
		case keyword != "":
			return fmt.Sprintf("The request body does not match the contract (%s).", keyword)
		}
		return "The request body does not match the contract."
	}
	return "The request does not match the contract."
}

var (
	// pointerToken is a member name or an index a JSON pointer may show. The
	// tokens of a pointer come from the request; one that is anything else
	// than a short plain name is not repeated.
	pointerToken = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	// keywordName is a keyword of JSON Schema.
	keywordName = regexp.MustCompile(`^[A-Za-z$]{1,32}$`)
)

// place renders the JSON pointer of a failing value, or nothing if a token
// of it is not safe to repeat.
func place(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	for _, token := range tokens {
		if !pointerToken.MatchString(token) {
			return ""
		}
	}
	return "/" + strings.Join(tokens, "/")
}
