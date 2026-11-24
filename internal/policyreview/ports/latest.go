package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// LatestChecks is the read of the check each of a set of revisions concluded
// last, for a use case of another context that has decided which revisions it
// may ask about. It asks nothing of anyone: what it hands over is for the
// caller to show or to keep to itself.
type LatestChecks interface {
	// Latest reads, all at once, the check of each revision that was
	// concluded last: by the instant it was concluded and then by its
	// identifier, as the history of the revision lists them. Each is shown
	// as that history shows it and is held to what the model recorded: a
	// check that is not one is ErrCheckRunDamaged, and not a check.
	//
	// A revision without a check is not among them. Neither is one whose
	// last check did not conclude status, unless status is empty. The
	// outcomes of a check are read only for the checks that are kept, so
	// that a status asked for names what is read. A revision is named under
	// its asset and its Workspace, as every read of its checks names it,
	// and the checks come in no order.
	Latest(ctx context.Context, product string, revisions []domain.RevisionRef, status domain.Status) ([]domain.Overview, error)
}
