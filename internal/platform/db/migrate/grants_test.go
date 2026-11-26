package migrate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
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
		 WHERE n.nspname = ANY($1) AND pg_has_role(current_user, c.relowner, 'USAGE')`, fabricSchemas(t),
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
		`CREATE TABLE platformoperations.intruder (id integer)`,
		`DROP SCHEMA platformoperations CASCADE`,
		// CR-0001 retired reviews: the role records none and moves no status.
		`INSERT INTO policyreview.review_conclusion (review_id) VALUES (gen_random_uuid())`,
		`UPDATE contractregistry.contract_revision SET status = 'WITHDRAWN'`,
	}
	for _, statement := range refused {
		if _, err := app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
}

// fabricSchemas are the schemas a migrated database holds for Fabric: the one
// of the history and the one of every step of the plan.
func fabricSchemas(t *testing.T) []string {
	t.Helper()

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	schemas := []string{migrate.SchemaHistory}
	for _, step := range plan {
		if !slices.Contains(schemas, step.Schema) {
			schemas = append(schemas, step.Schema)
		}
	}
	return schemas
}

// What a process may do on a table is decided when the table is added, in
// ApplicationPrivileges. A migration that adds a table without that decision
// fails here, instead of failing a process on its first statement.
func TestEveryFabricTableHasItsPrivilegesDeclared(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.MigratedDatabase(t).Migrator)

	rows, err := conn.Query(t.Context(),
		`SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = ANY($1) AND c.relkind IN ('r', 'p') ORDER BY 1`, fabricSchemas(t))
	if err != nil {
		t.Fatalf("list the tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("list the tables: %v", err)
	}

	var declared []string
	for _, table := range migrate.ApplicationPrivileges() {
		declared = append(declared, table.Schema+"."+table.Table)
		if len(table.Privileges) == 0 {
			t.Errorf("%s.%s is declared with no privilege at all", table.Schema, table.Table)
		}
	}
	slices.Sort(declared)

	if !slices.Equal(tables, declared) {
		t.Errorf("the migrated database holds the tables\n %v\nand privileges are declared for\n %v", tables, declared)
	}
}

// A privilege leaves ApplicationPrivileges for RetiredPrivileges: Grant
// gives none it takes back, and takes back none on a table it does not know.
func TestARetiredPrivilegeIsOneThisBuildNoLongerGrants(t *testing.T) {
	declared := make(map[string][]string)
	for _, table := range migrate.ApplicationPrivileges() {
		declared[table.Schema+"."+table.Table] = table.Privileges
	}
	for _, table := range migrate.RetiredPrivileges() {
		name := table.Schema + "." + table.Table
		granted, known := declared[name]
		if !known {
			t.Errorf("%s is retired from and declared for nothing", name)
		}
		for _, privilege := range table.Privileges {
			if slices.Contains(granted, privilege) {
				t.Errorf("%s on %s is both granted and retired", privilege, name)
			}
		}
	}
}

// A role an earlier build granted holds what this build retired (CR-0001):
// INSERT on the tables of reviews and UPDATE on revisions. Granting with this
// build takes exactly those back and leaves the rest, a privilege granted by
// hand included. A process of this build starts on the role before and
// after, and granting again changes nothing.
func TestGrantTakesBackTheRetiredPrivilegesAndNothingElse(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)
	role := pgx.Identifier{database.ApplicationRole}.Sanitize()

	// The role as the builds before this one left it: what this build
	// grants, with the writes of reviews and of statuses on top. And one
	// privilege an operator added by hand, which nothing retired.
	for _, statement := range []string{
		`GRANT SELECT, INSERT ON policyreview.review_request, policyreview.review_scope,
		     policyreview.review_conclusion, policyreview.review_waiver TO ` + role,
		`GRANT SELECT, INSERT, UPDATE ON contractregistry.contract_revision TO ` + role,
		`GRANT DELETE ON bento.outbox TO ` + role,
	} {
		if _, err := conn.Exec(t.Context(), statement); err != nil {
			t.Fatalf("grant as the builds before: %v", err)
		}
	}
	held := func() []string {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT n.nspname || '.' || c.relname || ' ' || a.privilege_type
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(c.relacl) a
			WHERE a.grantee = (SELECT oid FROM pg_roles WHERE rolname = $1) AND n.nspname = ANY($2)
			ORDER BY 1`, database.ApplicationRole, fabricSchemas(t))
		if err != nil {
			t.Fatalf("read the privileges of the role: %v", err)
		}
		privileges, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the privileges of the role: %v", err)
		}
		return privileges
	}
	retired := []string{
		"contractregistry.contract_revision UPDATE",
		"policyreview.review_conclusion INSERT",
		"policyreview.review_request INSERT",
		"policyreview.review_scope INSERT",
		"policyreview.review_waiver INSERT",
	}
	before := held()
	for _, privilege := range retired {
		if !slices.Contains(before, privilege) {
			t.Fatalf("the role as the builds before left it does not hold %s: %v", privilege, before)
		}
	}
	open := func(when string) {
		t.Helper()
		opened, err := db.Open(t.Context(), database.Application)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		opened.Close()
	}
	open("on the role as the builds before left it")

	want := slices.DeleteFunc(slices.Clone(before), func(privilege string) bool { return slices.Contains(retired, privilege) })
	for range 2 {
		if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
			t.Fatalf("grant with this build: %v", err)
		}
		if got := held(); !slices.Equal(got, want) {
			t.Errorf("granted with this build, the role holds\n %v\nwant what it held without the retired privileges\n %v", got, want)
		}
	}
	for _, table := range migrate.ApplicationPrivileges() {
		for _, privilege := range table.Privileges {
			if name := table.Schema + "." + table.Table + " " + privilege; !slices.Contains(want, name) {
				t.Errorf("the role does not hold %s, which this build grants", name)
			}
		}
	}
	if !slices.Contains(want, "bento.outbox DELETE") {
		t.Error("the privilege granted by hand is gone")
	}
	open("once this build granted the role")
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
