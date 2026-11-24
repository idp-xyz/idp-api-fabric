package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// ErrInvalidRevisions reports a read of last checks that names no revisions of
// one product: something that is no identifier, a revision of another
// product, one named twice, a status that is none, or more revisions than one
// read takes. Whoever asks has made a mistake of its own: nothing the caller
// sent says it.
var ErrInvalidRevisions = errors.New("the revisions are not revisions of the product named by their identifiers, or the status is none")

// MaxLatestRevisions is how many revisions one read of last checks takes. A
// caller that has more reads them a part at a time.
const MaxLatestRevisions = 1000

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// concluded are what a check can conclude: a check that is recorded has been
// concluded, so it is never NOT_RUN, which only a rule of it can be.
var concluded = []domain.Status{domain.StatusPass, domain.StatusWarn, domain.StatusFail, domain.StatusUnknown}

// Expected is a revision and the digest of the closure of its source as the
// registry holds it now: what a check of the revision has to have read.
type Expected struct {
	Revision    domain.RevisionRef
	InputDigest string
}

// Latest reads the check each of a set of revisions concluded last, for a
// use case of another context that has decided which revisions it may ask
// about, such as the Catalog listing assets to a caller that reads their
// revisions. It asks nothing of that caller and grants it nothing.
type Latest struct{ checks ports.LatestChecks }

func NewLatest(checks ports.LatestChecks) (*Latest, error) {
	if checks == nil {
		return nil, errors.New("reading the last checks requires the storage of check runs")
	}
	return &Latest{checks: checks}, nil
}

// Of returns the check each revision concluded last, as the history of the
// revision shows it, in no order. A revision without a check has none here,
// and with a status only the checks of that status are returned.
//
// A check is held to what a read of the history of its revision holds it to:
// the row is one the model recorded, which the storage sees to, and the check
// read the closure the revision has now, which is what is asked of the caller
// to say. A check that is not that fails the whole read with
// ErrCheckRunDamaged, as the history of the revision fails. What a check of a
// comparison says of its baseline is not looked at here: the history looks the
// baseline up at the registry for every page it shows, and this reads no more
// than the checks.
func (l *Latest) Of(ctx context.Context, product string, expected []Expected, status domain.Status) ([]domain.Overview, error) {
	if !identifier.MatchString(product) || len(expected) > MaxLatestRevisions ||
		status != "" && !slices.Contains(concluded, status) {
		return nil, ErrInvalidRevisions
	}
	revisions := make([]domain.RevisionRef, 0, len(expected))
	asked := make(map[string]Expected, len(expected))
	for _, want := range expected {
		revision := want.Revision
		if _, twice := asked[revision.RevisionID]; twice || revision.ProductID != product ||
			!identifier.MatchString(revision.WorkspaceID) || !identifier.MatchString(revision.AssetID) ||
			!identifier.MatchString(revision.RevisionID) {
			return nil, ErrInvalidRevisions
		}
		asked[revision.RevisionID] = want
		revisions = append(revisions, revision)
	}
	if len(revisions) == 0 {
		return nil, nil
	}
	found, err := l.checks.Latest(ctx, product, revisions, status)
	if err != nil {
		return nil, err
	}
	for _, check := range found {
		want, named := asked[check.Revision.RevisionID]
		switch {
		case !named || check.Revision != want.Revision:
			return nil, fmt.Errorf("the storage returned the check run %s of a revision that was not asked for: %w", check.ID, domain.ErrCheckRunDamaged)
		case check.InputDigest != want.InputDigest:
			return nil, fmt.Errorf("check run %s: %w", check.ID, domain.ErrCheckRunDamaged)
		}
	}
	return found, nil
}
