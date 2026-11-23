package application

import (
	"errors"
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// Where a principal reads the slots of a product is where it holds the
// author, the reviewer or the publisher grant now: each workspace once, in
// order, however many of the grants it holds there. What else it holds is no
// reading of a slot. Maintaining a workspace, owning the product and
// administering the platform are not, and the last two are only told that the
// product is there. Whoever holds nothing in the product, or held a grant that
// was revoked since, is not told even that; and what is held in one product
// reads nothing in another.
func TestWhereAPrincipalReadsSlotsIsWhereItHoldsAnAuthorReviewerOrPublisherGrant(t *testing.T) {
	s := newTeam(t)
	reviewer := domain.Principal{Issuer: trustedIssuer, Subject: "reviewer"}
	publisher := domain.Principal{Issuer: trustedIssuer, Subject: "publisher"}
	several := domain.Principal{Issuer: trustedIssuer, Subject: "several"}
	joined(t, s.service, s.engineering, alice, reviewer, domain.RoleInternalReviewer)
	joined(t, s.service, s.operations, alice, publisher, domain.RoleInternalPublisher)
	// Three grants in two workspaces: two of them in one.
	joined(t, s.service, s.operations, alice, several, domain.RoleInternalPublisher)
	joined(t, s.service, s.engineering, alice, several, domain.RoleInternalReviewer)
	joined(t, s.service, s.engineering, alice, several, domain.RoleInternalAuthor)
	s.w.quiet()

	// The workspaces come in the order of their identifiers.
	if s.engineering.ID >= s.operations.ID {
		t.Fatalf("the identifiers of the workspaces are %s and %s, want the first to come first", s.engineering.ID, s.operations.ID)
	}
	engineering, operations := []domain.WorkspaceID{s.engineering.ID}, []domain.WorkspaceID{s.operations.ID}
	cargopai := s.cargopai.ID

	for name, c := range map[string]struct {
		actor   domain.Principal
		product domain.ProductID
		want    ReleaseReaders
	}{
		"an author":                         {dave, cargopai, ReleaseReaders{Known: true, Workspaces: operations}},
		"a reviewer":                        {reviewer, cargopai, ReleaseReaders{Known: true, Workspaces: engineering}},
		"a publisher":                       {publisher, cargopai, ReleaseReaders{Known: true, Workspaces: operations}},
		"three grants in two workspaces":    {several, cargopai, ReleaseReaders{Known: true, Workspaces: []domain.WorkspaceID{s.engineering.ID, s.operations.ID}}},
		"the maintainer of a workspace":     {carol, cargopai, ReleaseReaders{Known: true}},
		"the owner of the product":          {alice, cargopai, ReleaseReaders{Known: true}},
		"the administrator of the platform": {admin, cargopai, ReleaseReaders{Known: true}},
		"a stranger":                        {mallory, cargopai, ReleaseReaders{}},
		"the owner of another product":      {bob, cargopai, ReleaseReaders{}},
		"an author of another product":      {dave, s.parcel.ID, ReleaseReaders{}},
		"whoever was a member and is not":   {erin, cargopai, ReleaseReaders{}},
	} {
		got, err := s.service.ReleaseReaderScope(ctx, c.actor, c.product)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.Known != c.want.Known || !slices.Equal(got.Workspaces, c.want.Workspaces) {
			t.Errorf("%s reads the slots of %v and is told the product is there: %t, want %v and %t", name, got.Workspaces, got.Known, c.want.Workspaces, c.want.Known)
		}
	}

	// A product that is not there is not there for anyone, an administrator
	// included: no readers, and no failure, which the caller reads as the
	// product not being known.
	for _, actor := range []domain.Principal{alice, admin, dave} {
		if got, err := s.service.ReleaseReaderScope(ctx, actor, missingID); err != nil || got.Known || len(got.Workspaces) != 0 {
			t.Errorf("a product that is not there is read as %+v (%v) by %s, want no readers and no failure", got, err, actor.Subject)
		}
	}

	// A grant that is revoked reads nothing from then on.
	leaver := domain.Principal{Issuer: trustedIssuer, Subject: "leaver"}
	membership := joined(t, s.service, s.engineering, alice, leaver, domain.RoleInternalReviewer)
	if got, err := s.service.ReleaseReaderScope(ctx, leaver, cargopai); err != nil || !slices.Equal(got.Workspaces, engineering) {
		t.Fatalf("a reviewer reads the slots of %v (%v), want those of engineering", got.Workspaces, err)
	}
	if err := s.revoke(alice, s.engineering, membership.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.service.ReleaseReaderScope(ctx, leaver, cargopai); err != nil || got.Known || len(got.Workspaces) != 0 {
		t.Errorf("a reviewer whose grant was revoked reads %+v (%v), want nothing", got, err)
	}

	// A read takes no lock, opens no transaction and leaves nothing behind.
	s.w.quiet()
	if _, err := s.service.ReleaseReaderScope(ctx, dave, cargopai); err != nil {
		t.Fatal(err)
	}
	if s.w.transactions != 0 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("the read opened %d transactions and left the audit entries %+v and the events %v", s.w.transactions, s.w.audited, s.w.events)
	}
}

// A request that names no principal, or names a product by something that is
// no identifier, is answered before the store is asked: the first is refused,
// since there is nobody to name in the record of a refusal and nobody to read
// for, and the second is a product that is not there. Everything the store
// holds is made to fail here, so that what is answered is the answer of the
// check and not of what was read.
func TestTheReadersOfASlotAreNotLookedForOfNobodyOrOfAProductThatIsNone(t *testing.T) {
	s := newTeam(t)
	gone := errors.New("the store is gone")
	for _, step := range []string{"read", "roles", "held"} {
		s.w.failing[step] = gone
	}
	s.w.quiet()

	for name, c := range map[string]struct {
		actor   domain.Principal
		product domain.ProductID
		want    error
	}{
		"no principal":                     {domain.Principal{}, s.cargopai.ID, ErrForbidden},
		"a principal without a subject":    {domain.Principal{Issuer: trustedIssuer}, s.cargopai.ID, ErrForbidden},
		"a principal without an issuer":    {domain.Principal{Subject: "alice"}, s.cargopai.ID, ErrForbidden},
		"no principal, no product":         {domain.Principal{}, "", ErrForbidden},
		"a key and not an identifier":      {alice, "cargopai", ErrProductNotFound},
		"words that are no identifier":     {alice, "not-an-identifier", ErrProductNotFound},
		"no product":                       {alice, "", ErrProductNotFound},
		"an administrator, and no product": {admin, "", ErrProductNotFound},
	} {
		got, err := s.service.ReleaseReaderScope(ctx, c.actor, c.product)
		if !errors.Is(err, c.want) || errors.Is(err, gone) || got.Known || len(got.Workspaces) != 0 {
			t.Errorf("%s: read as %+v and %v, want no readers and %v", name, got, err, c.want)
		}
	}
	if s.w.transactions != 0 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("the refused reads opened %d transactions and left the audit entries %+v and the events %v", s.w.transactions, s.w.audited, s.w.events)
	}

	// Whatever the store says when it is asked is what the read says,
	// whichever of the three reads of it failed.
	for _, step := range []string{"read", "roles", "held"} {
		s.w.failing = map[string]error{step: gone}
		if got, err := s.service.ReleaseReaderScope(ctx, dave, s.cargopai.ID); !errors.Is(err, gone) || got.Known || len(got.Workspaces) != 0 {
			t.Errorf("a store that fails to %s: read as %+v and %v, want no readers and %v", step, got, err, gone)
		}
	}
}
