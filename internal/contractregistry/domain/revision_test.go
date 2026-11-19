package domain

import (
	"errors"
	"strings"
	"testing"
)

// The status of a revision moves along its review and nowhere else: a draft
// is submitted, what is in review is approved, rejected or withdrawn, what
// was withdrawn is submitted again, and approved and rejected are where it
// ends. Every other pair of statuses is refused, and the refusal says where
// the revision is.
func TestTheStatusOfARevisionMovesAlongItsReview(t *testing.T) {
	statuses := []string{RevisionDraft, RevisionInReview, RevisionApproved, RevisionRejected, RevisionWithdrawn}
	allowed := map[[2]string]bool{
		{RevisionDraft, RevisionInReview}:     true,
		{RevisionWithdrawn, RevisionInReview}: true,
		{RevisionInReview, RevisionApproved}:  true,
		{RevisionInReview, RevisionRejected}:  true,
		{RevisionInReview, RevisionWithdrawn}: true,
	}
	for _, from := range statuses {
		for _, to := range append(statuses, "", "PUBLISHED") {
			err := Move(from, to)
			switch {
			case allowed[[2]string{from, to}] && err != nil:
				t.Errorf("%s to %s: refused: %v", from, to, err)
			case !allowed[[2]string{from, to}] && (!errors.Is(err, ErrStatusConflict) || !strings.HasSuffix(err.Error(), ": it is "+from)):
				t.Errorf("%s to %q: got %v, want %v naming %s", from, to, err, ErrStatusConflict, from)
			}
		}
	}
	for _, from := range []string{"", "PUBLISHED"} {
		if err := Move(from, RevisionInReview); !errors.Is(err, ErrStatusConflict) {
			t.Errorf("from %q: got %v, want %v", from, err, ErrStatusConflict)
		}
	}
}
