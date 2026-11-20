package application_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	rulesTool   = domain.Tool{Name: "fabric-openapi-rules", Version: "3"}
	compareTool = domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"}
	carol       = domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}
	erin        = domain.Principal{Issuer: "https://issuer.example", Subject: "erin"}
	shipment    = domain.RevisionRef{ProductID: "product", WorkspaceID: "workspace", AssetID: "asset", RevisionID: "revision"}
	closure     = "sha256:" + strings.Repeat("a", 64)
	partner     = []domain.Target{{Audience: "partner", Environment: "sandbox"}}

	errNoReading = errors.New("the caller may not read the revision")
	errStatus    = errors.New("the registry: the revision is not at a status this starts from")
	errOutbox    = errors.New("the outbox is unavailable")
)

// world stands in for everything the use cases of a review reach: the
// registry and its revision, the checks and the reviews that are stored, the
// audit trail and the events. A transaction that fails leaves it as it was.
type world struct {
	ports.CheckRuns

	// What the registry holds of the revision and says of the caller.
	status         string
	version        int64
	digest         string
	reading        error
	author, active bool
	// reviewer says that the caller holds the reviewer grant.
	reviewer bool
	moves    []string
	// holds counts the times the revision was held as it is.
	holds int

	runs     map[string]domain.CheckRun
	reviews  map[string]domain.Review
	versions map[string]int64
	audited  []ports.AuditEntry
	events   []string
	announce error

	now time.Time
	ids int
}

func newWorld() *world {
	return &world{
		status: "DRAFT", version: 1, digest: closure, author: true, active: true,
		runs: map[string]domain.CheckRun{}, reviews: map[string]domain.Review{}, versions: map[string]int64{},
		now: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC),
	}
}

func (w *world) WithinTransaction(ctx context.Context, work func(context.Context) error) error {
	before := *w
	before.reviews, before.versions = maps.Clone(w.reviews), maps.Clone(w.versions)
	before.moves, before.audited, before.events = slices.Clone(w.moves), slices.Clone(w.audited), slices.Clone(w.events)
	if err := work(ctx); err != nil {
		now, ids := w.now, w.ids
		*w = before
		w.now, w.ids = now, ids
		return err
	}
	return nil
}

func (w *world) Revision(context.Context, ports.Target) (domain.RevisionRef, string, error) {
	if w.reading != nil {
		return domain.RevisionRef{}, "", w.reading
	}
	return shipment, w.digest, nil
}

func (w *world) Source(context.Context, ports.Target) (domain.RevisionRef, domain.Source, error) {
	return domain.RevisionRef{}, domain.Source{}, errors.New("a review reads no source")
}

func (w *world) LockAuthor(context.Context, domain.Principal, domain.RevisionRef) (bool, bool, error) {
	return w.author, w.active, nil
}

func (w *world) LockReviewer(context.Context, domain.Principal, domain.RevisionRef) (bool, bool, error) {
	return w.reviewer, w.active, nil
}

// Recorder is who recorded the revision: dave, for whoever may read it.
func (w *world) Recorder(context.Context, ports.Target) (domain.Principal, error) {
	return dave, w.reading
}

// Move makes the moves the registry has, and refuses the others.
func (w *world) Move(_ context.Context, _ domain.RevisionRef, to string) (int64, error) {
	allowed := map[string][]string{"DRAFT": {"IN_REVIEW"}, "WITHDRAWN": {"IN_REVIEW"}, "IN_REVIEW": {"APPROVED", "REJECTED", "WITHDRAWN"}}
	if !slices.Contains(allowed[w.status], to) {
		return 0, errStatus
	}
	w.status, w.version = to, w.version+1
	w.moves = append(w.moves, to)
	return w.version, nil
}

// Hold says where the registry has the revision and changes nothing.
func (w *world) Hold(context.Context, domain.RevisionRef) (string, int64, error) {
	w.holds++
	return w.status, w.version, nil
}

func (w *world) Find(_ context.Context, revision domain.RevisionRef, id string) (domain.CheckRun, bool, error) {
	run, found := w.runs[id]
	return run, found && run.Revision == revision, nil
}

type reviewStore struct{ *world }

// Request keeps a request as the tables do: one that put the revision in
// review is the only one at the version the revision took then, and
// supplementary requests share the version of the approved revision.
func (s reviewStore) Request(_ context.Context, request domain.ReviewRequest, version int64) error {
	for id, taken := range s.versions {
		if taken == version && !request.Supplementary && !s.reviews[id].Request.Supplementary {
			return errors.New("the revision is in review under another request at this version")
		}
	}
	s.reviews[request.ID], s.versions[request.ID] = domain.Review{Request: request}, version
	return nil
}

func (s reviewStore) Open(_ context.Context, revision domain.RevisionRef) (bool, error) {
	for _, review := range s.reviews {
		if review.Request.Revision == revision && review.Conclusion == nil {
			return true, nil
		}
	}
	return false, nil
}

func (s reviewStore) Conclude(_ context.Context, conclusion domain.Conclusion) error {
	review, found := s.reviews[conclusion.ReviewID]
	if !found || review.Conclusion != nil {
		return errors.New("the review has its conclusion")
	}
	review.Conclusion = &conclusion
	s.reviews[conclusion.ReviewID] = review
	return nil
}

func (s reviewStore) Find(_ context.Context, revision domain.RevisionRef, id string) (domain.Review, bool, error) {
	review, found := s.reviews[id]
	return review, found && review.Request.Revision == revision, nil
}

func (s reviewStore) List(_ context.Context, revision domain.RevisionRef, after *ports.Position, limit int) ([]domain.Review, error) {
	var listed []domain.Review
	for _, review := range s.reviews {
		request := review.Request
		if request.Revision != revision {
			continue
		}
		if after != nil && (request.RequestedAt.Before(after.At) || (request.RequestedAt.Equal(after.At) && request.ID <= after.ID)) {
			continue
		}
		listed = append(listed, review)
	}
	slices.SortFunc(listed, func(a, b domain.Review) int {
		return cmp.Or(a.Request.RequestedAt.Compare(b.Request.RequestedAt), strings.Compare(a.Request.ID, b.Request.ID))
	})
	return listed[:min(limit, len(listed))], nil
}

type reviewEvents struct{ *world }

func (e reviewEvents) Requested(_ context.Context, request domain.ReviewRequest, _ string) error {
	e.events = append(e.events, "requested "+request.ID)
	return e.announce
}

func (e reviewEvents) Concluded(_ context.Context, review domain.Review, _ string) error {
	e.events = append(e.events, "concluded "+review.Request.ID+" "+review.Status())
	return e.announce
}

type toolOf domain.Tool

func (t toolOf) Tool() domain.Tool                                          { return domain.Tool(t) }
func (t toolOf) Evaluate(domain.Source, []domain.Rule) []domain.Observation { return nil }
func (t toolOf) Compare(_, _ domain.Source) domain.Comparison               { return domain.Comparison{} }

func (w *world) Now() time.Time {
	w.now = w.now.Add(time.Minute)
	return w.now
}

func (w *world) NewID() string {
	w.ids++
	return fmt.Sprintf("id-%d", w.ids)
}

func (w *world) dependencies() application.ReviewDependencies {
	return application.ReviewDependencies{
		Transactions: w, Revisions: w, Checks: w, Reviews: reviewStore{w},
		Evaluator: toolOf(rulesTool), Comparator: toolOf(compareTool),
		Audit: func(_ context.Context, entry ports.AuditEntry) error {
			w.audited = append(w.audited, entry)
			return nil
		},
		Events: reviewEvents{w}, Clock: w, Identifiers: w,
	}
}

func (w *world) reviewing(t *testing.T) *application.Reviews {
	t.Helper()
	reviews, err := application.NewReviews(w.dependencies())
	if err != nil {
		t.Fatal(err)
	}
	return reviews
}

// check stores a check of the revision under the baseline, by the tool and
// with what it observed.
func (w *world) check(t *testing.T, id string, tool domain.Tool, observed ...domain.Observation) domain.CheckRun {
	t.Helper()
	source := domain.Source{
		Entrypoint: "openapi.yaml", Digest: closure, Files: []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
	}
	run, err := domain.Complete(domain.Draft{
		ID: id, PolicySnapshotID: "snapshot", Revision: shipment, RequestedBy: carol, StartedAt: w.now.Add(-time.Hour),
	}, domain.Baseline(), source, tool, observed, w.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w.runs[id] = run
	return run
}

func target(caller domain.Principal) ports.Target {
	return ports.Target{Caller: caller, ProductID: "product", AssetID: "asset", RevisionID: "revision", CorrelationID: "correlation"}
}

// leaked is a credential the secret scan found: a rule nothing waives.
var leaked = domain.Observation{RuleID: "F04-GOV-004", Findings: []domain.Finding{{
	RuleID: "F04-GOV-004", Path: "openapi.yaml", Line: 1, Message: "the line holds what has the form of a credential",
}}}

// An author of the workspace asks for a decision on a draft, on a check a
// decision can rest on. The request, the move of the revision, the audit
// record and the event are made together; whatever is refused leaves the
// revision where it was and nothing behind but, for a refusal of who the
// caller is, its audit record.
func TestAnAuthorAsksForAReviewOfADraftOnACurrentCheck(t *testing.T) {
	t.Run("a draft and a current check", func(t *testing.T) {
		w := newWorld()
		run := w.check(t, "check", rulesTool)
		review, err := w.reviewing(t).RequestReview(context.Background(), target(carol),
			application.RequestReview{CheckRunID: "check", Scope: partner, Note: "ready"})
		if err != nil {
			t.Fatal(err)
		}
		want := domain.Review{Request: domain.ReviewRequest{
			ID: "id-1", Revision: shipment, CheckRunID: run.ID, InputDigest: closure, Scope: partner, Note: "ready",
			RequestedBy: carol, RequestedAt: w.now,
		}}
		if !reflect.DeepEqual(review, want) || !reflect.DeepEqual(w.reviews["id-1"], want) || w.versions["id-1"] != 2 {
			t.Errorf("asked %+v and stored %+v at version %d, want %+v at version 2", review, w.reviews["id-1"], w.versions["id-1"], want)
		}
		if w.status != "IN_REVIEW" || !reflect.DeepEqual(w.moves, []string{"IN_REVIEW"}) {
			t.Errorf("the revision is %s after the moves %v, want it submitted once", w.status, w.moves)
		}
		audit := ports.AuditEntry{Target: target(carol), ReviewID: "id-1", Action: "review.request", Rule: "AC-50", Allowed: true}
		if !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) || !reflect.DeepEqual(w.events, []string{"requested id-1"}) {
			t.Errorf("audited %+v and announced %v, want %+v and one request", w.audited, w.events, audit)
		}
	})

	refusal := ports.AuditEntry{Target: target(carol), Action: "review.request", Rule: "DENY-03"}
	refused := map[string]struct {
		arrange func(*testing.T, *world)
		command application.RequestReview
		want    error
		audited []ports.AuditEntry
	}{
		"someone who may not read the revision": {
			arrange: func(_ *testing.T, w *world) { w.reading = errNoReading }, want: errNoReading},
		"a check nobody recorded": {command: application.RequestReview{CheckRunID: "another"}, want: application.ErrCheckRunNotFound},
		"a check of another revision": {
			arrange: func(_ *testing.T, w *world) {
				run := w.runs["check"]
				run.Revision.RevisionID = "another"
				w.runs["check"] = run
			}, want: application.ErrCheckRunNotFound},
		"a check that read something else": {
			arrange: func(_ *testing.T, w *world) { w.digest = "sha256:" + strings.Repeat("b", 64) }, want: domain.ErrCheckRunDamaged},
		"someone who is no author": {
			arrange: func(_ *testing.T, w *world) { w.author = false }, want: application.ErrForbidden, audited: []ports.AuditEntry{refusal}},
		"someone who is no author, with a scope that is none": {
			arrange: func(_ *testing.T, w *world) { w.author = false }, command: application.RequestReview{CheckRunID: "check"},
			want: application.ErrForbidden, audited: []ports.AuditEntry{refusal}},
		"a suspended product": {arrange: func(_ *testing.T, w *world) { w.active = false }, want: application.ErrProductSuspended},
		"a check by another version of the tool": {
			arrange: func(t *testing.T, w *world) { w.check(t, "check", domain.Tool{Name: rulesTool.Name, Version: "2"}) }, want: domain.ErrCheckStale},
		"a check in which a rule nothing waives failed": {
			arrange: func(t *testing.T, w *world) { w.check(t, "check", rulesTool, leaked) }, want: domain.ErrReviewBlocked},
		"a scope that is none":            {command: application.RequestReview{CheckRunID: "check"}, want: domain.ErrInvalidReview},
		"a revision that is in review":    {arrange: func(_ *testing.T, w *world) { w.status, w.version = "IN_REVIEW", 2 }, want: errStatus},
		"a revision that is rejected":     {arrange: func(_ *testing.T, w *world) { w.status, w.version = "REJECTED", 3 }, want: errStatus},
		"an event that cannot be written": {arrange: func(_ *testing.T, w *world) { w.announce = errOutbox }, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			w.check(t, "check", rulesTool)
			if c.arrange != nil {
				c.arrange(t, w)
			}
			command := c.command
			if command.CheckRunID == "" {
				command = application.RequestReview{CheckRunID: "check", Scope: partner}
			}
			status, version := w.status, w.version
			review, err := w.reviewing(t).RequestReview(context.Background(), target(carol), command)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(review, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", review, err, c.want)
			}
			if len(w.reviews) != 0 || len(w.events) != 0 || len(w.moves) != 0 || w.status != status || w.version != version {
				t.Errorf("%d reviews, events %v, moves %v, the revision %s at %d; want nothing and the revision %s at %d",
					len(w.reviews), w.events, w.moves, w.status, w.version, status, version)
			}
			if !reflect.DeepEqual(w.audited, c.audited) {
				t.Errorf("audited %+v, want %+v", w.audited, c.audited)
			}
		})
	}
}

// Whoever asked for a review takes it back while it is open and while still
// an author. The revision is withdrawn then, and can be submitted again.
func TestWhoAskedForAReviewWithdrawsItWhileItIsOpen(t *testing.T) {
	asked := func(t *testing.T) (*world, *application.Reviews, domain.Review) {
		t.Helper()
		w := newWorld()
		w.check(t, "check", rulesTool)
		reviews := w.reviewing(t)
		review, err := reviews.RequestReview(context.Background(), target(carol), application.RequestReview{CheckRunID: "check", Scope: partner})
		if err != nil {
			t.Fatal(err)
		}
		w.audited, w.events, w.moves = nil, nil, nil
		return w, reviews, review
	}

	t.Run("whoever asked, while it is open", func(t *testing.T) {
		w, reviews, review := asked(t)
		withdrawn, err := reviews.WithdrawReview(context.Background(), target(carol), review.Request.ID, "the wrong baseline")
		if err != nil {
			t.Fatal(err)
		}
		conclusion := domain.Conclusion{
			ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, Reason: "the wrong baseline", ConcludedBy: carol, ConcludedAt: w.now,
		}
		want := domain.Review{Request: review.Request, Conclusion: &conclusion}
		if !reflect.DeepEqual(withdrawn, want) || !reflect.DeepEqual(w.reviews[review.Request.ID], want) {
			t.Errorf("withdrawn as %+v and stored as %+v, want %+v", withdrawn, w.reviews[review.Request.ID], want)
		}
		audit := ports.AuditEntry{Target: target(carol), ReviewID: review.Request.ID, Action: "review.withdraw", Rule: "AC-51", Allowed: true}
		if w.status != "WITHDRAWN" || w.version != 3 || !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) ||
			!reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " WITHDRAWN"}) {
			t.Errorf("the revision is %s at %d, audited %+v, announced %v", w.status, w.version, w.audited, w.events)
		}

		// What was withdrawn is submitted again, as the next review.
		again, err := reviews.RequestReview(context.Background(), target(carol), application.RequestReview{CheckRunID: "check", Scope: partner})
		if err != nil || again.Request.ID == review.Request.ID || w.status != "IN_REVIEW" || w.versions[again.Request.ID] != 4 || len(w.reviews) != 2 {
			t.Errorf("submitted again: %+v, %v; the revision is %s and %d reviews are stored", again, err, w.status, len(w.reviews))
		}
	})

	refused := map[string]struct {
		arrange func(*world, domain.Review)
		caller  domain.Principal
		id      string
		want    error
		denied  bool
	}{
		"someone who may not read the revision": {arrange: func(w *world, _ domain.Review) { w.reading = errNoReading }, caller: carol, want: errNoReading},
		"a review nobody asked for":             {caller: carol, id: "another", want: application.ErrReviewNotFound},
		"whoever asked, no longer an author":    {arrange: func(w *world, _ domain.Review) { w.author = false }, caller: carol, want: application.ErrForbidden, denied: true},
		"another author":                        {caller: erin, want: domain.ErrNotRequester, denied: true},
		"a review that is concluded": {
			arrange: func(w *world, review domain.Review) {
				conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, ConcludedBy: carol, ConcludedAt: w.now}
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
				w.status, w.version = "WITHDRAWN", 3
			}, caller: carol, want: domain.ErrReviewConcluded},
		"a revision the registry has moved on": {
			arrange: func(w *world, _ domain.Review) { w.status, w.version = "APPROVED", 3 }, caller: carol, want: errStatus},
		"an event that cannot be written": {arrange: func(w *world, _ domain.Review) { w.announce = errOutbox }, caller: carol, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := asked(t)
			if c.arrange != nil {
				c.arrange(w, review)
			}
			id := cmp.Or(c.id, review.Request.ID)
			stored, status, version := w.reviews[review.Request.ID], w.status, w.version
			got, err := reviews.WithdrawReview(context.Background(), target(c.caller), id, "")
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
			if !reflect.DeepEqual(w.reviews[review.Request.ID], stored) || w.status != status || w.version != version || len(w.events) != 0 || len(w.moves) != 0 {
				t.Errorf("the review is %+v and the revision %s at %d after events %v and moves %v; want both as they were",
					w.reviews[review.Request.ID], w.status, w.version, w.events, w.moves)
			}
			var audited []ports.AuditEntry
			if c.denied {
				audited = []ports.AuditEntry{{Target: target(c.caller), Action: "review.withdraw", Rule: "DENY-03"}}
			}
			if !reflect.DeepEqual(w.audited, audited) {
				t.Errorf("audited %+v, want %+v", w.audited, audited)
			}
		})
	}
}

// The reviews of a revision are read for whoever may read the revision, as
// long as they are reviews of what the revision still is.
func TestReviewsAreReadForWhoMayReadTheRevision(t *testing.T) {
	w := newWorld()
	w.check(t, "check", rulesTool)
	reviews := w.reviewing(t)
	var asked []domain.Review
	for range 3 {
		review, err := reviews.RequestReview(context.Background(), target(carol), application.RequestReview{CheckRunID: "check", Scope: partner})
		if err != nil {
			t.Fatal(err)
		}
		if review, err = reviews.WithdrawReview(context.Background(), target(carol), review.Request.ID, ""); err != nil {
			t.Fatal(err)
		}
		asked = append(asked, review)
	}
	w.audited, w.events = nil, nil

	if got, err := reviews.GetReview(context.Background(), target(erin), asked[1].Request.ID); err != nil || !reflect.DeepEqual(got, asked[1]) {
		t.Errorf("read %+v, want %+v: %v", got, asked[1], err)
	}
	if _, err := reviews.GetReview(context.Background(), target(erin), "another"); !errors.Is(err, application.ErrReviewNotFound) {
		t.Errorf("a review nobody asked for: %v", err)
	}
	page, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin)})
	if err != nil || !reflect.DeepEqual(page.Items, asked) || page.Next != nil {
		t.Errorf("the history: %+v, %v; want the three in the order they were asked for and no next page", page, err)
	}
	first, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin), PageSize: 2})
	want := &ports.Position{At: asked[1].Request.RequestedAt, ID: asked[1].Request.ID}
	if err != nil || !reflect.DeepEqual(first.Items, asked[:2]) || !reflect.DeepEqual(first.Next, want) {
		t.Fatalf("the first page of two: %+v, %v; want two reviews and the position of the second", first, err)
	}
	last, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin), PageSize: 2, After: first.Next})
	if err != nil || !reflect.DeepEqual(last.Items, asked[2:]) || last.Next != nil {
		t.Errorf("the page behind it: %+v, %v; want the third review and no next page", last, err)
	}
	if full, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin), PageSize: 3}); err != nil || len(full.Items) != 3 || full.Next != nil {
		t.Errorf("a page that holds exactly what there is: %+v, %v; want no next page", full, err)
	}
	for _, size := range []int{-1, 101} {
		if _, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin), PageSize: size}); !errors.Is(err, application.ErrInvalidReviewPage) {
			t.Errorf("a page of %d: %v, want %v", size, err, application.ErrInvalidReviewPage)
		}
	}
	if len(w.audited) != 0 || len(w.events) != 0 {
		t.Errorf("reading left audit records %+v and events %v", w.audited, w.events)
	}

	// A review of content the revision no longer has is not a review of it.
	w.digest = "sha256:" + strings.Repeat("b", 64)
	if _, err := reviews.GetReview(context.Background(), target(erin), asked[0].Request.ID); !errors.Is(err, domain.ErrReviewDamaged) || !strings.Contains(err.Error(), "review "+asked[0].Request.ID) {
		t.Errorf("a review of other content: %v, want %v naming the review", err, domain.ErrReviewDamaged)
	}
	if _, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin)}); !errors.Is(err, domain.ErrReviewDamaged) {
		t.Errorf("the history of other content: %v, want %v", err, domain.ErrReviewDamaged)
	}
	w.digest, w.reading = closure, errNoReading
	if _, err := reviews.GetReview(context.Background(), target(erin), asked[0].Request.ID); !errors.Is(err, errNoReading) {
		t.Errorf("someone who may not read the revision reads a review: %v", err)
	}
	if _, err := reviews.ListReviews(context.Background(), application.ListReviews{Target: target(erin)}); !errors.Is(err, errNoReading) {
		t.Errorf("someone who may not read the revision lists reviews: %v", err)
	}
}

func TestTheReviewsNeedEverythingTheyReach(t *testing.T) {
	for name, drop := range map[string]func(*application.ReviewDependencies){
		"transactions": func(d *application.ReviewDependencies) { d.Transactions = nil },
		"revisions":    func(d *application.ReviewDependencies) { d.Revisions = nil },
		"checks":       func(d *application.ReviewDependencies) { d.Checks = nil },
		"reviews":      func(d *application.ReviewDependencies) { d.Reviews = nil },
		"an evaluator": func(d *application.ReviewDependencies) { d.Evaluator = nil },
		"a comparator": func(d *application.ReviewDependencies) { d.Comparator = nil },
		"audit":        func(d *application.ReviewDependencies) { d.Audit = nil },
		"events":       func(d *application.ReviewDependencies) { d.Events = nil },
		"a clock":      func(d *application.ReviewDependencies) { d.Clock = nil },
		"identifiers":  func(d *application.ReviewDependencies) { d.Identifiers = nil },
	} {
		dependencies := newWorld().dependencies()
		drop(&dependencies)
		if reviews, err := application.NewReviews(dependencies); err == nil || reviews != nil {
			t.Errorf("without %s: built %v", name, reviews)
		}
	}
}
