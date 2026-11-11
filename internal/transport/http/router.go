// Package http is the inbound HTTP transport of the Fabric control plane:
// routing, the operations of the management API and the RFC 9457 error body.
//
// The DTOs and the handler interface in api.gen.go are generated from
// api/openapi.yaml and are never edited by hand. They stay in this package:
// no bounded context imports it (F07 §3.1).
package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

// NewHandler returns the root handler of fabric-api. Until a bounded context
// is mounted it serves liveness and the build identity only.
func NewHandler(logger *slog.Logger, build buildinfo.Info) http.Handler {
	return newHandler(logger, operations{build: build})
}

// newHandler routes the operations of the contract to server and answers
// everything else, and every failure, as a problem.
func newHandler(logger *slog.Logger, server StrictServerInterface) http.Handler {
	failures := failures{logger: logger}
	mux := http.NewServeMux()
	HandlerWithOptions(
		NewStrictHandlerWithOptions(server, []StrictMiddlewareFunc{requireResponse}, StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  failures.badRequest,
			ResponseErrorHandlerFunc: failures.operationFailed,
		}),
		StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: failures.badRequest},
	)
	return problemFallback{mux: mux}
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

// operations implements the operations of the contract that no bounded
// context owns.
type operations struct {
	build buildinfo.Info
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

// badRequest answers a request whose parameters or body could not be read.
// The error is not echoed: it quotes the request, which can carry a secret.
func (failures) badRequest(w http.ResponseWriter, _ *http.Request, _ error) {
	WriteProblem(w, Problem{Status: http.StatusBadRequest, ErrorCode: CodeRequestInvalid})
}

// operationFailed answers an operation that returned an error or a response
// it does not declare. The cause is logged and never sent.
func (f failures) operationFailed(w http.ResponseWriter, r *http.Request, err error) {
	f.logger.ErrorContext(r.Context(), "operation failed", "method", r.Method, "path", r.URL.Path, "error", err)
	WriteProblem(w, Problem{Status: http.StatusInternalServerError, ErrorCode: CodeInternalError})
}

// probedMethods are the methods tried to tell a missing route from a route
// that exists under another method.
var probedMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// problemFallback answers what the mux does not route. http.ServeMux would
// answer 404 and 405 as plain text, and every error of this API is
// application/problem+json.
type problemFallback struct {
	mux *http.ServeMux
}

func (f problemFallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := f.mux.Handler(r); pattern != "" {
		f.mux.ServeHTTP(w, r)
		return
	}
	if allowed := f.allowedMethods(r); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		WriteProblem(w, Problem{Status: http.StatusMethodNotAllowed, ErrorCode: CodeMethodNotAllowed})
		return
	}
	WriteProblem(w, Problem{Status: http.StatusNotFound, ErrorCode: CodeRouteNotFound})
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
