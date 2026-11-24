package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	catalogports "go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// savedHeads stands in for what the registry keeps: the heads of assets. It
// answers those of the assets it is asked about, and keeps what it was asked.
type savedHeads struct {
	heads []registryports.Head
	asked [][]registrydomain.AssetRef
	err   error
}

func (s *savedHeads) Heads(_ context.Context, _ string, assets []registrydomain.AssetRef) ([]registryports.Head, error) {
	s.asked = append(s.asked, slices.Clone(assets))
	if s.err != nil {
		return nil, s.err
	}
	var found []registryports.Head
	for _, head := range s.heads {
		if slices.Contains(assets, head.Revision.Asset) {
			found = append(found, head)
		}
	}
	return found, nil
}

// savedChecks stands in for what Policy & Review keeps: the last check of
// revisions. It answers those of the revisions it is asked about, that have
// the status if one is asked, and keeps what it was asked.
type savedChecks struct {
	last  map[string]checkdomain.Overview
	asked []askedChecks
	err   error
}

type askedChecks struct {
	Revisions []string
	Status    checkdomain.Status
}

func (s *savedChecks) Latest(_ context.Context, _ string, revisions []checkdomain.RevisionRef, status checkdomain.Status) ([]checkdomain.Overview, error) {
	asked := askedChecks{Status: status}
	for _, revision := range revisions {
		asked.Revisions = append(asked.Revisions, revision.RevisionID)
	}
	s.asked = append(s.asked, asked)
	if s.err != nil {
		return nil, s.err
	}
	var found []checkdomain.Overview
	for _, revision := range revisions {
		if check, has := s.last[revision.RevisionID]; has && (status == "" || check.Status == status) {
			found = append(found, check)
		}
	}
	return found, nil
}

const (
	composedProduct   = "10000000-0000-4000-8000-000000000001"
	composedWorkspace = "20000000-0000-4000-8000-000000000001"
)

func composedID(kind, n int) string { return fmt.Sprintf("%d0000000-0000-4000-8000-%012d", kind, n) }

var composedAt = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// composed is six assets of a workspace and where each stands, as the
// registry and Policy & Review keep it: none has a revision; one has a draft
// that was not checked; one has an approved revision that was checked and
// could not be judged; one has an approved revision that failed and was
// released; one has an approved revision that failed and has no release,
// though an earlier revision of it has; and one has a revision that was
// rejected and passed.
type composed struct {
	heads  *savedHeads
	checks *savedChecks
	of     assetOverviews
	asset  [7]string // by the number of the asset, from 1
}

func composedFor(t *testing.T) *composed {
	t.Helper()

	c := &composed{heads: &savedHeads{}, checks: &savedChecks{last: map[string]checkdomain.Overview{}}}
	for n := 1; n <= 6; n++ {
		c.asset[n] = composedID(3, n)
	}
	head := func(n int, status string, checked checkdomain.Status, released bool, release *registrydomain.Release) {
		ref := registrydomain.AssetRef{ProductID: composedProduct, WorkspaceID: composedWorkspace, AssetID: c.asset[n]}
		revision := registrydomain.Revision{
			ID: composedID(4, n), Asset: ref, Sequence: int64(n), Status: status,
			RefClosureDigest: fmt.Sprintf("sha256:%064x", n), CreatedAt: composedAt.Add(time.Duration(n) * time.Minute),
		}
		c.heads.heads = append(c.heads.heads, registryports.Head{Revision: revision, Release: release, Released: released})
		if checked != "" {
			c.checks.last[revision.ID] = checkdomain.Overview{
				ID: composedID(5, n), Revision: checkdomain.RevisionRef{
					ProductID: composedProduct, WorkspaceID: composedWorkspace, AssetID: c.asset[n], RevisionID: revision.ID,
				},
				InputDigest: revision.RefClosureDigest, Status: checked, CompletedAt: composedAt.Add(time.Duration(n) * time.Hour),
			}
		}
	}
	release := func(n int, from string) *registrydomain.Release {
		return &registrydomain.Release{
			ID: composedID(6, n), RevisionID: from, Version: fmt.Sprintf("1.%d.0", n), PreparedAt: composedAt.Add(time.Duration(n) * 24 * time.Hour),
		}
	}
	head(2, registrydomain.RevisionDraft, "", false, nil)
	head(3, registrydomain.RevisionApproved, checkdomain.StatusUnknown, false, nil)
	head(4, registrydomain.RevisionApproved, checkdomain.StatusFail, true, release(4, composedID(4, 4)))
	head(5, registrydomain.RevisionApproved, checkdomain.StatusFail, false, release(5, composedID(4, 99)))
	head(6, registrydomain.RevisionRejected, checkdomain.StatusPass, false, nil)

	heads, err := registry.NewHeads(c.heads)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := checks.NewLatest(c.checks)
	if err != nil {
		t.Fatal(err)
	}
	c.of = assetOverviews{heads: heads, latest: latest}
	return c
}

// places are the assets of the numbers, as the Catalog names them.
func (c *composed) places(numbers ...int) []catalogports.AssetPlace {
	var places []catalogports.AssetPlace
	for _, n := range numbers {
		places = append(places, catalogports.AssetPlace{ID: catalogdomain.AssetID(c.asset[n]), WorkspaceID: composedWorkspace})
	}
	return places
}

// standing is where the asset of the number stands, as the Catalog says it,
// with the members the number says are there.
func (c *composed) standing(n int, check string, pending bool, release string) catalogdomain.Overview {
	if n == 1 {
		return catalogdomain.Overview{}
	}
	var revision catalogdomain.OverviewRevision
	for _, head := range c.heads.heads {
		if head.Revision.Asset.AssetID == c.asset[n] {
			revision = catalogdomain.OverviewRevision{
				ID: head.Revision.ID, Sequence: head.Revision.Sequence, Status: head.Revision.Status, CreatedAt: head.Revision.CreatedAt,
			}
		}
	}
	overview := catalogdomain.Overview{LatestRevision: &revision, ReleasePending: pending}
	if check != "" {
		last := c.checks.last[revision.ID]
		overview.LatestCheck = &catalogdomain.OverviewCheck{ID: last.ID, Status: catalogdomain.CheckStatus(check), CompletedAt: last.CompletedAt}
	}
	if release != "" {
		for _, head := range c.heads.heads {
			if head.Revision.Asset.AssetID == c.asset[n] && head.Release != nil {
				overview.LatestRelease = &catalogdomain.OverviewRelease{
					ID: head.Release.ID, RevisionID: head.Release.RevisionID, Version: head.Release.Version, PreparedAt: head.Release.PreparedAt,
				}
			}
		}
	}
	return overview
}

// What is put together is what each context says of its own: the revision,
// the release and whether a release is pending from the registry, the check
// from Policy & Review. Without a filter every asset has an overview, the
// one without a revision an empty one; with one only the assets that satisfy
// it, and Policy & Review is asked about what the registry has not ruled out
// already: the revisions of the heads, or only those a release is pending
// for when the filter says so, and for the status the filter names.
func TestTheOverviewsPutTogetherWhatTheRegistryAndPolicyReviewSay(t *testing.T) {
	for name, c := range map[string]struct {
		filter catalogports.OverviewFilter
		// want is the assets that stay, and where each stands.
		want func(c *composed) map[int]catalogdomain.Overview
		// asked are the revisions Policy & Review is asked about.
		asked []int
	}{
		"no filter": {
			catalogports.OverviewFilter{},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{
					1: c.standing(1, "", false, ""), 2: c.standing(2, "", false, ""), 3: c.standing(3, "UNKNOWN", true, ""),
					4: c.standing(4, "FAIL", false, "release"), 5: c.standing(5, "FAIL", true, "release"), 6: c.standing(6, "PASS", false, ""),
				}
			},
			[]int{2, 3, 4, 5, 6},
		},
		"a release pending": {
			catalogports.OverviewFilter{ReleasePending: true},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{3: c.standing(3, "UNKNOWN", true, ""), 5: c.standing(5, "FAIL", true, "release")}
			},
			[]int{3, 5},
		},
		"a check that failed": {
			catalogports.OverviewFilter{CheckStatus: catalogdomain.CheckFail},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{4: c.standing(4, "FAIL", false, "release"), 5: c.standing(5, "FAIL", true, "release")}
			},
			[]int{2, 3, 4, 5, 6},
		},
		"a check that failed, and a release pending": {
			catalogports.OverviewFilter{CheckStatus: catalogdomain.CheckFail, ReleasePending: true},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{5: c.standing(5, "FAIL", true, "release")}
			},
			[]int{3, 5},
		},
		"a check that passed, of a revision that was rejected": {
			catalogports.OverviewFilter{CheckStatus: catalogdomain.CheckPass},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{6: c.standing(6, "PASS", false, "")}
			},
			[]int{2, 3, 4, 5, 6},
		},
		"a check that could not judge, and a release pending": {
			catalogports.OverviewFilter{CheckStatus: catalogdomain.CheckUnknown, ReleasePending: true},
			func(c *composed) map[int]catalogdomain.Overview {
				return map[int]catalogdomain.Overview{3: c.standing(3, "UNKNOWN", true, "")}
			},
			[]int{3, 5},
		},
		"a check that warned": {
			catalogports.OverviewFilter{CheckStatus: catalogdomain.CheckWarn},
			func(*composed) map[int]catalogdomain.Overview { return map[int]catalogdomain.Overview{} },
			[]int{2, 3, 4, 5, 6},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := composedFor(t)
			got, err := s.of.Of(t.Context(), catalogdomain.ProductID(composedProduct), s.places(1, 2, 3, 4, 5, 6), c.filter)
			if err != nil {
				t.Fatalf("the overviews: %v", err)
			}
			want := map[catalogdomain.AssetID]catalogdomain.Overview{}
			for n, overview := range c.want(s) {
				want[catalogdomain.AssetID(s.asset[n])] = overview
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the overviews are\n %v\nwant\n %v", describe(got), describe(want))
			}

			if len(s.heads.asked) != 1 || len(s.heads.asked[0]) != 6 {
				t.Errorf("the registry was asked %v, want one question of the six assets", s.heads.asked)
			}
			var revisions []string
			for _, n := range c.asked {
				revisions = append(revisions, composedID(4, n))
			}
			if wantAsked := []askedChecks{{Revisions: revisions, Status: checkdomain.Status(c.filter.CheckStatus)}}; !reflect.DeepEqual(s.checks.asked, wantAsked) {
				t.Errorf("Policy & Review was asked %+v, want %+v", s.checks.asked, wantAsked)
			}
		})
	}
}

// describe puts overviews in a form that shows what the pointers hold.
func describe(overviews map[catalogdomain.AssetID]catalogdomain.Overview) []string {
	var described []string
	for _, id := range slices.Sorted(maps.Keys(overviews)) {
		overview := overviews[id]
		line := fmt.Sprintf("%s: pending %v", id, overview.ReleasePending)
		if overview.LatestRevision != nil {
			line += fmt.Sprintf(", revision %+v", *overview.LatestRevision)
		}
		if overview.LatestCheck != nil {
			line += fmt.Sprintf(", check %+v", *overview.LatestCheck)
		}
		if overview.LatestRelease != nil {
			line += fmt.Sprintf(", release %+v", *overview.LatestRelease)
		}
		described = append(described, line)
	}
	return described
}

// Nothing is read for no assets, and what fails to be read fails the whole:
// neither the registry nor Policy & Review is asked on after the other
// failed, and an overview is not answered without what could not be read.
func TestTheOverviewsFailWithAnyReadThatFailsAndReadNothingForNoAssets(t *testing.T) {
	product := catalogdomain.ProductID(composedProduct)

	s := composedFor(t)
	got, err := s.of.Of(t.Context(), product, nil, catalogports.OverviewFilter{})
	if err != nil || len(got) != 0 || len(s.heads.asked) != 0 || len(s.checks.asked) != 0 {
		t.Errorf("no assets: %v (%v), the registry was asked %v and Policy & Review %v, want nothing read", got, err, s.heads.asked, s.checks.asked)
	}

	broken := errors.New("the registry is out of reach")
	s = composedFor(t)
	s.heads.err = broken
	if got, err := s.of.Of(t.Context(), product, s.places(1, 2, 3), catalogports.OverviewFilter{}); !errors.Is(err, broken) || got != nil || len(s.checks.asked) != 0 {
		t.Errorf("the registry fails: %v (%v), Policy & Review was asked %v, want the failure and nothing else", got, err, s.checks.asked)
	}

	broken = errors.New("the checks are out of reach")
	s = composedFor(t)
	s.checks.err = broken
	for _, filter := range []catalogports.OverviewFilter{{}, {ReleasePending: true}, {CheckStatus: catalogdomain.CheckFail}} {
		if got, err := s.of.Of(t.Context(), product, s.places(1, 2, 3), filter); !errors.Is(err, broken) || got != nil {
			t.Errorf("Policy & Review fails under %+v: %v (%v), want the failure and no overview", filter, got, err)
		}
	}

	// What names no product is refused by what reads the registry before it
	// is asked anything.
	s = composedFor(t)
	if got, err := s.of.Of(t.Context(), "not-a-product", s.places(2, 3), catalogports.OverviewFilter{}); !errors.Is(err, registry.ErrInvalidAssets) || got != nil || len(s.heads.asked) != 0 {
		t.Errorf("a product that is none: %v (%v), the registry was asked %v, want %v and nothing read", got, err, s.heads.asked, registry.ErrInvalidAssets)
	}
}
