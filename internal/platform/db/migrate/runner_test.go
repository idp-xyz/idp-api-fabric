package migrate_test

import (
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

var quiet = slog.New(slog.DiscardHandler)

// historyRow is one row of the migration history, in the order it was applied.
type historyRow struct {
	ID               string
	Origin           string
	Checksum         string
	FrameworkVersion string
	Schema           string
}

func history(t *testing.T, conn *pgx.Conn) []historyRow {
	t.Helper()

	rows, err := conn.Query(t.Context(),
		`SELECT migration_id, origin, canonical_checksum, coalesce(framework_version, ''), target_schema
		 FROM fabric_migration.applied_migration ORDER BY sequence`)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	recorded, err := pgx.CollectRows(rows, pgx.RowToStructByPos[historyRow])
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	return recorded
}

// planRows is the history a complete run of the plan leaves.
func planRows(t *testing.T) []historyRow {
	t.Helper()

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	rows := make([]historyRow, 0, len(plan))
	for _, step := range plan {
		rows = append(rows, historyRow{step.ID, string(step.Origin), step.Checksum, step.FrameworkVersion, step.Schema})
	}
	return rows
}

func exists(t *testing.T, conn *pgx.Conn, relation string) bool {
	t.Helper()

	var present bool
	if err := conn.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&present); err != nil {
		t.Fatalf("look up %s: %v", relation, err)
	}
	return present
}

func TestRunMigratesAnEmptyDatabase(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, want := history(t, conn), planRows(t); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, in order\n %+v", got, want)
	}

	// The framework's own check is the judge of its tables.
	pool, err := pgxpool.New(t.Context(), database.Migrator)
	if err != nil {
		t.Fatalf("open a pool: %v", err)
	}
	t.Cleanup(pool.Close)
	framework, err := bentopostgres.NewDB(pool, bentopostgres.WithSchema(migrate.SchemaBento))
	if err != nil {
		t.Fatalf("bind the framework database: %v", err)
	}
	if err := framework.CheckSchema(t.Context()); err != nil {
		t.Errorf("the framework does not accept the migrated schema: %v", err)
	}
}

func TestRunAppliesNothingTwice(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.FreshDatabase(t).Migrator)

	for run := range 2 {
		if err := migrate.Run(t.Context(), conn, quiet); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}
	if got, want := history(t, conn), planRows(t); !slices.Equal(got, want) {
		t.Errorf("after a second run the history records\n %+v\nwant\n %+v", got, want)
	}
}

// A database an older build migrated gets the steps that build did not carry,
// and a process of this build runs on it once its role is granted what the
// new tables need. Until then it refuses to start and says what is missing.
func TestRunBringsADatabaseOfAnOlderBuildUpToDate(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	// The plan of the build before Fabric had tables of its own.
	older := plan[:len(bentopostgres.Migrations())]
	if len(older) == len(plan) {
		t.Fatal("the plan carries no Fabric migration; this test would pass vacuously")
	}
	if err := migrate.Apply(t.Context(), conn, quiet, older); err != nil {
		t.Fatalf("migrate as the older build: %v", err)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	if got, want := history(t, conn), planRows(t); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, in order\n %+v", got, want)
	}

	if opened, err := db.Open(t.Context(), database.Application); !errors.Is(err, db.ErrNotGranted) {
		if err == nil {
			opened.Close()
		}
		t.Fatalf("before the grants: got %v, want %v", err, db.ErrNotGranted)
	}
	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant: %v", err)
	}
	opened, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("after the grants: %v", err)
	}
	opened.Close()
}

// roleAssignmentRevocation is the step that made a role assignment a record
// that ends. The build before it wrote an assignment once, without an
// identifier, and never changed it.
const roleAssignmentRevocation = "productgovernance/003_role_assignment_revocation"

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// A database the build before role assignments could end has migrated and
// written to is brought up to date with what it holds. Every assignment gets
// an identifier of its own and stays in force, as it was granted, and what
// that build still writes while this one is rolled out is an assignment like
// the others. A process of this build refuses to start, and says what is
// missing, until its role is granted what ending an assignment needs.
func TestRunGivesTheRoleAssignmentsOfTheBuildBeforeTheirIdentifiers(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)
	role := pgx.Identifier{database.ApplicationRole}.Sanitize()

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	// The plan of the build before: without that step, and without what
	// Product Governance added after it, which builds on it.
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return step.Schema == "productgovernance" && step.ID >= roleAssignmentRevocation
	})
	if !slices.ContainsFunc(plan, func(step migrate.Step) bool { return step.ID == roleAssignmentRevocation }) {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", roleAssignmentRevocation)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	// What that build granted: it had no statement that changed an
	// assignment.
	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := conn.Exec(t.Context(), `REVOKE UPDATE ON productgovernance.product_role_assignment FROM `+role); err != nil {
		t.Fatalf("take back what that build did not grant: %v", err)
	}

	// Two products as a process of that build registered them, with the
	// statements it ran: the first has two owners.
	const (
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		parcel   = "2d0b5f8c-63e1-4a9f-87b2-a1d4e3f5a6b7"

		registerProduct = `
			INSERT INTO productgovernance.product
			    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
			     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
			VALUES ($1, $2, $2, 'ACTIVE', 'https://issuer.example', 'org', 1, $3, 'https://issuer.example', 'platform-admin', $3)`
		assignOwner = `
			INSERT INTO productgovernance.product_role_assignment
			    (product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
			VALUES ($1, 'https://issuer.example', $2, 'product_owner', $3, 'https://issuer.example', 'platform-admin')`
	)
	registered := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	for _, product := range []struct{ id, key string }{{cargopai, "cargopai"}, {parcel, "parcel"}} {
		if _, err := app.Exec(t.Context(), registerProduct, product.id, product.key, registered); err != nil {
			t.Fatalf("register %s as the build before: %v", product.key, err)
		}
	}
	for _, owner := range []struct{ product, subject string }{{cargopai, "alice"}, {cargopai, "bob"}, {parcel, "alice"}} {
		if _, err := app.Exec(t.Context(), assignOwner, owner.product, owner.subject, registered); err != nil {
			t.Fatalf("make %s an owner as the build before: %v", owner.subject, err)
		}
	}

	// A process of this build does not start, and says what its role lacks,
	// whether or not the database was migrated already. One run of
	// fabric-migrate -apply -grant-to gives it both.
	const missing = "UPDATE on productgovernance.product_role_assignment"
	refused := func(when string) {
		t.Helper()

		opened, err := db.Open(t.Context(), database.Application)
		if err == nil {
			opened.Close()
		}
		if !errors.Is(err, db.ErrNotGranted) || !strings.Contains(err.Error(), missing) {
			t.Fatalf("%s: got %v, want %v naming %q", when, err, db.ErrNotGranted, missing)
		}
	}
	refused("on the database as the build before left it")

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	if got, want := history(t, conn), planRows(t); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, in order\n %+v", got, want)
	}

	// That build is still running while this one is rolled out, and names
	// another owner the way it does.
	if _, err := app.Exec(t.Context(), assignOwner, parcel, "carol", registered.Add(time.Hour)); err != nil {
		t.Fatalf("make carol an owner as the build before, after the migration: %v", err)
	}

	type assignment struct {
		ID        string
		Product   string
		Subject   string
		Version   int64
		GrantedAt time.Time
		GrantedBy string
		InForce   bool
	}
	rows, err := conn.Query(t.Context(), `
		SELECT role_assignment_id::text, product_id::text, principal_subject, role_assignment_version, granted_at, granted_by_subject,
		       revoked_at IS NULL AND revoked_by_issuer IS NULL AND revoked_by_subject IS NULL
		FROM productgovernance.product_role_assignment WHERE role = 'product_owner'
		ORDER BY product_id, principal_subject`)
	if err != nil {
		t.Fatalf("read the role assignments: %v", err)
	}
	assignments, err := pgx.CollectRows(rows, pgx.RowToStructByPos[assignment])
	if err != nil {
		t.Fatalf("read the role assignments: %v", err)
	}
	want := []assignment{
		{Product: cargopai, Subject: "alice", Version: 1, GrantedAt: registered, GrantedBy: "platform-admin", InForce: true},
		{Product: cargopai, Subject: "bob", Version: 1, GrantedAt: registered, GrantedBy: "platform-admin", InForce: true},
		{Product: parcel, Subject: "alice", Version: 1, GrantedAt: registered, GrantedBy: "platform-admin", InForce: true},
		{Product: parcel, Subject: "carol", Version: 1, GrantedAt: registered.Add(time.Hour), GrantedBy: "platform-admin", InForce: true},
	}
	identifiers := make(map[string]bool)
	for index := range assignments {
		got := &assignments[index]
		if !canonicalUUID.MatchString(got.ID) || identifiers[got.ID] {
			t.Errorf("the assignment of %s in %s has the identifier %q, want a UUID of its own", got.Subject, got.Product, got.ID)
		}
		identifiers[got.ID] = true
		// What is compared below is what the row held before.
		got.ID, got.GrantedAt = "", got.GrantedAt.UTC()
	}
	if !slices.Equal(assignments, want) {
		t.Errorf("the role assignments are\n %+v\nwant them as they were granted, in force at their first version\n %+v", assignments, want)
	}

	refused("once it is migrated, before the grants")
	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant: %v", err)
	}
	opened, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("after the grants: %v", err)
	}
	opened.Close()
}

func TestRunRefusesAnAppliedArtifactThatChanged(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.MigratedDatabase(t).Migrator)

	if _, err := conn.Exec(t.Context(),
		`UPDATE fabric_migration.applied_migration SET canonical_checksum = 'sha256:rewritten'`); err != nil {
		t.Fatalf("rewrite the recorded checksum: %v", err)
	}
	if err := migrate.Run(t.Context(), conn, quiet); !errors.Is(err, migrate.ErrChecksumDrift) {
		t.Fatalf("got %v, want %v", err, migrate.ErrChecksumDrift)
	}
}

func TestRunRefusesAMigrationThisBuildDoesNotCarry(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.MigratedDatabase(t).Migrator)

	if _, err := conn.Exec(t.Context(),
		`INSERT INTO fabric_migration.applied_migration (migration_id, origin, canonical_checksum, target_schema)
		 VALUES ('publication/0001_from_a_newer_build', 'FABRIC', 'sha256:newer', 'publication')`); err != nil {
		t.Fatalf("record a step of a newer build: %v", err)
	}
	if err := migrate.Run(t.Context(), conn, quiet); !errors.Is(err, migrate.ErrUnknownMigration) {
		t.Fatalf("got %v, want %v", err, migrate.ErrUnknownMigration)
	}
}

func TestAFailedStepLeavesNothingOfItselfBehind(t *testing.T) {
	conn := pgtest.Connect(t, pgtest.FreshDatabase(t).Migrator)

	first := migrate.Step{
		ID: "probe/0001_first", Origin: migrate.OriginFabric, Schema: migrate.SchemaHistory, Checksum: "sha256:first",
		SQL: `CREATE TABLE fabric_migration.probe_first (id integer)`,
	}
	second := migrate.Step{
		ID: "probe/0002_second", Origin: migrate.OriginFabric, Schema: migrate.SchemaHistory, Checksum: "sha256:second",
		// The table is created before the statement that fails, so a step that
		// was not atomic would leave it behind.
		SQL: `CREATE TABLE fabric_migration.probe_second (id integer); SELECT 1/0`,
	}

	if err := migrate.Apply(t.Context(), conn, quiet, []migrate.Step{first, second}); err == nil {
		t.Fatal("a plan whose second step fails must fail")
	}
	if got := history(t, conn); len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("the history records %+v, want only %s", got, first.ID)
	}
	if !exists(t, conn, "fabric_migration.probe_first") {
		t.Error("the step that committed before the failure is gone")
	}
	if exists(t, conn, "fabric_migration.probe_second") {
		t.Error("the failed step left a table behind")
	}

	// The repaired plan applies only what is missing, on the same connection.
	second.SQL = `CREATE TABLE fabric_migration.probe_second (id integer)`
	if err := migrate.Apply(t.Context(), conn, quiet, []migrate.Step{first, second}); err != nil {
		t.Fatalf("apply the repaired plan: %v", err)
	}
	got := history(t, conn)
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Errorf("the history records %+v, want %s then %s", got, first.ID, second.ID)
	}
}

func TestConcurrentRunsApplyEachStepOnce(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	connections := []*pgx.Conn{pgtest.Connect(t, database.Migrator), pgtest.Connect(t, database.Migrator)}

	failures := make([]error, len(connections))
	var runs sync.WaitGroup
	for index, conn := range connections {
		runs.Go(func() { failures[index] = migrate.Run(t.Context(), conn, quiet) })
	}
	runs.Wait()

	for index, err := range failures {
		if err != nil {
			t.Errorf("run %d: %v", index+1, err)
		}
	}
	if got, want := history(t, connections[0]), planRows(t); !slices.Equal(got, want) {
		t.Errorf("after two concurrent runs the history records\n %+v\nwant\n %+v", got, want)
	}
}
