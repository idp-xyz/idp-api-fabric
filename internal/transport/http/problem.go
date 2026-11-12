package http

import (
	"encoding/json"
	"net/http"
)

// ProblemContentType is the media type of every error response (RFC 9457).
const ProblemContentType = "application/problem+json"

// The error codes of the API. A code is stable: callers branch on error_code,
// never on title or detail.
const (
	// What the transport itself answers.
	CodeRouteNotFound        = "ROUTE_NOT_FOUND"
	CodeMethodNotAllowed     = "METHOD_NOT_ALLOWED"
	CodeRequestInvalid       = "REQUEST_INVALID"
	CodeRequestTooLarge      = "REQUEST_TOO_LARGE"
	CodeUnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
	CodeInternalError        = "INTERNAL_ERROR"

	// Who the caller is.
	CodeCredentialsMissing = "CREDENTIALS_MISSING"
	CodeTokenInvalid       = "TOKEN_INVALID"
	// The token could not be judged: the keys of the identity provider are
	// out of reach. It says nothing about the token.
	CodeIdentityProviderUnavailable = "IDENTITY_PROVIDER_UNAVAILABLE"

	// What the caller may do, and what there is.
	CodeForbidden          = "FORBIDDEN"
	CodeProductNotFound    = "PRODUCT_NOT_FOUND"
	CodeWorkspaceNotFound  = "WORKSPACE_NOT_FOUND"
	CodeMembershipNotFound = "MEMBERSHIP_NOT_FOUND"

	// What the state does not allow.
	CodeProductKeyTaken   = "PRODUCT_KEY_TAKEN"
	CodeWorkspaceKeyTaken = "WORKSPACE_KEY_TAKEN"
	CodeMembershipExists  = "MEMBERSHIP_EXISTS"
	CodeProductNotActive  = "PRODUCT_NOT_ACTIVE"

	// What matches the contract and still cannot be done.
	CodeOwnerIssuerNotTrusted  = "OWNER_ISSUER_NOT_TRUSTED"
	CodeMemberIssuerNotTrusted = "MEMBER_ISSUER_NOT_TRUSTED"
	CodeRequestUnprocessable   = "REQUEST_UNPROCESSABLE"

	// The change may or may not have been committed. Read before retrying.
	CodeOutcomeUncertain = "OUTCOME_UNCERTAIN"
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
		problem = Problem{Status: http.StatusInternalServerError, ErrorCode: CodeInternalError, TraceID: problem.TraceID}
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

// answer writes problem for the request r, with the identifier that ties it
// to the log lines and the audit record of that request.
func answer(w http.ResponseWriter, r *http.Request, problem Problem) {
	if state := stateOf(r.Context()); state != nil {
		problem.TraceID = &state.correlationID
	}
	WriteProblem(w, problem)
}

// problem is a problem with a status, a code and, if it is not empty, a
// detail.
func problem(status int, code, detail string) Problem {
	built := Problem{Status: status, ErrorCode: code}
	if detail != "" {
		built.Detail = &detail
	}
	return built
}
