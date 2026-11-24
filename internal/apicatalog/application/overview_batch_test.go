package application

import "testing"

// A round of a filtered list takes at most a thousand assets, and the number
// is pinned here because two others hang on it: the Catalog names the assets
// of a round in one question to Contract Registry, which takes at most
// MaxHeadAssets of them (internal/contractregistry/application/heads.go), and
// in one to Policy & Review, which takes at most MaxLatestRevisions
// (internal/policyreview/application/latest.go). A round that was bigger than
// either would be refused whole, and the list that asked for it would fail
// with a 500 instead of being answered. So maxBatch must never be greater than
// either, and the three contexts do not know one another, so no test can hold
// the three together by comparing them: each pins its own number and says
// which are the others. Whoever changes this one is asked to look at those two.
func TestARoundOfAFilteredListTakesAtMostAThousandAssets(t *testing.T) {
	if maxBatch != 1000 {
		t.Errorf("a round of a filtered list takes at most %d assets, want 1000: look at MaxHeadAssets of Contract Registry "+
			"and MaxLatestRevisions of Policy & Review, which a round must not exceed", maxBatch)
	}
}

// The first round of a filtered list takes the one asset more than a page
// holds that tells whether a page follows, and each round after it takes twice
// what the one before took, up to a thousand and no more, whatever the size of
// the page and however many rounds there are. The cap is on the doubling and not
// only on the result: a loop that went on doubling would overflow the batch to
// a negative number and then to none after some sixty rounds, which a filter
// that keeps next to nothing of a catalog of fifty thousand assets or more
// reaches, and a round of no assets or of a negative number of them is no round.
func TestTheBatchOfARoundGrowsFromOneMoreThanAPageToTheMostAndStaysThere(t *testing.T) {
	for _, size := range []int{1, 2, 10, 50, 99, 100} {
		if first := batchOf(size, 0); first != size+1 {
			t.Errorf("the first round of a list of pages of %d takes %d assets, want %d", size, first, size+1)
		}
		for round := 1; round <= 200; round++ {
			before, got := batchOf(size, round-1), batchOf(size, round)
			if got < size+1 || got > maxBatch {
				t.Errorf("round %d of a list of pages of %d takes %d assets, want from %d to %d", round, size, got, size+1, maxBatch)
				break
			}
			if want := min(2*before, maxBatch); got != want {
				t.Errorf("round %d of a list of pages of %d takes %d assets after %d, want %d", round, size, got, before, want)
				break
			}
		}
		if last := batchOf(size, 200); last != maxBatch {
			t.Errorf("round 200 of a list of pages of %d takes %d assets, want the most, %d", size, last, maxBatch)
		}
	}
}
