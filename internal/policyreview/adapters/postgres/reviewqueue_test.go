package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	dave           = domain.Principal{Issuer: "https://issuer.example", Subject: "dave"}
	erinElsewhere  = domain.Principal{Issuer: "https://other.example", Subject: "erin"}
	carolElsewhere = domain.Principal{Issuer: "https://other.example", Subject: "carol"}
	queueTarget    = domain.Target{Audience: "partner", Environment: "sandbox"}
)

// A place is where reviews are asked for: a workspace of a product. Two
// places may carry the identifier of one workspace under two products. No use
// case makes that, since a workspace belongs to one product, but a statement
// that forgot the product could not tell them apart.
type place struct{ product, workspace string }

func newPlace() place { return place{product: ids.NewUUID(), workspace: ids.NewUUID()} }

// asked is a review of a revision of its own, and the check it rests on.
type asked struct {
	review domain.Review
	run    domain.CheckRun
}

func (a asked) id() string { return a.review.Request.ID }

// askIn stores the request for a review of a revision of a new asset of the
// place, by the principal, at the instant. An empty identifier is a new one.
// The rules that are named failed in the check, which an approval then has to
// waive.
func (f *reviewFixture) askIn(where place, id string, by domain.Principal, at time.Time, failed ...string) asked {
	f.t.Helper()
	revision := domain.RevisionRef{
		ProductID: where.product, WorkspaceID: where.workspace, AssetID: ids.NewUUID(), RevisionID: ids.NewUUID(),
	}
	run := f.check(revision, failed...)
	if id == "" {
		id = ids.NewUUID()
	}
	request, err := domain.RequestReview(id, run, domain.Baseline(), []domain.Tool{reviewTool}, []domain.Target{queueTarget}, "for the queue", by, at)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, 2) }); err != nil {
		f.t.Fatal(err)
	}
	return asked{review: domain.Review{Request: request}, run: run}
}

// withdraw ends the review as whoever asked for it can.
func (f *reviewFixture) withdraw(review asked, at time.Time) asked {
	f.t.Helper()
	conclusion, err := domain.Withdraw(review.review, review.review.Request.RequestedBy, "no longer needed", at)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) }); err != nil {
		f.t.Fatal(err)
	}
	review.review.Conclusion = &conclusion
	return review
}

// approve ends the review with an approval of a day, which waives what is given.
func (f *reviewFixture) approve(review asked, at time.Time, waivers ...domain.Waiver) asked {
	f.t.Helper()
	conclusion := f.decide(review.review, review.run, domain.Decision{
		Outcome: domain.ReviewApproved, ValidUntil: at.Add(24 * time.Hour), Waivers: waivers,
	}, at)
	review.review.Conclusion = &conclusion
	return review
}

// reject ends the review with a rejection.
func (f *reviewFixture) reject(review asked, at time.Time) asked {
	f.t.Helper()
	conclusion := f.decide(review.review, review.run, domain.Decision{Outcome: domain.ReviewRejected, Reason: "not yet"}, at)
	review.review.Conclusion = &conclusion
	return review
}

// askAgain stores the request for a supplementary review of the revision of
// a review, on the check that review rests on, at the version the status of
// the approved revision has.
func (f *reviewFixture) askAgain(of asked, by domain.Principal, at time.Time) asked {
	f.t.Helper()
	request, err := domain.RequestReview(ids.NewUUID(), of.run, domain.Baseline(), []domain.Tool{reviewTool}, []domain.Target{queueTarget}, "again", by, at)
	if err != nil {
		f.t.Fatal(err)
	}
	request.Supplementary = true
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, 3) }); err != nil {
		f.t.Fatal(err)
	}
	return asked{review: domain.Review{Request: request}, run: of.run}
}

func reviewIDs(reviews []domain.Review) []string {
	listed := make([]string, 0, len(reviews))
	for _, review := range reviews {
		listed = append(listed, review.Request.ID)
	}
	return listed
}

// pagesOf reads the queue as the use case does: pages of size reviews, each
// asked for with one review more than it holds, which says whether another
// follows, and each behind the position of the last review of the page before.
// It fails the test for a page that is not the last and is not full.
func (f *reviewFixture) pagesOf(where place, filter ports.ReviewFilter, size int) [][]string {
	f.t.Helper()
	var pages [][]string
	var after *ports.Position
	for len(pages) < 1000 {
		got, err := f.reviews.ListInWorkspace(f.t.Context(), where.product, where.workspace, filter, after, size+1)
		if err != nil {
			f.t.Fatal(err)
		}
		page := got[:min(len(got), size)]
		pages = append(pages, reviewIDs(page))
		if len(got) <= size {
			return pages
		}
		last := page[size-1].Request
		after = &ports.Position{At: last.RequestedAt, ID: last.ID}
	}
	f.t.Fatalf("the queue has no end in pages of %d", size)
	return nil
}

// chunked is the list cut into pages of size, the last one with what is left.
func chunked(list []string, size int) [][]string {
	pages := [][]string{}
	for chunk := range slices.Chunk(list, size) {
		pages = append(pages, chunk)
	}
	if len(pages) == 0 {
		pages = append(pages, []string{})
	}
	return pages
}

// The queue of a workspace is the reviews asked for in that workspace of that
// product and no others, and a page is cut from them: it is full whatever the
// product holds beyond it. The reviews of three places are asked for in turn,
// so that a page cut from the reviews of all three and then relieved of those
// of the other two would not be full. Without the workspace in the statement
// the queue holds those of the other workspace of the product; without the
// product it holds those of the other product that has a workspace of the
// same identifier.
func TestTheQueueHoldsTheReviewsOfOneWorkspaceOfOneProductAndEveryPageIsFull(t *testing.T) {
	f, _ := newReviewFixture(t)
	here := newPlace()
	sibling := place{product: here.product, workspace: ids.NewUUID()}
	namesake := place{product: ids.NewUUID(), workspace: here.workspace}
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	queues := map[place][]string{}
	for i := range 7 {
		at := base.Add(time.Duration(i) * time.Minute)
		for offset, where := range []place{here, sibling, namesake} {
			review := f.askIn(where, "", carol, at.Add(time.Duration(offset)*10*time.Second))
			queues[where] = append(queues[where], review.id())
		}
	}

	for name, where := range map[string]place{"the workspace": here, "its sibling": sibling, "its namesake": namesake} {
		for _, size := range []int{1, 2, 3, 6, 7, 8, 100} {
			if got, want := f.pagesOf(where, ports.ReviewFilter{}, size), chunked(queues[where], size); !reflect.DeepEqual(got, want) {
				t.Errorf("%s, in pages of %d: %v\nwant %v", name, size, got, want)
			}
		}
	}
	// A workspace nothing was asked in has an empty queue, as one of a product
	// that is not there has.
	for name, where := range map[string]place{
		"a workspace without reviews": {product: here.product, workspace: ids.NewUUID()},
		"another product":             {product: ids.NewUUID(), workspace: ids.NewUUID()},
	} {
		got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 10)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("%s: %v, %v; want an empty list and no error", name, got, err)
		}
	}
}

// A review that the queue lists is the review that is read alone, whatever
// state it is in: asked and open, withdrawn, approved with the rules it
// waives, approved plainly, rejected, and a supplementary one, open,
// approved and withdrawn. The columns and the way a row is read back are the
// same for both.
func TestAReviewOfTheQueueIsTheReviewFindReads(t *testing.T) {
	f, _ := newReviewFixture(t)
	where := newPlace()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	waivers := []domain.Waiver{
		{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: at(60).Add(12 * time.Hour)},
		{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint", CompensatingControl: "the portal shows the asset name", ExpiresAt: at(60).Add(6 * time.Hour)},
	}

	open := f.askIn(where, "", carol, at(0))
	withdrawn := f.withdraw(f.askIn(where, "", dave, at(1)), at(61))
	waiving := f.approve(f.askIn(where, "", carol, at(2), "F04-GOV-003", "F04-GOV-002"), at(62), waivers...)
	plain := f.approve(f.askIn(where, "", erinElsewhere, at(3)), at(63))
	rejected := f.reject(f.askIn(where, "", dave, at(4)), at(64))
	again := f.askAgain(plain, carol, at(5))
	approvedAgain := f.approve(f.askAgain(plain, dave, at(6)), at(66))
	withdrawnAgain := f.withdraw(f.askAgain(plain, dave, at(7)), at(67))
	expected := []asked{open, withdrawn, waiving, plain, rejected, again, approvedAgain, withdrawnAgain}

	listed, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, review := range expected {
		want = append(want, review.id())
	}
	if got := reviewIDs(listed); !slices.Equal(got, want) {
		t.Fatalf("the queue holds %v, want %v", got, want)
	}
	for index, review := range listed {
		found, ok, err := f.reviews.Find(t.Context(), review.Request.Revision, review.Request.ID)
		if err != nil || !ok {
			t.Fatalf("review %s: found %t: %v", review.Request.ID, ok, err)
		}
		if !reflect.DeepEqual(review, found) || !reflect.DeepEqual(review, expected[index].review) {
			t.Errorf("review %d of the queue is\n %+v\nread alone it is\n %+v\nwant\n %+v", index, review, found, expected[index].review)
		}
	}
	// The states are those the cases were made of, so that the comparison is
	// of every kind of review there is.
	wantStatus := []string{"IN_REVIEW", "WITHDRAWN", "APPROVED", "APPROVED", "REJECTED", "IN_REVIEW", "APPROVED", "WITHDRAWN"}
	for index, review := range listed {
		if got := review.Status(); got != wantStatus[index] {
			t.Errorf("review %d is %s, want %s", index, got, wantStatus[index])
		}
	}
	if len(listed[2].Conclusion.Waivers) != 2 || listed[2].Conclusion.Waivers[0].RuleID != "F04-GOV-002" ||
		!listed[5].Request.Supplementary || !listed[6].Request.Supplementary || listed[0].Request.Supplementary ||
		listed[3].Conclusion.Waivers != nil {
		t.Errorf("the waivers and the kinds of the reviews are not what the cases were made of: %+v", listed)
	}
}

// The queue is oldest first, and among the reviews asked for at one instant by
// their identifier. The reviews are stored in an order that is neither, so that
// a statement that did not order them, or ordered them the other way, would
// show it; and a page ends among those of one instant, so that a position that
// named only the instant would skip the rest of them.
func TestTheQueueIsOrderedByWhenReviewsWereAskedForAndByIdentifierAmongTheSame(t *testing.T) {
	f, _ := newReviewFixture(t)
	where := newPlace()
	instant := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	id := func(n int) string { return "00000000-0000-4000-8000-00000000000" + string(rune('0'+n)) }

	// What is stored first is neither the oldest nor the first by identifier.
	for _, c := range []struct {
		n  int
		at time.Time
	}{
		{5, instant}, {7, instant.Add(time.Second)}, {2, instant}, {9, instant.Add(-time.Second)}, {4, instant},
		{6, instant.Add(time.Second)}, {1, instant}, {3, instant},
	} {
		f.askIn(where, id(c.n), carol, c.at)
	}
	want := []string{id(9), id(1), id(2), id(3), id(4), id(5), id(6), id(7)}

	for _, size := range []int{1, 2, 3, 4, 5, 7, 8, 9} {
		got := f.pagesOf(where, ports.ReviewFilter{}, size)
		if !reflect.DeepEqual(got, chunked(want, size)) {
			t.Errorf("in pages of %d the queue is %v, want %v", size, got, chunked(want, size))
		}
	}
	// A position names the instant and the identifier: a page behind one of
	// the reviews of the instant starts at the next of them.
	behind := ports.Position{At: instant, ID: id(3)}
	got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, &behind, 100)
	if err != nil || !slices.Equal(reviewIDs(got), want[4:]) {
		t.Errorf("behind the third review of the instant: %v, %v; want %v", reviewIDs(got), err, want[4:])
	}
	// Read twice, it is the same queue.
	first, _ := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 100)
	second, _ := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 100)
	if !reflect.DeepEqual(first, second) {
		t.Error("the queue read twice is not the same queue")
	}
}

// asking is what a test remembers of a review it asked for, in the order it
// asked: who asked and where the review stands.
type asking struct {
	id     string
	by     domain.Principal
	status string
}

// pick are the identifiers of the reviews of the list that the test keeps.
func pick(list []asking, keep func(asking) bool) []string {
	var picked []string
	for _, review := range list {
		if keep(review) {
			picked = append(picked, review.id)
		}
	}
	return picked
}

// A queue is kept to a status before it is cut into pages, so that every page
// but the last is full whatever the other statuses hold between the reviews of
// the one that is asked for. A review is open when it has no conclusion and
// otherwise stands as it ended. A status that is none of those is no filter
// that lets every review through: it is refused, whatever it looks like.
func TestTheQueueIsKeptToOneStatusBeforeItIsPaged(t *testing.T) {
	f, _ := newReviewFixture(t)
	where, other := newPlace(), newPlace()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	// The statuses are asked for in turn, in the two places.
	var made []asking
	for i := range 12 {
		for _, in := range []place{where, other} {
			review, status := f.askIn(in, "", carol, at(i)), "IN_REVIEW"
			switch i % 6 {
			case 1, 5:
				review, status = f.approve(review, at(100+i)), "APPROVED"
			case 2:
				review, status = f.withdraw(review, at(100+i)), "WITHDRAWN"
			case 3:
				review, status = f.reject(review, at(100+i)), "REJECTED"
			}
			if in == where {
				made = append(made, asking{id: review.id(), by: carol, status: status})
			}
		}
	}
	counts := map[string]int{}
	for _, review := range made {
		counts[review.status]++
	}
	if !reflect.DeepEqual(counts, map[string]int{"IN_REVIEW": 4, "APPROVED": 4, "WITHDRAWN": 2, "REJECTED": 2}) {
		t.Fatalf("the reviews are not those the cases were made of: %v", counts)
	}

	for _, status := range domain.ReviewStatuses() {
		want := pick(made, func(review asking) bool { return review.status == status })
		for _, size := range []int{1, 2, 3, 100} {
			if got := f.pagesOf(where, ports.ReviewFilter{Status: status}, size); !reflect.DeepEqual(got, chunked(want, size)) {
				t.Errorf("%s in pages of %d: %v, want %v", status, size, got, chunked(want, size))
			}
		}
	}
	// Kept to no status, the queue holds every review of the place.
	everyone := pick(made, func(asking) bool { return true })
	if got := f.pagesOf(where, ports.ReviewFilter{}, 5); !reflect.DeepEqual(got, chunked(everyone, 5)) {
		t.Errorf("a queue kept to no status holds %v, want %v", got, chunked(everyone, 5))
	}

	for _, status := range []string{"in_review", "OPEN", "ESCALATED", "IN_REVIEW' OR '1'='1", " IN_REVIEW", "APPROVED "} {
		if got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{Status: status}, nil, 100); err == nil {
			t.Errorf("kept to the status %q the queue holds %v and no error, want it refused", status, reviewIDs(got))
		}
	}
}

// A queue is kept to who asked for its reviews, by an exact match: the issuer
// alone, the subject alone under any issuer, or both. The filters narrow the
// queue together, and with the status, before it is cut into pages. An issuer
// and a subject that are swapped are not the principal that asked.
func TestTheQueueIsKeptToWhoAskedForTheReview(t *testing.T) {
	f, _ := newReviewFixture(t)
	where, other := newPlace(), newPlace()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	// Four principals ask in turn, three times each, in the two places. The
	// first review of each is approved: the status narrows with the asker.
	var made []asking
	for round := range 3 {
		for index, by := range []domain.Principal{carol, dave, carolElsewhere, erinElsewhere} {
			minutes := round*10 + index
			review, status := f.askIn(where, "", by, at(minutes)), "IN_REVIEW"
			f.askIn(other, "", by, at(minutes))
			if round == 0 {
				review, status = f.approve(review, at(200+minutes)), "APPROVED"
			}
			made = append(made, asking{id: review.id(), by: by, status: status})
		}
	}
	nobody := func(asking) bool { return false }
	issuer := func(issuer string) func(asking) bool {
		return func(review asking) bool { return review.by.Issuer == issuer }
	}
	subject := func(subject string) func(asking) bool {
		return func(review asking) bool { return review.by.Subject == subject }
	}

	for name, c := range map[string]struct {
		filter ports.ReviewFilter
		keep   func(asking) bool
	}{
		"the issuer alone":                    {ports.ReviewFilter{RequestedByIssuer: "https://issuer.example"}, issuer("https://issuer.example")},
		"the other issuer alone":              {ports.ReviewFilter{RequestedByIssuer: "https://other.example"}, issuer("https://other.example")},
		"the subject alone, under any issuer": {ports.ReviewFilter{RequestedBySubject: "carol"}, subject("carol")},
		"a subject that only one issuer has":  {ports.ReviewFilter{RequestedBySubject: "erin"}, subject("erin")},
		"the issuer and the subject": {ports.ReviewFilter{RequestedByIssuer: "https://other.example", RequestedBySubject: "carol"},
			func(review asking) bool { return review.by == carolElsewhere }},
		"an issuer that has not that subject": {ports.ReviewFilter{RequestedByIssuer: "https://issuer.example", RequestedBySubject: "erin"}, nobody},
		"the issuer and the subject swapped":  {ports.ReviewFilter{RequestedByIssuer: "carol", RequestedBySubject: "https://issuer.example"}, nobody},
		"an issuer that is a subject":         {ports.ReviewFilter{RequestedByIssuer: "carol"}, nobody},
		"a subject in another case":           {ports.ReviewFilter{RequestedBySubject: "CAROL"}, nobody},
		"a subject with a space after it":     {ports.ReviewFilter{RequestedBySubject: "carol "}, nobody},
		"a subject that is a piece of SQL":    {ports.ReviewFilter{RequestedBySubject: "carol' OR '1'='1"}, nobody},
		"an issuer with a suffix":             {ports.ReviewFilter{RequestedByIssuer: "https://issuer.example/"}, nobody},
		"the start of an issuer":              {ports.ReviewFilter{RequestedByIssuer: "https://issuer"}, nobody},
		"a wildcard for an issuer":            {ports.ReviewFilter{RequestedByIssuer: "%"}, nobody},
		"a piece of a subject":                {ports.ReviewFilter{RequestedBySubject: "aro"}, nobody},
		"the start of a subject":              {ports.ReviewFilter{RequestedBySubject: "car"}, nobody},
		"a wildcard for a subject":            {ports.ReviewFilter{RequestedBySubject: "%"}, nobody},
		"a wildcard for a character":          {ports.ReviewFilter{RequestedBySubject: "caro_"}, nobody},
		"the status and the subject together": {ports.ReviewFilter{Status: "APPROVED", RequestedBySubject: "carol"},
			func(review asking) bool { return review.status == "APPROVED" && review.by.Subject == "carol" }},
		"the status and an issuer": {ports.ReviewFilter{Status: "IN_REVIEW", RequestedByIssuer: "https://other.example"},
			func(review asking) bool {
				return review.status == "IN_REVIEW" && review.by.Issuer == "https://other.example"
			}},
	} {
		want := pick(made, c.keep)
		for _, size := range []int{1, 2, 100} {
			got := slices.Concat(f.pagesOf(where, c.filter, size)...)
			if !slices.Equal(got, want) {
				t.Errorf("%s, in pages of %d: %v, want %v", name, size, got, want)
			}
		}
	}
	// The cases ask for something: the ones that match nobody are not empty by
	// accident of what was made.
	if len(pick(made, subject("carol"))) != 6 || len(pick(made, issuer("https://other.example"))) != 6 {
		t.Fatalf("the reviews are not those the cases were made of: %v", made)
	}
}

// A page takes at least one review: what is asked of the store that cannot be
// answered is refused, not answered with everything there is.
func TestAPageOfTheQueueTakesAtLeastOneReview(t *testing.T) {
	f, _ := newReviewFixture(t)
	where := newPlace()
	f.askIn(where, "", carol, time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	for _, limit := range []int{0, -1} {
		if got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, limit); err == nil {
			t.Errorf("a limit of %d: got %v and no error", limit, reviewIDs(got))
		}
	}
	if got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 1); err != nil || len(got) != 1 {
		t.Errorf("a limit of one: %v, %v", reviewIDs(got), err)
	}
}

// What the queue lists is read through the model, as a review read alone is:
// a review whose rows are not ones the model recorded is an error that names
// it, and not a review, so that the queue of a workspace does not hand out
// what a read of one would refuse.
func TestADamagedReviewIsAnErrorOfTheQueueAndIsNamed(t *testing.T) {
	f, database := newReviewFixture(t)
	where := newPlace()
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	healthy := f.askIn(where, "", carol, base)
	withdrawn := f.withdraw(f.askIn(where, "", carol, base.Add(time.Minute)), base.Add(time.Hour))
	f.askIn(where, "", carol, base.Add(2*time.Minute))

	if _, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{}, nil, 100); err != nil {
		t.Fatalf("before the damage: %v", err)
	}
	// A withdrawal that waives a rule is no withdrawal the model recorded.
	owner := pgtest.Connect(t, database.Migrator)
	if _, err := owner.Exec(t.Context(), `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, expires_at) VALUES ($1, 'F04-GOV-003', 'accepted', now() + interval '1 day')`,
		withdrawn.id()); err != nil {
		t.Fatal(err)
	}
	for name, filter := range map[string]ports.ReviewFilter{"the whole queue": {}, "the withdrawn": {Status: "WITHDRAWN"}} {
		got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, filter, nil, 100)
		if !errors.Is(err, domain.ErrReviewDamaged) || !strings.Contains(err.Error(), "review "+withdrawn.id()) || got != nil {
			t.Errorf("%s: %v, %v; want no review and %v naming the review", name, reviewIDs(got), err, domain.ErrReviewDamaged)
		}
	}
	// What is not damaged is read as before, kept to a status that leaves the
	// damaged review out.
	if got, err := f.reviews.ListInWorkspace(t.Context(), where.product, where.workspace, ports.ReviewFilter{Status: "IN_REVIEW"}, nil, 100); err != nil || len(got) != 2 || got[0].Request.ID != healthy.id() {
		t.Errorf("the open reviews: %v, %v; want the two that are not damaged", reviewIDs(got), err)
	}
}
