package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// What a caller is to a workspace, for the reading of the reviews in it, is
// five facts: whether the product is there, whether the caller may know it,
// whether it may know the workspace, whether the workspace is there and
// whether it holds a grant that reads in it. The grant is the author, the
// reviewer or the publisher grant of that workspace, and no other: owning the
// product, maintaining the workspace and administering the platform see, or
// know, and read nothing. What is held in one workspace reads nothing in
// another, and a grant that was revoked grants nothing.
func TestWhatACallerIsToAWorkspaceForTheReadingOfItsReviews(t *testing.T) {
	s := newTeam(t)
	author := domain.Principal{Issuer: trustedIssuer, Subject: "queue-author"}
	reviewer := domain.Principal{Issuer: trustedIssuer, Subject: "queue-reviewer"}
	publisher := domain.Principal{Issuer: trustedIssuer, Subject: "queue-publisher"}
	several := domain.Principal{Issuer: trustedIssuer, Subject: "queue-several"}
	coowner := domain.Principal{Issuer: trustedIssuer, Subject: "queue-coowner"}
	joined(t, s.service, s.engineering, alice, author, domain.RoleInternalAuthor)
	joined(t, s.service, s.engineering, alice, reviewer, domain.RoleInternalReviewer)
	joined(t, s.service, s.engineering, alice, publisher, domain.RoleInternalPublisher)
	joined(t, s.service, s.engineering, alice, several, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, s.engineering, alice, several, domain.RoleInternalPublisher)
	named(t, s.service, s.cargopai, alice, coowner)
	joined(t, s.service, s.engineering, alice, coowner, domain.RoleInternalAuthor)
	if _, err := s.service.GrantProductRole(ctx, GrantProductRole{
		Caller: alice, CorrelationID: "correlation-a", ProductID: string(s.cargopai.ID), Principal: grace, Role: string(domain.RoleSecurityAdmin),
	}); err != nil {
		t.Fatalf("make grace a security administrator: %v", err)
	}
	s.w.quiet()

	reads := ReviewScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	sees := ReviewScope{Found: true, Known: true, Sees: true, WorkspaceFound: true}
	knows := ReviewScope{Found: true, Known: true, WorkspaceFound: true}
	// Whoever holds nothing is a caller the use case tells nothing of the
	// workspace, and what is read of it is read all the same.
	nothing := ReviewScope{Found: true, WorkspaceFound: true}
	absent := func(scope ReviewScope) ReviewScope { scope.WorkspaceFound, scope.Reads = false, false; return scope }
	cargopai, engineering, operations := s.cargopai.ID, s.engineering.ID, s.operations.ID

	for name, c := range map[string]struct {
		caller    domain.Principal
		product   domain.ProductID
		workspace domain.WorkspaceID
		want      ReviewScope
	}{
		"an author of the workspace":                       {author, cargopai, engineering, reads},
		"a reviewer of the workspace":                      {reviewer, cargopai, engineering, reads},
		"a publisher of the workspace":                     {publisher, cargopai, engineering, reads},
		"a maintainer that also publishes":                 {several, cargopai, engineering, reads},
		"an owner of the product that is also an author":   {coowner, cargopai, engineering, reads},
		"the author of the other workspace":                {dave, cargopai, operations, reads},
		"the maintainer of the workspace":                  {carol, cargopai, engineering, reads},
		"the owner of the product":                         {alice, cargopai, engineering, reads},
		"the owner, in the other workspace":                {alice, cargopai, operations, reads},
		"an author of the other workspace":                 {dave, cargopai, engineering, knows},
		"a maintainer of the other workspace":              {carol, cargopai, operations, knows},
		"the administrator of the platform":                {admin, cargopai, engineering, knows},
		"a holder of a role of the product":                {grace, cargopai, engineering, knows},
		"whoever was a member and is not":                  {erin, cargopai, engineering, nothing},
		"a stranger":                                       {mallory, cargopai, engineering, nothing},
		"the owner of another product":                     {bob, cargopai, engineering, nothing},
		"an author of this workspace, in another product":  {author, s.parcel.ID, engineering, absent(nothing)},
		"the owner, asking for a workspace of no such id":  {alice, cargopai, domain.WorkspaceID(missingID), absent(sees)},
		"a maintainer, asking for a workspace of no id":    {carol, cargopai, domain.WorkspaceID(missingID), absent(knows)},
		"a stranger, asking for a workspace of no such id": {mallory, cargopai, domain.WorkspaceID(missingID), absent(nothing)},
		"the owner, asking for a workspace of another product": {
			alice, cargopai, s.sorting.ID, absent(sees)},
		"an author of the other workspace, asking for one of another product": {
			dave, cargopai, s.sorting.ID, absent(knows)},
		"the owner of the other product, asking for its workspace here": {
			bob, cargopai, s.sorting.ID, absent(nothing)},
		"the owner, asking for what is not an identifier":         {alice, cargopai, "not-a-workspace", absent(sees)},
		"the owner, asking for no workspace":                      {alice, cargopai, "", absent(sees)},
		"an author, asking for what is not an identifier":         {author, cargopai, "not-a-workspace", absent(knows)},
		"an author of the product, asking for no workspace":       {dave, cargopai, "", absent(knows)},
		"the owner of the other product, in its own workspace":    {bob, s.parcel.ID, s.sorting.ID, reads},
		"an author of this workspace, in the workspace of Parcel": {author, s.parcel.ID, s.sorting.ID, nothing},
	} {
		got, err := s.service.ReadReviewScope(ctx, c.caller, c.product, c.workspace)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: read as %+v, want %+v", name, got, c.want)
		}
		// What reads is what reads a revision of the workspace, alone: the one
		// rule of who may read, in one place.
		alone, err := s.service.RevisionReader(ctx, c.caller, c.product, c.workspace)
		if err != nil || alone != got.Reads {
			t.Errorf("%s: the scope says it reads: %t, the read of a revision says %t (%v)", name, got.Reads, alone, err)
		}
	}

	// A product that is not there is not there for anyone, an administrator
	// included: no facts, and no failure, which the caller reads as the
	// product not being known.
	for _, caller := range []domain.Principal{alice, admin, dave, author} {
		if got, err := s.service.ReadReviewScope(ctx, caller, missingID, s.engineering.ID); err != nil || got != (ReviewScope{}) {
			t.Errorf("a product that is not there is read as %+v (%v) by %s, want no facts and no failure", got, err, caller.Subject)
		}
	}

	// A read takes no lock, opens no transaction and leaves nothing behind.
	s.w.quiet()
	if _, err := s.service.ReadReviewScope(ctx, author, cargopai, engineering); err != nil {
		t.Fatal(err)
	}
	if s.w.transactions != 0 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("the read opened %d transactions and left the audit entries %+v and the events %v", s.w.transactions, s.w.audited, s.w.events)
	}
}

// A grant that is revoked reads nothing from then on, and a reviewer that was
// a stranger to the workspace before it was granted is one again: the scope
// is read from what the caller holds then.
func TestARevokedGrantReadsTheReviewsOfAWorkspaceNoMore(t *testing.T) {
	s := newTeam(t)
	leaver := domain.Principal{Issuer: trustedIssuer, Subject: "queue-leaver"}
	before, err := s.service.ReadReviewScope(ctx, leaver, s.cargopai.ID, s.engineering.ID)
	if err != nil || before != (ReviewScope{Found: true, WorkspaceFound: true}) {
		t.Fatalf("before it was granted, the reviewer reads the scope as %+v (%v), want a stranger", before, err)
	}
	membership := joined(t, s.service, s.engineering, alice, leaver, domain.RoleInternalReviewer)
	if got, err := s.service.ReadReviewScope(ctx, leaver, s.cargopai.ID, s.engineering.ID); err != nil ||
		got != (ReviewScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}) {
		t.Fatalf("the reviewer reads the scope as %+v (%v), want one that reads", got, err)
	}
	if err := s.revoke(alice, s.engineering, membership.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.service.ReadReviewScope(ctx, leaver, s.cargopai.ID, s.engineering.ID); err != nil || got != before {
		t.Errorf("once revoked the reviewer reads the scope as %+v (%v), want a stranger again", got, err)
	}
}

// A request that names no principal, or names a product by something that is
// no identifier, is answered before the store is asked: the first is refused,
// since there is nobody to name in the record of a refusal and nobody to read
// for, and the second is a product that is not there. Everything the store
// holds is made to fail here, so that what is answered is the answer of the
// check and not of what was read.
func TestTheScopeOfAWorkspaceIsNotLookedForOfNobodyOrOfAProductThatIsNone(t *testing.T) {
	s := newTeam(t)
	gone := errors.New("the store is gone")
	for _, step := range []string{"read", "roles", "held"} {
		s.w.failing[step] = gone
	}
	s.w.quiet()

	for name, c := range map[string]struct {
		caller  domain.Principal
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
		got, err := s.service.ReadReviewScope(ctx, c.caller, c.product, s.engineering.ID)
		if !errors.Is(err, c.want) || errors.Is(err, gone) || got != (ReviewScope{}) {
			t.Errorf("%s: read as %+v and %v, want no facts and %v", name, got, err, c.want)
		}
	}
	if s.w.transactions != 0 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("the refused reads opened %d transactions and left the audit entries %+v and the events %v", s.w.transactions, s.w.audited, s.w.events)
	}

	// Whatever the store says when it is asked is what the read says,
	// whichever of the three reads of it failed.
	for _, step := range []string{"read", "roles", "held"} {
		s.w.failing = map[string]error{step: gone}
		if got, err := s.service.ReadReviewScope(ctx, dave, s.cargopai.ID, s.operations.ID); !errors.Is(err, gone) || got != (ReviewScope{}) {
			t.Errorf("a store that fails to %s: read as %+v and %v, want no facts and %v", step, got, err, gone)
		}
	}
}

// askedWorkspaces keeps the identifiers the store of workspaces was asked
// for. A store in a database reads them as UUIDs, so what is no UUID is not
// answered with a workspace that is not there but with a failure.
type askedWorkspaces struct {
	ports.Workspaces
	asked []domain.WorkspaceID
}

func (w *askedWorkspaces) Find(ctx context.Context, product domain.ProductID, id domain.WorkspaceID) (domain.Workspace, bool, error) {
	w.asked = append(w.asked, id)
	return w.Workspaces.Find(ctx, product, id)
}

// A workspace that is no identifier is a workspace that is not there, and it
// is not looked for: the store is asked about the identifiers it can read,
// and about the workspace that is named when it is one.
func TestAWorkspaceThatIsNoIdentifierIsNotLookedFor(t *testing.T) {
	s := newTeam(t)
	store := &askedWorkspaces{Workspaces: s.service.deps.Workspaces}
	s.service.deps.Workspaces = store
	engineering := string(s.engineering.ID)

	for name, workspace := range map[string]string{
		"nothing":                         "",
		"a word":                          "latest",
		"a key and not an identifier":     "engineering",
		"a digit too many":                engineering + "0",
		"a space before it":               " " + engineering,
		"a line feed after it":            engineering + "\n",
		"an identifier without hyphens":   strings.ReplaceAll(engineering, "-", ""),
		"an identifier in braces":         "{" + engineering + "}",
		"an identifier that is not a hex": strings.Replace(engineering, "0", "g", 1),
	} {
		got, err := s.service.ReadReviewScope(ctx, alice, s.cargopai.ID, domain.WorkspaceID(workspace))
		if err != nil || got != (ReviewScope{Found: true, Known: true, Sees: true}) {
			t.Errorf("%s: read as %+v (%v), want an owner of a product that has no such workspace", name, got, err)
		}
	}
	if len(store.asked) != 0 {
		t.Errorf("the store of workspaces was asked for %q, which are no identifiers", store.asked)
	}

	// The workspace that is one is looked for, once.
	if got, err := s.service.ReadReviewScope(ctx, alice, s.cargopai.ID, s.engineering.ID); err != nil || !got.WorkspaceFound ||
		!slices.Equal(store.asked, []domain.WorkspaceID{s.engineering.ID}) {
		t.Errorf("a workspace that is an identifier: read as %+v (%v) after the store was asked for %q, want it found by asking once for it",
			got, err, store.asked)
	}
}
