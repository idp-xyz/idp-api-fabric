package http

import (
	"encoding/json"
	"net/http"
)

// ProblemContentType is the media type of every error response (RFC 9457).
const ProblemContentType = "application/problem+json"

// Error codes the transport itself answers with. A code is stable: callers
// branch on error_code, never on title or detail.
const (
	CodeRouteNotFound    = "ROUTE_NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeRequestInvalid   = "REQUEST_INVALID"
	CodeInternalError    = "INTERNAL_ERROR"
)

// WriteProblem answers with problem, the error body the contract declares.
// The HTTP status is taken from the status member, so the two cannot disagree
// (F04 §11). An unset type is about:blank and an unset title is the status
// phrase.
//
// A problem outside 400–599 or without an error code is a programming mistake.
// It is answered as an internal error and its detail is dropped, since that
// detail was written for some other response.
func WriteProblem(w http.ResponseWriter, problem Problem) {
	if problem.Status < 400 || problem.Status > 599 || problem.ErrorCode == "" {
		problem = Problem{Status: http.StatusInternalServerError, ErrorCode: CodeInternalError}
	}
	if problem.Type == "" {
		problem.Type = "about:blank"
	}
	if problem.Title == "" {
		problem.Title = http.StatusText(problem.Status)
	}

	header := w.Header()
	header.Set("Content-Type", ProblemContentType)
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}
