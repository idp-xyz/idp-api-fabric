package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var mallory = domain.Principal{Issuer: "https://issuer.example", Subject: "mallory"}

// scene is CargoPai with two workspaces and Parcel with one, all stored.
type scene struct {
	cargopai, parcel        domain.Product
	engineering, operations domain.Workspace
	sorting                 domain.Workspace
}

func (f fixture) scene(t *testing.T) scene {
	t.Helper()

	var s scene
	s.cargopai, s.parcel = product(t, "cargopai"), product(t, "parcel")
	s.engineering, s.operations = workspace(t, s.cargopai, "api-engineering"), workspace(t, s.cargopai, "operations")
	s.sorting = workspace(t, s.parcel, "sorting")
	f.add(t, s.cargopai, s.engineering, s.operations)
	f.add(t, s.parcel, s.sorting)
	return s
}

// membership makes member hold role in the workspace, granted by alice.
func membership(t *testing.T, of domain.Product, in domain.Workspace, member domain.Principal, role domain.Role) domain.Membership {
	t.Helper()

	granted, err := domain.GrantMembership(domain.MembershipID(ids.NewUUID()), of, in, member, role, alice, created)
	if err != nil {
		t.Fatalf("build the membership: %v", err)
	}
	return granted
}

// ended is the membership as it is once by has revoked it, an hour after it
// was granted.
func ended(t *testing.T, granted domain.Membership, by domain.Principal) domain.Membership {
	t.Helper()

	revoked, err := granted.Revoke(by, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	return revoked
}

func (f fixture) grant(t *testing.T, memberships ...domain.Membership) {
	t.Helper()

	f.within(t, func(ctx context.Context) error {
		for _, granted := range memberships {
			if err := f.memberships.Add(ctx, granted); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f fixture) revoke(t *testing.T, revoked domain.Membership) {
	t.Helper()

	f.within(t, func(ctx context.Context) error { return f.memberships.Revoke(ctx, revoked) })
}

func (f fixture) heldBy(t *testing.T, productID domain.ProductID, principal domain.Principal) []domain.Membership {
	t.Helper()

	held, err := f.memberships.HeldBy(t.Context(), productID, principal)
	if err != nil {
		t.Fatalf("read the memberships: %v", err)
	}
	return held
}

// byID orders memberships, so that two lists of the same ones are equal.
func byID(memberships ...domain.Membership) []domain.Membership {
	sorted := slices.Clone(memberships)
	slices.SortFunc(sorted, func(a, b domain.Membership) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return sorted
}

// record is what the table keeps of one membership, in force or not.
type record struct {
	Version   int64
	GrantedBy string
	Revoked   bool
	RevokedAt time.Time
	RevokedBy string
}

func (f fixture) record(t *testing.T, id domain.MembershipID) record {
	t.Helper()

	var kept record
	if err := f.app.QueryRow(t.Context(), `
		SELECT membership_version, granted_by_subject, revoked_at IS NOT NULL,
		       coalesce(revoked_at, 'epoch'), coalesce(revoked_by_subject, '')
		FROM productgovernance.workspace_membership WHERE membership_id = $1`, string(id),
	).Scan(&kept.Version, &kept.GrantedBy, &kept.Revoked, &kept.RevokedAt, &kept.RevokedBy); err != nil {
		t.Fatalf("read the record of the membership: %v", err)
	}
	return kept
}

// What is stored is what is read back: the same membership, timed in UTC, to
// the microsecond.
func TestAMembershipIsReadBackAsItWasStored(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	maintainer := membership(t, s.cargopai, s.engineering, bob, domain.RoleWorkspaceMaintainer)

	f.within(t, func(ctx context.Context) error {
		if err := f.memberships.Add(ctx, maintainer); err != nil {
			return err
		}
		// Inside a transaction a read sees what that transaction wrote.
		if got, found, err := f.memberships.Find(ctx, s.cargopai.ID, s.engineering.ID, maintainer.ID); err != nil || !found || got != maintainer {
			t.Errorf("inside its transaction the membership is %+v (found %t, %v)", got, found, err)
		}
		if held, err := f.memberships.HeldBy(ctx, s.cargopai.ID, bob); err != nil || !slices.Equal(held, []domain.Membership{maintainer}) {
			t.Errorf("inside its transaction bob holds %+v (%v)", held, err)
		}
		return nil
	})

	got, found, err := f.memberships.Find(t.Context(), s.cargopai.ID, s.engineering.ID, maintainer.ID)
	if err != nil || !found || got != maintainer {
		t.Errorf("membership\n %+v (found %t, %v)\nwant\n %+v", got, found, err, maintainer)
	}
	if got.GrantedAt.Location() != time.UTC || got.GrantedAt.Nanosecond() != 123456000 {
		t.Errorf("the membership is timed %s, want UTC to the microsecond", got.GrantedAt)
	}
	if held := f.heldBy(t, s.cargopai.ID, bob); !slices.Equal(held, []domain.Membership{maintainer}) {
		t.Errorf("bob holds %+v in CargoPai, want the membership", held)
	}
	// The record says who granted it (FR-003).
	if kept := f.record(t, maintainer.ID); kept.Version != 1 || kept.GrantedBy != alice.Subject || kept.Revoked {
		t.Errorf("the record is %+v, want version 1, granted by alice, in force", kept)
	}
}

// A membership is of one workspace of one product and of one principal. It
// is found nowhere else.
func TestAMembershipIsFoundOnlyWhereItBelongs(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	f.grant(t, author)

	missing := map[string]struct {
		productID   domain.ProductID
		workspaceID domain.WorkspaceID
		id          domain.MembershipID
	}{
		"a membership nobody stored":              {s.cargopai.ID, s.engineering.ID, domain.MembershipID(ids.NewUUID())},
		"under another workspace of its product":  {s.cargopai.ID, s.operations.ID, author.ID},
		"under its workspace and another product": {s.parcel.ID, s.engineering.ID, author.ID},
		"under a workspace of another product":    {s.parcel.ID, s.sorting.ID, author.ID},
		"under a product nobody stored":           {domain.ProductID(ids.NewUUID()), s.engineering.ID, author.ID},
		"under a workspace nobody stored":         {s.cargopai.ID, domain.WorkspaceID(ids.NewUUID()), author.ID},
	}
	for name, asked := range missing {
		if got, found, err := f.memberships.Find(t.Context(), asked.productID, asked.workspaceID, asked.id); err != nil || found {
			t.Errorf("%s: found %t (%+v), %v", name, found, got, err)
		}
	}

	// A membership in one product is nothing in another, and a principal is
	// its issuer and its subject together.
	nothing := map[string][]domain.Membership{
		"bob in Parcel":                      f.heldBy(t, s.parcel.ID, bob),
		"alice, who granted it":              f.heldBy(t, s.cargopai.ID, alice),
		"bob's subject under another issuer": f.heldBy(t, s.cargopai.ID, domain.Principal{Issuer: "https://other.example", Subject: bob.Subject}),
		"another subject under bob's issuer": f.heldBy(t, s.cargopai.ID, domain.Principal{Issuer: bob.Issuer, Subject: "Bob"}),
		"bob in a product nobody stored":     f.heldBy(t, domain.ProductID(ids.NewUUID()), bob),
	}
	for who, held := range nothing {
		if len(held) != 0 {
			t.Errorf("%s holds %+v, want nothing", who, held)
		}
	}
}

// A principal holds a role in a workspace at most once at a time. A second
// grant is refused, and the refusal does not fail the transaction.
func TestAPrincipalHoldsARoleInAWorkspaceOnceAtATime(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	f.grant(t, author)

	maintainer := membership(t, s.cargopai, s.engineering, bob, domain.RoleWorkspaceMaintainer)
	elsewhere := membership(t, s.cargopai, s.operations, bob, domain.RoleInternalAuthor)
	namesake := membership(t, s.cargopai, s.engineering, domain.Principal{Issuer: "https://other.example", Subject: bob.Subject}, domain.RoleInternalAuthor)
	somebodyElse := membership(t, s.cargopai, s.engineering, mallory, domain.RoleInternalAuthor)
	f.within(t, func(ctx context.Context) error {
		again := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
		if err := f.memberships.Add(ctx, again); !errors.Is(err, ports.ErrMembershipExists) {
			t.Errorf("a second grant of the role: got %v, want %v", err, ports.ErrMembershipExists)
		}
		// The same transaction still writes: another role, another
		// workspace, the same subject under another issuer, somebody else.
		for _, other := range []domain.Membership{maintainer, elsewhere, namesake, somebodyElse} {
			if err := f.memberships.Add(ctx, other); err != nil {
				return err
			}
		}
		return nil
	})

	if held := f.heldBy(t, s.cargopai.ID, bob); !slices.Equal(byID(held...), byID(author, maintainer, elsewhere)) {
		t.Errorf("bob holds\n %+v\nwant the first membership as it was and the two that differ from it", held)
	}
	if held := f.heldBy(t, s.cargopai.ID, mallory); !slices.Equal(held, []domain.Membership{somebodyElse}) {
		t.Errorf("mallory holds %+v, want her membership", held)
	}
}

func TestTwoGrantsOfOneRoleAtOnceLeaveOneMembership(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	const racers = 6
	results := make(chan error, racers)
	for range racers {
		candidate := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.memberships.Add(ctx, candidate)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrMembershipExists):
			t.Errorf("a grant failed with %v, want it to win or to find the role held", err)
		}
	}
	if held := f.heldBy(t, s.cargopai.ID, bob); won != 1 || len(held) != 1 {
		t.Errorf("%d grants of one role succeeded and bob holds %d memberships, want 1 and 1", won, len(held))
	}
}

// A revoked membership grants nothing: no read finds it. Its row is kept,
// with who revoked it and when (F03 §13), and the role can be granted again
// as a new membership.
func TestARevokedMembershipGrantsNothingAndIsKept(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	maintainer := membership(t, s.cargopai, s.engineering, bob, domain.RoleWorkspaceMaintainer)
	f.grant(t, author, maintainer)

	revoked := ended(t, author, admin)
	f.within(t, func(ctx context.Context) error {
		if err := f.memberships.Revoke(ctx, revoked); err != nil {
			return err
		}
		// Inside the transaction that revoked it, it is gone already.
		if _, found, err := f.memberships.Find(ctx, s.cargopai.ID, s.engineering.ID, author.ID); err != nil || found {
			t.Errorf("inside the transaction that revoked it: found %t, %v", found, err)
		}
		return nil
	})

	if _, found, err := f.memberships.Find(t.Context(), s.cargopai.ID, s.engineering.ID, author.ID); err != nil || found {
		t.Errorf("the revoked membership: found %t, %v", found, err)
	}
	// What was not revoked is still held, as it was.
	if held := f.heldBy(t, s.cargopai.ID, bob); !slices.Equal(held, []domain.Membership{maintainer}) {
		t.Errorf("bob holds %+v, want only the membership that was not revoked", held)
	}

	kept := f.record(t, author.ID)
	if want := (record{Version: 2, GrantedBy: alice.Subject, Revoked: true, RevokedBy: admin.Subject}); kept.Version != want.Version ||
		kept.GrantedBy != want.GrantedBy || !kept.Revoked || kept.RevokedBy != want.RevokedBy || !kept.RevokedAt.Equal(revoked.RevokedAt) {
		t.Errorf("the record is %+v, want version 2, granted by alice, revoked by the administrator at %s", kept, revoked.RevokedAt)
	}

	// The role is granted again as a new membership. The record of the
	// first stays.
	again := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	f.grant(t, again)
	if held := f.heldBy(t, s.cargopai.ID, bob); !slices.Equal(byID(held...), byID(maintainer, again)) {
		t.Errorf("bob holds %+v, want the membership that was not revoked and the new one", held)
	}
	if kept := f.record(t, author.ID); !kept.Revoked || kept.Version != 2 {
		t.Errorf("the record of the first membership is now %+v, want it as the revocation left it", kept)
	}
}

// A membership is revoked once. A revocation that finds nothing in force
// changes nothing and does not fail its transaction.
func TestAMembershipIsRevokedOnce(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	inForce := membership(t, s.cargopai, s.engineering, mallory, domain.RoleInternalAuthor)
	f.grant(t, author, inForce)
	f.revoke(t, ended(t, author, alice))

	// A revocation names the membership with its workspace and its product.
	// Under another workspace there is nothing to revoke.
	misplaced := ended(t, inForce, admin)
	misplaced.WorkspaceID = s.operations.ID
	elsewhere := ended(t, inForce, admin)
	elsewhere.ProductID = s.parcel.ID

	later := membership(t, s.cargopai, s.operations, mallory, domain.RoleWorkspaceMaintainer)
	f.within(t, func(ctx context.Context) error {
		for name, revoked := range map[string]domain.Membership{
			"a second revocation":                  ended(t, author, admin),
			"a membership nobody stored":           ended(t, membership(t, s.cargopai, s.engineering, bob, domain.RoleWorkspaceMaintainer), admin),
			"a membership under another workspace": misplaced,
			"a membership under another product":   elsewhere,
		} {
			if err := f.memberships.Revoke(ctx, revoked); !errors.Is(err, ports.ErrMembershipGone) {
				t.Errorf("%s: got %v, want %v", name, err, ports.ErrMembershipGone)
			}
		}
		// The same transaction still writes.
		return f.memberships.Add(ctx, later)
	})

	// Who revoked it first is who revoked it.
	if kept := f.record(t, author.ID); kept.Version != 2 || kept.RevokedBy != alice.Subject {
		t.Errorf("the record is %+v, want it as the first revocation left it", kept)
	}
	if held := f.heldBy(t, s.cargopai.ID, mallory); !slices.Equal(byID(held...), byID(inForce, later)) {
		t.Errorf("mallory holds %+v, want her membership untouched and what the transaction wrote after the refusals", held)
	}
}

func TestTwoRevocationsOfOneMembershipAtOnceLeaveOneRevocation(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	f.grant(t, author)

	const racers = 6
	results := make(chan error, racers)
	for index := range racers {
		revoked := ended(t, author, domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("owner-%d", index)})
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.memberships.Revoke(ctx, revoked)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrMembershipGone):
			t.Errorf("a revocation failed with %v, want it to win or to find the membership gone", err)
		}
	}
	if kept := f.record(t, author.ID); won != 1 || kept.Version != 2 || !strings.HasPrefix(kept.RevokedBy, "owner-") {
		t.Errorf("%d revocations succeeded and the record is %+v, want 1 and one revocation", won, kept)
	}
}

func TestAWriteOfAMembershipNeedsATransaction(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)

	if err := f.memberships.Add(t.Context(), author); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("a grant outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	if held := f.heldBy(t, s.cargopai.ID, bob); len(held) != 0 {
		t.Errorf("a membership was written outside a transaction: %+v", held)
	}

	f.grant(t, author)
	if err := f.memberships.Revoke(t.Context(), ended(t, author, alice)); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("a revocation outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	if _, found, _ := f.memberships.Find(t.Context(), s.cargopai.ID, s.engineering.ID, author.ID); !found {
		t.Error("a membership was revoked outside a transaction")
	}
}

// A membership is added in force and revoked afterwards. The store refuses
// one that is in the other state before it sends anything.
func TestTheStoreTakesAMembershipInTheStateItsStatementIsFor(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	inForce := membership(t, s.cargopai, s.engineering, mallory, domain.RoleInternalAuthor)
	f.grant(t, inForce)

	f.within(t, func(ctx context.Context) error {
		if err := f.memberships.Add(ctx, ended(t, author, alice)); err == nil || errors.Is(err, ports.ErrMembershipExists) {
			t.Errorf("a membership added revoked: got %v, want it refused for what it is", err)
		}
		if err := f.memberships.Revoke(ctx, inForce); err == nil || errors.Is(err, ports.ErrMembershipGone) {
			t.Errorf("a revocation that says nothing of when: got %v, want it refused for what it is", err)
		}
		return nil
	})
	if held := f.heldBy(t, s.cargopai.ID, bob); len(held) != 0 {
		t.Errorf("a membership that was added revoked is held: %+v", held)
	}
	if kept := f.record(t, inForce.ID); kept.Revoked || kept.Version != 1 {
		t.Errorf("the record is %+v, want it in force at version 1", kept)
	}
}

// The schema refuses what the model never produces, whoever writes it. The
// application role holds UPDATE on the table, to end a membership, and still
// changes nothing else of one.
func TestTheSchemaHoldsTheLineOfAMembership(t *testing.T) {
	const (
		foreignKeyViolation = "23503"
		uniqueViolation     = "23505"
		checkViolation      = "23514"
		// What the trigger of the table raises.
		integrityViolation    = "23000"
		insufficientPrivilege = "42501"
	)

	f := newFixture(t)
	s := f.scene(t)
	author := membership(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor)
	gone := membership(t, s.cargopai, s.engineering, mallory, domain.RoleInternalAuthor)
	f.grant(t, author, gone)
	f.revoke(t, ended(t, gone, alice))

	// The store, handed what the model never produces.
	misplaced := membership(t, s.cargopai, s.engineering, alice, domain.RoleInternalAuthor)
	misplaced.ProductID = s.parcel.ID
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.memberships.Add(ctx, misplaced) })
	if sqlState(err) != foreignKeyViolation {
		t.Errorf("a membership of a workspace of CargoPai under Parcel: got %v, want a foreign key violation", err)
	}
	owner := membership(t, s.cargopai, s.engineering, alice, domain.RoleInternalAuthor)
	owner.Role = domain.RoleProductOwner
	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.memberships.Add(ctx, owner) })
	if sqlState(err) != checkViolation {
		t.Errorf("a membership in the role that owns a product: got %v, want a check violation", err)
	}

	// The application role, writing by hand.
	const update = `UPDATE productgovernance.workspace_membership SET `
	const revocation = `revoked_at = granted_at + interval '1 hour', revoked_by_issuer = 'https://issuer.example', revoked_by_subject = 'mallory'`
	statements := []struct {
		name      string
		statement string
		id        domain.MembershipID
		state     string
	}{
		{"give it to somebody else", update + `principal_subject = 'mallory' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"move it to another issuer", update + `principal_issuer = 'https://other.example' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"change its role", update + `role = 'workspace_maintainer' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"change who granted it", update + `granted_by_subject = 'mallory' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"change when it was granted", update + `granted_at = granted_at - interval '1 year' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"change its identifier", update + `membership_id = gen_random_uuid() WHERE membership_id = $1`, author.ID, integrityViolation},
		{"change nothing at all", update + `role = role WHERE membership_id = $1`, author.ID, integrityViolation},
		{"count a change that did not happen", update + `membership_version = 2 WHERE membership_id = $1`, author.ID, integrityViolation},
		{"end it without counting the change", update + revocation + ` WHERE membership_id = $1`, author.ID, integrityViolation},
		{"end it and give it to somebody else", update + revocation + `, membership_version = 2, principal_subject = 'mallory' WHERE membership_id = $1`, author.ID, integrityViolation},
		{"end it without saying who did", update + `revoked_at = granted_at + interval '1 hour', membership_version = 2 WHERE membership_id = $1`, author.ID, checkViolation},
		{"end it before it began", update + `revoked_at = granted_at - interval '1 second', revoked_by_issuer = 'i', revoked_by_subject = 's', membership_version = 2 WHERE membership_id = $1`,
			author.ID, checkViolation},
		{"bring a revoked one back", update + `revoked_at = NULL, revoked_by_issuer = NULL, revoked_by_subject = NULL, membership_version = 3 WHERE membership_id = $1`,
			gone.ID, integrityViolation},
		{"revoke a revoked one again", update + `revoked_by_subject = 'bob', membership_version = 3 WHERE membership_id = $1`, gone.ID, integrityViolation},
		{"hold the role twice at a time", `
			INSERT INTO productgovernance.workspace_membership
			    (membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
			     membership_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, workspace_id, principal_issuer, principal_subject, role,
			       1, granted_at, granted_by_issuer, granted_by_subject
			FROM productgovernance.workspace_membership WHERE membership_id = $1`, author.ID, uniqueViolation},
		{"remove it", `DELETE FROM productgovernance.workspace_membership WHERE membership_id = $1`, author.ID, insufficientPrivilege},
		{"remove a revoked one", `DELETE FROM productgovernance.workspace_membership WHERE membership_id = $1`, gone.ID, insufficientPrivilege},
	}
	for _, c := range statements {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.app.Exec(t.Context(), c.statement, string(c.id)); sqlState(err) != c.state {
				t.Errorf("%s\n got %v, want it refused with SQLSTATE %s", c.statement, err, c.state)
			}
		})
	}
	for _, statement := range []string{
		`TRUNCATE productgovernance.workspace_membership`,
		`ALTER TABLE productgovernance.workspace_membership DISABLE TRIGGER workspace_membership_only_ends`,
		`DROP TRIGGER workspace_membership_only_ends ON productgovernance.workspace_membership`,
		`DROP INDEX productgovernance.workspace_membership_in_force_unique`,
		`CREATE OR REPLACE FUNCTION productgovernance.workspace_membership_only_ends() RETURNS trigger
		     LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
	} {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Nothing of it took effect.
	if got, found, err := f.memberships.Find(t.Context(), s.cargopai.ID, s.engineering.ID, author.ID); err != nil || !found || got != author {
		t.Errorf("the membership is now %+v (found %t, %v), want it untouched", got, found, err)
	}
	if kept := f.record(t, gone.ID); !kept.Revoked || kept.Version != 2 || kept.RevokedBy != alice.Subject {
		t.Errorf("the record of the revoked membership is now %+v, want it as the revocation left it", kept)
	}
	if held := f.heldBy(t, s.cargopai.ID, mallory); len(held) != 0 {
		t.Errorf("mallory holds %+v, want nothing", held)
	}
}
