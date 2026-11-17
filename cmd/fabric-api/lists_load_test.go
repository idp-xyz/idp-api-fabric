package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// This is an opt-in load experiment, not a timing assertion in the CI suite.
// It uses the same assembled HTTP service, JWT verifier, application role
// and disposable database as the functional slice tests. Only the IdP is a
// stand-in. Bulk SQL prepares synthetic data; every measured request uses HTTP.
//
// Run without -race (instrumentation would change the measurement), and run
// make check with the same two DSNs separately. See tests/performance/README.md.
func TestProductGovernanceListLoad(t *testing.T) {
	if os.Getenv("IDP_FABRIC_LIST_LOAD") != "1" {
		t.Skip("opt-in load experiment; see tests/performance/README.md")
	}
	for _, variable := range []string{pgtest.MigratorDSNVariable, pgtest.ApplicationDSNVariable, "IDP_FABRIC_LIST_LOAD_REPORT"} {
		if os.Getenv(variable) == "" {
			t.Fatalf("%s is required for the load experiment", variable)
		}
	}
	duration := 3 * time.Second
	if configured := os.Getenv("IDP_FABRIC_LIST_LOAD_DURATION"); configured != "" {
		var err error
		duration, err = time.ParseDuration(configured)
		if err != nil || duration < time.Second || duration > time.Minute {
			t.Fatal("IDP_FABRIC_LIST_LOAD_DURATION must be between 1s and 1m")
		}
	}
	started := time.Now().UTC()
	s := start(t, "load-admin")
	product, workspace := seedListLoad(t, s)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 64
	transport.MaxConnsPerHost = 64
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	prefix := "/v1/products/" + product
	type listing struct {
		name, path, subject, id string
		count                   int
	}
	lists := []listing{
		{"products/admin", "/v1/products", "load-admin", "product_id", 20001},
		{"products/owner", "/v1/products", "load-owner", "product_id", 201},
		{"workspaces/owner", prefix + "/workspaces", "load-owner", "workspace_id", 20001},
		{"workspaces/member", prefix + "/workspaces", "load-member", "workspace_id", 200},
		{"memberships/owner", prefix + "/workspaces/" + workspace + "/memberships", "load-owner", "membership_id", 40000},
		{"roles/owner", prefix + "/role-assignments", "load-owner", "role_assignment_id", 2001},
	}
	var cases []listLoadCase
	for _, list := range lists {
		// Walk actual API-issued cursors. Setup also checks the complete list:
		// exact cardinality, no repeated IDs, and the last page terminates.
		path := list.path + "?page_size=100"
		seen := make(map[string]bool, list.count)
		halfway := false
		for {
			items, next := s.page(path, list.subject)
			ids := make([]string, 0, len(items))
			for _, item := range items {
				id, _ := item[list.id].(string)
				if id == "" || seen[id] {
					t.Fatalf("%s: missing or repeated identifier %q", list.name, id)
				}
				seen[id] = true
				ids = append(ids, id)
			}
			if len(seen) == len(items) || !halfway && len(seen) > list.count/2 {
				position := "first"
				if len(seen) != len(items) {
					position, halfway = "middle", true
				}
				cases = append(cases, listLoadCase{
					name: list.name, position: position, path: path, token: s.token(list.subject),
					id: list.id, wantIDs: ids, wantNext: next, status: http.StatusOK,
				})
			}
			if next == "" {
				break
			}
			path = list.path + "?page_size=100&page_token=" + next
		}
		if len(seen) != list.count {
			t.Fatalf("%s: listed %d items, want %d", list.name, len(seen), list.count)
		}
	}
	// Exercise denial and its audit transaction under concurrency as well.
	denials := []listLoadCase{
		{name: "roles/member-denied", position: "denied", path: prefix + "/role-assignments", token: s.token("load-member"), status: http.StatusForbidden, errorCode: "FORBIDDEN"},
		{name: "workspaces/stranger-hidden", position: "denied", path: prefix + "/workspaces", token: s.token("load-stranger"), status: http.StatusNotFound, errorCode: "PRODUCT_NOT_FOUND"},
	}

	pool, err := db.PoolConfig(s.database.Application)
	if err != nil {
		t.Fatal(err)
	}
	report := listLoadReport{
		SchemaVersion: 1, StartedAt: started, DurationPerCase: duration.String(),
		GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(), GoMaxProcs: runtime.GOMAXPROCS(0), PoolMaxConns: pool.MaxConns,
		Dataset:  map[string]int{"products": 20001, "workspaces": 40001, "memberships": 80200, "role_assignments": 22201},
		Identity: "local development issuer; real JWT signature verification; warmed JWKS",
		Model:    "closed loop; warm data; same-host HTTP client and server; no race instrumentation; latency includes body validation",
	}
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	report.Commit = string(commit[:len(commit)-1])
	source, err := os.ReadFile("lists_load_test.go")
	if err != nil {
		t.Fatal(err)
	}
	report.HarnessSHA256 = fmt.Sprintf("%x", sha256.Sum256(source))
	store, err := os.ReadFile("../../internal/productgovernance/adapters/postgres/store.go")
	if err != nil {
		t.Fatal(err)
	}
	report.ListStoreSHA256 = fmt.Sprintf("%x", sha256.Sum256(store))
	if err := s.owner.QueryRow(t.Context(), "SHOW server_version").Scan(&report.PostgreSQL); err != nil {
		t.Fatal(err)
	}
	// Verify row counts from PostgreSQL, rather than reporting the intended
	// fixture size as if it had been observed.
	for table, want := range report.Dataset {
		var count int
		if err := s.owner.QueryRow(t.Context(), "SELECT count(*) FROM productgovernance."+map[string]string{
			"products": "product", "workspaces": "workspace", "memberships": "workspace_membership", "role_assignments": "product_role_assignment",
		}[table]).Scan(&count); err != nil || count != want {
			t.Fatalf("%s has %d rows, want %d: %v", table, count, want, err)
		}
	}
	for _, concurrency := range []int{1, 8, 32} {
		for _, c := range cases {
			result := measureListLoad(client, s.server.URL, c, concurrency, duration)
			report.Results = append(report.Results, result)
			t.Logf("%s/%s c=%d requests=%d errors=%d rps=%.1f p95=%.2fms p99=%.2fms",
				c.name, c.position, concurrency, result.Requests, result.Errors, result.RPS, result.P95MS, result.P99MS)
		}
	}
	for _, c := range denials {
		report.Results = append(report.Results, measureListLoad(client, s.server.URL, c, 32, duration))
	}
	report.FinishedAt = time.Now().UTC()
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Never silently replace an earlier observation. Choose a new report path
	// for a new run, including a rerun after a failed experiment.
	file, err := os.OpenFile(os.Getenv("IDP_FABRIC_LIST_LOAD_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		if result.Requests == 0 || result.Errors != 0 {
			t.Errorf("%s/%s c=%d: %d requests, %d errors; first: %s",
				result.Name, result.Position, result.Concurrency, result.Requests, result.Errors, result.FirstError)
		}
	}
}

type listLoadReport struct {
	SchemaVersion   int                   `json:"schema_version"`
	StartedAt       time.Time             `json:"started_at"`
	FinishedAt      time.Time             `json:"finished_at"`
	Commit          string                `json:"application_commit"`
	HarnessSHA256   string                `json:"harness_sha256"`
	ListStoreSHA256 string                `json:"list_store_sha256"`
	GoVersion       string                `json:"go_version"`
	OS              string                `json:"os"`
	Arch            string                `json:"arch"`
	CPUs            int                   `json:"logical_cpus"`
	GoMaxProcs      int                   `json:"gomaxprocs"`
	PostgreSQL      string                `json:"postgresql"`
	PoolMaxConns    int32                 `json:"pool_max_conns"`
	Dataset         map[string]int        `json:"observed_dataset_rows"`
	Identity        string                `json:"identity"`
	Model           string                `json:"model"`
	DurationPerCase string                `json:"duration_per_case"`
	Results         []listLoadObservation `json:"results"`
}

type listLoadObservation struct {
	Name          string         `json:"name"`
	Position      string         `json:"page_position"`
	Concurrency   int            `json:"concurrency"`
	ExpectedHTTP  int            `json:"expected_http"`
	Requests      int            `json:"requests"`
	Errors        int            `json:"errors"`
	Statuses      map[string]int `json:"http_status_counts"`
	Seconds       float64        `json:"elapsed_seconds"`
	RPS           float64        `json:"requests_per_second"`
	P50MS         float64        `json:"p50_ms"`
	P95MS         float64        `json:"p95_ms"`
	P99MS         float64        `json:"p99_ms"`
	MaxMS         float64        `json:"max_ms"`
	ResponseBytes int64          `json:"response_body_bytes"`
	FirstError    string         `json:"first_error,omitempty"`
}

type listLoadCase struct {
	name, position, path, token, id, wantNext, errorCode string
	wantIDs                                              []string
	status                                               int
}

// No testing.T methods are called from load goroutines: every failure becomes
// an observation, and the complete report is retained before the test fails.
func measureListLoad(client *http.Client, baseURL string, c listLoadCase, concurrency int, duration time.Duration) listLoadObservation {
	type samples struct {
		times    []time.Duration
		statuses map[string]int
		errors   int
		bytes    int64
		first    string
	}
	results := make(chan samples, concurrency)
	started := time.Now()
	until := started.Add(duration)
	var ready sync.WaitGroup
	ready.Add(concurrency)
	begin := make(chan struct{})
	for range concurrency {
		go func() {
			got := samples{statuses: map[string]int{}}
			ready.Done()
			<-begin
			for time.Now().Before(until) {
				start := time.Now()
				status, bytes, err := requestListLoad(client, baseURL, c)
				got.times = append(got.times, time.Since(start))
				got.statuses[strconv.Itoa(status)]++
				got.bytes += bytes
				if err != nil {
					got.errors++
					if got.first == "" {
						got.first = err.Error()
					}
				}
			}
			results <- got
		}()
	}
	ready.Wait()
	close(begin)
	observed := listLoadObservation{Name: c.name, Position: c.position, Concurrency: concurrency, ExpectedHTTP: c.status, Statuses: map[string]int{}}
	var times []time.Duration
	for range concurrency {
		got := <-results
		times = append(times, got.times...)
		observed.Errors += got.errors
		observed.ResponseBytes += got.bytes
		if observed.FirstError == "" {
			observed.FirstError = got.first
		}
		for status, count := range got.statuses {
			observed.Statuses[status] += count
		}
	}
	observed.Seconds = time.Since(started).Seconds()
	observed.Requests = len(times)
	observed.RPS = float64(len(times)) / observed.Seconds
	slices.Sort(times)
	percentile := func(p float64) float64 {
		if len(times) == 0 {
			return 0
		}
		return float64(times[int(math.Ceil(p*float64(len(times))))-1]) / float64(time.Millisecond)
	}
	observed.P50MS, observed.P95MS, observed.P99MS, observed.MaxMS = percentile(.50), percentile(.95), percentile(.99), percentile(1)
	return observed
}

func requestListLoad(client *http.Client, baseURL string, c listLoadCase) (int, int64, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+c.path, nil)
	if err != nil {
		return 0, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, int64(len(body)), err
	}
	if response.StatusCode != c.status {
		return response.StatusCode, int64(len(body)), fmt.Errorf("HTTP %d, want %d", response.StatusCode, c.status)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return response.StatusCode, int64(len(body)), err
	}
	if c.status != http.StatusOK {
		var code string
		_ = json.Unmarshal(object["error_code"], &code)
		if _, leaked := object["items"]; leaked || code != c.errorCode {
			return response.StatusCode, int64(len(body)), fmt.Errorf("denial leaks a list or has error code %q", code)
		}
		return response.StatusCode, int64(len(body)), nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(object["items"], &items); err != nil {
		return response.StatusCode, int64(len(body)), err
	}
	ids := make([]string, len(items))
	for i, item := range items {
		_ = json.Unmarshal(item[c.id], &ids[i])
	}
	var next string
	_ = json.Unmarshal(object["next_page_token"], &next)
	if !slices.Equal(ids, c.wantIDs) || next != c.wantNext || response.Header.Get("Cache-Control") != "no-store" {
		return response.StatusCode, int64(len(body)), fmt.Errorf("page contents, cursor or cache policy changed under load")
	}
	return response.StatusCode, int64(len(body)), nil
}

func seedListLoad(t *testing.T, s *stack) (productID, workspaceID string) {
	t.Helper()
	productID = s.register("load-admin", "load-target", "load-owner")
	workspaceID = s.createWorkspace(productID, "load-owner", "load-target")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	tx, err := s.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	base := time.Now().UTC().Add(-24 * time.Hour)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO productgovernance.product
			(product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
			 aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
			SELECT gen_random_uuid(), 'load-product-'||i, 'Load product '||i, 'ACTIVE', $1, 'load-org',
				1, $2::timestamptz + i * interval '1 microsecond', $1, 'load-admin', $2::timestamptz + i * interval '1 microsecond'
			FROM generate_series(1,20000) i`, []any{s.issuer.URL(), base}},
		{`INSERT INTO productgovernance.workspace
			(workspace_id, product_id, workspace_key, display_name, status, aggregate_version,
			 created_at, created_by_issuer, created_by_subject, updated_at)
			SELECT gen_random_uuid(), $1::uuid, 'load-workspace-'||i, 'Load workspace '||i, 'ACTIVE',
				1, $3::timestamptz + i * interval '1 microsecond', $2, 'load-owner', $3::timestamptz + i * interval '1 microsecond'
			FROM generate_series(1,20000) i`, []any{productID, s.issuer.URL(), base}},
		{`INSERT INTO productgovernance.workspace
			(workspace_id, product_id, workspace_key, display_name, status, aggregate_version,
			 created_at, created_by_issuer, created_by_subject, updated_at)
			SELECT gen_random_uuid(), product_id, 'background', 'Background workspace', 'ACTIVE',
				1, created_at, $1, 'background-owner', created_at
			FROM productgovernance.product WHERE product_key LIKE 'load-product-%'`, []any{s.issuer.URL()}},
		{`INSERT INTO productgovernance.product_role_assignment
			(product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
			SELECT product_id, $1, 'background-owner', 'product_owner', created_at, $1, 'load-admin'
			FROM productgovernance.product WHERE product_key LIKE 'load-product-%'`, []any{s.issuer.URL()}},
		{`INSERT INTO productgovernance.product_role_assignment
			(product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
			SELECT $1::uuid, $2, 'owner-'||i, 'product_owner',
				$3::timestamptz + i * interval '1 microsecond', $2, 'load-owner'
			FROM generate_series(1,2000) i`, []any{productID, s.issuer.URL(), base}},
		{`INSERT INTO productgovernance.product_role_assignment
			(product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
			SELECT product_id, $1, 'load-owner', 'product_owner', created_at, $1, 'load-admin'
			FROM productgovernance.product WHERE product_key LIKE 'load-product-%' ORDER BY created_at LIMIT 200`, []any{s.issuer.URL()}},
		{`INSERT INTO productgovernance.workspace_membership
			(membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
			 membership_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), $1::uuid, $2::uuid, $3, 'member-'||i, 'internal_author',
				1, $4::timestamptz + i * interval '1 microsecond', $3, 'load-owner'
			FROM generate_series(1,40000) i`, []any{productID, workspaceID, s.issuer.URL(), base}},
		{`INSERT INTO productgovernance.workspace_membership
			(membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
			 membership_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, workspace_id, $1, 'background-member', 'internal_author',
				1, created_at, $1, 'load-owner'
			FROM productgovernance.workspace WHERE workspace_key <> 'load-target'`, []any{s.issuer.URL()}},
		{`INSERT INTO productgovernance.workspace_membership
			(membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
			 membership_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, workspace_id, $1, 'load-member', 'internal_author',
				1, created_at, $1, 'load-owner'
			FROM productgovernance.workspace WHERE product_id=$2::uuid AND workspace_key <> 'load-target'
			ORDER BY created_at LIMIT 200`, []any{s.issuer.URL(), productID}},
	}
	for i, statement := range statements {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed statement %d: %v", i+1, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"product", "workspace", "workspace_membership", "product_role_assignment"} {
		if _, err := s.owner.Exec(ctx, "ANALYZE productgovernance."+table); err != nil {
			t.Fatal(err)
		}
	}
	return productID, workspaceID
}
