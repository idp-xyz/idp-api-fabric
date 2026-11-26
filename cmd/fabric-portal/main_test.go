package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPortalForwardsSameOriginReviewOperationsWithCaller(t *testing.T) {
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

func TestPortalForwardsPublicationChangesWithTheRetryKey(t *testing.T) {
	const slot = "/v1/products/p/publication-targets/t/assets/a"
	const change = "{\"action\":\"WITHDRAW\",\"expected_generation\":2,\"reason\":\"incorrect contract\"}"
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != slot+"/operations" ||
			r.Header.Get("Idempotency-Key") != "publication-retry" || r.Header.Get("Authorization") != "Bearer example-access-token" {
			t.Errorf("misrouted publication operation %s %s or wrong request headers", r.Method, r.URL)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != change {
			t.Errorf("upstream received %q (%v), want the withdrawal request", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	handler, err := newPortal(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		path, site string
		want       int
	}{
		{slot + "/operations", "same-origin", http.StatusOK},
		{slot + "/operations", "cross-site", http.StatusForbidden},
		{"/v1/products/p/publication-targets/t/reconcile", "same-origin", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api"+c.path, strings.NewReader(change))
		req.Header.Set("Authorization", "Bearer example-access-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "publication-retry")
		req.Header.Set("Sec-Fetch-Site", c.site)
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, req)
		if got.Code != c.want || got.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("POST %s (%s) returned %d, want %d and no-store", c.path, c.site, got.Code, c.want)
		}
	}
	if got := forwarded.Load(); got != 1 {
		t.Errorf("upstream received %d requests, want the one same-origin publication operation", got)
	}
}

func TestPortalForwardsAccessBlockReadsAndSameOriginChanges(t *testing.T) {
	const blocks = "/v1/products/p/publication-targets/t/access-blocks"
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.Header.Get("Authorization") != "Bearer example-access-token" || r.Header.Get("Cookie") != "" {
			t.Error("the proxy did not preserve the caller boundary")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"received": r.Method + " " + r.URL.RequestURI()})
	}))
	defer upstream.Close()
	handler, err := newPortal(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		method, path, site string
		want               int
	}{
		{http.MethodGet, blocks + "?after_generation=20&page_size=20", "same-origin", http.StatusOK},
		{http.MethodPost, blocks, "same-origin", http.StatusOK},
		{http.MethodPost, blocks + "/existing/clearance", "same-origin", http.StatusOK},
		{http.MethodPost, blocks, "cross-site", http.StatusForbidden},
		{http.MethodPost, blocks + "/existing/clearance", "cross-site", http.StatusForbidden},
		{http.MethodPost, blocks + "/existing/reopen", "same-origin", http.StatusNotFound},
	} {
		req := httptest.NewRequest(c.method, "/api"+c.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer example-access-token")
		req.Header.Set("Cookie", "browser-session=private")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", c.site)
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, req)
		if got.Code != c.want || got.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s (%s) returned %d, want %d and no-store", c.method, c.path, c.site, got.Code, c.want)
		}
		if c.want == http.StatusOK {
			var result map[string]string
			if err := json.Unmarshal(got.Body.Bytes(), &result); err != nil || result["received"] != c.method+" "+c.path {
				t.Errorf("the proxy returned %q for %s %s", got.Body.String(), c.method, c.path)
			}
		}
	}
	if got := forwarded.Load(); got != 3 {
		t.Errorf("upstream received %d requests, want only the three allowed requests", got)
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
	for _, path := range []string{"/", "/app.css", "/app.js", "/workbench.js", "/publication.js", "/access-blocks.js", "/catalog.js"} {
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

func TestPortalForwardsOnlyTheCatalogReads(t *testing.T) {
	const asset = "/v1/products/p/assets/a"
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"received": r.Method + " " + r.URL.RequestURI()})
	}))
	defer upstream.Close()
	handler, err := newPortal(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/products/p/assets?workspace_id=w&q=ship&check_status=FAIL&page_size=20", http.StatusOK},
		{http.MethodGet, asset + "/publications?page_size=100", http.StatusOK},
		{http.MethodGet, asset + "/revisions/r/reviews?page_size=100", http.StatusOK},
		{http.MethodGet, asset + "/revisions/r/source", http.StatusNotFound},
		{http.MethodPost, "/v1/products/p/assets", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(c.method, "/api"+c.path, nil)
		req.Header.Set("Authorization", "Bearer example-access-token")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, req)
		if got.Code != c.want {
			t.Errorf("%s %s returned %d, want %d", c.method, c.path, got.Code, c.want)
		}
		if c.want == http.StatusOK {
			var result map[string]string
			if err := json.Unmarshal(got.Body.Bytes(), &result); err != nil || result["received"] != c.method+" "+c.path {
				t.Errorf("the proxy returned %q for %s %s", got.Body.String(), c.method, c.path)
			}
		}
	}
	if got := forwarded.Load(); got != 3 {
		t.Errorf("upstream received %d requests, want only the three catalog reads", got)
	}
}
