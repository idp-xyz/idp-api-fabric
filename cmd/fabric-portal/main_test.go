package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPortalForwardsTheCallerOnlyToReviewOperations(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.URL.Path != "/v1/products/p/assets/a/revisions/r/reviews/x/decision" || r.URL.RawQuery != "test=1" || r.Method != http.MethodPost {
			t.Errorf("upstream received %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer example-access-token" || r.Header.Get("Cookie") != "" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Forwarded-User") != "" {
			t.Error("the portal forwarded the wrong identity or content headers")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "{}" {
			t.Errorf("upstream body: %q (%v)", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "untrusted_cookie=value")
		w.Header().Set("Cache-Control", "public")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	handler, err := newPortal(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/products/p/assets/a/revisions/r/reviews/x/decision?test=1"
	request := func(method, target, site string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer example-access-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "session=private")
		req.Header.Set("X-Forwarded-User", "somebody-else")
		req.Header.Set("Sec-Fetch-Site", site)
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, req)
		return got
	}

	if got := request(http.MethodPost, path, "cross-site"); got.Code != http.StatusForbidden {
		t.Errorf("a cross-site decision returned %d", got.Code)
	}
	if got := request(http.MethodPost, "/api/v1/products/p/role-assignments", "same-origin"); got.Code != http.StatusNotFound {
		t.Errorf("a role grant through the portal returned %d", got.Code)
	}
	if got := request(http.MethodPost, path, "same-origin"); got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" ||
		got.Header().Get("Set-Cookie") != "" {
		t.Errorf("same-origin decision returned %d with headers %v", got.Code, got.Header())
	}
	if got := forwarded.Load(); got != 1 {
		t.Errorf("upstream received %d requests, want only the same-origin decision", got)
	}
}

func TestPortalServesTheWorkbenchAndRefusesAnHTMLAPIResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<script>alert(1)</script>")
	}))
	defer upstream.Close()
	handler, err := newPortal(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/app.css", "/app.js"} {
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, httptest.NewRequest(http.MethodGet, path, nil))
		if got.Code != http.StatusOK || got.Body.Len() == 0 || got.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s returned %d and %d bytes with headers %v", path, got.Code, got.Body.Len(), got.Header())
		}
	}
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, httptest.NewRequest(http.MethodGet, "/api/v1/caller", nil))
	if got.Code != http.StatusBadGateway || !strings.Contains(got.Body.String(), "FABRIC_UNAVAILABLE") {
		t.Errorf("unexpected upstream HTML returned %d %q", got.Code, got.Body.String())
	}
}
