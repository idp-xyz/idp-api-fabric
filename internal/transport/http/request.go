package http

import (
	"context"
	"net/http"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
)

// requestBodyMaxBytes bounds the body of a request. The validator reads a
// body whole, and reads it before it has asked who sends the request, so the
// bound comes first: it is what an unauthenticated caller can make the
// process hold. The largest body the contract declares is a product
// registration, a few kilobytes at most.
const requestBodyMaxBytes = 64 << 10

// requestState is what the transport learns about one request on its way to
// an operation. It is created before anything else looks at the request and
// travels in its context.
type requestState struct {
	// correlationID ties the request to its log lines, its problem, its
	// audit record and its events. The server generates it: nothing a caller
	// sends is written into the audit trail as an identifier.
	correlationID string

	// principal is who the request acts as, once its token has verified.
	principal     identity.Principal
	authenticated bool
	// refusal is why authentication failed, for the answer to say the right
	// thing: the validator reports only that it did.
	refusal authenticationRefusal
}

type stateKey struct{}

// stateOf returns the state of the request ctx belongs to, or nil for a
// context that is not one of a request.
func stateOf(ctx context.Context) *requestState {
	state, _ := ctx.Value(stateKey{}).(*requestState)
	return state
}

// begin gives every request its state.
func begin(newID func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &requestState{correlationID: newID()}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, state)))
	})
}

// prepare makes a routed request ready for the validator.
//
// A HEAD request is a GET whose answer has no body. The contract declares
// GET, so that is what the request is validated and served as; the server
// drops the body of the answer by itself, since it still knows the request
// was a HEAD. Serving it apart from GET would mean serving it unvalidated.
//
// A body is bounded before anyone reads it.
func prepare(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			r = r.Clone(r.Context())
			r.Method = http.MethodGet
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, requestBodyMaxBytes)
		}
		next.ServeHTTP(w, r)
	})
}
