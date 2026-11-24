package application

import (
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// Where a principal reads the contract revisions of a product is where it
// holds the author, the reviewer or the publisher grant now, and the catalog
// scope says so beside the Workspaces it is a member of: each Workspace once,
// in order, however many of the grants it holds there, and always among those
// it is a member of. Maintaining a Workspace, owning the product and
// administering the platform are not grants that read them. A grant that was
// revoked reads nothing from then on, and what is held in one product reads
// nothing in another.
func TestTheCatalogScopeNamesTheWorkspacesWhereThePrincipalReadsContractRevisions(t *testing.T) {
	s := newTeam(t)
	reviewer := domain.Principal{Issuer: trustedIssuer, Subject: "reviewer"}
	publisher := domain.Principal{Issuer: trustedIssuer, Subject: "publisher"}
	several := domain.Principal{Issuer: trustedIssuer, Subject: "several"}
	both := domain.Principal{Issuer: trustedIssuer, Subject: "both"}
	joined(t, s.service, s.engineering, alice, reviewer, domain.RoleInternalReviewer)
	joined(t, s.service, s.operations, alice, publisher, domain.RoleInternalPublisher)
	// Three grants in two Workspaces: two of them in one.
	joined(t, s.service, s.operations, alice, several, domain.RoleInternalPublisher)
	joined(t, s.service, s.engineering, alice, several, domain.RoleInternalReviewer)
	joined(t, s.service, s.engineering, alice, several, domain.RoleInternalAuthor)
	// A maintainer of one Workspace, an author of the other.
	joined(t, s.service, s.engineering, alice, both, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, s.operations, alice, both, domain.RoleInternalAuthor)
	s.w.quiet()

	engineering, operations := s.engineering.ID, s.operations.ID
	bothWorkspaces := []domain.WorkspaceID{engineering, operations}
	slices.Sort(bothWorkspaces)
	cargopai := s.cargopai.ID

	for name, c := range map[string]struct {
		actor            domain.Principal
		product          domain.ProductID
		visible, readers []domain.WorkspaceID
		owner            bool
	}{
		"an author":                      {dave, cargopai, []domain.WorkspaceID{operations}, []domain.WorkspaceID{operations}, false},
		"a reviewer":                     {reviewer, cargopai, []domain.WorkspaceID{engineering}, []domain.WorkspaceID{engineering}, false},
		"a publisher":                    {publisher, cargopai, []domain.WorkspaceID{operations}, []domain.WorkspaceID{operations}, false},
		"three grants in two Workspaces": {several, cargopai, bothWorkspaces, bothWorkspaces, false},
		"a maintainer of one Workspace and an author of the other": {both, cargopai, bothWorkspaces, []domain.WorkspaceID{operations}, false},
		"the maintainer of a Workspace":                            {carol, cargopai, []domain.WorkspaceID{engineering}, nil, false},
		"the owner of the product":                                 {alice, cargopai, nil, nil, true},
		"the administrator of the platform":                        {admin, cargopai, nil, nil, false},
		"a stranger":                                               {mallory, cargopai, nil, nil, false},
		"the owner of another product":                             {bob, cargopai, nil, nil, false},
		"an author of another product":                             {dave, s.parcel.ID, nil, nil, false},
		"whoever was a member and is not":                          {erin, cargopai, nil, nil, false},
	} {
		got, err := s.service.ReadCatalogScope(ctx, c.actor, c.product)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.Equal(got.Workspaces, c.visible) || !slices.Equal(got.ContractReaders, c.readers) || got.Owner != c.owner {
			t.Errorf("%s is a member of %v and reads the contract revisions of %v, owner %t; want %v, %v and %t",
				name, got.Workspaces, got.ContractReaders, got.Owner, c.visible, c.readers, c.owner)
		}
		for _, workspace := range got.ContractReaders {
			if !slices.Contains(got.Workspaces, workspace) {
				t.Errorf("%s reads the contract revisions of %s without being a member of it", name, workspace)
			}
		}
	}

	// A product that is not there is not there for anyone, an administrator
	// included: no readers, and no failure.
	for _, actor := range []domain.Principal{alice, admin, dave} {
		if got, err := s.service.ReadCatalogScope(ctx, actor, missingID); err != nil || got.Found || len(got.ContractReaders) != 0 {
			t.Errorf("a product that is not there is read as %+v (%v) by %s, want none found and no readers", got, err, actor.Subject)
		}
	}

	// A grant that is revoked reads nothing from then on.
	leaver := domain.Principal{Issuer: trustedIssuer, Subject: "leaver"}
	membership := joined(t, s.service, s.engineering, alice, leaver, domain.RoleInternalReviewer)
	if got, err := s.service.ReadCatalogScope(ctx, leaver, cargopai); err != nil || !slices.Equal(got.ContractReaders, []domain.WorkspaceID{engineering}) {
		t.Fatalf("a reviewer reads the contract revisions of %v (%v), want those of engineering", got.ContractReaders, err)
	}
	if err := s.revoke(alice, s.engineering, membership.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.service.ReadCatalogScope(ctx, leaver, cargopai); err != nil || len(got.ContractReaders) != 0 || got.Known {
		t.Errorf("a reviewer whose grant was revoked reads %+v (%v), want nothing", got, err)
	}

	// A read takes no lock, opens no transaction and leaves nothing behind.
	s.w.quiet()
	if _, err := s.service.ReadCatalogScope(ctx, dave, cargopai); err != nil {
		t.Fatal(err)
	}
	if s.w.transactions != 0 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("the read opened %d transactions and left the audit entries %+v and the events %v", s.w.transactions, s.w.audited, s.w.events)
	}
}
