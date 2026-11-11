package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

var testBuild = buildinfo.Info{Version: "v-test", Commit: "c-test", BuiltAt: "t-test"}

func request(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	NewHandler(testBuild).ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
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

func TestHealthzReportsLiveness(t *testing.T) {
	response := request(t, http.MethodGet, "/healthz")

	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	if got := members(t, response)["status"]; got != "ok" {
		t.Errorf("status member %v, want ok", got)
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
	var got buildinfo.Info
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode the build identity: %v", err)
	}
	if got != testBuild {
		t.Errorf("build identity %+v, want %+v", got, testBuild)
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
			want := map[string]any{
				"type":       "about:blank",
				"title":      http.StatusText(c.status),
				"status":     float64(c.status),
				"error_code": c.code,
			}
			if got := members(t, response); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v", got, want)
			}
		})
	}
}

func TestWriteProblemKeepsWhatTheCallerSet(t *testing.T) {
	problem := Problem{
		Type:      "https://fabric.example.org/problems/stale-generation",
		Title:     "The slot generation is stale",
		Status:    http.StatusPreconditionFailed,
		Detail:    "Read the slot again before retrying.",
		Instance:  "/v1/publication-slots/slot-1",
		ErrorCode: "CONCURRENT_MODIFICATION",
		TraceID:   "4bf92f3577b34da6a3ce929d0e0e4736",
	}
	recorder := httptest.NewRecorder()
	WriteProblem(recorder, problem)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status %d, want 412", recorder.Code)
	}
	var got Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	if got != problem {
		t.Errorf("problem %+v, want %+v", got, problem)
	}
}

func TestWriteProblemAnswersInternalErrorForWhatIsNotAProblem(t *testing.T) {
	cases := map[string]Problem{
		"status below 400":   {Status: http.StatusOK, ErrorCode: "ANY", Detail: "written for another response"},
		"status above 599":   {Status: 600, ErrorCode: "ANY"},
		"missing error code": {Status: http.StatusConflict, Detail: "written for another response"},
	}
	for name, problem := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteProblem(recorder, problem)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", recorder.Code)
			}
			want := map[string]any{
				"type":       "about:blank",
				"title":      http.StatusText(http.StatusInternalServerError),
				"status":     float64(http.StatusInternalServerError),
				"error_code": CodeInternalError,
			}
			if got := members(t, recorder); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v", got, want)
			}
		})
	}
}
