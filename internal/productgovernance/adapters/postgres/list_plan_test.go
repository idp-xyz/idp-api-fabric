package postgres_test

import (
	"encoding/json"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// This guards work performed, not elapsed time or a particular join type.
// A sparse member's last page must not probe thousands of other workspaces
// in the hope of finding one more row for the page's lookahead.
func TestASparseMembersLastPageDoesNotScanTheRestOfTheProduct(t *testing.T) {
	f := newFixture(t)
	of := product(t, "sparse-member")
	f.add(t, of)
	owner := pgtest.Connect(t, f.database.Migrator)
	ctx := t.Context()
	_, err := owner.Exec(ctx, `INSERT INTO productgovernance.workspace
        (workspace_id, product_id, workspace_key, display_name, status, aggregate_version,
         created_at, created_by_issuer, created_by_subject, updated_at)
        SELECT gen_random_uuid(), $1::uuid, 'space-'||i, 'Workspace '||i, 'ACTIVE', 1,
            $3::timestamptz + i * interval '1 microsecond', $2, 'alice',
            $3::timestamptz + i * interval '1 microsecond'
        FROM generate_series(1,20000) i`, string(of.ID), bob.Issuer, created)
	if err != nil {
		t.Fatal(err)
	}
	// Two roles in each of 200 visible workspaces must still yield 200
	// workspaces, and no row is shared with the remaining 19,800.
	_, err = owner.Exec(ctx, `INSERT INTO productgovernance.workspace_membership
        (membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
         membership_version, granted_at, granted_by_issuer, granted_by_subject)
        SELECT gen_random_uuid(), product_id, workspace_id, $1, $2, role, 1, created_at, $1, 'alice'
        FROM (SELECT * FROM productgovernance.workspace ORDER BY created_at LIMIT 200) w
        CROSS JOIN unnest(ARRAY['internal_author','workspace_maintainer']) AS role`, bob.Issuer, bob.Subject)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workspace", "workspace_membership"} {
		if _, err := owner.Exec(ctx, "ANALYZE productgovernance."+table); err != nil {
			t.Fatal(err)
		}
	}
	first, err := f.workspaces.ListFor(ctx, of.ID, bob, ports.Page{Limit: 100})
	if err != nil || len(first) != 100 {
		t.Fatalf("first page: %d rows: %v", len(first), err)
	}
	position := ports.PositionOfWorkspace(first[99])
	last, err := f.workspaces.ListFor(ctx, of.ID, bob, ports.Page{Limit: 101, After: &position})
	if err != nil || len(last) != 100 {
		t.Fatalf("last page with lookahead: %d rows, want 100: %v", len(last), err)
	}
	var encoded []byte
	err = f.app.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+postgres.ListWorkspacesForTest,
		position.At, position.ID, 101, string(of.ID), bob.Issuer, bob.Subject).Scan(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	type planNode struct {
		Relation string     `json:"Relation Name"`
		Rows     float64    `json:"Actual Rows"`
		Loops    float64    `json:"Actual Loops"`
		Removed  float64    `json:"Rows Removed by Filter"`
		Children []planNode `json:"Plans"`
	}
	var plans []struct{ Plan planNode }
	if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("decode execution plan: %v: %s", err, encoded)
	}
	var visited float64
	var walk func(planNode)
	walk = func(node planNode) {
		if node.Relation == "workspace" {
			visited += (node.Rows + node.Removed) * node.Loops
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(plans[0].Plan)
	if visited < 1 || visited > 400 {
		t.Fatalf("visited %.0f workspace tuples for a member of 200 workspaces: %s", visited, encoded)
	}
	t.Logf("workspace tuples visited: %.0f (20,000 total, 200 visible, two roles each); plan: %s", visited, encoded)
}
