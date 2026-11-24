package application_test

import (
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
)

// One read of last checks takes at most a thousand revisions, and the number
// is pinned here because the Catalog hangs on it: every round of a filtered
// list names the revisions of the assets it read in one question to this read,
// and a round takes at most maxBatch assets, so at most as many revisions
// (internal/apicatalog/application/overview.go). A round that was bigger than
// this read takes is refused whole (ErrInvalidRevisions), and the list that
// asked for it fails with a 500 instead of being answered. So maxBatch must
// never be greater than this number, nor than MaxHeadAssets of Contract
// Registry (internal/contractregistry/application/heads.go), which the same
// round asks of for the heads of those assets. The three contexts do not know
// one another, so no test can hold the three together by comparing them: each
// pins its own number and says which are the others. Whoever changes this one
// is asked to look at those two.
//
// The tests of what a read refuses take this number from the constant, as
// they should; this is the one that says what it is.
func TestOneReadOfLastChecksTakesAtMostAThousandRevisions(t *testing.T) {
	if application.MaxLatestRevisions != 1000 {
		t.Errorf("one read of last checks takes at most %d revisions, want 1000: look at maxBatch of the Catalog "+
			"and MaxHeadAssets of Contract Registry, which a round of a filtered list is held to together", application.MaxLatestRevisions)
	}
}
