package application_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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
	rita        = domain.Principal{Issuer: "https://issuer.example", Subject: "rita"}
	shipment    = domain.RevisionRef{ProductID: "product", WorkspaceID: "workspace", AssetID: "asset", RevisionID: "revision"}
	closure     = "sha256:" + strings.Repeat("a", 64)
	partner     = []domain.Target{{Audience: "partner", Environment: "sandbox"}}

	errNoReading = errors.New("the caller may not read the revision")
)

// world stands in for everything the use cases of Policy & Review reach
// here: the registry and its revision, and the checks and the reviews that
// are stored. Nothing writes a review any more, so the reviews are what the
// test put there, as storage holds those of before.
type world struct {
	ports.CheckRuns

	// What the registry holds of the revision and says of the caller.
	digest  string
	reading error

	runs    map[string]domain.CheckRun
	reviews map[string]domain.Review

	now time.Time
	ids int
}

func newWorld() *world {
	return &world{
		digest: closure, runs: map[string]domain.CheckRun{}, reviews: map[string]domain.Review{},
		now: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC),
	}
}

func (w *world) WithinTransaction(ctx context.Context, work func(context.Context) error) error {
	return work(ctx)
}

func (w *world) Revision(context.Context, ports.Target) (domain.RevisionRef, string, error) {
	if w.reading != nil {
		return domain.RevisionRef{}, "", w.reading
	}
	return shipment, w.digest, nil
}

func (w *world) Source(context.Context, ports.Target) (domain.RevisionRef, domain.Source, error) {
	return domain.RevisionRef{}, domain.Source{}, errors.New("these use cases read no source")
}

func (w *world) LockAuthor(context.Context, domain.Principal, domain.RevisionRef) (bool, bool, error) {
	return false, false, errors.New("these use cases lock no author")
}

func (w *world) Find(_ context.Context, revision domain.RevisionRef, id string) (domain.CheckRun, bool, error) {
	run, found := w.runs[id]
	return run, found && run.Revision == revision, nil
}

type reviewStore struct{ *world }

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
	return application.ReviewDependencies{Revisions: w, Checks: w, Reviews: reviewStore{w}}
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

// recorded stores a review that was asked for and concluded before reviews
// were retired: erin asked on the check, and rita concluded it as the
// outcome says, a minute later.
func (w *world) recorded(id, check string, outcome domain.ReviewOutcome) domain.Review {
	request := domain.ReviewRequest{
		ID: id, Revision: shipment, CheckRunID: check, InputDigest: closure, Scope: partner,
		RequestedBy: erin, RequestedAt: w.Now(),
	}
	conclusion := domain.Conclusion{ReviewID: id, Outcome: outcome, ConcludedBy: rita, ConcludedAt: w.Now()}
	switch outcome {
	case domain.ReviewApproved:
		conclusion.ValidUntil = conclusion.ConcludedAt.Add(90 * 24 * time.Hour)
	case domain.ReviewRejected:
		conclusion.Reason = "the 400 response lost its problem body"
	}
	review := domain.Review{Request: request, Conclusion: &conclusion}
	w.reviews[id] = review
	return review
}

func target(caller domain.Principal) ports.Target {
	return ports.Target{Caller: caller, ProductID: "product", AssetID: "asset", RevisionID: "revision", CorrelationID: "correlation"}
}

// The reviews of a revision that were recorded before reviews were retired
// are read for whoever may read the revision, as long as they are reviews of
// what the revision still is.
func TestReviewsAreReadForWhoMayReadTheRevision(t *testing.T) {
	w := newWorld()
	w.check(t, "check", rulesTool)
	asked := []domain.Review{
		w.recorded("review-1", "check", domain.ReviewWithdrawn),
		w.recorded("review-2", "check", domain.ReviewRejected),
		w.recorded("review-3", "check", domain.ReviewApproved),
	}
	reviews := w.reviewing(t)

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
		"revisions": func(d *application.ReviewDependencies) { d.Revisions = nil },
		"checks":    func(d *application.ReviewDependencies) { d.Checks = nil },
		"reviews":   func(d *application.ReviewDependencies) { d.Reviews = nil },
	} {
		dependencies := newWorld().dependencies()
		drop(&dependencies)
		if reviews, err := application.NewReviews(dependencies); err == nil || reviews != nil {
			t.Errorf("without %s: built %v", name, reviews)
		}
	}
}
