package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

const (
	queueProduct   = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	queueWorkspace = "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"
)

var queueCaller = domain.Principal{Issuer: "https://issuer.example", Subject: "reader"}

// queueScopes stands in for what Product Governance says of a caller to a
// workspace. It answers what the test set and keeps what it was asked.
type queueScopes struct {
	scope ports.WorkspaceScope
	err   error
	asked []queueScopeAsked
}

type queueScopeAsked struct {
	caller             domain.Principal
	product, workspace string
}

func (s *queueScopes) ReviewScope(_ context.Context, caller domain.Principal, product, workspace string) (ports.WorkspaceScope, error) {
	s.asked = append(s.asked, queueScopeAsked{caller, product, workspace})
	return s.scope, s.err
}

// queueStore stands in for the reviews that are stored. It answers as many of
// the reviews the test set as it is asked for, and keeps what it was asked.
type queueStore struct {
	reviews []domain.Review
	err     error
	asked   []queueStoreAsked
}

type queueStoreAsked struct {
	product, workspace string
	filter             ports.ReviewFilter
	after              *ports.Position
	limit              int
}

func (s *queueStore) ListInWorkspace(_ context.Context, product, workspace string, filter ports.ReviewFilter, after *ports.Position, limit int) ([]domain.Review, error) {
	s.asked = append(s.asked, queueStoreAsked{product, workspace, filter, after, limit})
	return s.reviews[:min(limit, len(s.reviews))], s.err
}

// queueWorld is a queue of reviews with what stands behind it, and what was
// recorded of what it refused.
type queueWorld struct {
	scopes   *queueScopes
	store    *queueStore
	audited  []ports.AuditEntry
	auditErr error
	queue    *application.ReviewQueue
}

func newQueueWorld(t *testing.T, scope ports.WorkspaceScope, reviews int) *queueWorld {
	t.Helper()

	w := &queueWorld{scopes: &queueScopes{scope: scope}, store: &queueStore{}}
	base := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	for n := range reviews {
		w.store.reviews = append(w.store.reviews, domain.Review{Request: domain.ReviewRequest{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n+1), RequestedAt: base.Add(time.Duration(n) * time.Minute),
		}})
	}
	queue, err := application.NewReviewQueue(application.QueueDependencies{
		Reviews: w.store, Scopes: w.scopes,
		Audit: func(_ context.Context, entry ports.AuditEntry) error {
			w.audited = append(w.audited, entry)
			return w.auditErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.queue = queue
	return w
}

// untouched says that nothing was asked of Product Governance or of the store,
// and nothing was recorded.
func (w *queueWorld) untouched() bool {
	return len(w.scopes.asked) == 0 && len(w.store.asked) == 0 && len(w.audited) == 0
}

func asking(size int) application.ListWorkspaceReviews {
	return application.ListWorkspaceReviews{
		Caller: queueCaller, CorrelationID: "correlation", ProductID: queueProduct, WorkspaceID: queueWorkspace, PageSize: size,
	}
}

// What the caller is told is as far as it may know. Whoever may not know the
// product is told there is none, and whoever knows it and may not know the
// workspace is told there is none; whoever sees the workspace and reads none
// of it is told that it may not; and whoever reads is answered. The refusals
// are on record, for the kind of resource a review is, under the rule of
// refusals and against the product, with no review named and no other member
// but who asked and what the request was; those that are not on record are
// the product that is not there and the workspace that an owner asks for and
// the product has not. Nothing is asked of the store for a caller that does
// not read.
func TestTheQueueIsRefusedAsFarAsTheCallerMayKnow(t *testing.T) {
	refusal := ports.AuditEntry{
		Target: ports.Target{Caller: queueCaller, ProductID: queueProduct, CorrelationID: "correlation"},
		Action: "review.read", Rule: "DENY-03",
	}
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	for name, c := range map[string]struct {
		scope    ports.WorkspaceScope
		want     error
		recorded bool
	}{
		"a product that is not there":                      {ports.WorkspaceScope{}, application.ErrProductNotFound, false},
		"a product that is not there, to whoever knows it": {ports.WorkspaceScope{Known: true, Sees: true, WorkspaceFound: true, Reads: true}, application.ErrProductNotFound, false},
		"a stranger":                                          {ports.WorkspaceScope{Found: true, WorkspaceFound: true}, application.ErrProductNotFound, true},
		"a stranger that reads a workspace":                   {ports.WorkspaceScope{Found: true, Sees: true, WorkspaceFound: true, Reads: true}, application.ErrProductNotFound, true},
		"whoever knows the product and sees no workspace":     {ports.WorkspaceScope{Found: true, Known: true, WorkspaceFound: true}, application.ErrWorkspaceNotFound, true},
		"whoever knows the product, of a workspace not there": {ports.WorkspaceScope{Found: true, Known: true}, application.ErrWorkspaceNotFound, true},
		"whoever does not see the workspace but reads it":     {ports.WorkspaceScope{Found: true, Known: true, WorkspaceFound: true, Reads: true}, application.ErrWorkspaceNotFound, true},
		"an owner, asking for a workspace that is not there":  {ports.WorkspaceScope{Found: true, Known: true, Sees: true}, application.ErrWorkspaceNotFound, false},
		"an owner that reads none of the workspace":           {ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true}, application.ErrReviewsForbidden, true},
		"whoever reads a workspace that is not there":         {ports.WorkspaceScope{Found: true, Known: true, Sees: true, Reads: true}, application.ErrWorkspaceNotFound, false},
		"whoever reads": {reads, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newQueueWorld(t, c.scope, 3)
			page, err := w.queue.ListWorkspaceReviews(t.Context(), asking(10))
			if c.want != nil {
				if !errors.Is(err, c.want) || len(page.Items) != 0 || page.Next != nil {
					t.Fatalf("got %+v and %v, want nothing and %v", page, err, c.want)
				}
				if len(w.store.asked) != 0 {
					t.Errorf("the store was asked %+v for a caller that does not read", w.store.asked)
				}
			} else if err != nil || len(page.Items) != 3 || len(w.store.asked) != 1 {
				t.Fatalf("got %+v and %v, want the three reviews of one read of the store", page, err)
			}
			var want []ports.AuditEntry
			if c.recorded {
				want = []ports.AuditEntry{refusal}
			}
			if !reflect.DeepEqual(w.audited, want) {
				t.Errorf("audited %+v, want %+v", w.audited, want)
			}
			// Product Governance is asked once, who the caller is and where.
			if want := []queueScopeAsked{{queueCaller, queueProduct, queueWorkspace}}; !reflect.DeepEqual(w.scopes.asked, want) {
				t.Errorf("Product Governance was asked %+v, want %+v", w.scopes.asked, want)
			}
		})
	}
}

// A page is the reviews the store gave, as many as it holds, and the position
// of the last of them when the store gave one more: the token of the next
// page is handed out for as long as a review follows and not after the last,
// whether that page is full or not. What the request keeps the queue to, and
// where the page starts, reach the store as they were said, and the store is
// asked for one review more than the page holds.
func TestAPageIsCutBehindTheFilterAndHandsOutATokenOnlyWhenMoreFollow(t *testing.T) {
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	for name, c := range map[string]struct {
		reviews, size int
		items         int
		next          bool
	}{
		"fewer reviews than the page holds": {1, 2, 1, false},
		"exactly as many as the page holds": {2, 2, 2, false},
		"one more than the page holds":      {3, 2, 2, true},
		"many more":                         {7, 3, 3, true},
		"no review":                         {0, 5, 0, false},
		"a page of one, and one":            {1, 1, 1, false},
		"a page of one, and two":            {2, 1, 1, true},
		"the largest page, filled":          {100, 100, 100, false},
		"the largest page, and one more":    {101, 100, 100, true},
	} {
		t.Run(name, func(t *testing.T) {
			w := newQueueWorld(t, reads, c.reviews)
			query := asking(c.size)
			query.Status, query.RequestedByIssuer, query.RequestedBySubject = "APPROVED", "https://issuer.example", "dave"
			behind := ports.Position{At: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC), ID: "00000000-0000-4000-8000-0000000000ff"}
			query.After = &behind
			page, err := w.queue.ListWorkspaceReviews(t.Context(), query)
			if err != nil || len(page.Items) != c.items || (page.Next != nil) != c.next {
				t.Fatalf("got %d reviews and the token %+v (%v), want %d reviews and a token: %t", len(page.Items), page.Next, err, c.items, c.next)
			}
			if c.next {
				last := w.store.reviews[c.size-1].Request
				if want := (ports.Position{At: last.RequestedAt, ID: last.ID}); *page.Next != want {
					t.Errorf("the token names %+v, want the position of the last review of the page, %+v", *page.Next, want)
				}
			}
			for index, review := range page.Items {
				if review.Request.ID != w.store.reviews[index].Request.ID {
					t.Errorf("review %d of the page is %s, want the one the store gave first, %s", index, review.Request.ID, w.store.reviews[index].Request.ID)
				}
			}
			want := []queueStoreAsked{{
				queueProduct, queueWorkspace, ports.ReviewFilter{Status: "APPROVED", RequestedByIssuer: "https://issuer.example", RequestedBySubject: "dave"},
				&behind, c.size + 1,
			}}
			if !reflect.DeepEqual(w.store.asked, want) {
				t.Errorf("the store was asked %+v, want %+v", w.store.asked, want)
			}
			if len(w.audited) != 0 {
				t.Errorf("an answered read left the audit entries %+v", w.audited)
			}
		})
	}
}

// The size of a page is one of 1 to the largest, and the use case does not
// default it: whoever calls names one, as the transport does with the size of
// the contract. A size that cannot be is refused before anything is asked,
// to Product Governance or to the store, and before anything is recorded.
func TestAPageSizeThatCannotBeIsRefusedBeforeAnythingIsRead(t *testing.T) {
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	if application.DefaultQueuePage != 50 || application.MaxQueuePage != 100 {
		t.Errorf("a page holds %d reviews by default and at most %d, want 50 and 100", application.DefaultQueuePage, application.MaxQueuePage)
	}
	for _, size := range []int{0, -1, -100, 101, 1000} {
		w := newQueueWorld(t, reads, 3)
		if page, err := w.queue.ListWorkspaceReviews(t.Context(), asking(size)); !errors.Is(err, application.ErrInvalidReviewPage) || len(page.Items) != 0 {
			t.Errorf("a page of %d: got %+v and %v, want %v", size, page, err, application.ErrInvalidReviewPage)
		}
		if !w.untouched() {
			t.Errorf("a page of %d: Product Governance was asked %+v, the store %+v and %+v was recorded", size, w.scopes.asked, w.store.asked, w.audited)
		}
	}
	// Not even for whoever may not read: what the request says is looked at
	// first, as that of a page of the reviews of a revision is.
	w := newQueueWorld(t, ports.WorkspaceScope{}, 3)
	if _, err := w.queue.ListWorkspaceReviews(t.Context(), asking(0)); !errors.Is(err, application.ErrInvalidReviewPage) || !w.untouched() {
		t.Errorf("a page of none, asked by a caller that reads nothing: %v, and %+v was asked of Product Governance", err, w.scopes.asked)
	}
	for size, limit := range map[int]int{1: 2, 100: 101} {
		w := newQueueWorld(t, reads, 3)
		if _, err := w.queue.ListWorkspaceReviews(t.Context(), asking(size)); err != nil || len(w.store.asked) != 1 || w.store.asked[0].limit != limit {
			t.Errorf("a page of %d: %v, the store was asked %+v, want a limit of %d", size, err, w.store.asked, limit)
		}
	}
}

// What the request says of the reviews is looked at once the caller is known
// to read them, and whoever is refused is told nothing else: a status no
// review has, a principal that is no reference, a position that is none of the
// queue. They are told that the page cannot be, and nothing is asked of the
// store.
func TestWhatTheRequestSaysOfTheQueueIsLookedAtOnceTheCallerReadsIt(t *testing.T) {
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	longer := strings.Repeat("a", domain.MaxReference+1)
	for name, change := range map[string]func(*application.ListWorkspaceReviews){
		"a status in lower case":           func(q *application.ListWorkspaceReviews) { q.Status = "in_review" },
		"a status no review has":           func(q *application.ListWorkspaceReviews) { q.Status = "ESCALATED" },
		"a status with a space":            func(q *application.ListWorkspaceReviews) { q.Status = "IN_REVIEW " },
		"the status of a revision":         func(q *application.ListWorkspaceReviews) { q.Status = "DRAFT" },
		"an issuer of a control character": func(q *application.ListWorkspaceReviews) { q.RequestedByIssuer = "https://issuer\n.example" },
		"an issuer that is too long":       func(q *application.ListWorkspaceReviews) { q.RequestedByIssuer = longer },
		"an issuer that is not UTF-8":      func(q *application.ListWorkspaceReviews) { q.RequestedByIssuer = "issuer\xff" },
		"a subject of a control character": func(q *application.ListWorkspaceReviews) { q.RequestedBySubject = "dave\x00" },
		"a subject that is too long":       func(q *application.ListWorkspaceReviews) { q.RequestedBySubject = longer },
		"a position without an instant":    func(q *application.ListWorkspaceReviews) { q.After = &ports.Position{ID: queueWorkspace} },
		"a position of no identifier":      func(q *application.ListWorkspaceReviews) { q.After = &ports.Position{At: time.Now(), ID: "latest"} },
		"a position of an identifier in caps": func(q *application.ListWorkspaceReviews) {
			q.After = &ports.Position{At: time.Now(), ID: strings.ToUpper(queueWorkspace)}
		},
		"a position of no review at all": func(q *application.ListWorkspaceReviews) { q.After = &ports.Position{} },
	} {
		t.Run(name, func(t *testing.T) {
			query := asking(10)
			change(&query)
			w := newQueueWorld(t, reads, 3)
			if page, err := w.queue.ListWorkspaceReviews(t.Context(), query); !errors.Is(err, application.ErrInvalidReviewPage) || len(page.Items) != 0 {
				t.Errorf("got %+v and %v, want %v", page, err, application.ErrInvalidReviewPage)
			}
			if len(w.store.asked) != 0 || len(w.audited) != 0 {
				t.Errorf("the store was asked %+v and %+v was recorded", w.store.asked, w.audited)
			}
			// A caller that does not read is told so, and not that its request
			// is wrong.
			w = newQueueWorld(t, ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true}, 3)
			if _, err := w.queue.ListWorkspaceReviews(t.Context(), query); !errors.Is(err, application.ErrReviewsForbidden) {
				t.Errorf("a caller that reads nothing, with the same request: got %v, want %v", err, application.ErrReviewsForbidden)
			}
		})
	}
	// What a queue can be kept to is kept to, whole.
	for _, status := range domain.ReviewStatuses() {
		query := asking(10)
		query.Status, query.RequestedByIssuer, query.RequestedBySubject = status, strings.Repeat("é", domain.MaxReference), "dave"
		query.After = &ports.Position{At: time.Now(), ID: queueWorkspace}
		w := newQueueWorld(t, reads, 1)
		if _, err := w.queue.ListWorkspaceReviews(t.Context(), query); err != nil {
			t.Errorf("a queue kept to %s, to a principal of the longest references and behind a position: %v", status, err)
		}
	}
}

// A request that names no principal, or names a product by something that is
// no identifier, is answered before Product Governance and the store are
// asked: the first is refused, since there is nobody to name in the record of
// a refusal, and the second is a product that is not there. A workspace that
// is no identifier is another matter: it is a workspace that is not there,
// which Product Governance says of it as it says it of one it has not, and
// so the use case asks it, to tell a stranger from whoever may be told.
func TestTheQueueIsNotReadForNobodyOrOfAProductThatIsNone(t *testing.T) {
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	for name, c := range map[string]struct {
		caller  domain.Principal
		product string
		want    error
	}{
		"no principal":                  {domain.Principal{}, queueProduct, application.ErrReviewsForbidden},
		"a principal without a subject": {domain.Principal{Issuer: "https://issuer.example"}, queueProduct, application.ErrReviewsForbidden},
		"a principal without an issuer": {domain.Principal{Subject: "reader"}, queueProduct, application.ErrReviewsForbidden},
		"no principal, no product":      {domain.Principal{}, "", application.ErrReviewsForbidden},
		"a key and not an identifier":   {queueCaller, "cargopai", application.ErrProductNotFound},
		"words that are no identifier":  {queueCaller, "not-an-identifier", application.ErrProductNotFound},
		"no product":                    {queueCaller, "", application.ErrProductNotFound},
		"an identifier in capitals":     {queueCaller, strings.ToUpper(queueProduct), application.ErrProductNotFound},
		"an identifier with a suffix":   {queueCaller, queueProduct + "0", application.ErrProductNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			w := newQueueWorld(t, reads, 3)
			query := asking(10)
			query.Caller, query.ProductID = c.caller, c.product
			if page, err := w.queue.ListWorkspaceReviews(t.Context(), query); !errors.Is(err, c.want) || len(page.Items) != 0 {
				t.Errorf("got %+v and %v, want nothing and %v", page, err, c.want)
			}
			if !w.untouched() {
				t.Errorf("Product Governance was asked %+v, the store %+v and %+v was recorded", w.scopes.asked, w.store.asked, w.audited)
			}
		})
	}
	for _, workspace := range []string{"", "not-a-workspace", strings.ToUpper(queueWorkspace)} {
		w := newQueueWorld(t, ports.WorkspaceScope{Found: true, Known: true}, 3)
		query := asking(10)
		query.WorkspaceID = workspace
		if _, err := w.queue.ListWorkspaceReviews(t.Context(), query); !errors.Is(err, application.ErrWorkspaceNotFound) {
			t.Errorf("the workspace %q: got %v, want %v", workspace, err, application.ErrWorkspaceNotFound)
		}
		if want := []queueScopeAsked{{queueCaller, queueProduct, workspace}}; !reflect.DeepEqual(w.scopes.asked, want) || len(w.store.asked) != 0 {
			t.Errorf("the workspace %q: Product Governance was asked %+v and the store %+v, want it asked as it was named", workspace, w.scopes.asked, w.store.asked)
		}
	}
}

// A refusal that cannot be recorded is a refusal all the same, and says that it
// could not be, for whoever operates the service. It changes nothing of what
// the caller is told. What is not recorded is not reported as lost.
func TestARefusalThatCannotBeAuditedIsStillARefusalOfTheQueue(t *testing.T) {
	gone := errors.New("the audit trail is unavailable")
	for name, c := range map[string]struct {
		scope    ports.WorkspaceScope
		want     error
		recorded bool
	}{
		"a stranger":                  {ports.WorkspaceScope{Found: true, WorkspaceFound: true}, application.ErrProductNotFound, true},
		"no workspace to see":         {ports.WorkspaceScope{Found: true, Known: true, WorkspaceFound: true}, application.ErrWorkspaceNotFound, true},
		"an owner that reads nothing": {ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true}, application.ErrReviewsForbidden, true},
		"a product that is not there": {ports.WorkspaceScope{}, application.ErrProductNotFound, false},
		"a workspace that is not":     {ports.WorkspaceScope{Found: true, Known: true, Sees: true}, application.ErrWorkspaceNotFound, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newQueueWorld(t, c.scope, 3)
			w.auditErr = gone
			_, err := w.queue.ListWorkspaceReviews(t.Context(), asking(10))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want it to say %v", err, c.want)
			}
			if reported := errors.Is(err, application.ErrReviewNotAudited); reported != c.recorded {
				t.Errorf("it says that it could not be audited: %t, want %t (%v)", reported, c.recorded, err)
			}
			if c.recorded && !strings.Contains(err.Error(), gone.Error()) {
				t.Errorf("%v does not say why", err)
			}
			if len(w.store.asked) != 0 {
				t.Errorf("the store was asked %+v", w.store.asked)
			}
		})
	}
}

// What Product Governance or the store cannot answer fails the page, with
// what they said: it is no refusal and is not recorded as one, and what the
// store cannot read back is not handed out.
func TestAQueueThatCannotBeReadFailsAndRefusesNobody(t *testing.T) {
	reads := ports.WorkspaceScope{Found: true, Known: true, Sees: true, WorkspaceFound: true, Reads: true}
	gone := errors.New("the database is gone")

	w := newQueueWorld(t, reads, 3)
	w.scopes.err = gone
	if _, err := w.queue.ListWorkspaceReviews(t.Context(), asking(10)); !errors.Is(err, gone) ||
		errors.Is(err, application.ErrReviewsForbidden) || len(w.store.asked) != 0 || len(w.audited) != 0 {
		t.Errorf("Product Governance that fails: got %v, the store was asked %+v and %+v was recorded", err, w.store.asked, w.audited)
	}
	// A refusal that Product Governance gives with its failure is one: the
	// caller named nobody, say.
	w = newQueueWorld(t, ports.WorkspaceScope{}, 3)
	w.scopes.err = errors.Join(gone, application.ErrReviewsForbidden)
	if _, err := w.queue.ListWorkspaceReviews(t.Context(), asking(10)); !errors.Is(err, application.ErrReviewsForbidden) || len(w.store.asked) != 0 {
		t.Errorf("a refusal of Product Governance: got %v and the store was asked %+v", err, w.store.asked)
	}

	for _, failure := range []error{gone, domain.ErrReviewDamaged} {
		w = newQueueWorld(t, reads, 3)
		w.store.err = failure
		if page, err := w.queue.ListWorkspaceReviews(t.Context(), asking(10)); !errors.Is(err, failure) || len(page.Items) != 0 || page.Next != nil || len(w.audited) != 0 {
			t.Errorf("a store that says %v: got %+v and %v, with %+v recorded", failure, page, err, w.audited)
		}
	}
}

// A queue is made of what it needs, or it is not made.
func TestTheQueueIsWiredCompletelyOrNotAtAll(t *testing.T) {
	complete := application.QueueDependencies{
		Reviews: &queueStore{}, Scopes: &queueScopes{},
		Audit: func(context.Context, ports.AuditEntry) error { return nil },
	}
	if queue, err := application.NewReviewQueue(complete); err != nil || queue == nil {
		t.Fatalf("a complete wiring: %v", err)
	}
	for name, remove := range map[string]func(*application.QueueDependencies){
		"the reviews": func(d *application.QueueDependencies) { d.Reviews = nil },
		"the scopes":  func(d *application.QueueDependencies) { d.Scopes = nil },
		"the audit":   func(d *application.QueueDependencies) { d.Audit = nil },
	} {
		incomplete := complete
		remove(&incomplete)
		if queue, err := application.NewReviewQueue(incomplete); err == nil || queue != nil {
			t.Errorf("a wiring without %s was accepted", name)
		}
	}
}
