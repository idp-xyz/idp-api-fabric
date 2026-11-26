package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

// releaseDouble stands in for the scopes, the releases and the events a
// preparation reaches, beside the revisions and the rest registryDouble
// stands in for. checked is what Policy & Review answers of the check: the
// evidence and the waivers it hands back, or a refusal.
type releaseDouble struct {
	*registryDouble
	publisher, active bool
	checked           struct {
		evidence domain.CheckEvidence
		waivers  []domain.Waiver
		err      error
	}
	asked    []checkAsked
	releases []domain.Release
	prepared int
}

type checkAsked struct {
	revision string
	check    string
	waivers  []domain.Waiver
}

func (d *releaseDouble) ReleaseAccess(context.Context, domain.Principal, domain.AssetRef) (ports.ReleaseAccess, error) {
	return ports.ReleaseAccess{Metadata: true, Material: true, Publisher: d.publisher, Active: d.active}, nil
}

func (d *releaseDouble) Check(_ context.Context, revision domain.Revision, checkRunID string, waivers []domain.Waiver) (domain.CheckEvidence, []domain.Waiver, error) {
	d.asked = append(d.asked, checkAsked{revision.ID, checkRunID, waivers})
	return d.checked.evidence, d.checked.waivers, d.checked.err
}

func (d *releaseDouble) Evidence(context.Context, domain.Release) (domain.ReleaseEvidence, error) {
	return domain.ReleaseEvidence{}, errors.New("a preparation reads no evidence")
}

func (d *releaseDouble) Find(_ context.Context, asset domain.AssetRef, id string) (domain.Release, bool, error) {
	for _, release := range d.releases {
		if release.Asset == asset && release.ID == id {
			return release, true, nil
		}
	}
	return domain.Release{}, false, nil
}

func (d *releaseDouble) Version(_ context.Context, asset domain.AssetRef, version string) (domain.Release, bool, error) {
	for _, release := range d.releases {
		if release.Asset == asset && release.Version == version {
			return release, true, nil
		}
	}
	return domain.Release{}, false, nil
}

func (d *releaseDouble) List(context.Context, domain.AssetRef, *ports.Position, int) ([]domain.Release, error) {
	return d.releases, nil
}

func (d *releaseDouble) Record(_ context.Context, release domain.Release, _ []byte) (bool, error) {
	if _, taken, _ := d.Version(context.Background(), release.Asset, release.Version); taken {
		return false, nil
	}
	d.releases = append(d.releases, release)
	return true, nil
}

func (d *releaseDouble) Artifact(context.Context, string) ([]byte, error) { return nil, nil }

func (d *releaseDouble) Prepared(context.Context, domain.Release, string) error {
	d.prepared++
	return nil
}

// releases is the release use cases over the double, which holds one revision
// of the asset with its files, at the status given.
func releases(t *testing.T, status string) (*application.Releases, *releaseDouble) {
	t.Helper()
	files := []domain.SourceFile{{Path: "openapi.yaml", Content: contract}}
	source, err := domain.CaptureSource("openapi.yaml", "3.1.2", files)
	if err != nil {
		t.Fatal(err)
	}
	revision := domain.Revision{
		ID: "c0ffee00-0000-4000-8000-000000000001", Asset: shipments, Sequence: 1, Status: status, StatusVersion: 1,
		SourceKind: domain.SourceUpload, Entrypoint: source.Entrypoint, SpecVersion: source.SpecVersion,
		SourceDigest: source.SourceDigest, RefClosureDigest: source.RefClosureDigest,
	}
	double := &releaseDouble{registryDouble: &registryDouble{recorded: &revision, stored: files}, publisher: true, active: true}
	double.checked.evidence = domain.CheckEvidence{CheckRunID: "check", PolicySnapshotID: "snapshot", PolicyDigest: "policy"}
	uses, err := application.NewReleases(application.ReleaseDependencies{
		Transactions: double, Scopes: double, Revisions: double.registryDouble, Releases: double,
		Audit: double.audit, Events: double, Clock: double, Identifiers: double,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uses, double
}

func preparing(check, version string, waivers ...domain.Waiver) application.PrepareRelease {
	return application.PrepareRelease{
		ScopeQuery: ports.ScopeQuery{
			Caller: domain.Principal{Issuer: "https://issuer.example", Subject: "publisher"}, ProductID: shipments.ProductID,
			AssetID: shipments.AssetID, CorrelationID: "correlation",
		},
		RevisionID: "c0ffee00-0000-4000-8000-000000000001", CheckRunID: check, Waivers: waivers, Version: version,
	}
}

var (
	summaryLater = domain.Waiver{RuleID: "F04-GOV-002", Reason: "the summary follows in 1.1"}
	unnamed      = domain.Waiver{RuleID: "F04-GOV-003", Reason: "the gateway names the operations"}
)

// A release is prepared on a check of the revision, with the waivers whoever
// prepares it gives. The registry hands both to Policy & Review and records
// what comes back: the check and the waivers in the order of their rules.
// Whatever status the revision has in the review it may have had, it is
// released alike: the status is no gate.
func TestAReleaseIsPreparedOnACheckWithTheWaiversPolicyAndReviewHandsBack(t *testing.T) {
	for _, status := range []string{"DRAFT", "IN_REVIEW", "APPROVED", "REJECTED", "WITHDRAWN"} {
		t.Run(status, func(t *testing.T) {
			uses, double := releases(t, status)
			double.checked.waivers = []domain.Waiver{summaryLater, unnamed}
			prepared, err := uses.Prepare(context.Background(), preparing("check", "1.0.0", unnamed, summaryLater))
			if err != nil || !prepared.Created {
				t.Fatalf("prepared %+v: %v", prepared, err)
			}
			release := prepared.Release
			if release.ReviewID != "" || release.Evidence != double.checked.evidence ||
				!reflect.DeepEqual(release.Waivers, []domain.Waiver{summaryLater, unnamed}) || release.RevisionID != double.recorded.ID {
				t.Errorf("the release is %+v, want it on the check, with the waivers handed back and no review", release)
			}
			if want := []checkAsked{{double.recorded.ID, "check", []domain.Waiver{unnamed, summaryLater}}}; !reflect.DeepEqual(double.asked, want) {
				t.Errorf("Policy & Review was asked %+v, want %+v", double.asked, want)
			}
			if len(double.releases) != 1 || len(double.audited) != 1 || double.audited[0].Action != "release.prepare" || double.prepared != 1 {
				t.Errorf("recorded %d releases, %+v and %d events", len(double.releases), double.audited, double.prepared)
			}
		})
	}
}

// What Policy & Review refuses of the check is the refusal of the
// preparation, and nothing is recorded.
func TestAReleaseIsNotPreparedOnACheckPolicyAndReviewRefuses(t *testing.T) {
	uses, double := releases(t, "DRAFT")
	refused := errors.New("rules that nothing waives did not pass")
	double.checked.err = refused
	if _, err := uses.Prepare(context.Background(), preparing("check", "1.0.0")); !errors.Is(err, refused) {
		t.Fatalf("got %v, want %v", err, refused)
	}
	if len(double.releases) != 0 || len(double.audited) != 0 || double.prepared != 0 {
		t.Errorf("a refused preparation left %d releases, %d audit records and %d events", len(double.releases), len(double.audited), double.prepared)
	}
}

// Asking again under a version answers the release that is there when it was
// prepared from the same revision, on the same check, with the same waivers
// in whatever order, and asks nothing of Policy & Review. Anything else under
// the version is a conflict, and so is a release of before that names a
// review, which no preparation of today asks for.
func TestAVersionNamesOneReleaseOfOneRevisionCheckAndWaivers(t *testing.T) {
	uses, double := releases(t, "DRAFT")
	double.checked.waivers = []domain.Waiver{summaryLater, unnamed}
	first, err := uses.Prepare(context.Background(), preparing("check", "1.0.0", summaryLater, unnamed))
	if err != nil {
		t.Fatal(err)
	}
	asked := len(double.asked)

	again, err := uses.Prepare(context.Background(), preparing("check", "1.0.0", unnamed, summaryLater))
	if err != nil || again.Created || !reflect.DeepEqual(again.Release, first.Release) {
		t.Errorf("the same preparation again: %+v, %v; want the release that is there", again, err)
	}
	if len(double.asked) != asked || len(double.releases) != 1 || double.prepared != 1 {
		t.Errorf("asking again asked Policy & Review %d more times, and left %d releases and %d events",
			len(double.asked)-asked, len(double.releases), double.prepared)
	}

	otherReason := unnamed
	otherReason.Reason = "another reason"
	for name, command := range map[string]application.PrepareRelease{
		"another check":               preparing("another check", "1.0.0", summaryLater, unnamed),
		"fewer waivers":               preparing("check", "1.0.0", unnamed),
		"no waivers":                  preparing("check", "1.0.0"),
		"a waiver for another reason": preparing("check", "1.0.0", summaryLater, otherReason),
		"another revision": func() application.PrepareRelease {
			command := preparing("check", "1.0.0", summaryLater, unnamed)
			command.RevisionID = "c0ffee00-0000-4000-8000-000000000002"
			return command
		}(),
	} {
		if _, err := uses.Prepare(context.Background(), command); !errors.Is(err, application.ErrReleaseConflict) {
			t.Errorf("%s under the version: %v, want %v", name, err, application.ErrReleaseConflict)
		}
	}

	reviewed := first.Release
	reviewed.ID, reviewed.Version, reviewed.ReviewID, reviewed.Waivers = "reviewed", "0.9.0", "review", nil
	double.releases = append(double.releases, reviewed)
	if _, err := uses.Prepare(context.Background(), preparing("check", "0.9.0")); !errors.Is(err, application.ErrReleaseConflict) {
		t.Errorf("the version of a release prepared under a review: %v, want %v", err, application.ErrReleaseConflict)
	}
}
