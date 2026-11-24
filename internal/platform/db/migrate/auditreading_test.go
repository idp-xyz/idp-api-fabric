package migrate_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// What a process may do on the audit trail is decided in ApplicationPrivileges
// and nowhere else: it records into it and reads it back, for the auditors of
// a product, and holds nothing that would let it change or remove a record,
// whatever is granted and however often. A privilege added there by mistake
// fails here, before a process runs with it.
func TestTheAuditTrailIsGrantedSelectAndInsertOnly(t *testing.T) {
	var declared []string
	for _, table := range migrate.ApplicationPrivileges() {
		if table.Schema == "platformoperations" {
			if table.Table != "audit_record" {
				t.Errorf("platformoperations.%s is declared: this test knows one table of the context", table.Table)
			}
			declared = slices.Sorted(slices.Values(table.Privileges))
		}
	}
	if want := []string{"INSERT", "SELECT"}; !slices.Equal(declared, want) {
		t.Errorf("the audit trail is declared for %v, want %v", declared, want)
	}

	// Nor does the role hold more on the database that was granted: nothing
	// of the tables, nothing of a column of them.
	database := pgtest.MigratedDatabase(t)
	if err := migrate.Grant(t.Context(), pgtest.Connect(t, database.Migrator), database.ApplicationRole); err != nil {
		t.Fatalf("grant a second time: %v", err)
	}
	app := pgtest.Connect(t, database.Application)
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		var held bool
		if err := app.QueryRow(t.Context(),
			`SELECT has_table_privilege(current_user, 'platformoperations.audit_record', $1)`, privilege).Scan(&held); err != nil {
			t.Fatalf("read the privileges of the role: %v", err)
		}
		if want := privilege == "SELECT" || privilege == "INSERT"; held != want {
			t.Errorf("the role holds %s on the audit trail: %t, want %t", privilege, held, want)
		}
	}
	// A column has no other privileges than these.
	for _, privilege := range []string{"UPDATE", "REFERENCES"} {
		var onAColumn bool
		if err := app.QueryRow(t.Context(),
			`SELECT has_any_column_privilege(current_user, 'platformoperations.audit_record', $1)`, privilege).Scan(&onAColumn); err != nil {
			t.Fatalf("read the privileges of the role: %v", err)
		}
		if onAColumn {
			t.Errorf("the role holds %s on a column of the audit trail", privilege)
		}
	}
}

// auditorRole is the step that gave a product its fourth role.
const auditorRole = "productgovernance/009_auditor_role"

// A database the build before the auditor role has migrated and written to is
// brought up to date with what it holds. Every assignment stays what it was
// granted as. Before the step the table takes no auditor and a process of this
// build does not start; after it the table takes one, with the statement that
// build already ran, and still no role nobody defined. The step adds no table
// and no privilege, so a process of this build starts as soon as the database
// is migrated, and that build keeps granting the roles it knows.
func TestRunLetsAProductHaveAuditorsAndLeavesItsRolesWhatTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool { return step.ID == auditorRole })
	if len(before) != len(plan)-1 {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", auditorRole)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	// A product and the roles in it as a process of that build writes them,
	// with the statements it runs.
	const (
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"

		registerProduct = `
			INSERT INTO productgovernance.product
			    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
			     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
			VALUES ($1, 'cargopai', 'CargoPai', 'ACTIVE', 'https://issuer.example', 'org', 1, $2, 'https://issuer.example', 'platform-admin', $2)`
		grantRole = `
			INSERT INTO productgovernance.product_role_assignment
			    (role_assignment_id, product_id, principal_issuer, principal_subject, role,
			     role_assignment_version, granted_at, granted_by_issuer, granted_by_subject)
			VALUES (gen_random_uuid(), $1, 'https://issuer.example', $2, $3, 1, $4, 'https://issuer.example', 'alice')`
		checkViolation = "23514"
	)
	granted := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), registerProduct, cargopai, granted); err != nil {
		t.Fatalf("register a product as the build before: %v", err)
	}
	grant := func(subject, role string) error {
		_, err := app.Exec(t.Context(), grantRole, cargopai, subject, role, granted)
		return err
	}
	for subject, role := range map[string]string{"alice": "product_owner", "bob": "security_admin", "carol": "security_reviewer"} {
		if err := grant(subject, role); err != nil {
			t.Fatalf("grant %s as the build before: %v", role, err)
		}
	}
	if err := grant("dave", "internal_auditor"); sqlState(err) != checkViolation {
		t.Fatalf("an auditor before the step: got %v, want the table to refuse the role", err)
	}

	// A process of this build does not start on the database as that build
	// left it, and says which step is missing.
	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "migration "+auditorRole+" is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v naming %s", err, db.ErrNotMigrated, auditorRole)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	if got, want := history(t, conn), planRows(t, before...); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant the steps of the build before and the one it lacked\n %+v", got, want)
	}
	// Migrated, it starts: the role was granted nothing since.
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated: %v", err)
	}
	opened.Close()

	if err := grant("dave", "internal_auditor"); err != nil {
		t.Errorf("an auditor after the step: %v", err)
	}
	// That build is still running while this one is rolled out, and grants
	// the roles it knows the way it does.
	if err := grant("erin", "security_admin"); err != nil {
		t.Errorf("a security administrator as the build before grants one, after the migration: %v", err)
	}
	if err := grant("frank", "internal_observer"); sqlState(err) != checkViolation {
		t.Errorf("a role nobody defined: got %v, want the table to refuse it", err)
	}
	// A role of a workspace is no role of a product.
	if err := grant("grace", "internal_author"); sqlState(err) != checkViolation {
		t.Errorf("a role of a workspace: got %v, want the table to refuse it", err)
	}

	type held struct{ Subject, Role string }
	rows, err := conn.Query(t.Context(), `
		SELECT principal_subject, role FROM productgovernance.product_role_assignment
		WHERE revoked_at IS NULL ORDER BY principal_subject`)
	if err != nil {
		t.Fatalf("read the role assignments: %v", err)
	}
	assignments, err := pgx.CollectRows(rows, pgx.RowToStructByPos[held])
	if err != nil {
		t.Fatalf("read the role assignments: %v", err)
	}
	want := []held{
		{"alice", "product_owner"}, {"bob", "security_admin"}, {"carol", "security_reviewer"},
		{"dave", "internal_auditor"}, {"erin", "security_admin"},
	}
	if !slices.Equal(assignments, want) {
		t.Errorf("the roles are\n %+v\nwant those of before as they were granted, and the auditor\n %+v", assignments, want)
	}
}

// auditReadOrder is the step that indexes the audit trail for the reading of
// a product's records.
const auditReadOrder = "platformoperations/002_audit_read_order"

// A database the build before the reading of the audit trail has migrated and
// written to is brought up to date with what it holds. The records stay as
// they were written, and the index of the list and the one of a request are
// there from then on. The step is no grant: a process of this build starts
// only once its role may read the trail (see the tests of the privileges),
// and the role still cannot change or remove a record.
func TestRunIndexesTheAuditTrailAndLeavesItsRowsAsTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool { return step.ID == auditReadOrder })
	if len(before) != len(plan)-1 {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", auditReadOrder)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	// Records as a process of that build writes them, with the statement it
	// runs: one of a product, a refusal that left no resource, and one of the
	// platform scope.
	const record = `
		INSERT INTO platformoperations.audit_record
		    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
		     product_id, resource_type, resource_id, correlation_id)
		VALUES ($1, $2, 'https://issuer.example', $3, $4, $5, $6, $7, $8, $9, $10)`
	app := pgtest.Connect(t, database.Application)
	recorded := time.Date(2026, 11, 23, 8, 0, 0, 123456000, time.UTC)
	const cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	for _, row := range [][]any{
		{"00000000-0000-4000-8000-000000000001", recorded, "alice", "product.register", "SUCCEEDED", "AC-08", cargopai, "Product", cargopai, "request-1"},
		{"00000000-0000-4000-8000-000000000002", recorded.Add(time.Minute), "mallory", "product_role.grant", "DENIED", "DENY-03", cargopai, "RoleAssignment", nil, "request-2"},
		{"00000000-0000-4000-8000-000000000003", recorded.Add(2 * time.Minute), "mallory", "product.register", "DENIED", "DENY-03", nil, "Product", nil, "request-3"},
	} {
		if _, err := app.Exec(t.Context(), record, row...); err != nil {
			t.Fatalf("record as the build before: %v", err)
		}
	}
	rowsOf := func() []string {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT concat_ws('|', audit_id, recorded_at AT TIME ZONE 'UTC', actor_subject, action, outcome, rule_id,
			                 coalesce(product_id::text, '-'), resource_type, coalesce(resource_id::text, '-'), correlation_id)
			FROM platformoperations.audit_record ORDER BY audit_id`)
		if err != nil {
			t.Fatalf("read the audit trail: %v", err)
		}
		kept, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the audit trail: %v", err)
		}
		return kept
	}
	indexes := func() []string {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT indexname || ': ' || regexp_replace(indexdef, '^.* USING ', '') FROM pg_indexes
			WHERE schemaname = 'platformoperations' AND tablename = 'audit_record' ORDER BY indexname`)
		if err != nil {
			t.Fatalf("read the indexes: %v", err)
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the indexes: %v", err)
		}
		return found
	}
	written := rowsOf()
	if len(written) != 3 {
		t.Fatalf("the build before wrote %d records, want 3", len(written))
	}
	if got, want := indexes(), []string{"audit_record_pkey: btree (audit_id)"}; !slices.Equal(got, want) {
		t.Fatalf("before the step the audit trail has the indexes %v, want only %v", got, want)
	}

	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "migration "+auditReadOrder+" is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v naming %s", err, db.ErrNotMigrated, auditReadOrder)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	if got, want := history(t, conn), planRows(t, before...); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant the steps of the build before and the one it lacked\n %+v", got, want)
	}
	if got := rowsOf(); !slices.Equal(got, written) {
		t.Errorf("the audit trail holds\n %v\nwant the records as they were written\n %v", got, written)
	}
	if got, want := indexes(), []string{
		"audit_record_by_correlation: btree (product_id, correlation_id)",
		"audit_record_list_order: btree (product_id, recorded_at, audit_id)",
		"audit_record_pkey: btree (audit_id)",
	}; !slices.Equal(got, want) {
		t.Errorf("the audit trail has the indexes\n %v\nwant\n %v", got, want)
	}

	// Migrated, a process starts: the step grants nothing and needs nothing.
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated: %v", err)
	}
	opened.Close()
	for _, statement := range []string{
		`UPDATE platformoperations.audit_record SET outcome = 'SUCCEEDED'`,
		`DELETE FROM platformoperations.audit_record`,
	} {
		_, err := app.Exec(t.Context(), statement)
		if sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
}
