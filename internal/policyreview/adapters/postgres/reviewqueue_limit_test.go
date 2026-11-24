package postgres_test

import (
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// A page of the queue is at most as many reviews as the store was asked for.
// The use case asks for one more than the page holds and reads no more than
// that: a store that handed back one more again would cost a review on every
// page, and nothing the use case answers would say so, as it drops what is
// beyond the page. So the store is asked for fewer reviews than the queue
// holds, whatever narrows it: nothing, a status, who asked for the review, the
// two together, or a position the page starts behind. It returns the first of
// what is left, exactly as many as it was asked for, and fewer only where fewer
// are left.
func TestAPageOfTheQueueHoldsNoMoreReviewsThanTheStoreWasAskedFor(t *testing.T) {
	f, _ := newReviewFixture(t)
	where := newPlace()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	// Seven reviews, asked for in turn by carol and dave. The third and the
	// fifth are approved; the others are open.
	type made struct {
		id       string
		position ports.Position
		by       domain.Principal
		open     bool
	}
	var queue []made
	for i, by := range []domain.Principal{carol, dave, carol, carol, dave, carol, carol} {
		review, open := f.askIn(where, "", by, at(i)), true
		if i == 2 || i == 4 {
			review, open = f.approve(review, at(100+i)), false
		}
		queue = append(queue, made{
			id: review.id(), by: by, open: open,
			position: ports.Position{At: review.review.Request.RequestedAt, ID: review.id()},
		})
	}
	everyone := func(made) bool { return true }
	opened := func(review made) bool { return review.open }
	byCarol := func(review made) bool { return review.by == carol }

	for name, c := range map[string]struct {
		filter ports.ReviewFilter
		behind int // the index of the review the page starts behind, or -1 for the start
		keep   func(made) bool
	}{
		"the queue":                               {ports.ReviewFilter{}, -1, everyone},
		"the open reviews":                        {ports.ReviewFilter{Status: "IN_REVIEW"}, -1, opened},
		"the reviews carol asked for":             {ports.ReviewFilter{RequestedBySubject: "carol"}, -1, byCarol},
		"the open reviews carol asked for":        {ports.ReviewFilter{Status: "IN_REVIEW", RequestedBySubject: "carol"}, -1, func(review made) bool { return opened(review) && byCarol(review) }},
		"the queue behind its first review":       {ports.ReviewFilter{}, 0, everyone},
		"the open reviews behind the first":       {ports.ReviewFilter{Status: "IN_REVIEW"}, 0, opened},
		"the reviews carol asked for behind that": {ports.ReviewFilter{RequestedBySubject: "carol"}, 0, byCarol},
	} {
		var after *ports.Position
		var want []string
		for i, review := range queue {
			if i > c.behind && c.keep(review) {
				want = append(want, review.id)
			}
		}
		if c.behind >= 0 {
			after = &queue[c.behind].position
		}
		// The cases leave more than the smallest page asks for, so that a page
		// that is full has something behind it.
		if len(want) < 4 {
			t.Fatalf("%s: %d reviews are left, want at least 4 for the cases to ask for something", name, len(want))
		}
		for _, limit := range []int{1, 2, 3, len(want) - 1, len(want), len(want) + 1} {
			got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, c.filter, after, limit)
			if err != nil {
				t.Errorf("%s, with a limit of %d: %v", name, limit, err)
				continue
			}
			if len(got) > limit {
				t.Errorf("%s, with a limit of %d: %d reviews were returned, want at most %d", name, limit, len(got), limit)
			}
			if expected := want[:min(limit, len(want))]; !slices.Equal(reviewIDs(got), expected) {
				t.Errorf("%s, with a limit of %d: %v, want the first %d of what is left: %v", name, limit, reviewIDs(got), len(expected), expected)
			}
		}
	}
}
