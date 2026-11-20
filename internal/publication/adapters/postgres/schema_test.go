package postgres_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// The tables hold every writer to what publication is, whatever the model
// let through. A slot moves one generation at a time and stays the slot of
// its asset on its target. A target counts its blocks one at a time and
// stays what it is. An operation is written once and says what its action
// needs. A block is cleared once, by someone other than whoever placed it,
// and is otherwise what it was. The role a process connects with removes
// nothing and changes no operation.
func TestTheTablesHoldPublicationToOneStepAtATime(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	app, owner := pgtest.Connect(t, database.Application), pgtest.Connect(t, database.Migrator)
	product, config, target, slot, asset := ids.NewUUID(), ids.NewUUID(), ids.NewUUID(), ids.NewUUID(), ids.NewUUID()
	release, placed, lifted := ids.NewUUID(), ids.NewUUID(), ids.NewUUID()
	key := strings.Repeat("a", 64)

	targetRow := func(id, product, config, portal, audience string) string {
		return fmt.Sprintf(`INSERT INTO publication.target (target_id, product_id, audience_config_id, portal_key, audience, environment, created_at)
VALUES ('%s', '%s', '%s', '%s', '%s', 'sandbox', now())`, id, product, config, portal, audience)
	}
	slotRow := func(product, target, asset, serving string) string {
		return fmt.Sprintf(`INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id, active_release_id)
VALUES (gen_random_uuid(), '%s', '%s', '%s', %s)`, product, target, asset, serving)
	}
	operation := func(slot string, generation int, key, action, release, reason string) string {
		return fmt.Sprintf(`INSERT INTO publication.operation
    (operation_id, slot_id, generation, key_digest, action, release_id, reason, actor_issuer, actor_subject, occurred_at)
VALUES (gen_random_uuid(), '%s', %d, '%s', '%s', %s, '%s', 'https://issuer.example', 'publisher', now())`, slot, generation, key, action, release, reason)
	}
	blockRow := func(id, product, target, asset, release string, generation int, reason string) string {
		return fmt.Sprintf(`INSERT INTO publication.access_block
    (block_id, product_id, target_id, api_asset_id, release_id, generation, reason, created_by_issuer, created_by_subject, created_at)
VALUES ('%s', '%s', '%s', %s, %s, %d, '%s', 'https://issuer.example', 'security', now())`, id, product, target, asset, release, generation, reason)
	}
	// clearing is the statement that clears a block: by whom, at which
	// generation of the blocks of the target, and what else it sets.
	clearing := func(id string, generation int, subject, besides string) string {
		at := "now()"
		if besides == "earlier" {
			at, besides = "created_at - interval '1 hour'", ""
		}
		return fmt.Sprintf(`UPDATE publication.access_block SET clear_generation = %d, clear_reason = 'verified safe',
    cleared_by_issuer = 'https://issuer.example', cleared_by_subject = '%s', cleared_at = %s%s WHERE block_id = '%s'`,
			generation, subject, at, besides, id)
	}
	quoted := func(id string) string { return "'" + id + "'" }
	const random = "gen_random_uuid()"
	slotStep := `UPDATE publication.slot SET `
	targetStep := `UPDATE publication.target SET `
	of := func(table, id string) string { return fmt.Sprintf(` WHERE %s_id = '%s'`, table, id) }

	// What the model writes is what the tables take, by hand as well.
	for _, statement := range []string{
		targetRow(target, product, config, "engineering", "internal"),
		fmt.Sprintf(`INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id) VALUES ('%s', '%s', '%s', '%s')`, slot, product, target, asset),
		slotStep + `generation = 1, active_release_id = ` + quoted(release) + of("slot", slot),
		operation(slot, 1, key, "ACTIVATE", quoted(release), ""),
		blockRow(placed, product, target, "NULL", "NULL", 1, "investigate publication"),
		targetStep + `block_generation = 1` + of("target", target),
		blockRow(lifted, product, target, quoted(asset), "NULL", 2, "investigate the asset"),
		targetStep + `block_generation = 2` + of("target", target),
		clearing(lifted, 3, "clearer", ""),
		targetStep + `block_generation = 3` + of("target", target),
	} {
		if _, err := app.Exec(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	const (
		raised                = "P0001"
		uniqueViolation       = "23505"
		foreignKeyViolation   = "23503"
		checkViolation        = "23514"
		insufficientPrivilege = "42501"
	)
	statements := []struct {
		name      string
		statement string
		state     string
	}{
		// A slot.
		{"a slot skips a generation", slotStep + `generation = generation + 2` + of("slot", slot), raised},
		{"a slot goes back a generation", slotStep + `generation = generation - 1, active_release_id = NULL` + of("slot", slot), raised},
		{"a slot serves another release at its generation", slotStep + `active_release_id = gen_random_uuid()` + of("slot", slot), raised},
		{"a slot moves to another asset", slotStep + `generation = generation + 1, api_asset_id = gen_random_uuid()` + of("slot", slot), raised},
		{"a slot moves to another product", slotStep + `generation = generation + 1, product_id = gen_random_uuid()` + of("slot", slot), raised},
		{"a slot serves before it was ever changed", slotRow(product, target, ids.NewUUID(), random), checkViolation},
		{"a second slot of an asset on a target", slotRow(product, target, asset, "NULL"), uniqueViolation},
		{"a slot on a target of another product", slotRow(ids.NewUUID(), target, ids.NewUUID(), "NULL"), foreignKeyViolation},
		{"remove a slot", `DELETE FROM publication.slot`, insufficientPrivilege},

		// A target.
		{"the blocks of a target skip a generation", targetStep + `block_generation = block_generation + 2` + of("target", target), raised},
		{"the blocks of a target go back", targetStep + `block_generation = block_generation - 1` + of("target", target), raised},
		{"a target takes another portal", targetStep + `portal_key = 'other'` + of("target", target), raised},
		{"a target takes another audience with a block", targetStep + `block_generation = block_generation + 1, audience = 'public'` + of("target", target), raised},
		{"a target of an audience nobody defined", targetRow(ids.NewUUID(), product, config, "everyone", "everyone"), checkViolation},
		{"a portal key in upper case", targetRow(ids.NewUUID(), product, config, "Engineering", "internal"), checkViolation},
		{"a portal of a configuration twice", targetRow(ids.NewUUID(), product, config, "engineering", "internal"), uniqueViolation},
		{"remove a target", `DELETE FROM publication.target`, insufficientPrivilege},

		// An operation.
		{"a withdrawal that names a release", operation(slot, 2, strings.Repeat("b", 64), "WITHDRAW", random, "wrong audience"), checkViolation},
		{"an activation that names none", operation(slot, 2, strings.Repeat("b", 64), "ACTIVATE", "NULL", ""), checkViolation},
		{"a return to a release without a reason", operation(slot, 2, strings.Repeat("b", 64), "RESTORE", random, ""), checkViolation},
		{"a second operation at a generation", operation(slot, 1, strings.Repeat("b", 64), "ACTIVATE", random, ""), uniqueViolation},
		{"a key used twice on a slot", operation(slot, 2, key, "ACTIVATE", random, ""), uniqueViolation},
		{"a key that is no digest", operation(slot, 2, "first", "ACTIVATE", random, ""), checkViolation},
		{"an action nobody defined", operation(slot, 2, strings.Repeat("b", 64), "PAUSE", random, "why"), checkViolation},
		{"an operation at generation zero", operation(slot, 0, strings.Repeat("b", 64), "ACTIVATE", random, ""), checkViolation},
		{"an operation of no slot", operation(ids.NewUUID(), 1, strings.Repeat("b", 64), "ACTIVATE", random, ""), foreignKeyViolation},
		{"change an operation", `UPDATE publication.operation SET reason = 'rewritten'`, insufficientPrivilege},
		{"remove an operation", `DELETE FROM publication.operation`, insufficientPrivilege},

		// A block.
		{"a block of a release without its asset", blockRow(ids.NewUUID(), product, target, "NULL", random, 4, "why"), checkViolation},
		{"a second block of a target in force", blockRow(ids.NewUUID(), product, target, "NULL", "NULL", 4, "why"), uniqueViolation},
		{"a block without a reason", blockRow(ids.NewUUID(), product, target, random, "NULL", 4, ""), checkViolation},
		{"a block at a generation that is taken", blockRow(ids.NewUUID(), product, target, random, "NULL", 1, "why"), uniqueViolation},
		{"a block on a target of another product", blockRow(ids.NewUUID(), ids.NewUUID(), target, random, "NULL", 4, "why"), foreignKeyViolation},
		{"a block that is half cleared", `UPDATE publication.access_block SET cleared_at = now()` + of("block", placed), checkViolation},
		{"whoever placed a block clears it", clearing(placed, 4, "security", ""), raised},
		{"a block takes another reason as it is cleared", clearing(placed, 4, "clearer", ", reason = 'never mind'"), raised},
		{"a block takes another scope as it is cleared", clearing(placed, 4, "clearer", ", api_asset_id = gen_random_uuid()"), raised},
		{"a block is written again without being cleared", `UPDATE publication.access_block SET reason = reason` + of("block", placed), raised},
		{"a block is cleared at a generation not after its own", clearing(placed, 1, "clearer", ""), checkViolation},
		{"a block is cleared before it was placed", clearing(placed, 4, "clearer", "earlier"), checkViolation},
		{"a block that was cleared is cleared again", clearing(lifted, 4, "another", ""), raised},
		{"a block that was cleared is put back in force", `UPDATE publication.access_block SET clear_generation = NULL, clear_reason = NULL,
    cleared_by_issuer = NULL, cleared_by_subject = NULL, cleared_at = NULL` + of("block", lifted), raised},
		{"remove a block", `DELETE FROM publication.access_block`, insufficientPrivilege},
	}
	state := func(err error) string {
		var refused *pgconn.PgError
		if errors.As(err, &refused) {
			return refused.Code
		}
		return ""
	}
	// The role a process connects with, and the owner of the tables: a
	// trigger and a constraint are no privilege, so they hold both.
	for _, s := range statements {
		if _, err := app.Exec(t.Context(), s.statement); state(err) != s.state {
			t.Errorf("%s, as the application role: got %v, want SQLSTATE %q", s.name, err, s.state)
		}
		if s.state == insufficientPrivilege {
			continue
		}
		if _, err := owner.Exec(t.Context(), s.statement); state(err) != s.state {
			t.Errorf("%s, as the owner of the tables: got %v, want SQLSTATE %q", s.name, err, s.state)
		}
	}

	// Nothing of what was refused is there.
	var (
		generation, blocks                  int64
		serving, portal                     string
		slots, targets, operations, inForce int
	)
	if err := owner.QueryRow(t.Context(), `SELECT s.generation, s.active_release_id::text, t.block_generation, t.portal_key,
       (SELECT count(*) FROM publication.slot), (SELECT count(*) FROM publication.target),
       (SELECT count(*) FROM publication.operation), (SELECT count(*) FROM publication.access_block WHERE cleared_at IS NULL)
FROM publication.slot s JOIN publication.target t USING (target_id) WHERE s.slot_id = $1`, slot).Scan(
		&generation, &serving, &blocks, &portal, &slots, &targets, &operations, &inForce); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || serving != release || blocks != 3 || portal != "engineering" || slots != 1 || targets != 1 || operations != 1 || inForce != 1 {
		t.Errorf("the slot is at generation %d serving %s, the target %q at block generation %d; %d slots, %d targets, %d operations, %d blocks in force",
			generation, serving, portal, blocks, slots, targets, operations, inForce)
	}

	// The next steps the model makes are ones the tables take, by hand as
	// well: a withdrawal, and the clearance of the block by another.
	for _, statement := range []string{
		slotStep + `generation = 2, active_release_id = NULL` + of("slot", slot),
		operation(slot, 2, strings.Repeat("b", 64), "WITHDRAW", "NULL", "wrong audience"),
		clearing(placed, 4, "clearer", ""),
		targetStep + `block_generation = 4` + of("target", target),
	} {
		if _, err := app.Exec(t.Context(), statement); err != nil {
			t.Errorf("%s: %v", statement, err)
		}
	}
}
