package migrate_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// insufficientPrivilege is the SQLSTATE of a statement refused for lack of a
// privilege or of ownership.
const insufficientPrivilege = "42501"

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// The application role is the one a Fabric process connects with. On a
// migrated database it can do its work and nothing an operator would have to
// undo (F02 §3.2, F05 §8).
func TestTheApplicationRoleHoldsWhatItNeedsAndNothingElse(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	app := pgtest.Connect(t, database.Application)

	var superuser, bypassesRLS, createsDatabases, createsRoles bool
	if err := app.QueryRow(t.Context(),
		`SELECT rolsuper, rolbypassrls, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user`,
	).Scan(&superuser, &bypassesRLS, &createsDatabases, &createsRoles); err != nil {
		t.Fatalf("read the role's attributes: %v", err)
	}
	if superuser || bypassesRLS || createsDatabases || createsRoles {
		t.Errorf("the application role of the test server is privileged: superuser=%t bypassrls=%t createdb=%t createrole=%t",
			superuser, bypassesRLS, createsDatabases, createsRoles)
	}

	var owned int
	if err := app.QueryRow(t.Context(),
		`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname IN ('bento', 'fabric_migration') AND pg_has_role(current_user, c.relowner, 'USAGE')`,
	).Scan(&owned); err != nil {
		t.Fatalf("count what the role owns: %v", err)
	}
	if owned != 0 {
		t.Errorf("the application role owns %d objects of the Fabric schemas, want none", owned)
	}

	var recorded int
	if err := app.QueryRow(t.Context(),
		`SELECT count(*) FROM fabric_migration.applied_migration`).Scan(&recorded); err != nil || recorded == 0 {
		t.Errorf("the application role must be able to read the history: %d rows, %v", recorded, err)
	}

	refused := []string{
		`CREATE SCHEMA intruder`,
		`CREATE TABLE bento.intruder (id integer)`,
		`CREATE TABLE fabric_migration.intruder (id integer)`,
		`ALTER TABLE bento.outbox ADD COLUMN intruder integer`,
		`ALTER TABLE bento.inbox DISABLE TRIGGER inbox_settled_before_commit`,
		`DROP TABLE bento.inbox`,
		`DROP SCHEMA bento CASCADE`,
		`TRUNCATE bento.outbox`,
		`DELETE FROM bento.outbox`,
		`DELETE FROM bento.inbox`,
		`INSERT INTO fabric_migration.applied_migration (migration_id, origin, canonical_checksum, target_schema)
		 VALUES ('intruder/0001', 'FABRIC', 'sha256:intruder', 'bento')`,
		`UPDATE fabric_migration.applied_migration SET canonical_checksum = 'sha256:rewritten'`,
		`DELETE FROM fabric_migration.applied_migration`,
	}
	for _, statement := range refused {
		if _, err := app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
}

func TestGrantIsRepeatable(t *testing.T) {
	database := pgtest.MigratedDatabase(t) // which has granted once already
	conn := pgtest.Connect(t, database.Migrator)

	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant a second time: %v", err)
	}
}

func TestGrantTakesARoleNameAndNothingElse(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.MigratedDatabase(t).Migrator)

	for name, role := range map[string]string{
		"no role":              "",
		"a role nobody made":   "fabric_no_such_role",
		"SQL in the role name": `fabric_app"; DROP SCHEMA bento CASCADE; --`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := migrate.Grant(t.Context(), conn, role); err == nil {
				t.Fatalf("granting to %q must fail", role)
			}
			if !exists(t, conn, "bento.outbox") {
				t.Fatal("the role name was executed as SQL: the framework schema is gone")
			}
		})
	}
}
