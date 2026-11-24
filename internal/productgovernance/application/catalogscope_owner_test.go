package application

import (
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// Owning a product is no grant that reads the contract revisions of a
// workspace, and it does not become one for the workspaces the owner is also a
// member of: an owner reads the contract revisions of the workspaces where it
// holds the author, the reviewer or the publisher grant, as any member does,
// and of no other. One that maintains a workspace reads nothing there, one that
// maintains a workspace and writes in another reads the other only, and an owner
// that is a member of none reads none. In every case the workspaces it reads in
// are among those it is a member of.
func TestAnOwnerOfTheProductThatIsAlsoAMemberReadsContractRevisionsOnlyWhereItsGrantsSay(t *testing.T) {
	s := newTeam(t)
	maintaining := domain.Principal{Issuer: trustedIssuer, Subject: "owner-maintainer"}
	mixed := domain.Principal{Issuer: trustedIssuer, Subject: "owner-mixed"}
	writing := domain.Principal{Issuer: trustedIssuer, Subject: "owner-author"}
	for _, owner := range []domain.Principal{maintaining, mixed, writing} {
		named(t, s.service, s.cargopai, alice, owner)
	}
	// An owner that maintains operations; one that maintains engineering and
	// writes in operations; one that writes in engineering.
	joined(t, s.service, s.operations, alice, maintaining, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, s.engineering, alice, mixed, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, s.operations, alice, mixed, domain.RoleInternalAuthor)
	joined(t, s.service, s.engineering, alice, writing, domain.RoleInternalAuthor)
	s.w.quiet()

	engineering, operations := s.engineering.ID, s.operations.ID
	bothWorkspaces := []domain.WorkspaceID{engineering, operations}
	slices.Sort(bothWorkspaces)

	for name, c := range map[string]struct {
		actor            domain.Principal
		visible, readers []domain.WorkspaceID
	}{
		"an owner that maintains a workspace":                         {maintaining, []domain.WorkspaceID{operations}, nil},
		"an owner that maintains a workspace and writes in the other": {mixed, bothWorkspaces, []domain.WorkspaceID{operations}},
		"an owner that writes in a workspace":                         {writing, []domain.WorkspaceID{engineering}, []domain.WorkspaceID{engineering}},
		"the owner of the product that is a member of no workspace":   {alice, nil, nil},
	} {
		got, err := s.service.ReadCatalogScope(ctx, c.actor, s.cargopai.ID)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !got.Owner || !got.Found {
			t.Errorf("%s is read as %+v, want an owner of a product that is there", name, got)
		}
		if !slices.Equal(got.Workspaces, c.visible) || !slices.Equal(got.ContractReaders, c.readers) {
			t.Errorf("%s is a member of %v and reads the contract revisions of %v; want %v and %v",
				name, got.Workspaces, got.ContractReaders, c.visible, c.readers)
		}
		for _, workspace := range got.ContractReaders {
			if !slices.Contains(got.Workspaces, workspace) {
				t.Errorf("%s reads the contract revisions of %s without being a member of it", name, workspace)
			}
		}
	}
}
