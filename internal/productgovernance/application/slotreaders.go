package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// ReleaseReaders is where in a product a principal reads the slots of
// publication, as the grants it holds in the product say.
type ReleaseReaders struct {
	// Known is whether the principal may know that the product exists: it
	// holds something in it, or it administers the platform.
	Known bool
	// Owner may read release and slot evidence in every workspace of the product.
	Owner bool
	// Workspaces are the workspaces of the product where the principal holds
	// the author, the reviewer or the publisher grant now, in order. A
	// principal that owns the product holds none of them by owning it.
	Workspaces []domain.WorkspaceID
}

// ReleaseReaderScope reads where actor reads the slots of the product: the
// workspaces whose releases ReleaseMembership says it reads, all at once. It
// is the question that function answers of one workspace, asked of every
// workspace of the product, for a list of slots. A read takes no lock: the
// grants are read again with every request, as RevisionReader reads them.
func (s *ProductGovernance) ReleaseReaderScope(ctx context.Context, actor domain.Principal, product domain.ProductID) (ReleaseReaders, error) {
	if err := known(actor); err != nil {
		return ReleaseReaders{}, err
	}
	if _, err := domain.ParseProductID(string(product)); err != nil {
		return ReleaseReaders{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, actor, product, "", "")
	if err != nil || !seen.productFound {
		return ReleaseReaders{}, err
	}
	readers := ReleaseReaders{Known: s.knows(seen.standing, actor), Owner: seen.standing.Owns()}
	for _, membership := range seen.standing.Memberships {
		if slices.Contains(revisionReaders, membership.Role) {
			readers.Workspaces = append(readers.Workspaces, membership.WorkspaceID)
		}
	}
	slices.Sort(readers.Workspaces)
	readers.Workspaces = slices.Compact(readers.Workspaces)
	return readers, nil
}
