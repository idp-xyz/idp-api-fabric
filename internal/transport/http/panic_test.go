package http

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// panicking is Product Governance with a use case that panics, as a nil
// dereference in one would.
type panicking struct{ *governance }

func (panicking) GetProduct(context.Context, application.GetProduct) (domain.Product, error) {
	panic("canary: a use case panicked")
}

// sharedLog is a log a server writes while the test reads it.
type sharedLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (l *sharedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.Write(p)
}

func (l *sharedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

// A panic in an operation is the failure nobody expected: the caller gets an
// internal error with the identifier of the request and nothing else, and
// the log has what panicked and where under that identifier. The service
// goes on answering.
func TestAPanicInAnOperationIsAnInternalErrorThatIsLoggedAndNotSent(t *testing.T) {
	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{},
		Governance: panicking{s.governance},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler

	response := s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: alice.Subject})
	traceID := wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
	if strings.Contains(response.Body.String(), "canary") {
		t.Errorf("the answer says what panicked: %s", response.Body)
	}

	logged := s.log.String()
	for _, want := range []string{
		"an operation panicked", "correlation_id=" + traceID, "method=GET", "path=/v1/products/" + productID,
		"answer_started=false", "canary: a use case panicked", "panicking.GetProduct",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}

	// The panic was that of one request. The next one is served.
	if health := s.do(t, call{Method: http.MethodGet, Target: "/healthz"}); health.Code != http.StatusOK {
		t.Errorf("GET /healthz after the panic: %d, want 200", health.Code)
	}
	if created := s.do(t, call{Method: http.MethodPost, Target: "/v1/products", Token: admin.Subject, Body: registration}); created.Code != http.StatusCreated {
		t.Errorf("POST /v1/products after the panic: %d, want 201", created.Code)
	}
}

// An answer that has started cannot be followed by a problem: the caller
// would read two answers as one. The connection is cut instead, so that the
// caller knows what it received is not whole, and the panic is logged like
// any other.
func TestAPanicAfterTheAnswerStartedCutsTheConnection(t *testing.T) {
	log := &sharedLog{}
	halfAnswered := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":`)
		// Through the wrapper, to the writer of the server.
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		panic("canary: the answer was half written")
	})
	server := httptest.NewServer(begin(func() string { return "request-1" }, slog.New(slog.NewTextHandler(log, nil)), halfAnswered))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err == nil {
		t.Errorf("the answer ended as if it were whole: %q", body)
	}
	if string(body) != `{"status":` {
		t.Errorf("the caller read %q, want what was written before the panic and nothing after it", body)
	}

	logged := log.String()
	for _, want := range []string{"an operation panicked", "correlation_id=request-1", "answer_started=true", "canary: the answer was half written"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
}

// A handler that panics with http.ErrAbortHandler asks the server to cut the
// connection and to log nothing. The request is left to the server.
func TestAHandlerThatAbortsIsLeftToTheServer(t *testing.T) {
	log := &bytes.Buffer{}
	aborting := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	handler := begin(func() string { return "request-1" }, slog.New(slog.NewTextHandler(log, nil)), aborting)

	recorder := httptest.NewRecorder()
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler passed on", recovered)
			}
		}()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	}()
	if recorder.Body.Len() != 0 || log.Len() != 0 {
		t.Errorf("an aborted request was answered %q and logged %q, want neither", recorder.Body, log)
	}
}

// A body over its bound makes the server close the connection, so that it
// does not read the rest of it. The bound tells the writer of the server so,
// and has to reach it through whatever wraps that writer.
func TestABodyOverItsBoundStillClosesTheConnection(t *testing.T) {
	server := httptest.NewServer(newService(t).handler)
	t.Cleanup(server.Close)

	tooLarge := `{"product_key":"` + strings.Repeat("a", requestBodyMaxBytes) + `"}`
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/products", strings.NewReader(tooLarge))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+admin.Subject)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", response.StatusCode)
	}
	if !response.Close {
		t.Error("the connection stays open after a body over its bound")
	}
}
