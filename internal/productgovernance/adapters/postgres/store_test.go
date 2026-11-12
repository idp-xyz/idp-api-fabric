package postgres_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	bentoapplication "go.idp.xyz/idp-bento-go/application"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var (
	admin = domain.Principal{Issuer: "https://issuer.example", Subject: "platform-admin"}
	alice = domain.Principal{Issuer: "https://issuer.example", Subject: "alice"}
	bob   = domain.Principal{Issuer: "https://issuer.example", Subject: "bob"}
	cargo = domain.OrganizationRef{IdentityProvider: "https://issuer.example", Organization: "org-cargopai"}
	// A time with more digits than the store keeps, in a zone that is not UTC.
	created = time.Date(2026, 11, 11, 17, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
)

// fixture is a migrated database opened the way a Fabric process opens it,
// as the application role, with the stores on it.
type fixture struct {
	fabric      *db.Database
	products    *postgres.Products
	workspaces  *postgres.Workspaces
	roles       *postgres.RoleAssignments
	memberships *postgres.Memberships
	app         *pgx.Conn
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	return fixture{
		fabric:      fabric,
		products:    postgres.NewProducts(fabric.Framework),
		workspaces:  postgres.NewWorkspaces(fabric.Framework),
		roles:       postgres.NewRoleAssignments(fabric.Framework),
		memberships: postgres.NewMemberships(fabric.Framework),
		app:         pgtest.Connect(t, database.Application),
	}
}

// within runs fn in a transaction that must commit.
func (f fixture) within(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()

	if err := f.fabric.WithinTransaction(t.Context(), fn); err != nil {
		t.Fatalf("transaction: %v", err)
	}
}

func product(t *testing.T, key string) domain.Product {
	t.Helper()

	product, err := domain.NewProduct(domain.ProductID(ids.NewUUID()), key, "CargoPai 货代", cargo, admin, created)
	if err != nil {
		t.Fatalf("build the product: %v", err)
	}
	return product
}

func workspace(t *testing.T, of domain.Product, key string) domain.Workspace {
	t.Helper()

	workspace, err := domain.NewWorkspace(domain.WorkspaceID(ids.NewUUID()), of, key, "API engineering", alice, created)
	if err != nil {
		t.Fatalf("build the workspace: %v", err)
	}
	return workspace
}

func (f fixture) add(t *testing.T, product domain.Product, workspaces ...domain.Workspace) {
	t.Helper()

	f.within(t, func(ctx context.Context) error {
		if err := f.products.Add(ctx, product); err != nil {
			return err
		}
		for _, workspace := range workspaces {
			if err := f.workspaces.Add(ctx, workspace); err != nil {
				return err
			}
		}
		return nil
	})
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// What is stored is what is read back: the same object, timed in UTC, to the
// microsecond.
func TestAProductAndAWorkspaceAreReadBackAsTheyWereStored(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	engineering := workspace(t, cargopai, "api-engineering")
	f.add(t, cargopai, engineering)

	got, found, err := f.products.Find(t.Context(), cargopai.ID)
	if err != nil || !found || got != cargopai {
		t.Errorf("product\n %+v (found %t, %v)\nwant\n %+v", got, found, err, cargopai)
	}
	if got.CreatedAt.Location() != time.UTC || got.CreatedAt.Nanosecond() != 123456000 {
		t.Errorf("the product is timed %s, want UTC to the microsecond", got.CreatedAt)
	}

	gotWorkspace, found, err := f.workspaces.Find(t.Context(), cargopai.ID, engineering.ID)
	if err != nil || !found || gotWorkspace != engineering {
		t.Errorf("workspace\n %+v (found %t, %v)\nwant\n %+v", gotWorkspace, found, err, engineering)
	}

	// Inside a transaction a read sees what that transaction wrote.
	parcel := product(t, "parcel")
	f.within(t, func(ctx context.Context) error {
		if err := f.products.Add(ctx, parcel); err != nil {
			return err
		}
		if got, found, err := f.products.Find(ctx, parcel.ID); err != nil || !found || got != parcel {
			t.Errorf("inside its transaction the product is %+v (found %t, %v)", got, found, err)
		}
		return nil
	})
}

func TestWhatIsNotThereIsNotFound(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	engineering := workspace(t, cargopai, "api-engineering")
	f.add(t, cargopai, engineering)
	f.add(t, parcel)

	if _, found, err := f.products.Find(t.Context(), domain.ProductID(ids.NewUUID())); err != nil || found {
		t.Errorf("a product nobody stored: found %t, %v", found, err)
	}
	if _, found, err := f.workspaces.Find(t.Context(), cargopai.ID, domain.WorkspaceID(ids.NewUUID())); err != nil || found {
		t.Errorf("a workspace nobody stored: found %t, %v", found, err)
	}
	// The workspace exists. Under another product it is not there.
	if _, found, err := f.workspaces.Find(t.Context(), parcel.ID, engineering.ID); err != nil || found {
		t.Errorf("a workspace of CargoPai under Parcel: found %t, %v", found, err)
	}
}

// FR-001: a key that is taken is refused. The refusal does not fail the
// transaction, which can go on.
func TestAKeyThatIsTakenIsRefusedWithoutFailingTheTransaction(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	engineering := workspace(t, cargopai, "api-engineering")
	f.add(t, cargopai, engineering)

	parcel := product(t, "parcel")
	f.within(t, func(ctx context.Context) error {
		if err := f.products.Add(ctx, product(t, "cargopai")); !errors.Is(err, ports.ErrProductKeyTaken) {
			t.Errorf("a second product with the key: got %v, want %v", err, ports.ErrProductKeyTaken)
		}
		if err := f.workspaces.Add(ctx, workspace(t, cargopai, "api-engineering")); !errors.Is(err, ports.ErrWorkspaceKeyTaken) {
			t.Errorf("a second workspace with the key: got %v, want %v", err, ports.ErrWorkspaceKeyTaken)
		}
		// The same transaction still writes.
		if err := f.products.Add(ctx, parcel); err != nil {
			return err
		}
		// A key is unique inside its product, not across products.
		return f.workspaces.Add(ctx, workspace(t, parcel, "api-engineering"))
	})

	if got, _, _ := f.products.Find(t.Context(), cargopai.ID); got != cargopai {
		t.Errorf("the product that held the key is now %+v, want it untouched", got)
	}
	if _, found, _ := f.products.Find(t.Context(), parcel.ID); !found {
		t.Error("what the transaction wrote after the refusals is gone")
	}
}

func TestTwoRegistrationsOfOneKeyAtOnceLeaveOneProduct(t *testing.T) {
	f := newFixture(t)

	const racers = 6
	results := make(chan error, racers)
	for range racers {
		candidate := product(t, "cargopai")
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.products.Add(ctx, candidate)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrProductKeyTaken):
			t.Errorf("a registration failed with %v, want it to win or to find the key taken", err)
		}
	}
	if won != 1 {
		t.Errorf("%d registrations of one key succeeded, want 1", won)
	}
}

func TestRolesAreHeldByAPrincipalInAProduct(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)

	owners, err := cargopai.AssignFirstOwners(
		[]domain.RoleAssignmentID{domain.RoleAssignmentID(ids.NewUUID()), domain.RoleAssignmentID(ids.NewUUID())},
		[]domain.Principal{alice, bob}, admin)
	if err != nil {
		t.Fatalf("assign the owners: %v", err)
	}
	f.assign(t, owners...)

	owner := []domain.Role{domain.RoleProductOwner}
	if got := f.rolesOf(t, cargopai.ID, alice); !slices.Equal(got, owner) {
		t.Errorf("alice in CargoPai holds %v, want %v", got, owner)
	}
	if got := f.rolesOf(t, cargopai.ID, bob); !slices.Equal(got, owner) {
		t.Errorf("bob in CargoPai holds %v, want %v", got, owner)
	}

	// A role in one product is nothing in another, and a principal is its
	// issuer and its subject together.
	nothing := map[string][]domain.Role{
		"alice in Parcel":                      f.rolesOf(t, parcel.ID, alice),
		"the registrant in CargoPai":           f.rolesOf(t, cargopai.ID, admin),
		"alice's subject under another issuer": f.rolesOf(t, cargopai.ID, domain.Principal{Issuer: "https://other.example", Subject: alice.Subject}),
		"another subject under alice's issuer": f.rolesOf(t, cargopai.ID, domain.Principal{Issuer: alice.Issuer, Subject: "Alice"}),
		"alice in a product nobody stored":     f.rolesOf(t, domain.ProductID(ids.NewUUID()), alice),
	}
	for who, roles := range nothing {
		if len(roles) != 0 {
			t.Errorf("%s holds %v, want nothing", who, roles)
		}
	}

	// The record says who granted the role and when (FR-003).
	var grantedBy string
	var grantedAt time.Time
	if err := f.app.QueryRow(t.Context(),
		`SELECT granted_by_subject, granted_at FROM productgovernance.product_role_assignment
		 WHERE product_id = $1 AND principal_subject = $2`, string(cargopai.ID), alice.Subject).Scan(&grantedBy, &grantedAt); err != nil {
		t.Fatalf("read the assignment: %v", err)
	}
	if grantedBy != admin.Subject || !grantedAt.Equal(cargopai.CreatedAt) {
		t.Errorf("granted by %q at %s, want %q at %s", grantedBy, grantedAt, admin.Subject, cargopai.CreatedAt)
	}
}

func TestAWriteNeedsATransaction(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	owners, _ := cargopai.AssignFirstOwners([]domain.RoleAssignmentID{domain.RoleAssignmentID(ids.NewUUID())}, []domain.Principal{alice}, admin)

	for what, err := range map[string]error{
		"a product":         f.products.Add(t.Context(), cargopai),
		"a workspace":       f.workspaces.Add(t.Context(), workspace(t, cargopai, "api")),
		"a role assignment": f.roles.Add(t.Context(), owners[0]),
	} {
		if !errors.Is(err, bentopostgres.ErrTransactionRequired) {
			t.Errorf("%s outside a transaction: got %v, want %v", what, err, bentopostgres.ErrTransactionRequired)
		}
	}
	if _, found, _ := f.products.Find(t.Context(), cargopai.ID); found {
		t.Error("a product was written outside a transaction")
	}
}

// The schema refuses what the model never produces, whoever writes it.
func TestTheSchemaHoldsItsOwnLine(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	orphan := workspace(t, cargopai, "orphan")
	orphan.ProductID = domain.ProductID(ids.NewUUID())
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.workspaces.Add(ctx, orphan) })
	if sqlState(err) != "23503" {
		t.Errorf("a workspace of a product nobody stored: got %v, want a foreign key violation", err)
	}

	unknown := product(t, "unknown-status")
	unknown.Status = "ARCHIVED"
	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.products.Add(ctx, unknown) })
	if sqlState(err) != "23514" {
		t.Errorf("a product in a status nobody defined: got %v, want a check violation", err)
	}

	administrator := owning(t, cargopai, alice)
	administrator.Role = "platform_admin"
	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.roles.Add(ctx, administrator) })
	if sqlState(err) != "23514" {
		t.Errorf("a role nobody defined: got %v, want a check violation", err)
	}
}

// A process registers a product and a workspace and reads them. It has no
// statement that changes or removes one, or that removes a role assignment,
// and its role holds no privilege to. What it may change of a role
// assignment, the table decides.
func TestTheApplicationRoleCanRegisterAndReadAndNothingElse(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai, workspace(t, cargopai, "api-engineering"))

	refused := []string{
		`UPDATE productgovernance.product SET status = 'SUSPENDED'`,
		`UPDATE productgovernance.product SET product_key = 'renamed'`,
		`DELETE FROM productgovernance.product`,
		`UPDATE productgovernance.workspace SET product_id = gen_random_uuid()`,
		`DELETE FROM productgovernance.workspace`,
		`DELETE FROM productgovernance.product_role_assignment`,
		`TRUNCATE productgovernance.product CASCADE`,
		`ALTER TABLE productgovernance.product DROP CONSTRAINT product_key_unique`,
		`CREATE TABLE productgovernance.intruder (id integer)`,
	}
	for _, statement := range refused {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != "42501" {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
	if got, _, _ := f.products.Find(t.Context(), cargopai.ID); got != cargopai {
		t.Errorf("the product is now %+v, want it untouched", got)
	}
}

func TestACommitOfUnknownOutcomeIsNamedInTheTermsOfTheUseCases(t *testing.T) {
	lost := errors.New("the connection was lost during the commit")
	uncertain := postgres.NewTransactions(func(context.Context, func(context.Context) error) error {
		return errors.Join(bentoapplication.ErrCommitUncertain, lost)
	})
	err := uncertain.WithinTransaction(t.Context(), func(context.Context) error { return nil })
	if !errors.Is(err, ports.ErrOutcomeUncertain) || !errors.Is(err, lost) {
		t.Errorf("got %v, want %v with its cause", err, ports.ErrOutcomeUncertain)
	}

	// Any other failure passes through as it is.
	failure := errors.New("the write failed")
	failing := postgres.NewTransactions(func(context.Context, func(context.Context) error) error { return failure })
	if err := failing.WithinTransaction(t.Context(), func(context.Context) error { return nil }); err != failure || errors.Is(err, ports.ErrOutcomeUncertain) {
		t.Errorf("got %v, want the failure as it is", err)
	}

	f := newFixture(t)
	real := postgres.NewTransactions(f.fabric.WithinTransaction)
	cargopai := product(t, "cargopai")
	if err := real.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.products.Add(ctx, cargopai) }); err != nil {
		t.Fatalf("a transaction on the database: %v", err)
	}
	if _, found, _ := f.products.Find(t.Context(), cargopai.ID); !found {
		t.Error("what the transaction wrote is not there")
	}
}
