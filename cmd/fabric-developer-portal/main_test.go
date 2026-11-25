package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

const testAsset = "20000000-0000-4000-8000-000000000001"

func TestReferenceAndDownloadCheckCurrentOriginAuthorization(t *testing.T) {
	payload := testArtifact(t)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(payload))
	var revoked atomic.Bool
	var reads atomic.Int32
	publisher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/reference/"+testAsset || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer test-partner" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected reference request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if revoked.Load() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Artifact-Digest", digest)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(publisher.Close)
	portal, err := newPortal(publisher.URL, testAsset)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, authorized bool, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if authorized {
			request.Header.Set("Authorization", "Bearer test-partner")
			request.Header.Set("Cookie", "private-session=test")
		}
		recorder := httptest.NewRecorder()
		portal.ServeHTTP(recorder, request)
		if recorder.Code != status || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s returned %d %v, want %d with no-store", path, recorder.Code, recorder.Header(), status)
		}
		return recorder
	}

	get("/", false, http.StatusUnauthorized)
	get("/v1/products", true, http.StatusNotFound)
	if reads.Load() != 0 {
		t.Fatal("an unauthenticated or unknown route called the origin")
	}
	reference := get("/", true, http.StatusOK).Body.String()
	for _, expected := range []string{"CargoPai Developers", "Shipping &amp; Tracking", "List shipments",
		"/shipments", "Awaiting dispatch", "OpenAPI 3.1.2", "&lt;script&gt;"} {
		if !strings.Contains(reference, expected) {
			t.Errorf("reference is missing %q", expected)
		}
	}
	if strings.Contains(reference, "<script>") {
		t.Error("reference emitted active HTML from the contract")
	}
	search := get("/?q=shipments", true, http.StatusOK).Body.String()
	if !strings.Contains(search, "/shipments") || strings.Contains(search, "/health") {
		t.Error("search failed to filter the authorized operations")
	}
	download := get("/openapi-source.json", true, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), payload) || download.Header().Get("X-Artifact-Digest") != digest {
		t.Fatal("download changed the immutable artifact")
	}
	revoked.Store(true)
	for _, path := range []string{"/", "/openapi-source.json"} {
		if strings.Contains(get(path, true, http.StatusNotFound).Body.String(), "Shipping") {
			t.Errorf("%s revealed the contract after revocation", path)
		}
	}
	if reads.Load() != 5 {
		t.Fatalf("expected each authorized page, search and download to contact the origin, got %d reads", reads.Load())
	}
}

func TestReferenceRefusesAnArtifactWithTheWrongDigest(t *testing.T) {
	publisher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Artifact-Digest", "sha256:"+strings.Repeat("0", 64))
		_, _ = w.Write([]byte("unverified bytes"))
	}))
	t.Cleanup(publisher.Close)
	portal, err := newPortal(publisher.URL, testAsset)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/openapi-source.json", nil)
	request.Header.Set("Authorization", "Bearer test-partner")
	recorder := httptest.NewRecorder()
	portal.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "unverified bytes") {
		t.Fatalf("download served an unverified artifact: %d %s", recorder.Code, recorder.Body)
	}
}

func testArtifact(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"openapi.yaml": `openapi: 3.1.2
info:
  title: Shipping & Tracking
  version: 1.0.0
  description: "<script>Example contract prose</script>"
paths:
  /shipments:
    $ref: './shipments.yaml#/operation'
  /health:
    get:
      summary: Service health
      responses:
        '200': {description: Fine}
`,
		"shipments.yaml": `operation:
  get:
    summary: List shipments
    responses:
      '200': {description: Awaiting dispatch}
`,
	}
	archive := make([]map[string]any, 0, len(files))
	for path, content := range files {
		archive = append(archive, map[string]any{"path": path, "content_base64": []byte(content)})
	}
	data, err := json.Marshal(map[string]any{"format": registry.ArtifactFormat, "entrypoint": "openapi.yaml", "files": archive})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
