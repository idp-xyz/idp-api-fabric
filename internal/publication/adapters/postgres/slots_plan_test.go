package postgres_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/publication/adapters/postgres"
)

// planNode is a node of the plan PostgreSQL makes of a statement, with what
// it did when it ran.
type planNode struct {
	Type     string     `json:"Node Type"`
	Relation string     `json:"Relation Name"`
	Rows     float64    `json:"Actual Rows"`
	Loops    float64    `json:"Actual Loops"`
	Removed  float64    `json:"Rows Removed by Filter"`
	Recheck  float64    `json:"Rows Removed by Index Recheck"`
	Children []planNode `json:"Plans"`
}

// slotsRead is how many slots the plan read, whether it kept them or not, and
// whether it read the table from its first row to its last.
func slotsRead(root planNode) (read float64, scanned bool) {
	var walk func(planNode)
	walk = func(node planNode) {
		if node.Relation == "slot" {
			read += (node.Rows + node.Removed + node.Recheck) * node.Loops
			scanned = scanned || node.Type == "Seq Scan"
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return read, scanned
}

// This guards work performed, not elapsed time or a particular join type. The
// slots of an asset are found among the targets of its product and the slots
// of a target among those of that target: neither reads the slots of the
// products and the targets around it, in the hope of finding more for the
// page. The table is made big on purpose: twenty thousand slots of other
// products, and fifteen hundred of this one.
func TestTheSlotsOfAnAssetAreNotFoundByReadingEverySlot(t *testing.T) {
	f := newSlotStore(t)
	owner, app := pgtest.Connect(t, f.database.Migrator), pgtest.Connect(t, f.database.Application)
	ctx := t.Context()
	exec := func(statement string, arguments ...any) {
		t.Helper()

		if _, err := owner.Exec(ctx, statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	// Two hundred products with a target of a hundred slots each.
	exec(`INSERT INTO publication.target (target_id, product_id, audience_config_id, portal_key, audience, environment, created_at)
SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 'portal', 'internal', 'sandbox', now() FROM generate_series(1, 200)`)
	exec(`INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id, active_release_id, generation)
SELECT gen_random_uuid(), t.product_id, t.target_id, gen_random_uuid(), gen_random_uuid(), 1
FROM publication.target t CROSS JOIN generate_series(1, 100) g`)
	// The product of the test: five targets, each with the same 300 assets.
	exec(`INSERT INTO publication.target (target_id, product_id, audience_config_id, portal_key, audience, environment, created_at)
SELECT gen_random_uuid(), $1::uuid, gen_random_uuid(), 'portal-' || g, 'internal', 'sandbox', now() FROM generate_series(1, 5) g`, f.product)
	exec(`CREATE TEMPORARY TABLE assets AS SELECT gen_random_uuid() AS asset_id, g AS n FROM generate_series(1, 300) g`)
	exec(`INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id, active_release_id, generation)
SELECT gen_random_uuid(), t.product_id, t.target_id, a.asset_id, gen_random_uuid(), 1
FROM publication.target t CROSS JOIN assets a WHERE t.product_id = $1::uuid`, f.product)
	exec(`INSERT INTO publication.operation
    (operation_id, slot_id, generation, key_digest, action, release_id, reason, actor_issuer, actor_subject, occurred_at)
SELECT gen_random_uuid(), slot_id, 1, md5(slot_id::text) || md5(slot_id::text), 'ACTIVATE', active_release_id, '',
       'https://issuer.example', 'publisher', now() - random() * interval '30 days'
FROM publication.slot`)
	for _, table := range []string{"target", "slot", "operation"} {
		exec("ANALYZE publication." + table)
	}

	var asset, target string
	if err := owner.QueryRow(ctx, `SELECT asset_id::text FROM assets WHERE n = 17`).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT target_id::text FROM publication.target WHERE product_id = $1 ORDER BY portal_key LIMIT 1`,
		f.product).Scan(&target); err != nil {
		t.Fatal(err)
	}
	rows, err := owner.Query(ctx, `SELECT asset_id::text FROM assets WHERE n % 2 = 0`)
	if err != nil {
		t.Fatal(err)
	}
	readable, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(readable) != 150 {
		t.Fatalf("name the assets the caller reads: %d, %v", len(readable), err)
	}
	var total int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM publication.slot`).Scan(&total); err != nil || total != 21500 {
		t.Fatalf("the table holds %d slots (%v), want 21500", total, err)
	}

	explain := func(statement string, arguments ...any) planNode {
		t.Helper()

		var encoded []byte
		if err := app.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement, arguments...).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var plans []struct{ Plan planNode }
		if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode the execution plan: %v: %s", err, encoded)
		}
		return plans[0].Plan
	}

	// The asset is on the five targets of its product: five slots to find.
	plan := explain(postgres.SlotsOfAssetForTest, f.product, asset, nil, nil, 101)
	read, scanned := slotsRead(plan)
	t.Logf("the slots of an asset read %.0f slots of %d", read, total)
	if read < 1 || read > 50 || scanned {
		t.Errorf("the slots of an asset read %.0f slots of %d (every one: %t), want about five", read, total, scanned)
	}
	// The target holds 300 slots, of which the caller reads 150: the page
	// reads the slots of the target and no others.
	plan = explain(postgres.SlotsOfTargetForTest, f.product, target, readable, nil, nil, 101)
	read, scanned = slotsRead(plan)
	t.Logf("the slots of a target read %.0f slots of %d", read, total)
	if read < 1 || read > 600 || scanned {
		t.Errorf("the slots of a target read %.0f slots of %d (every one: %t), want about the 300 of the target", read, total, scanned)
	}
}
