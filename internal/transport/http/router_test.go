package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

var testBuild = buildinfo.Info{Version: "v-test", Commit: "c-test", BuiltAt: "t-test"}

func request(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler := NewHandler(slog.New(slog.DiscardHandler), testBuild)
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

// members decodes a JSON object body into its members.
func members(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, recorder.Body)
	}
	return decoded
}

// problemMembers are the members of a problem that carries nothing beyond its
// status and its code.
func problemMembers(status int, code string) map[string]any {
	return map[string]any{
		"type":       "about:blank",
		"title":      http.StatusText(status),
		"status":     float64(status),
		"error_code": code,
	}
}

func TestHealthzReportsLiveness(t *testing.T) {
	response := request(t, http.MethodGet, "/healthz")

	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	if got, want := members(t, response), (map[string]any{"status": "ok"}); !reflect.DeepEqual(got, want) {
		t.Errorf("body %v, want %v", got, want)
	}
}

func TestVersionReportsTheBuildIdentity(t *testing.T) {
	response := request(t, http.MethodGet, "/version")

	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	want := map[string]any{"version": "v-test", "commit": "c-test", "built_at": "t-test"}
	if got := members(t, response); !reflect.DeepEqual(got, want) {
		t.Errorf("build identity %v, want %v", got, want)
	}
}

func TestUnroutedRequestsAreProblems(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		status int
		code   string
		allow  string
	}{
		{"unknown path", http.MethodGet, "/v1/products", http.StatusNotFound, CodeRouteNotFound, ""},
		{"known path under another method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, CodeMethodNotAllowed, "GET, HEAD"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			response := request(t, c.method, c.target)

			if response.Code != c.status {
				t.Fatalf("status %d, want %d", response.Code, c.status)
			}
			if got := response.Header().Get("Content-Type"); got != ProblemContentType {
				t.Errorf("Content-Type %q, want %s", got, ProblemContentType)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q, want no-store", got)
			}
			if got := response.Header().Get("Allow"); got != c.allow {
				t.Errorf("Allow %q, want %q", got, c.allow)
			}
			if got, want := members(t, response), problemMembers(c.status, c.code); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v", got, want)
			}
		})
	}
}

// failing stands in for the operations of the contract. Its liveness
// operation fails, and its version operation returns nothing at all.
type failing struct {
	cause error
}

func (f failing) GetHealthz(context.Context, GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return nil, f.cause
}

func (failing) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return nil, nil
}

func TestAFailedOperationIsAnInternalErrorWhoseCauseIsLoggedAndNotSent(t *testing.T) {
	cause := errors.New("connect to 10.0.0.7: password authentication failed for user fabric_app")
	cases := []struct {
		name   string
		target string
		logged string
	}{
		{"the operation returns an error", "/healthz?token=in-the-query", cause.Error()},
		{"the operation returns neither a response nor an error", "/version?token=in-the-query", "GetVersion returned neither"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log bytes.Buffer
			handler := newHandler(slog.New(slog.NewTextHandler(&log, nil)), failing{cause: cause})

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, c.target, nil))

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != ProblemContentType {
				t.Errorf("Content-Type %q, want %s", got, ProblemContentType)
			}
			want := problemMembers(http.StatusInternalServerError, CodeInternalError)
			if got := members(t, recorder); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v: the cause must not reach the caller", got, want)
			}
			if !strings.Contains(log.String(), c.logged) {
				t.Errorf("the cause must be logged, got %q", log.String())
			}
			if strings.Contains(log.String(), "in-the-query") {
				t.Errorf("the log must not quote the query string, got %q", log.String())
			}
		})
	}
}

func TestARequestThatCannotBeReadIsAProblemThatDoesNotQuoteIt(t *testing.T) {
	recorder := httptest.NewRecorder()
	failures{}.badRequest(recorder, httptest.NewRequest(http.MethodPost, "/", nil),
		errors.New(`can't decode JSON body: invalid character 's' in "secret-token"`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	want := problemMembers(http.StatusBadRequest, CodeRequestInvalid)
	if got := members(t, recorder); !reflect.DeepEqual(got, want) {
		t.Errorf("problem members %v, want %v", got, want)
	}
}

func TestWriteProblemKeepsWhatTheCallerSet(t *testing.T) {
	detail, instance, trace := "Read the slot again before retrying.", "/v1/publication-slots/slot-1", "4bf92f3577b34da6a3ce929d0e0e4736"
	problem := Problem{
		Type:      "https://fabric.example.org/problems/stale-generation",
		Title:     "The slot generation is stale",
		Status:    http.StatusPreconditionFailed,
		Detail:    &detail,
		Instance:  &instance,
		ErrorCode: "CONCURRENT_MODIFICATION",
		TraceID:   &trace,
	}
	recorder := httptest.NewRecorder()
	WriteProblem(recorder, problem)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status %d, want 412", recorder.Code)
	}
	want := map[string]any{
		"type":       problem.Type,
		"title":      problem.Title,
		"status":     float64(http.StatusPreconditionFailed),
		"detail":     detail,
		"instance":   instance,
		"error_code": problem.ErrorCode,
		"trace_id":   trace,
	}
	if got := members(t, recorder); !reflect.DeepEqual(got, want) {
		t.Errorf("problem members %v, want %v", got, want)
	}
}

func TestWriteProblemAnswersInternalErrorForWhatIsNotAProblem(t *testing.T) {
	detail := "written for another response"
	cases := map[string]Problem{
		"status below 400":   {Status: http.StatusOK, ErrorCode: "ANY", Detail: &detail},
		"status above 599":   {Status: 600, ErrorCode: "ANY"},
		"missing error code": {Status: http.StatusConflict, Detail: &detail},
	}
	for name, problem := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteProblem(recorder, problem)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", recorder.Code)
			}
			want := problemMembers(http.StatusInternalServerError, CodeInternalError)
			if got := members(t, recorder); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v", got, want)
			}
		})
	}
}
