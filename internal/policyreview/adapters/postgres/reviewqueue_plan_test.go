package postgres_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// A page of the queue costs what it scans, and what it scans is the reviews
// of the workspace and not of the table: the index of the list (migration 006)
// is read where the page starts, in the order the page is cut in, and nothing
// is sorted. This guards the work the statement performs, not the time it
// takes: the number of rows it visits in the tables.
//
// Two workspaces of one product hold 20,000 reviews each, written alternately.
// In the first one review in ten is open, one is rejected, two are withdrawn
// and the rest are approved, and each was asked for by one of 50 principals.
// In the second all are concluded but the newest 30, which is the worst case
// of the open ones: they are at the end of a list that is read from its start.
// A status is no column, so a page kept to one costs the reviews that are
// scanned for the ones it returns. How many rows each page visits is logged,
// and the bounds are those a page that is read in the order of the index
// cannot exceed; what a page kept to the open reviews visits is recorded and
// has no bound.
func TestAPageOfTheQueueCostsWhatItScans(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	owner := pgtest.Connect(t, database.Migrator)
	app := pgtest.Connect(t, database.Application)

	const (
		product   = "11111111-1111-4111-8111-111111111111"
		spread    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		tail      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		elsewhere = "22222222-2222-4222-8222-222222222222"
		snapshot  = "33333333-3333-4333-8333-333333333333"
	)
	// Every row is derived from the workspace and its number, so that the
	// conclusions can be written for the requests without a table to keep
	// what was made: 400 checks, 40,000 requests and what they concluded.
	// $1 and $2 are the workspaces, $3 the product and $4 the snapshot.
	for _, step := range []struct {
		statement string
		arguments []any
	}{
		{`INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
		  VALUES ($2::uuid, $1::uuid, 'fabric-global-contract-governance', 'sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea, now())`,
			[]any{product, snapshot}},
		{`INSERT INTO policyreview.check_run (
		      check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
		      tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject)
		  SELECT md5('check' || w.id::text || n::text)::uuid, $3::uuid, w.id, md5('asset' || w.id::text || n::text)::uuid,
		         md5('revision' || w.id::text || n::text)::uuid, $4::uuid, 'sha256:' || repeat('a', 64),
		         '[]'::bytea, 'sha256:' || encode(sha256('[]'::bytea), 'hex'), 'PASS', now(), now(), 'https://issuer.example', 'author'
		  FROM (VALUES ($1::uuid, 1), ($2::uuid, 2)) AS w(id, slot), generate_series(1, 200) AS n`,
			[]any{spread, tail, product, snapshot}},
		{`INSERT INTO policyreview.review_request (
		      review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
		      input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary)
		  SELECT md5('review' || w.id::text || j::text)::uuid, $3::uuid, w.id, md5('asset' || w.id::text || x.n::text)::uuid,
		         md5('revision' || w.id::text || x.n::text)::uuid, 2 + j / 200, md5('check' || w.id::text || x.n::text)::uuid,
		         'sha256:' || repeat('a', 64), NULL, timestamptz '2026-01-01 00:00:00+00' + (j * 2 + w.slot) * interval '1 second',
		         'https://issuer.example', 'user-' || (j % 50), false
		  FROM (VALUES ($1::uuid, 1), ($2::uuid, 2)) AS w(id, slot), generate_series(1, 20000) AS j,
		       LATERAL (SELECT j % 200 + 1 AS n) AS x`,
			[]any{spread, tail, product}},
		{`INSERT INTO policyreview.review_conclusion (
		      review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until)
		  SELECT md5('review' || w.id::text || j::text)::uuid, c.outcome, CASE c.outcome WHEN 'REJECTED' THEN 'not yet' END,
		         c.t + interval '1 hour', 'https://issuer.example', 'reviewer', CASE c.outcome WHEN 'APPROVED' THEN c.t + interval '1 day' END
		  FROM (VALUES ($1::uuid, 1), ($2::uuid, 2)) AS w(id, slot), generate_series(1, 20000) AS j,
		       LATERAL (SELECT timestamptz '2026-01-01 00:00:00+00' + (j * 2 + w.slot) * interval '1 second' AS t,
		                       CASE WHEN w.slot = 1 AND j % 10 = 0 THEN NULL
		                            WHEN w.slot = 2 AND j > 19970 THEN NULL
		                            WHEN j % 10 = 1 THEN 'REJECTED'
		                            WHEN j % 10 IN (2, 3) THEN 'WITHDRAWN'
		                            ELSE 'APPROVED' END AS outcome) AS c
		  WHERE c.outcome IS NOT NULL`,
			[]any{spread, tail}},
	} {
		if _, err := owner.Exec(t.Context(), step.statement, step.arguments...); err != nil {
			t.Fatalf("make the reviews: %v", err)
		}
	}
	for _, table := range []string{"review_request", "review_conclusion"} {
		if _, err := owner.Exec(t.Context(), "ANALYZE policyreview."+table); err != nil {
			t.Fatal(err)
		}
	}
	var requests, conclusions, open int
	if err := owner.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM policyreview.review_request), (SELECT count(*) FROM policyreview.review_conclusion),
		       (SELECT count(*) FROM policyreview.review_request q WHERE NOT EXISTS
		            (SELECT 1 FROM policyreview.review_conclusion c WHERE c.review_id = q.review_id))`).Scan(&requests, &conclusions, &open); err != nil {
		t.Fatal(err)
	}
	if requests != 40000 || conclusions != 40000-2000-30 || open != 2030 {
		t.Fatalf("the table holds %d requests, %d conclusions and %d open reviews, want the 40,000, 37,970 and 2,030 the cases were made of", requests, conclusions, open)
	}
	// A page in the middle of the first workspace: behind its 10,000th review.
	var middle ports.Position
	if err := owner.QueryRow(t.Context(), `
		SELECT requested_at, review_id::text FROM policyreview.review_request WHERE workspace_id = $1
		ORDER BY requested_at, review_id OFFSET 10000 LIMIT 1`, spread).Scan(&middle.At, &middle.ID); err != nil {
		t.Fatal(err)
	}

	type planNode struct {
		Type     string     `json:"Node Type"`
		Parent   string     `json:"Parent Relationship"`
		Relation string     `json:"Relation Name"`
		Index    string     `json:"Index Name"`
		Rows     float64    `json:"Actual Rows"`
		Loops    float64    `json:"Actual Loops"`
		Removed  float64    `json:"Rows Removed by Filter"`
		Rechecks float64    `json:"Rows Removed by Index Recheck"`
		Children []planNode `json:"Plans"`
	}
	// visited is what PostgreSQL says the statement read of each table to
	// answer one page, and whether it had to sort what it read: a page that is
	// read from the index in its own order is not sorted.
	visited := func(where string, filter ports.ReviewFilter, position *ports.Position) (requested, concluded int, sorted bool, indexes, plan string) {
		t.Helper()
		statement, err := postgres.ListWorkspaceStatementForTest(filter.Status)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement, "ORDER BY q.requested_at, q.review_id") || !strings.Contains(statement, "q.product_id = $1 AND q.workspace_id = $2") {
			t.Fatalf("the statement does not narrow to the workspace and order the reviews as the queue is ordered:\n%s", statement)
		}
		var at, id any
		if position != nil {
			at, id = position.At, position.ID
		}
		var encoded []byte
		if err := app.QueryRow(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement,
			product, where, at, id, filter.RequestedByIssuer, filter.RequestedBySubject, 101).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var plans []struct{ Plan planNode }
		if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode the execution plan: %v: %s", err, encoded)
		}
		var used []string
		var walk func(planNode)
		walk = func(node planNode) {
			// What a review is made of, its scope and its waivers, is read for
			// each review of the page by a plan of its own, which orders them.
			// It is not the order of the page.
			if node.Parent == "SubPlan" {
				return
			}
			rows := (node.Rows + node.Removed + node.Rechecks) * node.Loops
			switch node.Relation {
			case "review_request":
				requested += int(rows)
				if node.Index != "" {
					used = append(used, node.Index)
				}
			case "review_conclusion":
				concluded += int(rows)
			}
			if strings.Contains(node.Type, "Sort") {
				sorted = true
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(plans[0].Plan)
		return requested, concluded, sorted, strings.Join(used, ","), string(encoded)
	}

	for _, c := range []struct {
		name     string
		where    string
		filter   ports.ReviewFilter
		position *ports.Position
		// most is the most rows of the table of requests the page may visit,
		// and -1 says that it has no bound but is recorded; inOrder that the
		// page is read from the index of the list and is not sorted.
		most    int
		inOrder bool
	}{
		{name: "the first page", where: spread, most: 400, inOrder: true},
		{name: "a page in the middle", where: spread, position: &middle, most: 400, inOrder: true},
		{name: "the approved reviews, six in ten", where: spread, filter: ports.ReviewFilter{Status: "APPROVED"}, most: 2000, inOrder: true},
		{name: "the withdrawn reviews, two in ten", where: spread, filter: ports.ReviewFilter{Status: "WITHDRAWN"}, most: 2000, inOrder: true},
		{name: "the rejected reviews, one in ten", where: spread, filter: ports.ReviewFilter{Status: "REJECTED"}, most: 2000, inOrder: true},
		{name: "the open reviews, one in ten", where: spread, filter: ports.ReviewFilter{Status: "IN_REVIEW"}, most: -1},
		{name: "one principal of fifty", where: spread, filter: ports.ReviewFilter{RequestedBySubject: "user-7"}, most: 6000, inOrder: true},
		{name: "one issuer and one principal of fifty", where: spread,
			filter: ports.ReviewFilter{RequestedByIssuer: "https://issuer.example", RequestedBySubject: "user-7"}, most: 6000, inOrder: true},
		{name: "the open reviews, the newest 30 of 20000", where: tail, filter: ports.ReviewFilter{Status: "IN_REVIEW"}, most: -1},
		{name: "the first page of the other workspace", where: tail, most: 400, inOrder: true},
		{name: "a workspace of another product", where: elsewhere, most: 5, inOrder: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			requested, concluded, sorted, indexes, plan := visited(c.where, c.filter, c.position)
			t.Logf("%d rows of review_request and %d of review_conclusion visited of 40,000 and 37,970; sorted: %t; index: %s",
				requested, concluded, sorted, indexes)
			if c.most >= 0 && requested > c.most {
				t.Errorf("%d rows of 40,000 were visited, want at most %d: %s", requested, c.most, plan)
			}
			if c.inOrder && sorted {
				t.Errorf("the reviews are sorted, and not read in the order of the index of the queue: %s", plan)
			}
			if c.inOrder && c.where != elsewhere && !strings.Contains(indexes, "review_request_queue_order") {
				t.Errorf("the page is read through %q, want review_request_queue_order: %s", indexes, plan)
			}
		})
	}
}
