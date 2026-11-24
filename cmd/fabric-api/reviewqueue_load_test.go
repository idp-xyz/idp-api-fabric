package main

import (
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
)

// This is an opt-in measurement, not a timing assertion of the CI suite. It
// answers one question: how long does a page of the queue of a workspace take
// to read when the workspace holds many reviews, above all when it is kept to
// the reviews that are open, which a status that is no column cannot read
// from the index of the list (policyreview/006). It runs the whole service as
// the functional tests do: the transport, the validator, the verifier of the
// tokens, the use cases and a real PostgreSQL reached as the application role.
// Only the identity provider is a stand-in. The reviews are written by bulk
// SQL, as the owner of the tables, into two workspaces of one product: in the
// first one review in ten is open, one is rejected, two are withdrawn and the
// rest are approved, and each was asked for by one of 50 principals; in the
// second all are concluded but the newest ones, the worst case of the open
// ones, which are at the end of a list that is read from its start. Every
// measured request is an HTTP request.
//
// Run it without -race, which would change the measurement:
//
//	IDP_FABRIC_REVIEW_QUEUE_LOAD=1 go test -count=1 -v -run TestAPageOfTheQueueIsReadInTheTimeItTakes ./cmd/fabric-api
//
// with the two DSNs the other tests take. IDP_FABRIC_REVIEW_QUEUE_LOAD_REVIEWS
// (10000 by default) is the number of reviews of each of the two workspaces,
// IDP_FABRIC_REVIEW_QUEUE_LOAD_OPEN (30) the number of open reviews at the end
// of the second, and IDP_FABRIC_REVIEW_QUEUE_LOAD_READS (7) how many times each
// page is read. A review is up to three rows (its request, its scope and, but
// for the open ones, its conclusion), and the shared test database keeps its
// data in memory: the reviews are at most 15000 of each workspace, which is
// 87,000 rows in all, and the database of the test is dropped when it ends.
// The numbers are of one machine, a request at a time, with a warm database,
// and are good for their order of magnitude.
func TestAPageOfTheQueueIsReadInTheTimeItTakes(t *testing.T) {
	if os.Getenv("IDP_FABRIC_REVIEW_QUEUE_LOAD") != "1" {
		t.Skip("opt-in measurement; see the comment of the test")
	}
	reviews, newest, reads := queueLoad(t, "REVIEWS", 10000), queueLoad(t, "OPEN", 30), queueLoad(t, "READS", 7)
	if reviews < 200 || reviews > 15000 || newest < 1 || newest > reviews/10 || reads < 1 {
		t.Fatalf("%d reviews, %d open at the end and %d reads: at least 200 reviews and at most 15000 of each workspace, "+
			"at least one open at the end and at most a tenth of the reviews, and at least one read", reviews, newest, reads)
	}

	w := reviewsFor(t)
	backlog := w.createWorkspace(w.product, "owner", "backlog")
	w.join(w.product, backlog, "owner", "reviewer", "internal_reviewer")
	started := time.Now()
	w.seedQueue(w.product, w.workspace, reviews, 10, 0)
	w.seedQueue(w.product, backlog, reviews, 0, newest)
	var version string
	if err := w.owner.QueryRow(t.Context(), `SHOW server_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d reviews in each of two workspaces, written in %v; PostgreSQL %s, %d CPUs",
		reviews, time.Since(started).Round(time.Millisecond), version, runtime.NumCPU())

	token := w.token("reviewer")
	timed := func(what, path string, want int) {
		t.Helper()

		var durations []time.Duration
		for range reads + 1 {
			begun := time.Now()
			got := w.callWith(http.MethodGet, path, token, "")
			durations = append(durations, time.Since(begun))
			if items, _ := got.body["items"].([]any); got.status != http.StatusOK || len(items) != want {
				t.Fatalf("%s: %d with %d items, want 200 with %d", what, got.status, len(items), want)
			}
		}
		cold, warm := durations[0], slices.Clone(durations[1:])
		slices.Sort(warm)
		t.Logf("%-62s first %8v; then of %d reads: min %8v  median %8v  max %8v", what, cold.Round(time.Microsecond), reads,
			warm[0].Round(time.Microsecond), warm[len(warm)/2].Round(time.Microsecond), warm[len(warm)-1].Round(time.Microsecond))
	}
	spread := "/v1/products/" + w.product + "/workspaces/" + w.workspace + "/reviews"
	tail := "/v1/products/" + w.product + "/workspaces/" + backlog + "/reviews"
	timed("the first page of the queue (page_size=100)", spread+"?page_size=100", 100)
	timed("the first page of the queue (the default page_size)", spread, 50)
	timed("a page of 1 review of the queue (page_size=1)", spread+"?page_size=1", 1)
	// The position of a page in the middle of the queue is the one the page
	// before it hands out.
	var middle string
	for seen := 0; seen < reviews/2; seen += 100 {
		path := spread + "?page_size=100"
		if middle != "" {
			path += "&page_token=" + middle
		}
		var items []map[string]any
		items, middle = w.page(path, "reviewer")
		if len(items) != 100 || middle == "" {
			t.Fatalf("the queue ends after %d reviews of %d", seen+len(items), reviews)
		}
	}
	timed("a page in the middle of the queue (page_size=100)", spread+"?page_size=100&page_token="+middle, 100)
	// What a page is kept to holds as many reviews as the tables say, a page
	// of them at most.
	holding := func(workspace, where string, arguments ...any) int {
		var number int
		if err := w.owner.QueryRow(t.Context(), `SELECT count(*) FROM policyreview.review_request q
			LEFT JOIN policyreview.review_conclusion c USING (review_id) WHERE q.workspace_id = $1 AND `+where,
			append([]any{workspace}, arguments...)...).Scan(&number); err != nil {
			t.Fatal(err)
		}
		return min(number, 100)
	}
	status := func(workspace, wanted string) int {
		return holding(workspace, `coalesce(c.outcome, 'IN_REVIEW') = $2`, wanted)
	}
	timed("the approved reviews, six in ten (page_size=100)", spread+"?status=APPROVED&page_size=100", status(w.workspace, "APPROVED"))
	timed("the withdrawn reviews, two in ten (page_size=100)", spread+"?status=WITHDRAWN&page_size=100", status(w.workspace, "WITHDRAWN"))
	timed("the rejected reviews, one in ten (page_size=100)", spread+"?status=REJECTED&page_size=100", status(w.workspace, "REJECTED"))
	timed("the open reviews, one in ten (page_size=100)", spread+"?status=IN_REVIEW&page_size=100", status(w.workspace, "IN_REVIEW"))
	timed("the open reviews, the newest of the other workspace", tail+"?status=IN_REVIEW&page_size=100", status(backlog, "IN_REVIEW"))
	timed("the reviews that one principal of fifty asked for", spread+"?requested_by_subject=user-7&page_size=100",
		holding(w.workspace, `q.requested_by_subject = $2`, "user-7"))

	begun := time.Now()
	pages := w.queuePages(tail, "", "reviewer", 100)
	t.Logf("the whole queue of %d reviews, read in %d pages of 100: %v", reviews, len(pages), time.Since(begun).Round(time.Millisecond))
	if got := len(listed(pages)); got != reviews {
		t.Errorf("the queue of the second workspace holds %d reviews, want %d", got, reviews)
	}
}

// seedQueue writes count reviews into the workspace of the product, with the
// checks they rest on: a review asked for one second after the one before it,
// by one of 50 principals. A tenth of the reviews are rejected, two tenths are
// withdrawn and the rest approved, but a review is open when it is every
// openEvery-th one, or one of the newest openNewest.
func (s *stack) seedQueue(product, workspace string, count, openEvery, openNewest int) {
	s.t.Helper()

	const snapshot = "33333333-3333-4333-8333-333333333333"
	for _, step := range []struct {
		statement string
		arguments []any
	}{
		{`INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
		  VALUES ($2::uuid, $1::uuid, 'fabric-global-contract-governance', 'sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea, now())
		  ON CONFLICT DO NOTHING`,
			[]any{product, snapshot}},
		{`INSERT INTO policyreview.check_run (
		      check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
		      tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject)
		  SELECT md5('check' || w.id::text || n::text)::uuid, $1::uuid, w.id, md5('asset' || w.id::text || n::text)::uuid,
		         md5('revision' || w.id::text || n::text)::uuid, $3::uuid, 'sha256:' || repeat('a', 64),
		         '[]'::bytea, 'sha256:' || encode(sha256('[]'::bytea), 'hex'), 'PASS', now(), now(), 'https://issuer.example', 'author'
		  FROM (SELECT $2::uuid AS id) AS w, generate_series(1, 200) AS n`,
			[]any{product, workspace, snapshot}},
		{`INSERT INTO policyreview.review_request (
		      review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
		      input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary)
		  SELECT md5('review' || w.id::text || j::text)::uuid, $1::uuid, w.id, md5('asset' || w.id::text || x.n::text)::uuid,
		         md5('revision' || w.id::text || x.n::text)::uuid, 2 + j / 200, md5('check' || w.id::text || x.n::text)::uuid,
		         'sha256:' || repeat('a', 64), NULL, timestamptz '2026-01-01 00:00:00+00' + j * interval '1 second',
		         'https://issuer.example', 'user-' || (j % 50), false
		  FROM (SELECT $2::uuid AS id) AS w, generate_series(1, $3::int) AS j, LATERAL (SELECT j % 200 + 1 AS n) AS x`,
			[]any{product, workspace, count}},
		{`INSERT INTO policyreview.review_scope (review_id, audience, environment)
		  SELECT md5('review' || w.id::text || j::text)::uuid, 'internal', 'production'
		  FROM (SELECT $1::uuid AS id) AS w, generate_series(1, $2::int) AS j`,
			[]any{workspace, count}},
		{`INSERT INTO policyreview.review_conclusion (
		      review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until)
		  SELECT md5('review' || w.id::text || j::text)::uuid, c.outcome, CASE c.outcome WHEN 'REJECTED' THEN 'not yet' END,
		         c.t + interval '1 hour', 'https://issuer.example', 'reviewer', CASE c.outcome WHEN 'APPROVED' THEN c.t + interval '1 day' END
		  FROM (SELECT $1::uuid AS id) AS w, generate_series(1, $2::int) AS j,
		       LATERAL (SELECT timestamptz '2026-01-01 00:00:00+00' + j * interval '1 second' AS t,
		                       CASE WHEN ($3::int > 0 AND j % $3::int = 0) OR j > $2::int - $4::int THEN NULL
		                            WHEN j % 10 = 1 THEN 'REJECTED'
		                            WHEN j % 10 IN (2, 3) THEN 'WITHDRAWN'
		                            ELSE 'APPROVED' END AS outcome) AS c
		  WHERE c.outcome IS NOT NULL`,
			[]any{workspace, count, openEvery, openNewest}},
	} {
		if _, err := s.owner.Exec(s.t.Context(), step.statement, step.arguments...); err != nil {
			s.t.Fatalf("make the reviews of the workspace %s: %v", workspace, err)
		}
	}
	for _, table := range []string{"review_request", "review_scope", "review_conclusion"} {
		if _, err := s.owner.Exec(s.t.Context(), "ANALYZE policyreview."+table); err != nil {
			s.t.Fatal(err)
		}
	}
}

// queueLoad is the number the variable of the measurement says, or what it is
// by default.
func queueLoad(t *testing.T, name string, fallback int) int {
	t.Helper()

	value := os.Getenv("IDP_FABRIC_REVIEW_QUEUE_LOAD_" + name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("IDP_FABRIC_REVIEW_QUEUE_LOAD_%s is %q, want a number", name, value)
	}
	return number
}
