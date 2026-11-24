package application_test

import (
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
)

// One read of heads takes at most a thousand assets, and the number is pinned
// here because the Catalog hangs on it: every round of a filtered list names
// the assets it read in one question to this read, and a round takes at most
// maxBatch of them (internal/apicatalog/application/overview.go). A round that
// was bigger than this read takes is refused whole (ErrInvalidAssets), and the
// list that asked for it fails with a 500 instead of being answered. So
// maxBatch must never be greater than this number, nor than MaxLatestRevisions
// of Policy & Review (internal/policyreview/application/latest.go), which the
// same round asks of for the last checks of those assets. The three contexts do
// not know one another, so no test can hold the three together by comparing
// them: each pins its own number and says which are the others. Whoever changes
// this one is asked to look at those two.
//
// The tests of what a read refuses take this number from the constant, as
// they should; this is the one that says what it is.
func TestOneReadOfHeadsTakesAtMostAThousandAssets(t *testing.T) {
	if application.MaxHeadAssets != 1000 {
		t.Errorf("one read of heads takes at most %d assets, want 1000: look at maxBatch of the Catalog "+
			"and MaxLatestRevisions of Policy & Review, which a round of a filtered list is held to together", application.MaxHeadAssets)
	}
}
