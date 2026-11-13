package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

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

	// query is the query of the request as it was sent. The validator
	// rewrites the one of the request when it fills in a default, and what
	// it could not read is gone by then.
	query string
}

type stateKey struct{}

// stateOf returns the state of the request ctx belongs to, or nil for a
// context that is not one of a request.
func stateOf(ctx context.Context) *requestState {
	state, _ := ctx.Value(stateKey{}).(*requestState)
	return state
}

// begin gives every request its state, and stands between a panic and the
// caller.
//
// An operation that panics has answered nothing. Left to the server, the
// connection would be cut and the stack written to a log that knows nothing
// of the request. Here the caller gets the internal error that any failure
// nobody expected gets, and the log line carries the identifier of the
// request. What panicked is logged and never sent.
//
// An answer that has started cannot be taken back or followed by another.
// The connection is cut then, which is how the caller learns that what it
// received is not whole.
func begin(newID func() string, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &requestState{correlationID: newID()}
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, state))
		answered := &answerWriter{ResponseWriter: w}

		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// A handler that panics with http.ErrAbortHandler asks for the
			// connection to be cut and for nothing to be logged. It gets
			// both from the server.
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			logger.ErrorContext(r.Context(), "an operation panicked",
				"correlation_id", state.correlationID, "method", r.Method, "path", r.URL.Path,
				"answer_started", answered.started, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
			if answered.started {
				panic(http.ErrAbortHandler)
			}
			answer(answered, r, problem(http.StatusInternalServerError, CodeInternalError, ""))
		}()
		next.ServeHTTP(answered, r)
	})
}

// answerWriter knows whether the answer has started: once its status has
// gone out, nothing takes it back.
type answerWriter struct {
	http.ResponseWriter
	started bool
}

func (w *answerWriter) WriteHeader(status int) {
	// An informational response announces the answer; it is not the answer.
	if status >= http.StatusOK {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *answerWriter) Write(p []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(p)
}

// Unwrap hands out the writer beneath, for http.ResponseController and for
// serverWriter.
func (w *answerWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// serverWriter is the writer the server made, beneath whatever wraps it.
func serverWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		wrapper, wraps := w.(interface{ Unwrap() http.ResponseWriter })
		if !wraps {
			return w
		}
		w = wrapper.Unwrap()
	}
}

// prepare makes a routed request ready for the validator.
//
// A HEAD request is a GET whose answer has no body. The contract declares
// GET, so that is what the request is validated and served as; the server
// drops the body of the answer by itself, since it still knows the request
// was a HEAD. Serving it apart from GET would mean serving it unvalidated.
//
// A body is bounded before anyone reads it. The bound is given the writer of
// the server itself: it tells that writer to close the connection once a
// body has run over, so that the rest of it is not read either, and a
// wrapper it does not know would swallow that.
//
// The query is kept as it was sent, for the check that it says nothing the
// operation does not declare: the validator rewrites the query it is given.
func prepare(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			r = r.Clone(r.Context())
			r.Method = http.MethodGet
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(serverWriter(w), r.Body, requestBodyMaxBytes)
		}
		if state := stateOf(r.Context()); state != nil {
			state.query = r.URL.RawQuery
		}
		next.ServeHTTP(w, r)
	})
}
