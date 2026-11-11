// Package http is the inbound HTTP transport of the Fabric control plane:
// routing, the liveness and version endpoints and the RFC 9457 error body.
//
// DTOs and handlers generated from the management API contract land here and
// stay here: no bounded context imports this package (F07 §3.1).
package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

// NewHandler returns the root handler of fabric-api. Until a bounded context
// is mounted it serves liveness and the build identity only.
func NewHandler(build buildinfo.Info) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, build)
	})
	return problemFallback{mux: mux}
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
