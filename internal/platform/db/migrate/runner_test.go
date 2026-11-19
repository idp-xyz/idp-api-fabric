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

// grantOnWhatIsThere grants role what ApplicationPrivileges says of the
// tables the database holds, as a build before this one granted them: a
// table that came after it is not there, and that build granted nothing on
// it.
func grantOnWhatIsThere(t *testing.T, conn *pgx.Conn, role string) {
	t.Helper()

	grantee := pgx.Identifier{role}.Sanitize()
	for _, table := range migrate.ApplicationPrivileges() {
		if !exists(t, conn, table.Schema+"."+table.Table) {
			continue
		}
		for _, statement := range []string{
			`GRANT USAGE ON SCHEMA ` + table.Schema + ` TO ` + grantee,
			`GRANT ` + strings.Join(table.Privileges, ", ") + ` ON ` + table.Schema + `.` + table.Table + ` TO ` + grantee,
		} {
			if _, err := conn.Exec(t.Context(), statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
}

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
	// What that build granted: on the tables it had, and no statement that
	// changed an assignment or a product.
	grantOnWhatIsThere(t, conn, database.ApplicationRole)
	for _, table := range []string{"product_role_assignment", "product"} {
		if _, err := conn.Exec(t.Context(), `REVOKE UPDATE ON productgovernance.`+table+` FROM `+role); err != nil {
			t.Fatalf("take back what that build did not grant on %s: %v", table, err)
		}
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

	// A process of this build does not start on the database as that build
	// left it: what Product Governance added after that build is not there.
	// Migrated, it still does not start, and says what its role lacks. One
	// run of fabric-migrate -apply -grant-to gives it both.
	const missing = "UPDATE on productgovernance.product_role_assignment"
	refused := func(when string, want error, naming string) {
		t.Helper()

		opened, err := db.Open(t.Context(), database.Application)
		if err == nil {
			opened.Close()
		}
		if !errors.Is(err, want) || !strings.Contains(err.Error(), naming) {
			t.Fatalf("%s: got %v, want %v naming %q", when, err, want, naming)
		}
	}
	refused("on the database as the build before left it", db.ErrNotMigrated, "does not exist")

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

	refused("once it is migrated, before the grants", db.ErrNotGranted, missing)
	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant: %v", err)
	}
	opened, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("after the grants: %v", err)
	}
	opened.Close()
}

// productBrand is the step that gave a product its brand. The build before
// it registered a product and never changed one.
const productBrand = "productgovernance/006_product_brand"

// A database the build before the brand has migrated and written to is
// brought up to date with what it holds. Every product stays as it was
// registered, and its row says nothing of a brand, which reads as its
// display name. What that build still registers while this one is rolled out
// is a product like the others. A process of this build refuses to start,
// and says what is missing, until its role is granted what changing a brand
// needs: one run of fabric-migrate -apply -grant-to gives it the step and
// the grant.
func TestRunLeavesTheProductsOfTheBuildBeforeTheBrandAsTheyWereRegistered(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)
	role := pgx.Identifier{database.ApplicationRole}.Sanitize()

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	// The plan of the build before: without that step, and without what
	// Product Governance may add after it.
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return step.Schema == "productgovernance" && step.ID >= productBrand
	})
	if len(before) == len(plan) {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", productBrand)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	// What that build granted: on the tables it had, and no statement that
	// changed a product.
	grantOnWhatIsThere(t, conn, database.ApplicationRole)
	if _, err := conn.Exec(t.Context(), `REVOKE UPDATE ON productgovernance.product FROM `+role); err != nil {
		t.Fatalf("take back what that build did not grant: %v", err)
	}

	// A product as a process of that build registers it, with the statement
	// it runs, and a change of its brand with the statement of this build.
	const (
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		parcel   = "2d0b5f8c-63e1-4a9f-87b2-a1d4e3f5a6b7"

		registerProduct = `
			INSERT INTO productgovernance.product
			    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
			     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
			VALUES ($1, $2, $2, 'ACTIVE', 'https://issuer.example', 'org', 1, $3, 'https://issuer.example', 'platform-admin', $3)`
		changeBrand = `
			UPDATE productgovernance.product
			SET brand_name = 'CargoPai Developers', updated_at = $2,
			    updated_by_issuer = 'https://issuer.example', updated_by_subject = 'alice', aggregate_version = 2
			WHERE product_id = $1 AND aggregate_version = 1`
	)
	registered := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), registerProduct, cargopai, "cargopai", registered); err != nil {
		t.Fatalf("register cargopai as the build before: %v", err)
	}

	// A process of this build does not start on the database as that build
	// left it, nor once it is migrated: either way its role lacks what
	// changing a brand needs, and that is what it is told. The privileges
	// are read before the history, so the answer names the one command that
	// brings both.
	refused := func(when string) {
		t.Helper()

		opened, err := db.Open(t.Context(), database.Application)
		if err == nil {
			opened.Close()
		}
		if !errors.Is(err, db.ErrNotGranted) || !strings.HasSuffix(err.Error(), ": missing UPDATE on productgovernance.product") {
			t.Fatalf("%s: got %v, want %v naming UPDATE on productgovernance.product and nothing else", when, err, db.ErrNotGranted)
		}
	}
	refused("on the database as the build before left it")

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	if got, want := history(t, conn), planRows(t); !slices.Equal(got, want) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, in order\n %+v", got, want)
	}

	// That build is still running while this one is rolled out, and
	// registers another product the way it does.
	if _, err := app.Exec(t.Context(), registerProduct, parcel, "parcel", registered.Add(time.Hour)); err != nil {
		t.Fatalf("register parcel as the build before, after the migration: %v", err)
	}

	type product struct {
		Key       string
		Name      string
		Status    string
		Version   int64
		UpdatedAt time.Time
		// SaysNothing is whether the row is silent about a brand and about
		// who changed the product last.
		SaysNothing bool
	}
	rows, err := conn.Query(t.Context(), `
		SELECT product_key, display_name, status, aggregate_version, updated_at,
		       brand_name IS NULL AND brand_logo_url IS NULL AND brand_primary_color IS NULL
		       AND updated_by_issuer IS NULL AND updated_by_subject IS NULL
		FROM productgovernance.product ORDER BY product_key`)
	if err != nil {
		t.Fatalf("read the products: %v", err)
	}
	products, err := pgx.CollectRows(rows, pgx.RowToStructByPos[product])
	if err != nil {
		t.Fatalf("read the products: %v", err)
	}
	for index := range products {
		products[index].UpdatedAt = products[index].UpdatedAt.UTC()
	}
	want := []product{
		{Key: "cargopai", Name: "cargopai", Status: "ACTIVE", Version: 1, UpdatedAt: registered, SaysNothing: true},
		{Key: "parcel", Name: "parcel", Status: "ACTIVE", Version: 1, UpdatedAt: registered.Add(time.Hour), SaysNothing: true},
	}
	if !slices.Equal(products, want) {
		t.Errorf("the products are\n %+v\nwant them as they were registered, their rows silent about a brand\n %+v", products, want)
	}

	// Migrated and not granted, the role cannot change a brand, and a
	// process says so before it serves anything.
	if _, err := app.Exec(t.Context(), changeBrand, cargopai, registered.Add(2*time.Hour)); sqlState(err) != insufficientPrivilege {
		t.Errorf("a change of the brand before the grants: got %v, want it refused for lack of privilege", err)
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

	// Granted, it changes the brand of a product that build registered.
	if changed, err := app.Exec(t.Context(), changeBrand, cargopai, registered.Add(2*time.Hour)); err != nil || changed.RowsAffected() != 1 {
		t.Errorf("a change of the brand after the grants: %v (%v), want one product changed", changed, err)
	}
}

// gitOrigin is the step that let a source snapshot say which commit its
// files came from. The build before it recorded uploads and nothing else.
const gitOrigin = "contractregistry/002_git_origin"

// A database the build before Git origins has migrated and written to is
// brought up to date with what it holds. Every snapshot stays the upload it
// was recorded as, and its row says nothing of an origin. What that build
// still records while this one is rolled out is an upload like the others,
// and a closure it has recorded is still recorded once. The step adds no
// table and no statement a process needs a new privilege for, so a process
// of this build starts as soon as the database is migrated.
func TestRunLeavesTheSnapshotsOfTheBuildBeforeGitOriginsAsTheUploadsTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	// The plan of the build before: without that step, and without what
	// Contract Registry may add after it.
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return step.Schema == "contractregistry" && step.ID >= gitOrigin
	})
	if len(before) == len(plan) {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", gitOrigin)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	// A snapshot as a process of that build records it, with the statement
	// it runs: it names no origin.
	const (
		shipping = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"

		recordSnapshot = `
			INSERT INTO contractregistry.source_snapshot (
			    source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
			    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject
			) VALUES ($1, $2, $3, 'UPLOAD', 'openapi.yaml', $4, $4, '{}', $5, 'https://issuer.example', 'author')`
		uniqueViolation = "23505"
	)
	digest := func(of string) string { return "sha256:" + strings.Repeat(of, 64) }
	recorded := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	record := func(id, closure string, at time.Time) error {
		_, err := app.Exec(t.Context(), recordSnapshot, id, cargopai, shipping, digest(closure), at)
		return err
	}
	if err := record("5a3c8e1f-96b4-4d2c-8ae5-d4a7b6c8e9f0", "a", recorded); err != nil {
		t.Fatalf("record a snapshot as the build before: %v", err)
	}

	// A process of this build does not start on the database as that build
	// left it, and says which step is missing.
	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "migration "+gitOrigin+" is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v naming %s", err, db.ErrNotMigrated, gitOrigin)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	// The step and what Contract Registry added after it are recorded
	// last, behind the steps of the contexts the plan lists after Contract
	// Registry: the history is in the order of what happened, and a later
	// run finds nothing left to apply.
	applied, planned := history(t, conn), planRows(t)
	var late []string
	for _, step := range plan {
		if step.Schema == "contractregistry" && step.ID >= gitOrigin {
			late = append(late, step.ID)
		}
	}
	var last []string
	for _, row := range applied[max(0, len(applied)-len(late)):] {
		last = append(last, row.ID)
	}
	if !slices.Equal(last, late) {
		t.Errorf("the history records\n %+v\nwant %v applied last, in that order", applied, late)
	}
	byID := func(a, b historyRow) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(applied, byID)
	slices.SortFunc(planned, byID)
	if !slices.Equal(applied, planned) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, each once\n %+v", applied, planned)
	}
	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build a second time: %v", err)
	}
	if again := history(t, conn); len(again) != len(planned) {
		t.Errorf("a second run left %d steps on record, want %d", len(again), len(planned))
	}
	// Migrated, it starts: the role was granted nothing since.
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated: %v", err)
	}
	opened.Close()

	// That build is still running while this one is rolled out. It records
	// another upload the way it does, and a closure it has is still
	// recorded once.
	if err := record("6b4d9f2a-a7c5-4e3d-9bf6-e5b8c7d9fa01", "b", recorded.Add(time.Hour)); err != nil {
		t.Fatalf("record a snapshot as the build before, after the migration: %v", err)
	}
	if err := record("7c5ea03b-b8d6-4f4e-8c07-f6c9d8eafb12", "a", recorded.Add(2*time.Hour)); sqlState(err) != uniqueViolation {
		t.Errorf("the first closure again, as the build before records it: got %v, want it refused as a duplicate", err)
	}

	type snapshot struct {
		Kind       string
		Closure    string
		CapturedAt time.Time
		// SaysNothing is whether the row is silent about an origin.
		SaysNothing bool
	}
	rows, err := conn.Query(t.Context(), `
		SELECT source_kind, ref_closure_digest, captured_at,
		       origin_repository IS NULL AND origin_commit IS NULL AND origin_ref IS NULL
		FROM contractregistry.source_snapshot ORDER BY captured_at`)
	if err != nil {
		t.Fatalf("read the snapshots: %v", err)
	}
	snapshots, err := pgx.CollectRows(rows, pgx.RowToStructByPos[snapshot])
	if err != nil {
		t.Fatalf("read the snapshots: %v", err)
	}
	for index := range snapshots {
		snapshots[index].CapturedAt = snapshots[index].CapturedAt.UTC()
	}
	want := []snapshot{
		{Kind: "UPLOAD", Closure: digest("a"), CapturedAt: recorded, SaysNothing: true},
		{Kind: "UPLOAD", Closure: digest("b"), CapturedAt: recorded.Add(time.Hour), SaysNothing: true},
	}
	if !slices.Equal(snapshots, want) {
		t.Errorf("the snapshots are\n %+v\nwant them as they were recorded, their rows silent about an origin\n %+v", snapshots, want)
	}
}

// reviewerRole is the step that gave a workspace its third role.
const reviewerRole = "productgovernance/007_reviewer_role"

// A database the build before the reviewer role has migrated and written to
// is brought up to date with what it holds. Every membership stays what it
// was granted as. Before the step the table takes no reviewer and a process
// of this build does not start; after it the table takes one, with the
// statement that build already ran, and still no role nobody defined. The
// step adds no table and no privilege, so a process of this build starts as
// soon as the database is migrated, and that build keeps granting the roles
// it knows.
func TestRunLetsAWorkspaceHaveReviewersAndLeavesItsMembersWhatTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return step.Schema == "productgovernance" && step.ID >= reviewerRole
	})
	if len(before) == len(plan) {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", reviewerRole)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	// A product, a workspace and its members as a process of that build
	// writes them, with the statements it runs.
	const (
		cargopai    = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		engineering = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"

		registerProduct = `
			INSERT INTO productgovernance.product
			    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
			     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
			VALUES ($1, 'cargopai', 'CargoPai', 'ACTIVE', 'https://issuer.example', 'org', 1, $2, 'https://issuer.example', 'platform-admin', $2)`
		createWorkspace = `
			INSERT INTO productgovernance.workspace
			    (workspace_id, product_id, workspace_key, display_name, status, aggregate_version,
			     created_at, created_by_issuer, created_by_subject, updated_at)
			VALUES ($1, $2, 'engineering', 'Engineering', 'ACTIVE', 1, $3, 'https://issuer.example', 'alice', $3)`
		grantMembership = `
			INSERT INTO productgovernance.workspace_membership
			    (membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
			     membership_version, granted_at, granted_by_issuer, granted_by_subject)
			VALUES (gen_random_uuid(), $1, $2, 'https://issuer.example', $3, $4, 1, $5, 'https://issuer.example', 'alice')`
		checkViolation = "23514"
	)
	granted := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), registerProduct, cargopai, granted); err != nil {
		t.Fatalf("register a product as the build before: %v", err)
	}
	if _, err := app.Exec(t.Context(), createWorkspace, engineering, cargopai, granted); err != nil {
		t.Fatalf("create a workspace as the build before: %v", err)
	}
	grant := func(subject, role string) error {
		_, err := app.Exec(t.Context(), grantMembership, cargopai, engineering, subject, role, granted)
		return err
	}
	if err := grant("bob", "workspace_maintainer"); err != nil {
		t.Fatalf("grant a maintainer as the build before: %v", err)
	}
	if err := grant("carol", "internal_author"); err != nil {
		t.Fatalf("grant an author as the build before: %v", err)
	}
	if err := grant("rita", "internal_reviewer"); sqlState(err) != checkViolation {
		t.Fatalf("a reviewer before the step: got %v, want the table to refuse the role", err)
	}

	// A process of this build does not start on the database as that build
	// left it, and says which step is missing.
	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "migration "+reviewerRole+" is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v naming %s", err, db.ErrNotMigrated, reviewerRole)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	applied, planned := history(t, conn), planRows(t)
	byID := func(a, b historyRow) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(applied, byID)
	slices.SortFunc(planned, byID)
	if !slices.Equal(applied, planned) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, each once\n %+v", applied, planned)
	}
	// Migrated, it starts: the role was granted nothing since.
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated: %v", err)
	}
	opened.Close()

	if err := grant("rita", "internal_reviewer"); err != nil {
		t.Errorf("a reviewer after the step: %v", err)
	}
	// That build is still running while this one is rolled out, and grants
	// the roles it knows the way it does.
	if err := grant("dave", "internal_author"); err != nil {
		t.Errorf("an author as the build before grants one, after the migration: %v", err)
	}
	if err := grant("eve", "internal_observer"); sqlState(err) != checkViolation {
		t.Errorf("a role nobody defined: got %v, want the table to refuse it", err)
	}

	type member struct{ Subject, Role string }
	rows, err := conn.Query(t.Context(), `
		SELECT principal_subject, role FROM productgovernance.workspace_membership
		WHERE revoked_at IS NULL ORDER BY principal_subject`)
	if err != nil {
		t.Fatalf("read the memberships: %v", err)
	}
	members, err := pgx.CollectRows(rows, pgx.RowToStructByPos[member])
	if err != nil {
		t.Fatalf("read the memberships: %v", err)
	}
	want := []member{
		{"bob", "workspace_maintainer"}, {"carol", "internal_author"}, {"dave", "internal_author"}, {"rita", "internal_reviewer"},
	}
	if !slices.Equal(members, want) {
		t.Errorf("the members are\n %+v\nwant those of before as they were granted, and the reviewer\n %+v", members, want)
	}
}

// reviewState and reviewRecords are the steps that let a revision be
// reviewed: its status moves, and Policy & Review keeps what was asked.
const (
	reviewState   = "contractregistry/003_revision_review_state"
	reviewRecords = "policyreview/003_review"
)

// A database the build before reviews has migrated and written to is brought
// up to date with what it holds. Every revision stays the draft it is, at
// version 1 of its status, and what that build still records while this one
// is rolled out is a draft like the others. A process of this build does not
// start until the database is migrated and its role may move a status and
// keep reviews: one run of fabric-migrate -apply -grant-to gives it both.
func TestRunLeavesTheRevisionsOfTheBuildBeforeReviewsTheDraftsTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)
	role := pgx.Identifier{database.ApplicationRole}.Sanitize()

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return (step.Schema == "contractregistry" && step.ID >= reviewState) || (step.Schema == "policyreview" && step.ID >= reviewRecords)
	})
	if len(before) > len(plan)-2 {
		t.Fatalf("the plan does not carry %s and %s; this test would pass vacuously", reviewState, reviewRecords)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	// What that build granted: on the tables it had, and no statement that
	// changed a revision.
	grantOnWhatIsThere(t, conn, database.ApplicationRole)
	if _, err := conn.Exec(t.Context(), `REVOKE UPDATE ON contractregistry.contract_revision FROM `+role); err != nil {
		t.Fatalf("take back what that build did not grant: %v", err)
	}

	// A revision as a process of that build records it, with the statements
	// it runs: they name no version of a status.
	const (
		shipping = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		team     = "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"

		recordSnapshot = `
			INSERT INTO contractregistry.source_snapshot (
			    source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
			    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject
			) VALUES ($1, $2, $3, 'UPLOAD', 'openapi.yaml', $4, $4, '{}', now(), 'https://issuer.example', 'author')`
		recordRevision = `
			INSERT INTO contractregistry.contract_revision (
			    revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status
			) VALUES ($1, $2, $3, $4, $5, $6, '3.1.2', 'DRAFT')`
		submit = `
			UPDATE contractregistry.contract_revision SET status = 'IN_REVIEW', status_version = 2 WHERE revision_id = $1`
		insufficientPrivilege = "42501"
	)
	digest := func(of string) string { return "sha256:" + strings.Repeat(of, 64) }
	app := pgtest.Connect(t, database.Application)
	record := func(revision, snapshot, closure string, sequence int) error {
		if _, err := app.Exec(t.Context(), recordSnapshot, snapshot, cargopai, shipping, digest(closure)); err != nil {
			return err
		}
		_, err := app.Exec(t.Context(), recordRevision, revision, cargopai, team, shipping, snapshot, sequence)
		return err
	}
	const first, second = "5a3c8e1f-96b4-4d2c-8ae5-d4a7b6c8e9f0", "6b4d9f2a-a7c5-4e3d-9bf6-e5b8c7d9fa01"
	if err := record(first, "7c5ea03b-b8d6-4f4e-8c07-f6c9d8eafb12", "a", 1); err != nil {
		t.Fatalf("record a revision as the build before: %v", err)
	}

	// A process of this build does not start on the database as that build
	// left it: tables it keeps reviews in are not there.
	refused := func(when string, want error, naming string) {
		t.Helper()

		opened, err := db.Open(t.Context(), database.Application)
		if err == nil {
			opened.Close()
		}
		if !errors.Is(err, want) || !strings.Contains(err.Error(), naming) {
			t.Fatalf("%s: got %v, want %v naming %q", when, err, want, naming)
		}
	}
	refused("on the database as the build before left it", db.ErrNotMigrated, "policyreview.review_request does not exist")

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	applied, planned := history(t, conn), planRows(t)
	byID := func(a, b historyRow) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(applied, byID)
	slices.SortFunc(planned, byID)
	if !slices.Equal(applied, planned) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, each once\n %+v", applied, planned)
	}
	// Migrated, its role still may not move a status, and it says so.
	refused("once it is migrated, before the grant", db.ErrNotGranted, "UPDATE on contractregistry.contract_revision")
	if _, err := app.Exec(t.Context(), submit, first); sqlState(err) != insufficientPrivilege {
		t.Errorf("a submission by the role as that build granted it: got %v, want it refused for lack of privilege", err)
	}

	// That build is still running while this one is rolled out, and records
	// a draft the way it does.
	if err := record(second, "8d6fb14c-c9e7-4a5f-9d18-a7dae9fb0c23", "b", 2); err != nil {
		t.Fatalf("record a revision as the build before, after the migration: %v", err)
	}

	if err := migrate.Grant(t.Context(), conn, database.ApplicationRole); err != nil {
		t.Fatalf("grant with this build: %v", err)
	}
	opened, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated and granted: %v", err)
	}
	opened.Close()

	type revision struct {
		Sequence int64
		Status   string
		Version  int64
	}
	read := func() []revision {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT revision_sequence, status, status_version FROM contractregistry.contract_revision ORDER BY revision_sequence`)
		if err != nil {
			t.Fatalf("read the revisions: %v", err)
		}
		revisions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[revision])
		if err != nil {
			t.Fatalf("read the revisions: %v", err)
		}
		return revisions
	}
	if got, want := read(), []revision{{1, "DRAFT", 1}, {2, "DRAFT", 1}}; !slices.Equal(got, want) {
		t.Errorf("the revisions are %+v, want the drafts they were recorded as, at version 1 of their status: %+v", got, want)
	}
	// Granted, the role moves a status, and the revision of that build is
	// one that can be submitted.
	if _, err := app.Exec(t.Context(), submit, first); err != nil {
		t.Errorf("a submission once the role is granted: %v", err)
	}
	if got, want := read(), []revision{{1, "IN_REVIEW", 2}, {2, "DRAFT", 1}}; !slices.Equal(got, want) {
		t.Errorf("the revisions are %+v, want %+v", got, want)
	}
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
