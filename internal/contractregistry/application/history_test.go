package application_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

func digestOf(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}

// The source of a revision is read for whoever may read the revision, from
// the files stored under it, and only once they are found to be the files
// the revision was recorded from. A refusal is audited as one of reading the
// revision; a read leaves nothing behind.
func TestTheSourceOfARevisionIsReadForWhoMayReadTheRevision(t *testing.T) {
	const revisionID = "c0ffee00-0000-4000-8000-000000000001"
	files := []domain.SourceFile{
		{Path: "schemas/common.json", Content: "{\"Shipment\":{\"type\":\"object\"}}\n"},
		{Path: "openapi.yaml", Content: contract},
	}
	snapshot, err := domain.CaptureSource("openapi.yaml", "3.1.2", files)
	if err != nil {
		t.Fatal(err)
	}
	revision := domain.Revision{
		ID: revisionID, SourceSnapshotID: "snapshot", Asset: shipments, Sequence: 1, Status: domain.RevisionDraft,
		SourceKind: domain.SourceUpload, Entrypoint: "openapi.yaml", SpecVersion: "3.1.2",
		SourceDigest: snapshot.SourceDigest, RefClosureDigest: snapshot.RefClosureDigest,
	}
	query := importOf(domain.GitOrigin{}).ScopeQuery
	holding := func(stored []domain.SourceFile) *registryDouble {
		return &registryDouble{author: true, active: true, http: true, recorded: &revision, stored: stored}
	}
	untouched := func(t *testing.T, double *registryDouble, audited int) {
		t.Helper()
		if len(double.audited) != audited || double.events != 0 || double.captured != 0 || len(double.drafts) != 0 {
			t.Errorf("audited %d, announced %d, captured %d, stored %d; want %d audit records and nothing else",
				len(double.audited), double.events, double.captured, len(double.drafts), audited)
		}
	}

	t.Run("the files as they were recorded", func(t *testing.T) {
		double := holding(files)
		got, err := newRegistry(t, double).GetRevisionSource(context.Background(), query, revisionID)
		if err != nil {
			t.Fatal(err)
		}
		want := []domain.RecordedFile{
			{Path: "openapi.yaml", Digest: snapshot.SourceDigest, Content: contract},
			{Path: "schemas/common.json", Digest: digestOf(files[0].Content), Content: files[0].Content},
		}
		if got.Revision != revision || !reflect.DeepEqual(got.Files, want) {
			t.Errorf("read %+v, want the revision with its files in the order of their paths %+v", got, want)
		}
		if !reflect.DeepEqual(double.filesRead, []string{revisionID}) {
			t.Errorf("the files were read for %v, want once for the revision", double.filesRead)
		}
		untouched(t, double, 0)
	})

	t.Run("a product that is suspended", func(t *testing.T) {
		double := holding(files)
		double.active = false
		if got, err := newRegistry(t, double).GetRevisionSource(context.Background(), query, revisionID); err != nil || len(got.Files) != 2 {
			t.Errorf("read %d files, want the two of the revision: %v", len(got.Files), err)
		}
		untouched(t, double, 0)
	})

	// Whoever reviews the revisions of the workspace reads them and their
	// files, the history included, and imports none.
	t.Run("a reviewer who is no author", func(t *testing.T) {
		double := holding(files)
		double.author, double.reviewer = false, true
		registry := newRegistry(t, double)
		if got, err := registry.GetRevisionSource(context.Background(), query, revisionID); err != nil || len(got.Files) != 2 {
			t.Errorf("read %d files, want the two of the revision: %v", len(got.Files), err)
		}
		if got, err := registry.GetRevision(context.Background(), query, revisionID); err != nil || got != revision {
			t.Errorf("read %+v, want the revision: %v", got, err)
		}
		if _, err := registry.ListRevisions(context.Background(), application.ListRevisions{ScopeQuery: query}); err != nil {
			t.Errorf("the history: %v, want it read", err)
		}
		untouched(t, double, 0)
		if _, err := registry.ImportRevision(context.Background(), importOf(domain.GitOrigin{})); !errors.Is(err, application.ErrForbidden) {
			t.Errorf("an import by a reviewer: %v, want %v", err, application.ErrForbidden)
		}
		if want := (ports.AuditEntry{Query: query, Action: "revision.create", Rule: "DENY-03"}); len(double.audited) != 1 || double.audited[0] != want {
			t.Errorf("audited %+v, want %+v", double.audited, want)
		}
		if double.captured != 0 || len(double.drafts) != 0 || double.events != 0 {
			t.Errorf("captured %d, stored %d, announced %d for a reviewer", double.captured, len(double.drafts), double.events)
		}
	})

	t.Run("someone who is neither author nor reviewer", func(t *testing.T) {
		double := holding(files)
		double.author = false
		got, err := newRegistry(t, double).GetRevisionSource(context.Background(), query, revisionID)
		if !errors.Is(err, application.ErrForbidden) || got.Files != nil || got.Revision != (domain.Revision{}) {
			t.Fatalf("got %+v and %v, want nothing and %v", got, err, application.ErrForbidden)
		}
		if len(double.found) != 0 || len(double.filesRead) != 0 {
			t.Errorf("the revision was read %d times and its files %d times for someone who may read neither", len(double.found), len(double.filesRead))
		}
		untouched(t, double, 1)
		if want := (ports.AuditEntry{Query: query, Action: "revision.read", Rule: "DENY-03"}); len(double.audited) == 1 && double.audited[0] != want {
			t.Errorf("audited %+v, want %+v", double.audited[0], want)
		}
	})

	t.Run("a revision the asset does not have", func(t *testing.T) {
		double := holding(files)
		got, err := newRegistry(t, double).GetRevisionSource(context.Background(), query, "c0ffee00-0000-4000-8000-000000000002")
		if !errors.Is(err, application.ErrRevisionNotFound) || got.Files != nil {
			t.Fatalf("got %+v and %v, want nothing and %v", got, err, application.ErrRevisionNotFound)
		}
		if len(double.filesRead) != 0 {
			t.Errorf("files were read for a revision that is not there: %v", double.filesRead)
		}
		untouched(t, double, 0)
	})

	damaged := map[string][]domain.SourceFile{
		"a file that was altered": {files[0], {Path: "openapi.yaml", Content: contract + "\n"}},
		"a file that is gone":     {files[1]},
		"a file that was added":   {files[0], files[1], {Path: "extra.yaml", Content: "type: string\n"}},
		"no file at all":          nil,
	}
	for name, stored := range damaged {
		t.Run(name, func(t *testing.T) {
			double := holding(stored)
			got, err := newRegistry(t, double).GetRevisionSource(context.Background(), query, revisionID)
			if !errors.Is(err, domain.ErrSourceDamaged) || got.Files != nil || got.Revision != (domain.Revision{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, domain.ErrSourceDamaged)
			}
			// Whoever operates the service finds the revision by this.
			if !strings.Contains(err.Error(), "the source of revision "+revisionID) {
				t.Errorf("the failure does not name the revision: %v", err)
			}
			for _, file := range files {
				if strings.Contains(err.Error(), file.Content) {
					t.Errorf("the failure carries a file: %v", err)
				}
			}
			untouched(t, double, 0)
		})
	}
}
