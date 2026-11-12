package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// owning makes principal an owner of the product, granted by the
// administrator of the platform.
func owning(t *testing.T, of domain.Product, principal domain.Principal) domain.RoleAssignment {
	t.Helper()

	granted, err := domain.GrantProductRole(domain.RoleAssignmentID(ids.NewUUID()), of, principal, domain.RoleProductOwner, admin, created)
	if err != nil {
		t.Fatalf("build the role assignment: %v", err)
	}
	return granted
}

// disowned is the assignment as it is once by has revoked it, an hour after
// it was granted, while its product had another owner.
func disowned(t *testing.T, granted domain.RoleAssignment, by domain.Principal) domain.RoleAssignment {
	t.Helper()

	revoked, err := granted.Revoke(by, created.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("revoke the role assignment: %v", err)
	}
	return revoked
}

func (f fixture) assign(t *testing.T, assignments ...domain.RoleAssignment) {
	t.Helper()

	f.within(t, func(ctx context.Context) error {
		for _, granted := range assignments {
			if err := f.roles.Add(ctx, granted); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f fixture) unassign(t *testing.T, revoked domain.RoleAssignment) {
	t.Helper()

	f.within(t, func(ctx context.Context) error { return f.roles.Revoke(ctx, revoked) })
}

func (f fixture) rolesOf(t *testing.T, productID domain.ProductID, principal domain.Principal) []domain.Role {
	t.Helper()

	roles, err := f.roles.RolesOf(t.Context(), productID, principal)
	if err != nil {
		t.Fatalf("read the roles: %v", err)
	}
	return roles
}

// owners counts the owners of the product the way a revocation does: where
// they are locked, in a transaction of its own.
func (f fixture) owners(t *testing.T, productID domain.ProductID) (owners int) {
	t.Helper()

	f.within(t, func(ctx context.Context) (err error) {
		owners, err = f.roles.LockOwners(ctx, productID)
		return err
	})
	return owners
}

// waiting returns once that many sessions of the database wait for a lock:
// the transactions a test started have reached the rows they compete for.
func (f fixture) waiting(t *testing.T, sessions int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		if err := f.app.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&blocked); err != nil {
			t.Fatalf("count the sessions that wait for a lock: %v", err)
		}
		if blocked == sessions {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d sessions wait for a lock, want %d", blocked, sessions)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// roleRecord is what the table keeps of one role assignment, in force or
// not.
type roleRecord struct {
	Version   int64
	GrantedBy string
	Revoked   bool
	RevokedAt time.Time
	RevokedBy string
}

func (f fixture) roleRecord(t *testing.T, id domain.RoleAssignmentID) roleRecord {
	t.Helper()

	var kept roleRecord
	if err := f.app.QueryRow(t.Context(), `
		SELECT role_assignment_version, granted_by_subject, revoked_at IS NOT NULL,
		       coalesce(revoked_at, 'epoch'), coalesce(revoked_by_subject, '')
		FROM productgovernance.product_role_assignment WHERE role_assignment_id = $1`, string(id),
	).Scan(&kept.Version, &kept.GrantedBy, &kept.Revoked, &kept.RevokedAt, &kept.RevokedBy); err != nil {
		t.Fatalf("read the record of the role assignment: %v", err)
	}
	return kept
}

// ownerRoles is what an owner holds in its product.
var ownerRoles = []domain.Role{domain.RoleProductOwner}

// What is stored is what is read back: the same assignment, timed in UTC, to
// the microsecond.
func TestARoleAssignmentIsReadBackAsItWasStored(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	granted := owning(t, cargopai, alice)

	f.within(t, func(ctx context.Context) error {
		if err := f.roles.Add(ctx, granted); err != nil {
			return err
		}
		// Inside a transaction a read sees what that transaction wrote.
		if got, found, err := f.roles.Find(ctx, cargopai.ID, granted.ID); err != nil || !found || got != granted {
			t.Errorf("inside its transaction the assignment is %+v (found %t, %v)", got, found, err)
		}
		if roles, err := f.roles.RolesOf(ctx, cargopai.ID, alice); err != nil || !slices.Equal(roles, ownerRoles) {
			t.Errorf("inside its transaction alice holds %v (%v)", roles, err)
		}
		return nil
	})

	got, found, err := f.roles.Find(t.Context(), cargopai.ID, granted.ID)
	if err != nil || !found || got != granted {
		t.Errorf("assignment\n %+v (found %t, %v)\nwant\n %+v", got, found, err, granted)
	}
	if got.GrantedAt.Location() != time.UTC || got.GrantedAt.Nanosecond() != 123456000 {
		t.Errorf("the assignment is timed %s, want UTC to the microsecond", got.GrantedAt)
	}
	if roles := f.rolesOf(t, cargopai.ID, alice); !slices.Equal(roles, ownerRoles) {
		t.Errorf("alice holds %v in CargoPai, want %v", roles, ownerRoles)
	}
	// The record says who granted it (FR-003).
	if kept := f.roleRecord(t, granted.ID); kept.Version != 1 || kept.GrantedBy != admin.Subject || kept.Revoked {
		t.Errorf("the record is %+v, want version 1, granted by the administrator, in force", kept)
	}
}

// A role assignment is of one product. It is found nowhere else.
func TestARoleAssignmentIsFoundOnlyUnderItsProduct(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	granted := owning(t, cargopai, alice)
	f.assign(t, granted)

	missing := map[string]struct {
		productID domain.ProductID
		id        domain.RoleAssignmentID
	}{
		"an assignment nobody stored":   {cargopai.ID, domain.RoleAssignmentID(ids.NewUUID())},
		"under another product":         {parcel.ID, granted.ID},
		"under a product nobody stored": {domain.ProductID(ids.NewUUID()), granted.ID},
	}
	for name, asked := range missing {
		if got, found, err := f.roles.Find(t.Context(), asked.productID, asked.id); err != nil || found {
			t.Errorf("%s: found %t (%+v), %v", name, found, got, err)
		}
	}
}

// A principal holds a role in a product at most once at a time. A second
// grant is refused, and the refusal does not fail the transaction.
func TestAPrincipalHoldsARoleInAProductOnceAtATime(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	first := owning(t, cargopai, alice)
	f.assign(t, first)

	elsewhere := owning(t, parcel, alice)
	namesake := owning(t, cargopai, domain.Principal{Issuer: "https://other.example", Subject: alice.Subject})
	somebodyElse := owning(t, cargopai, bob)
	f.within(t, func(ctx context.Context) error {
		if err := f.roles.Add(ctx, owning(t, cargopai, alice)); !errors.Is(err, ports.ErrRoleAssignmentExists) {
			t.Errorf("a second grant of the role: got %v, want %v", err, ports.ErrRoleAssignmentExists)
		}
		// The same transaction still writes: the role in another product,
		// the same subject under another issuer, somebody else.
		for _, other := range []domain.RoleAssignment{elsewhere, namesake, somebodyElse} {
			if err := f.roles.Add(ctx, other); err != nil {
				return err
			}
		}
		return nil
	})

	for name, kept := range map[string]domain.RoleAssignment{
		"the first assignment": first, "the role in another product": elsewhere,
		"the subject under another issuer": namesake, "the assignment of somebody else": somebodyElse,
	} {
		if got, found, err := f.roles.Find(t.Context(), kept.ProductID, kept.ID); err != nil || !found || got != kept {
			t.Errorf("%s is %+v (found %t, %v), want it as it was stored", name, got, found, err)
		}
	}
	if owners := f.owners(t, cargopai.ID); owners != 3 {
		t.Errorf("CargoPai has %d owners, want the first and the two that differ from it", owners)
	}
}

func TestTwoGrantsOfOneRoleInAProductAtOnceLeaveOneAssignment(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	const racers = 6
	results := make(chan error, racers)
	for range racers {
		candidate := owning(t, cargopai, alice)
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.roles.Add(ctx, candidate)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrRoleAssignmentExists):
			t.Errorf("a grant failed with %v, want it to win or to find the role held", err)
		}
	}
	if owners := f.owners(t, cargopai.ID); won != 1 || owners != 1 {
		t.Errorf("%d grants of one role succeeded and CargoPai has %d owners, want 1 and 1", won, owners)
	}
}

// A revoked assignment grants nothing: no read finds it. Its row is kept,
// with who revoked it and when (F03 §13), and the role can be granted again
// as a new assignment.
func TestARevokedRoleAssignmentGrantsNothingAndIsKept(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	first, second := owning(t, cargopai, alice), owning(t, cargopai, bob)
	f.assign(t, first, second)

	revoked := disowned(t, first, bob)
	f.within(t, func(ctx context.Context) error {
		if err := f.roles.Revoke(ctx, revoked); err != nil {
			return err
		}
		// Inside the transaction that revoked it, it is gone already.
		if _, found, err := f.roles.Find(ctx, cargopai.ID, first.ID); err != nil || found {
			t.Errorf("inside the transaction that revoked it: found %t, %v", found, err)
		}
		if roles, err := f.roles.RolesOf(ctx, cargopai.ID, alice); err != nil || len(roles) != 0 {
			t.Errorf("inside the transaction that revoked it alice holds %v (%v)", roles, err)
		}
		return nil
	})

	if _, found, err := f.roles.Find(t.Context(), cargopai.ID, first.ID); err != nil || found {
		t.Errorf("the revoked assignment: found %t, %v", found, err)
	}
	if roles := f.rolesOf(t, cargopai.ID, alice); len(roles) != 0 {
		t.Errorf("alice holds %v, want nothing once her role is revoked", roles)
	}
	// What was not revoked is still held, as it was.
	if got, found, err := f.roles.Find(t.Context(), cargopai.ID, second.ID); err != nil || !found || got != second {
		t.Errorf("the assignment that was not revoked is %+v (found %t, %v)", got, found, err)
	}
	if roles, owners := f.rolesOf(t, cargopai.ID, bob), f.owners(t, cargopai.ID); !slices.Equal(roles, ownerRoles) || owners != 1 {
		t.Errorf("bob holds %v and CargoPai has %d owners, want %v and 1", roles, owners, ownerRoles)
	}

	kept := f.roleRecord(t, first.ID)
	if kept.Version != 2 || kept.GrantedBy != admin.Subject || !kept.Revoked || kept.RevokedBy != bob.Subject || !kept.RevokedAt.Equal(revoked.RevokedAt) {
		t.Errorf("the record is %+v, want version 2, granted by the administrator, revoked by bob at %s", kept, revoked.RevokedAt)
	}

	// The role is granted again as a new assignment. The record of the
	// first stays.
	again := owning(t, cargopai, alice)
	f.assign(t, again)
	if got, found, err := f.roles.Find(t.Context(), cargopai.ID, again.ID); err != nil || !found || got != again {
		t.Errorf("the new assignment is %+v (found %t, %v)", got, found, err)
	}
	if roles, owners := f.rolesOf(t, cargopai.ID, alice), f.owners(t, cargopai.ID); !slices.Equal(roles, ownerRoles) || owners != 2 {
		t.Errorf("alice holds %v and CargoPai has %d owners, want %v and 2", roles, owners, ownerRoles)
	}
	if kept := f.roleRecord(t, first.ID); !kept.Revoked || kept.Version != 2 {
		t.Errorf("the record of the first assignment is now %+v, want it as the revocation left it", kept)
	}
}

// A role assignment is revoked once. A revocation that finds nothing in
// force changes nothing and does not fail its transaction.
func TestARoleAssignmentIsRevokedOnce(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	first, inForce := owning(t, cargopai, alice), owning(t, cargopai, bob)
	f.assign(t, first, inForce)
	f.unassign(t, disowned(t, first, bob))

	// A revocation names the assignment with its product. Under another
	// product there is nothing to revoke.
	elsewhere := disowned(t, inForce, admin)
	elsewhere.ProductID = parcel.ID

	later := owning(t, cargopai, mallory)
	f.within(t, func(ctx context.Context) error {
		for name, revoked := range map[string]domain.RoleAssignment{
			"a second revocation":                 disowned(t, first, admin),
			"an assignment nobody stored":         disowned(t, owning(t, cargopai, alice), admin),
			"an assignment under another product": elsewhere,
		} {
			if err := f.roles.Revoke(ctx, revoked); !errors.Is(err, ports.ErrRoleAssignmentGone) {
				t.Errorf("%s: got %v, want %v", name, err, ports.ErrRoleAssignmentGone)
			}
		}
		// The same transaction still writes.
		return f.roles.Add(ctx, later)
	})

	// Who revoked it first is who revoked it.
	if kept := f.roleRecord(t, first.ID); kept.Version != 2 || kept.RevokedBy != bob.Subject {
		t.Errorf("the record is %+v, want it as the first revocation left it", kept)
	}
	for name, kept := range map[string]domain.RoleAssignment{"the assignment in force": inForce, "what the transaction wrote after the refusals": later} {
		if got, found, err := f.roles.Find(t.Context(), cargopai.ID, kept.ID); err != nil || !found || got != kept {
			t.Errorf("%s is %+v (found %t, %v), want it in force as it was stored", name, got, found, err)
		}
	}
}

func TestTwoRevocationsOfOneRoleAssignmentAtOnceLeaveOneRevocation(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	granted := owning(t, cargopai, alice)
	f.assign(t, granted, owning(t, cargopai, bob))

	const racers = 6
	results := make(chan error, racers)
	for index := range racers {
		revoked := disowned(t, granted, domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("owner-%d", index)})
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				return f.roles.Revoke(ctx, revoked)
			})
		}()
	}
	won := 0
	for range racers {
		switch err := <-results; {
		case err == nil:
			won++
		case !errors.Is(err, ports.ErrRoleAssignmentGone):
			t.Errorf("a revocation failed with %v, want it to win or to find the assignment gone", err)
		}
	}
	if kept := f.roleRecord(t, granted.ID); won != 1 || kept.Version != 2 || !strings.HasPrefix(kept.RevokedBy, "owner-") {
		t.Errorf("%d revocations succeeded and the record is %+v, want 1 and one revocation", won, kept)
	}
}

// The owners of a product are the assignments in force that make somebody an
// owner of it: not one that was revoked, and not an owner of another product.
// A transaction counts what it changed itself.
func TestTheOwnersOfAProductAreCountedWhereTheyAreLocked(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel, orphan := product(t, "cargopai"), product(t, "parcel"), product(t, "orphan")
	f.add(t, cargopai)
	f.add(t, parcel)
	f.add(t, orphan)
	first, second, gone := owning(t, cargopai, alice), owning(t, cargopai, bob), owning(t, cargopai, mallory)
	f.assign(t, first, second, gone, owning(t, parcel, alice))
	f.unassign(t, disowned(t, gone, alice))

	for name, c := range map[string]struct {
		productID domain.ProductID
		want      int
	}{
		"a product with two owners and one that was revoked": {cargopai.ID, 2},
		"a product with one owner":                           {parcel.ID, 1},
		"a product nobody owns":                              {orphan.ID, 0},
		"a product nobody stored":                            {domain.ProductID(ids.NewUUID()), 0},
	} {
		if got := f.owners(t, c.productID); got != c.want {
			t.Errorf("%s: %d owners, want %d", name, got, c.want)
		}
	}

	f.within(t, func(ctx context.Context) error {
		if err := f.roles.Revoke(ctx, disowned(t, second, alice)); err != nil {
			return err
		}
		owners, err := f.roles.LockOwners(ctx, cargopai.ID)
		if err == nil && owners != 1 {
			t.Errorf("after it revoked one of two owners the transaction counts %d, want 1", owners)
		}
		return err
	})
}

// A revocation counts the owners where no other revocation can end one of
// them. Whoever locks them second waits for the first, and then counts what
// the first left.
func TestWhoeverLocksTheOwnersSecondWaitsAndCountsWhatTheFirstLeft(t *testing.T) {
	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	first, second := owning(t, cargopai, alice), owning(t, cargopai, bob)
	f.assign(t, first, second, owning(t, parcel, alice))
	revoked := disowned(t, first, bob)

	type counted struct {
		owners int
		err    error
	}
	// The first transaction locks the owners, says how many it counted and
	// holds them until it is told to go on. Then it revokes one of them.
	locked, proceed, finished := make(chan counted, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	release := func() { once.Do(func() { close(proceed) }) }
	defer release()
	go func() {
		finished <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			owners, err := f.roles.LockOwners(ctx, cargopai.ID)
			locked <- counted{owners, err}
			if err != nil {
				return err
			}
			<-proceed
			return f.roles.Revoke(ctx, revoked)
		})
	}()
	if held := <-locked; held.err != nil || held.owners != 2 {
		t.Fatalf("the first transaction counted %d owners (%v), want 2", held.owners, held.err)
	}

	// The owners of another product are not held by it.
	if owners := f.owners(t, parcel.ID); owners != 1 {
		t.Errorf("Parcel has %d owners, want 1", owners)
	}

	waited := make(chan counted, 1)
	go func() {
		var owners int
		err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) (err error) {
			owners, err = f.roles.LockOwners(ctx, cargopai.ID)
			return err
		})
		waited <- counted{owners, err}
	}()
	f.waiting(t, 1)
	select {
	case early := <-waited:
		t.Fatalf("the second transaction counted %d owners (%v) while the first held them, want it to wait", early.owners, early.err)
	default:
	}
	// A read is not a revocation, and waits for nobody.
	if roles := f.rolesOf(t, cargopai.ID, alice); !slices.Equal(roles, ownerRoles) {
		t.Errorf("while her role is locked alice holds %v, want %v", roles, ownerRoles)
	}

	release()
	if err := <-finished; err != nil {
		t.Fatalf("the first transaction: %v", err)
	}
	if after := <-waited; after.err != nil || after.owners != 1 {
		t.Errorf("once the first had revoked an owner the second counted %d owners (%v), want the 1 that is left", after.owners, after.err)
	}
}

// Two owners who revoke each other at the same time, each locking and
// counting the owners first as a revocation does, leave one of them: the
// second counts what the first left and finds the last owner.
func TestOwnersWhoRevokeEachOtherAtOnceLeaveOneOwner(t *testing.T) {
	f := newFixture(t)
	at := created.Add(time.Hour)

	for round := range 8 {
		cargopai := product(t, fmt.Sprintf("cargopai-%d", round))
		f.add(t, cargopai)
		first, second := owning(t, cargopai, alice), owning(t, cargopai, bob)
		f.assign(t, first, second)

		revoke := func(target domain.RoleAssignment, by domain.Principal) error {
			return f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				owners, err := f.roles.LockOwners(ctx, cargopai.ID)
				if err != nil {
					return err
				}
				inForce, found, err := f.roles.Find(ctx, cargopai.ID, target.ID)
				if err != nil {
					return err
				}
				if !found {
					return ports.ErrRoleAssignmentGone
				}
				revoked, err := inForce.Revoke(by, at, owners)
				if err != nil {
					return err
				}
				return f.roles.Revoke(ctx, revoked)
			})
		}
		results := make(chan error, 2)
		go func() { results <- revoke(second, alice) }()
		go func() { results <- revoke(first, bob) }()
		won := 0
		for range 2 {
			switch err := <-results; {
			case err == nil:
				won++
			case !errors.Is(err, domain.ErrLastOwner):
				t.Errorf("round %d: a revocation failed with %v, want it to win or to find the last owner", round+1, err)
			}
		}
		if owners := f.owners(t, cargopai.ID); won != 1 || owners != 1 {
			t.Errorf("round %d: %d revocations succeeded and %d owners are left, want 1 and 1", round+1, won, owners)
		}
	}
}

func TestEndingARoleAssignmentNeedsATransaction(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	first, second := owning(t, cargopai, alice), owning(t, cargopai, bob)
	f.assign(t, first, second)

	if err := f.roles.Revoke(t.Context(), disowned(t, first, bob)); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("a revocation outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	// A lock no transaction holds would be given back at once: whatever was
	// counted under it could change before anything was decided.
	if owners, err := f.roles.LockOwners(t.Context(), cargopai.ID); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("the owners locked outside a transaction: got %d and %v, want %v", owners, err, bentopostgres.ErrTransactionRequired)
	}
	if _, found, _ := f.roles.Find(t.Context(), cargopai.ID, first.ID); !found {
		t.Error("a role assignment was revoked outside a transaction")
	}
}

// A role assignment is added in force and revoked afterwards. The store
// refuses one that is in the other state before it sends anything.
func TestTheStoreTakesARoleAssignmentInTheStateItsStatementIsFor(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)
	inForce := owning(t, cargopai, bob)
	f.assign(t, inForce)
	never := owning(t, cargopai, alice)

	f.within(t, func(ctx context.Context) error {
		if err := f.roles.Add(ctx, disowned(t, never, bob)); err == nil || errors.Is(err, ports.ErrRoleAssignmentExists) {
			t.Errorf("an assignment added revoked: got %v, want it refused for what it is", err)
		}
		if err := f.roles.Revoke(ctx, inForce); err == nil || errors.Is(err, ports.ErrRoleAssignmentGone) {
			t.Errorf("a revocation that says nothing of when: got %v, want it refused for what it is", err)
		}
		return nil
	})
	if roles := f.rolesOf(t, cargopai.ID, alice); len(roles) != 0 {
		t.Errorf("an assignment that was added revoked is held: %v", roles)
	}
	if kept := f.roleRecord(t, inForce.ID); kept.Revoked || kept.Version != 1 {
		t.Errorf("the record is %+v, want it in force at version 1", kept)
	}
}

// The build before this one writes the first owners of a product without an
// identifier and without a version, and may still run while this one is
// rolled out. What it writes is an assignment in force like any other: the
// table gives it an identifier of its own and its first version.
func TestARowWrittenAsTheBuildBeforeWroteItIsAnAssignmentInForce(t *testing.T) {
	f := newFixture(t)
	cargopai := product(t, "cargopai")
	f.add(t, cargopai)

	// The statement of that build, as it is in its store.
	const before = `
		INSERT INTO productgovernance.product_role_assignment
		    (product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	write := func(principal domain.Principal) error {
		_, err := f.app.Exec(t.Context(), before, string(cargopai.ID), principal.Issuer, principal.Subject,
			string(domain.RoleProductOwner), cargopai.CreatedAt, admin.Issuer, admin.Subject)
		return err
	}
	for _, principal := range []domain.Principal{alice, bob} {
		if err := write(principal); err != nil {
			t.Fatalf("write the role of %s as the build before did: %v", principal.Subject, err)
		}
	}

	written := map[string]domain.RoleAssignment{}
	for _, principal := range []domain.Principal{alice, bob} {
		var id string
		if err := f.app.QueryRow(t.Context(),
			`SELECT role_assignment_id::text FROM productgovernance.product_role_assignment
			 WHERE product_id = $1 AND principal_subject = $2`, string(cargopai.ID), principal.Subject).Scan(&id); err != nil {
			t.Fatalf("read the identifier of the role of %s: %v", principal.Subject, err)
		}
		if _, err := domain.ParseRoleAssignmentID(id); err != nil {
			t.Errorf("the role of %s has the identifier %q: %v", principal.Subject, id, err)
		}
		got, found, err := f.roles.Find(t.Context(), cargopai.ID, domain.RoleAssignmentID(id))
		want := domain.RoleAssignment{
			ID: domain.RoleAssignmentID(id), ProductID: cargopai.ID, Principal: principal, Role: domain.RoleProductOwner,
			Version: 1, GrantedAt: cargopai.CreatedAt, GrantedBy: admin,
		}
		if err != nil || !found || got != want {
			t.Errorf("the role of %s is %+v (found %t, %v)\nwant\n %+v", principal.Subject, got, found, err, want)
		}
		written[id] = got
	}
	if len(written) != 2 || f.owners(t, cargopai.ID) != 2 {
		t.Errorf("the two rows are %d assignments and CargoPai has %d owners, want 2 and 2", len(written), f.owners(t, cargopai.ID))
	}

	// Such a row is revoked like any other, and that build cannot make
	// somebody an owner twice either.
	if err := write(alice); sqlState(err) != "23505" {
		t.Errorf("the role of alice written again: got %v, want a unique violation", err)
	}
	for _, assignment := range written {
		if assignment.Principal == alice {
			f.unassign(t, disowned(t, assignment, bob))
		}
	}
	if roles, owners := f.rolesOf(t, cargopai.ID, alice), f.owners(t, cargopai.ID); len(roles) != 0 || owners != 1 {
		t.Errorf("once her role is revoked alice holds %v and CargoPai has %d owners, want nothing and 1", roles, owners)
	}
}

// The schema refuses what the model never produces, whoever writes it. The
// application role holds UPDATE on the table, to end an assignment, and still
// changes nothing else of one.
func TestTheSchemaHoldsTheLineOfARoleAssignment(t *testing.T) {
	const (
		foreignKeyViolation = "23503"
		uniqueViolation     = "23505"
		checkViolation      = "23514"
		// What the trigger of the table raises.
		integrityViolation    = "23000"
		insufficientPrivilege = "42501"
	)

	f := newFixture(t)
	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	f.add(t, cargopai)
	f.add(t, parcel)
	held, gone := owning(t, cargopai, alice), owning(t, cargopai, mallory)
	f.assign(t, held, gone, owning(t, cargopai, bob))
	f.unassign(t, disowned(t, gone, alice))

	// The store, handed what the model never produces.
	orphan := owning(t, cargopai, bob)
	orphan.ProductID = domain.ProductID(ids.NewUUID())
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.roles.Add(ctx, orphan) })
	if sqlState(err) != foreignKeyViolation {
		t.Errorf("an owner of a product nobody stored: got %v, want a foreign key violation", err)
	}
	twin := owning(t, parcel, alice)
	twin.ID = held.ID
	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error { return f.roles.Add(ctx, twin) })
	if sqlState(err) != uniqueViolation {
		t.Errorf("an assignment under the identifier of another: got %v, want a unique violation", err)
	}

	// The application role, writing by hand.
	const update = `UPDATE productgovernance.product_role_assignment SET `
	const revocation = `revoked_at = granted_at + interval '1 hour', revoked_by_issuer = 'https://issuer.example', revoked_by_subject = 'mallory'`
	statements := []struct {
		name      string
		statement string
		id        domain.RoleAssignmentID
		state     string
	}{
		{"make somebody else the owner", update + `principal_subject = 'mallory' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"move it to another issuer", update + `principal_issuer = 'https://other.example' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"move it to another product", update + `product_id = '` + string(parcel.ID) + `' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"change its role", update + `role = 'platform_admin' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"change who granted it", update + `granted_by_subject = 'mallory' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"change when it was granted", update + `granted_at = granted_at - interval '1 year' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"change its identifier", update + `role_assignment_id = gen_random_uuid() WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"change nothing at all", update + `role = role WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"count a change that did not happen", update + `role_assignment_version = 2 WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"end it without counting the change", update + revocation + ` WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"end it and make somebody else the owner", update + revocation + `, role_assignment_version = 2, principal_subject = 'mallory' WHERE role_assignment_id = $1`, held.ID, integrityViolation},
		{"end it without saying who did", update + `revoked_at = granted_at + interval '1 hour', role_assignment_version = 2 WHERE role_assignment_id = $1`, held.ID, checkViolation},
		{"end it before it began", update + `revoked_at = granted_at - interval '1 second', revoked_by_issuer = 'i', revoked_by_subject = 's', role_assignment_version = 2 WHERE role_assignment_id = $1`,
			held.ID, checkViolation},
		{"bring a revoked one back", update + `revoked_at = NULL, revoked_by_issuer = NULL, revoked_by_subject = NULL, role_assignment_version = 3 WHERE role_assignment_id = $1`,
			gone.ID, integrityViolation},
		{"revoke a revoked one again", update + `revoked_by_subject = 'bob', role_assignment_version = 3 WHERE role_assignment_id = $1`, gone.ID, integrityViolation},
		{"hold the role twice at a time", `
			INSERT INTO productgovernance.product_role_assignment
			    (role_assignment_id, product_id, principal_issuer, principal_subject, role,
			     role_assignment_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, principal_issuer, principal_subject, role,
			       1, granted_at, granted_by_issuer, granted_by_subject
			FROM productgovernance.product_role_assignment WHERE role_assignment_id = $1`, held.ID, uniqueViolation},
		{"begin at a version that is none", `
			INSERT INTO productgovernance.product_role_assignment
			    (role_assignment_id, product_id, principal_issuer, principal_subject, role,
			     role_assignment_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, principal_issuer, 'somebody-else', role,
			       0, granted_at, granted_by_issuer, granted_by_subject
			FROM productgovernance.product_role_assignment WHERE role_assignment_id = $1`, held.ID, checkViolation},
		{"remove it", `DELETE FROM productgovernance.product_role_assignment WHERE role_assignment_id = $1`, held.ID, insufficientPrivilege},
		{"remove a revoked one", `DELETE FROM productgovernance.product_role_assignment WHERE role_assignment_id = $1`, gone.ID, insufficientPrivilege},
	}
	for _, c := range statements {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.app.Exec(t.Context(), c.statement, string(c.id)); sqlState(err) != c.state {
				t.Errorf("%s\n got %v, want it refused with SQLSTATE %s", c.statement, err, c.state)
			}
		})
	}
	for _, statement := range []string{
		`TRUNCATE productgovernance.product_role_assignment`,
		`ALTER TABLE productgovernance.product_role_assignment DISABLE TRIGGER product_role_assignment_only_ends`,
		`DROP TRIGGER product_role_assignment_only_ends ON productgovernance.product_role_assignment`,
		`DROP INDEX productgovernance.product_role_assignment_in_force_unique`,
		`CREATE OR REPLACE FUNCTION productgovernance.product_role_assignment_only_ends() RETURNS trigger
		     LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END'`,
	} {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != insufficientPrivilege {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Nothing of it took effect.
	if got, found, err := f.roles.Find(t.Context(), cargopai.ID, held.ID); err != nil || !found || got != held {
		t.Errorf("the assignment is now %+v (found %t, %v), want it untouched", got, found, err)
	}
	if kept := f.roleRecord(t, gone.ID); !kept.Revoked || kept.Version != 2 || kept.RevokedBy != alice.Subject {
		t.Errorf("the record of the revoked assignment is now %+v, want it as the revocation left it", kept)
	}
	if roles, owners := f.rolesOf(t, cargopai.ID, mallory), f.owners(t, cargopai.ID); len(roles) != 0 || owners != 2 {
		t.Errorf("mallory holds %v and CargoPai has %d owners, want nothing and 2", roles, owners)
	}
}
