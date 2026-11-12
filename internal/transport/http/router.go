// Package http is the inbound HTTP transport of the Fabric control plane:
// routing, validation against the contract, authentication, the operations
// of the management API and the RFC 9457 error body.
//
// The DTOs and the handler interface in api.gen.go are generated from
// api/openapi.yaml and are never edited by hand. They stay in this package:
// no bounded context imports it (F07 §3.1).
//
// A request passes, in this order: the router, which answers what the
// contract does not route; the validator, which checks who sends the request,
// then its parameters, then its body, against the contract; the operation.
// The transport establishes who the caller is. Whether the caller may do what
// it asks is decided by the use case, never here (F05 §7).
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
)

// Config is what the transport is wired with. All of it is required.
type Config struct {
	Logger *slog.Logger
	Build  buildinfo.Info
	// Verifier judges the bearer token of a request.
	Verifier TokenVerifier
	// Governance are the use cases of Product Governance.
	Governance ProductGovernance
}

// NewHandler returns the root handler of fabric-api.
func NewHandler(config Config) (http.Handler, error) {
	switch {
	case config.Logger == nil:
		return nil, errors.New("the transport needs a logger")
	case config.Verifier == nil:
		return nil, errors.New("the transport needs a token verifier")
	case config.Governance == nil:
		return nil, errors.New("the transport needs the use cases of product governance")
	}
	return newHandler(config.Logger, config.Verifier, operations{build: config.Build, governance: config.Governance})
}

// newHandler routes the operations of the contract to server, behind the
// validator, and answers everything else, and every failure, as a problem.
// A panic is such a failure: it is answered as an internal error, unless the
// answer had started, and then the connection is cut.
func newHandler(logger *slog.Logger, verifier TokenVerifier, server StrictServerInterface) (http.Handler, error) {
	contract, err := loadContract()
	if err != nil {
		return nil, err
	}

	failures := failures{logger: logger}
	mux := http.NewServeMux()
	HandlerWithOptions(
		NewStrictHandlerWithOptions(server, []StrictMiddlewareFunc{requireResponse}, StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  failures.badRequest,
			ResponseErrorHandlerFunc: failures.operationFailed,
		}),
		StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: failures.badRequest},
	)

	validated, err := validation{logger: logger}.validated(
		contract, authenticator{verifier: verifier, logger: logger}.authenticate, mux)
	if err != nil {
		return nil, err
	}
	return begin(ids.NewUUID, logger, problemFallback{mux: mux, routed: prepare(validated)}), nil
}

// requireResponse fails an operation that returns neither a response nor an
// error. The generated code would write nothing for it, which a client reads
// as an empty 200 the contract does not declare.
func requireResponse(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		response, err := next(ctx, w, r, request)
		if err == nil && response == nil {
			return nil, fmt.Errorf("operation %s returned neither a response nor an error", operationID)
		}
		return response, err
	}
}

// operations implements the operations of the contract: those no bounded
// context owns, here, and those of Product Governance in productgovernance.go.
type operations struct {
	build      buildinfo.Info
	governance ProductGovernance
}

func (operations) GetHealthz(context.Context, GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: HealthStatusOk}, nil
}

func (o operations) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return GetVersion200JSONResponse{
		Body:    BuildInfo{Version: o.build.Version, Commit: o.build.Commit, BuiltAt: o.build.BuiltAt},
		Headers: GetVersion200ResponseHeaders{CacheControl: "no-store"},
	}, nil
}

// failures turns what the generated code reports into problems. The generated
// defaults would answer in plain text and echo the error.
type failures struct {
	logger *slog.Logger
}

// badRequest answers a request whose parameters or body the generated code
// could not bind. The validator has checked both before, so this is a request
// the two read differently. The error is not echoed: it quotes the request,
// which can carry a secret.
func (failures) badRequest(w http.ResponseWriter, r *http.Request, _ error) {
	answer(w, r, problem(http.StatusBadRequest, CodeRequestInvalid, ""))
}

// operationFailed answers an operation that returned an error or a response
// it does not declare.
//
// A failure a use case names is answered with its problem. Anything else is
// an internal error whose cause is logged and never sent.
func (f failures) operationFailed(w http.ResponseWriter, r *http.Request, err error) {
	correlationID := ""
	if state := stateOf(r.Context()); state != nil {
		correlationID = state.correlationID
	}

	expected, named := problemOf(err)
	if !named {
		f.logger.ErrorContext(r.Context(), "operation failed",
			"correlation_id", correlationID, "method", r.Method, "path", r.URL.Path, "error", err)
		answer(w, r, problem(http.StatusInternalServerError, CodeInternalError, ""))
		return
	}
	switch {
	case errors.Is(err, application.ErrRefusalNotAudited):
		// The caller was refused all the same. Whoever operates the service
		// has to know that the audit trail has a gap.
		f.logger.ErrorContext(r.Context(), "a refusal could not be audited",
			"correlation_id", correlationID, "method", r.Method, "path", r.URL.Path, "error", err)
	case expected.Status >= http.StatusInternalServerError:
		f.logger.ErrorContext(r.Context(), "operation failed",
			"correlation_id", correlationID, "method", r.Method, "path", r.URL.Path, "error", err)
	}
	answer(w, r, expected)
}

// probedMethods are the methods tried to tell a missing route from a route
// that exists under another method.
var probedMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// problemFallback answers what the mux does not route and hands what it does
// route to the validator. http.ServeMux would answer 404 and 405 as plain
// text, and every error of this API is application/problem+json.
type problemFallback struct {
	mux *http.ServeMux
	// routed serves a request the mux routes: the validator, and behind it
	// the mux itself.
	routed http.Handler
}

func (f problemFallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := f.mux.Handler(r); pattern != "" {
		f.routed.ServeHTTP(w, r)
		return
	}
	if allowed := f.allowedMethods(r); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		answer(w, r, problem(http.StatusMethodNotAllowed, CodeMethodNotAllowed, ""))
		return
	}
	answer(w, r, problem(http.StatusNotFound, CodeRouteNotFound, ""))
}

// allowedMethods lists the methods under which the request's path is routed.
func (f problemFallback) allowedMethods(r *http.Request) []string {
	var allowed []string
	for _, method := range probedMethods {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := f.mux.Handler(probe); pattern != "" {
			allowed = append(allowed, method)
		}
	}
	return allowed
}
