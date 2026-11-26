package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

const (
	contractsRepository = "https://git.example/logistics/contracts"
	contractsCommit     = "2ff6cad0b6a1e8c4f0d3a5b79c1e2d4f6a8b0c1d"
	contract            = "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\npaths: {}\n"
)

var shipments = domain.AssetRef{ProductID: "product", WorkspaceID: "workspace", AssetID: "asset"}

// registryDouble stands in for everything a use case reaches, and keeps what
// reached it.
type registryDouble struct {
	author, active, http bool
	// reviewer says that the caller holds the reviewer grant of the
	// workspace. A reviewer reads revisions and imports none.
	reviewer bool
	// recorded and stored are the revision the store holds and the files it
	// holds for it.
	recorded *domain.Revision
	stored   []domain.SourceFile

	captured  int
	drafts    []domain.Draft
	found     []string
	filesRead []string
	audited   []ports.AuditEntry
	events    int
}

func (d *registryDouble) WithinTransaction(ctx context.Context, work func(context.Context) error) error {
	return work(ctx)
}

func (d *registryDouble) Asset(context.Context, ports.ScopeQuery) (ports.AssetScope, error) {
	return ports.AssetScope{Asset: shipments, HTTP: d.http}, nil
}

func (d *registryDouble) Author(context.Context, domain.Principal, domain.AssetRef, bool) (bool, bool, error) {
	return d.author, d.active, nil
}

func (d *registryDouble) Reader(context.Context, domain.Principal, domain.AssetRef) (bool, error) {
	return d.author || d.reviewer, nil
}

func (d *registryDouble) capture(entrypoint string, files []domain.SourceFile) (domain.SourceSnapshot, error) {
	d.captured++
	return domain.CaptureSource(entrypoint, "3.1.2", files)
}

func (d *registryDouble) Register(_ context.Context, draft domain.Draft, source domain.SourceSnapshot) (ports.Registration, error) {
	d.drafts = append(d.drafts, draft)
	return ports.Registration{Created: true, Revision: domain.Revision{
		ID: draft.ID, SourceSnapshotID: draft.SourceSnapshotID, Asset: draft.Asset, Sequence: int64(len(d.drafts)),
		Status: domain.RevisionDraft, SourceKind: draft.Origin.SourceKind(), Origin: draft.Origin,
		Entrypoint: source.Entrypoint, SpecVersion: source.SpecVersion,
		SourceDigest: source.SourceDigest, RefClosureDigest: source.RefClosureDigest, CreatedAt: draft.CapturedAt,
	}}, nil
}

func (d *registryDouble) Find(_ context.Context, asset domain.AssetRef, id string) (domain.Revision, bool, error) {
	d.found = append(d.found, id)
	if d.recorded == nil || d.recorded.ID != id || d.recorded.Asset != asset {
		return domain.Revision{}, false, nil
	}
	return *d.recorded, true, nil
}

func (d *registryDouble) Files(_ context.Context, asset domain.AssetRef, id string) ([]domain.SourceFile, error) {
	d.filesRead = append(d.filesRead, id)
	if d.recorded == nil || d.recorded.ID != id || d.recorded.Asset != asset {
		return nil, nil
	}
	return d.stored, nil
}

func (d *registryDouble) List(context.Context, domain.AssetRef, *ports.Position, int) ([]domain.Revision, error) {
	return nil, nil
}

func (d *registryDouble) audit(_ context.Context, entry ports.AuditEntry) error {
	d.audited = append(d.audited, entry)
	return nil
}

func (d *registryDouble) Registered(context.Context, domain.Revision, string) error {
	d.events++
	return nil
}

func (d *registryDouble) Now() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) }
func (d *registryDouble) NewID() string  { return "identifier" }

func newRegistry(t *testing.T, double *registryDouble) *application.Registry {
	t.Helper()
	registry, err := application.New(application.Dependencies{
		Transactions: double, Scopes: double, Capture: double.capture, Revisions: double,
		Audit: double.audit, Events: double, Clock: double, Identifiers: double,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func importOf(origin domain.GitOrigin) application.ImportRevision {
	return application.ImportRevision{
		ScopeQuery: ports.ScopeQuery{
			Caller: domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}, ProductID: shipments.ProductID,
			AssetID: shipments.AssetID, CorrelationID: "correlation",
		},
		Entrypoint: "openapi.yaml", Files: []domain.SourceFile{{Path: "openapi.yaml", Content: contract}}, Origin: origin,
	}
}

// The contract of the API states the form of an origin, and the validator in
// front of the use case refuses what does not have it. The use case asks the
// model all the same: whatever calls it, an origin the model refuses is
// neither captured, stored, audited nor announced.
func TestAnImportRecordsOnlyAnOriginTheModelAccepts(t *testing.T) {
	const token = "glpat-s3cr3t"
	refused := map[string]domain.GitOrigin{
		"plain http":            {Repository: "http://git.example/logistics/contracts", Commit: contractsCommit},
		"a token in the URL":    {Repository: "https://ci:" + token + "@git.example/logistics/contracts", Commit: contractsCommit},
		"a host in upper case":  {Repository: "https://Git.Example/logistics/contracts", Commit: contractsCommit},
		"an abbreviated commit": {Repository: contractsRepository, Commit: contractsCommit[:7]},
		"a short branch name":   {Repository: contractsRepository, Commit: contractsCommit, Ref: "main"},
		"a repository alone":    {Repository: contractsRepository},
		"a commit alone":        {Commit: contractsCommit},
		"a reference alone":     {Ref: "refs/heads/main"},
	}
	for name, origin := range refused {
		t.Run(name, func(t *testing.T) {
			double := &registryDouble{author: true, active: true, http: true}
			_, err := newRegistry(t, double).ImportRevision(context.Background(), importOf(origin))
			if !errors.Is(err, domain.ErrInvalidOrigin) {
				t.Fatalf("got %v, want %v", err, domain.ErrInvalidOrigin)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("the refusal repeats the token: %v", err)
			}
			if double.captured != 0 || len(double.drafts) != 0 || len(double.audited) != 0 || double.events != 0 {
				t.Fatalf("captured %d, stored %d, audited %d, announced %d; want nothing of a refused origin",
					double.captured, len(double.drafts), len(double.audited), double.events)
			}
		})
	}

	accepted := map[string]domain.GitOrigin{
		"no origin":                {},
		"a commit":                 {Repository: contractsRepository, Commit: contractsCommit},
		"a commit and a reference": {Repository: contractsRepository, Commit: contractsCommit, Ref: "refs/tags/v1.2.0"},
	}
	for name, origin := range accepted {
		t.Run(name, func(t *testing.T) {
			double := &registryDouble{author: true, active: true, http: true}
			registered, err := newRegistry(t, double).ImportRevision(context.Background(), importOf(origin))
			if err != nil {
				t.Fatal(err)
			}
			if len(double.drafts) != 1 || double.drafts[0].Origin != origin {
				t.Fatalf("stored %+v, want one draft with the origin %+v", double.drafts, origin)
			}
			if !registered.Created || registered.Revision.Origin != origin || registered.Revision.SourceKind != origin.SourceKind() {
				t.Fatalf("answered %+v, want a new revision with the origin %+v", registered, origin)
			}
			want := ports.AuditEntry{
				Query: importOf(origin).ScopeQuery, RevisionID: registered.Revision.ID, Action: "revision.create", Rule: "AC-01", Allowed: true,
			}
			if len(double.audited) != 1 || double.audited[0] != want || double.events != 1 {
				t.Fatalf("audited %+v and announced %d, want %+v and one event", double.audited, double.events, want)
			}
		})
	}
}

// What a caller is told who may not import, or who imports into an asset or
// a product that takes no import, does not depend on the origin it names:
// the origin is looked at after all of that.
func TestAnOriginIsLookedAtAfterWhoImportsAndInto(t *testing.T) {
	malformed := domain.GitOrigin{Repository: "http://git.example/logistics/contracts", Commit: "main"}
	cases := map[string]struct {
		double  registryDouble
		want    error
		audited int
	}{
		"someone who is no author":  {registryDouble{author: false, active: true, http: true}, application.ErrForbidden, 1},
		"a suspended product":       {registryDouble{author: true, active: false, http: true}, application.ErrProductSuspended, 0},
		"an asset that is not HTTP": {registryDouble{author: true, active: true, http: false}, application.ErrAssetKind, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			double := c.double
			_, err := newRegistry(t, &double).ImportRevision(context.Background(), importOf(malformed))
			if !errors.Is(err, c.want) || errors.Is(err, domain.ErrInvalidOrigin) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if len(double.audited) != c.audited || double.captured != 0 || len(double.drafts) != 0 || double.events != 0 {
				t.Fatalf("audited %d, captured %d, stored %d, announced %d; want %d audit records and nothing else",
					len(double.audited), double.captured, len(double.drafts), double.events, c.audited)
			}
			if c.audited == 1 && (double.audited[0].Action != "revision.create" || double.audited[0].Rule != "DENY-03" || double.audited[0].Allowed) {
				t.Fatalf("audited %+v, want a refusal of revision.create under DENY-03", double.audited[0])
			}
		})
	}
}
