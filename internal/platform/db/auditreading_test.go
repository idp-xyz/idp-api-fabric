package db_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// The build before the reading of the audit trail recorded it and never read
// it back, so the role it granted may insert into the table and may not read
// it. A process of this build does not start on that grant and names the one
// privilege that is missing. Granting again with this build is all it takes:
// nothing is migrated, and the role still cannot change or remove a record.
func TestAProcessStartsOnceTheRoleOfTheBuildBeforeMayReadTheAuditTrail(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	mustAlter(t, database, `REVOKE SELECT ON platformoperations.audit_record FROM `+role(database))
	app := pgtest.Connect(t, database.Application)
	// refusedFor is the SQLSTATE the role is refused the statement with, and
	// empty if the statement runs.
	refusedFor := func(statement string) string {
		t.Helper()
		_, err := app.Exec(t.Context(), statement)
		var refused *pgconn.PgError
		switch {
		case err == nil:
			return ""
		case errors.As(err, &refused):
			return refused.Code
		}
		t.Fatalf("%s: %v", statement, err)
		return ""
	}
	const (
		insufficientPrivilege = "42501"
		read                  = `SELECT count(*) FROM platformoperations.audit_record`
		// What that build records with, and this one.
		record = `
			INSERT INTO platformoperations.audit_record
			    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id, resource_type, correlation_id)
			VALUES (gen_random_uuid(), now(), 'https://issuer.example', 'alice', 'product.register', 'DENIED', 'DENY-03', 'Product', 'request')`
	)
	if got := refusedFor(read); got != insufficientPrivilege {
		t.Fatalf("the role of the build before reads the audit trail: SQLSTATE %q, want it refused", got)
	}
	if got := refusedFor(record); got != "" {
		t.Fatalf("the role of the build before records into the audit trail: SQLSTATE %q, want it allowed", got)
	}

	err := refused(t, database.Application)
	if !errors.Is(err, db.ErrNotGranted) || !strings.HasSuffix(err.Error(), ": missing SELECT on platformoperations.audit_record") {
		t.Fatalf("got %v, want %v naming the one privilege that is missing", err, db.ErrNotGranted)
	}

	migrator := pgtest.Connect(t, database.Migrator)
	history := func() (steps int) {
		if err := migrator.QueryRow(t.Context(), `SELECT count(*) FROM fabric_migration.applied_migration`).Scan(&steps); err != nil {
			t.Fatalf("read the history: %v", err)
		}
		return steps
	}
	before := history()
	if err := migrate.Grant(t.Context(), migrator, database.ApplicationRole); err != nil {
		t.Fatalf("grant with this build: %v", err)
	}
	open(t, database.Application)
	if after := history(); after != before {
		t.Errorf("the history holds %d steps, it held %d: granting migrated something", after, before)
	}
	if got := refusedFor(read); got != "" {
		t.Errorf("the role still cannot read the audit trail: SQLSTATE %q", got)
	}
	for _, statement := range []string{
		`UPDATE platformoperations.audit_record SET outcome = 'SUCCEEDED'`,
		`DELETE FROM platformoperations.audit_record`,
		`TRUNCATE platformoperations.audit_record`,
		`SELECT audit_id FROM platformoperations.audit_record FOR UPDATE`,
	} {
		if got := refusedFor(statement); got != insufficientPrivilege {
			t.Errorf("%s: SQLSTATE %q, want it refused for lack of privilege", statement, got)
		}
	}
}
