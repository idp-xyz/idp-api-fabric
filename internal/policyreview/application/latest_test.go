package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

const (
	latestProduct   = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	latestElsewhere = "2d0b5f8c-63e1-4a9f-87b2-a1d4e3f5a6b7"
	latestWorkspace = "b0000000-0000-4000-8000-000000000001"
	latestDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherDigest     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func revisionOf(product string, n int) domain.RevisionRef {
	return domain.RevisionRef{
		ProductID: product, WorkspaceID: latestWorkspace,
		AssetID:    fmt.Sprintf("a0000000-0000-4000-8000-%012d", n),
		RevisionID: fmt.Sprintf("c0000000-0000-4000-8000-%012d", n),
	}
}

func expecting(product string, n int) application.Expected {
	return application.Expected{Revision: revisionOf(product, n), InputDigest: latestDigest}
}

func checkOf(revision domain.RevisionRef, digest string, status domain.Status) domain.Overview {
	return domain.Overview{ID: "d" + revision.RevisionID[1:], Revision: revision, InputDigest: digest, Status: status}
}

// lastChecks stands in for the storage of check runs: it answers what the
// test says and keeps what it was asked. The real one is tested against
// PostgreSQL where it lives.
type lastChecks struct {
	checks  []domain.Overview
	failure error
	asked   []checksAsked
}

type checksAsked struct {
	product   string
	revisions []domain.RevisionRef
	status    domain.Status
}

func (s *lastChecks) Latest(_ context.Context, product string, revisions []domain.RevisionRef, status domain.Status) ([]domain.Overview, error) {
	s.asked = append(s.asked, checksAsked{product, append([]domain.RevisionRef(nil), revisions...), status})
	return s.checks, s.failure
}

func latestOver(t *testing.T, store ports.LatestChecks) *application.Latest {
	t.Helper()

	latest, err := application.NewLatest(store)
	if err != nil {
		t.Fatalf("wire the read of last checks: %v", err)
	}
	return latest
}

// What the storage says of the revisions of a product is what is answered,
// the status asked for is passed on as it was asked for, and a failure of the
// storage is the failure of the read.
func TestTheLastChecksAreWhatTheStorageSaysOfTheProductAndTheRevisionsNamed(t *testing.T) {
	first, second := revisionOf(latestProduct, 1), revisionOf(latestProduct, 2)
	said := []domain.Overview{checkOf(first, latestDigest, domain.StatusFail), checkOf(second, latestDigest, domain.StatusUnknown)}
	store := &lastChecks{checks: said}
	expected := []application.Expected{expecting(latestProduct, 1), expecting(latestProduct, 2)}

	for _, status := range []domain.Status{"", domain.StatusFail} {
		store.asked = nil
		got, err := latestOver(t, store).Of(t.Context(), latestProduct, expected, status)
		if err != nil || !reflect.DeepEqual(got, said) {
			t.Errorf("the last checks, of the status %q, are %+v (%v), want %+v", status, got, err, said)
		}
		if want := []checksAsked{{latestProduct, []domain.RevisionRef{first, second}, status}}; !reflect.DeepEqual(store.asked, want) {
			t.Errorf("the storage was asked %+v, want %+v", store.asked, want)
		}
	}

	broken := errors.New("the storage is gone")
	if got, err := latestOver(t, &lastChecks{failure: broken}).Of(t.Context(), latestProduct, expected, ""); !errors.Is(err, broken) || len(got) != 0 {
		t.Errorf("a storage that fails: %+v (%v), want no checks and %v", got, err, broken)
	}
}

// A check that did not read the closure the revision has now is not the check
// of that revision, as a read of its history says: the whole read fails with
// ErrCheckRunDamaged, as does the storage handing over the check of a revision
// that was not asked about.
func TestACheckThatDidNotReadWhatTheRevisionHasNowFailsTheRead(t *testing.T) {
	first, second := revisionOf(latestProduct, 1), revisionOf(latestProduct, 2)
	expected := []application.Expected{expecting(latestProduct, 1), expecting(latestProduct, 2)}
	for name, checks := range map[string][]domain.Overview{
		"a check of another closure":                   {checkOf(first, latestDigest, domain.StatusFail), checkOf(second, otherDigest, domain.StatusFail)},
		"a check that read no closure":                 {checkOf(first, "", domain.StatusFail)},
		"the check of a revision that was not asked":   {checkOf(first, latestDigest, domain.StatusFail), checkOf(revisionOf(latestProduct, 3), latestDigest, domain.StatusFail)},
		"the check of a revision of the other product": {checkOf(revisionOf(latestElsewhere, 1), latestDigest, domain.StatusFail)},
	} {
		got, err := latestOver(t, &lastChecks{checks: checks}).Of(t.Context(), latestProduct, expected, "")
		if !errors.Is(err, domain.ErrCheckRunDamaged) || len(got) != 0 {
			t.Errorf("%s: %+v (%v), want no checks and %v", name, got, err, domain.ErrCheckRunDamaged)
		}
	}
}

// Nothing is read for no revisions. A set that is not revisions of one
// product named by their identifiers, that names one twice, that is more than a
// read takes, or a status that no check concludes, is refused whole, and the
// storage is asked of none of it.
func TestLastChecksAreNotReadOfNoRevisionsOrOfRevisionsThatAreNotOfTheProductNamedByIdentifiers(t *testing.T) {
	many := make([]application.Expected, 0, application.MaxLatestRevisions+1)
	for n := range application.MaxLatestRevisions + 1 {
		many = append(many, expecting(latestProduct, n))
	}
	with := func(change func(*domain.RevisionRef)) []application.Expected {
		revision := revisionOf(latestProduct, 1)
		change(&revision)
		return []application.Expected{expecting(latestProduct, 2), {Revision: revision, InputDigest: latestDigest}}
	}
	one := []application.Expected{expecting(latestProduct, 1)}

	for name, c := range map[string]struct {
		product  string
		expected []application.Expected
		status   domain.Status
	}{
		"a key and not an identifier":           {"cargopai", one, ""},
		"no product":                            {"", one, ""},
		"an identifier in capitals":             {strings.ToUpper(latestProduct), one, ""},
		"an identifier and something more":      {latestProduct + "0", one, ""},
		"an identifier and a line":              {latestProduct + "\n", one, ""},
		"an identifier a character short":       {latestProduct[1:], one, ""},
		"no product and no revisions":           {"", nil, ""},
		"a revision that is no identifier":      {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID = "revision-1" }), ""},
		"a revision without an identifier":      {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID = "" }), ""},
		"a revision in capitals":                {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID = strings.ToUpper(r.RevisionID) }), ""},
		"an asset that is no identifier":        {latestProduct, with(func(r *domain.RevisionRef) { r.AssetID = "shipping-api" }), ""},
		"a Workspace that is no identifier":     {latestProduct, with(func(r *domain.RevisionRef) { r.WorkspaceID = "engineering" }), ""},
		"a Workspace that is not there":         {latestProduct, with(func(r *domain.RevisionRef) { r.WorkspaceID = "" }), ""},
		"a revision of another product":         {latestProduct, with(func(r *domain.RevisionRef) { r.ProductID = latestElsewhere }), ""},
		"a revision without a product":          {latestProduct, with(func(r *domain.RevisionRef) { r.ProductID = "" }), ""},
		"a revision named twice":                {latestProduct, []application.Expected{expecting(latestProduct, 1), expecting(latestProduct, 1)}, ""},
		"more revisions than one read takes":    {latestProduct, many, ""},
		"a status that is none":                 {latestProduct, one, "fail"},
		"the status of a rule that was not run": {latestProduct, one, domain.StatusNotRun},
		"a revision and something more":         {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID += "0" }), ""},
		"a revision and a line":                 {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID += "\n" }), ""},
		"a revision with a letter for a dash":   {latestProduct, with(func(r *domain.RevisionRef) { r.RevisionID = strings.Replace(r.RevisionID, "-", "g", 1) }), ""},
		"an asset and something more":           {latestProduct, with(func(r *domain.RevisionRef) { r.AssetID += "0" }), ""},
		"a Workspace and something more":        {latestProduct, with(func(r *domain.RevisionRef) { r.WorkspaceID += "0" }), ""},
	} {
		store := &lastChecks{checks: []domain.Overview{checkOf(revisionOf(latestProduct, 1), latestDigest, domain.StatusFail)}}
		got, err := latestOver(t, store).Of(t.Context(), c.product, c.expected, c.status)
		if !errors.Is(err, application.ErrInvalidRevisions) || len(got) != 0 {
			t.Errorf("%s: %+v (%v), want no checks and %v", name, got, err, application.ErrInvalidRevisions)
		}
		if len(store.asked) != 0 {
			t.Errorf("%s: the storage was asked %d times, want it never", name, len(store.asked))
		}
	}

	// No revisions are no checks, and no question; and as many as a read takes are asked.
	store := &lastChecks{checks: []domain.Overview{checkOf(revisionOf(latestProduct, 1), latestDigest, domain.StatusFail)}}
	for _, none := range [][]application.Expected{nil, {}} {
		if got, err := latestOver(t, store).Of(t.Context(), latestProduct, none, domain.StatusFail); err != nil || len(got) != 0 || len(store.asked) != 0 {
			t.Errorf("the last checks of no revision are %+v (%v) after %d questions, want none and none", got, err, len(store.asked))
		}
	}
	store.checks = nil
	if _, err := latestOver(t, store).Of(t.Context(), latestProduct, many[:application.MaxLatestRevisions], ""); err != nil ||
		len(store.asked) != 1 || len(store.asked[0].revisions) != application.MaxLatestRevisions {
		t.Errorf("%d revisions, as many as a read takes: %v, %d questions", application.MaxLatestRevisions, err, len(store.asked))
	}
}

// A character missing from any of the five groups of an identifier makes it no
// identifier, whichever of the four names it is the identifier of.
func TestAnIdentifierThatLacksACharacterOfAGroupIsRefusedWhicheverItNames(t *testing.T) {
	for group, at := range []int{0, 9, 14, 19, 24} {
		cut := func(id string) string { return id[:at] + id[at+1:] }
		revision := revisionOf(latestProduct, 1)
		bad := func(change func(*domain.RevisionRef)) application.Expected {
			changed := revision
			change(&changed)
			return application.Expected{Revision: changed, InputDigest: latestDigest}
		}
		for name, c := range map[string]struct {
			product  string
			expected application.Expected
		}{
			"the product":   {cut(latestProduct), expecting(latestProduct, 1)},
			"the revision":  {latestProduct, bad(func(r *domain.RevisionRef) { r.RevisionID = cut(r.RevisionID) })},
			"the asset":     {latestProduct, bad(func(r *domain.RevisionRef) { r.AssetID = cut(r.AssetID) })},
			"the Workspace": {latestProduct, bad(func(r *domain.RevisionRef) { r.WorkspaceID = cut(r.WorkspaceID) })},
		} {
			store := &lastChecks{}
			if got, err := latestOver(t, store).Of(t.Context(), c.product, []application.Expected{c.expected}, ""); !errors.Is(err, application.ErrInvalidRevisions) ||
				len(got) != 0 || len(store.asked) != 0 {
				t.Errorf("%s, with a character missing from group %d: %+v (%v), %d questions, want it refused and nothing asked",
					name, group+1, got, err, len(store.asked))
			}
		}
	}
}

func TestTheReadOfLastChecksIsWiredToTheStorageOfCheckRuns(t *testing.T) {
	if latest, err := application.NewLatest(nil); err == nil || latest != nil {
		t.Errorf("a read of last checks without storage was wired: %v, %v", latest, err)
	}
}
