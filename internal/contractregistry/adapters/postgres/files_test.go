package postgres_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// The files of a revision are read through the revision: byte for byte, in
// the order of the bytes of their paths, and only under the product, the
// asset and the workspace the revision belongs to.
func TestTheFilesOfARevisionAreReadUnderItsAsset(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	store := postgres.NewRevisions(fabric.Framework)
	asset := domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()}
	actor := domain.Principal{Issuer: "https://issuer.example", Subject: "author"}

	record := func(asset domain.AssetRef, entrypoint string, files ...domain.SourceFile) (domain.Revision, domain.SourceSnapshot) {
		t.Helper()
		snapshot, err := domain.CaptureSource(entrypoint, "3.1.2", files)
		if err != nil {
			t.Fatal(err)
		}
		var revision domain.Revision
		err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			registered, err := store.Register(ctx, domain.Draft{
				ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: asset, CapturedBy: actor, CapturedAt: time.Now(),
			}, snapshot)
			revision = registered.Revision
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return revision, snapshot
	}

	// A collation of a language would put a/ before B/; the bytes do not,
	// and the digest of the closure is taken over the order of the bytes.
	bundle := []domain.SourceFile{
		{Path: "b/openapi.yaml", Content: "openapi: 3.1.2\r\ninfo: {title: 运单, version: v1}\r\n"},
		{Path: "a/common.json", Content: "{\"type\":\"object\"}\n\n"},
		{Path: "B/upper.yaml", Content: "\xef\xbb\xbftype: string\n"},
	}
	first, snapshot := record(asset, "b/openapi.yaml", bundle...)
	second, _ := record(asset, "openapi.yaml", domain.SourceFile{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"})
	sibling := domain.AssetRef{ProductID: asset.ProductID, WorkspaceID: asset.WorkspaceID, AssetID: ids.NewUUID()}
	third, _ := record(sibling, "openapi.yaml", domain.SourceFile{Path: "openapi.yaml", Content: "openapi: 3.1.2\n# of the sibling\n"})

	got, err := store.Files(t.Context(), asset, first.ID)
	want := []domain.SourceFile{bundle[2], bundle[1], bundle[0]}
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(got, snapshot.Files()) {
		t.Fatalf("the files of the bundle: %q %v, want them as they were captured %q", got, err, want)
	}
	if _, err := domain.RecordedSource(first, got); err != nil {
		t.Errorf("the files read back are not the source of the revision: %v", err)
	}
	if got, err := store.Files(t.Context(), asset, second.ID); err != nil || len(got) != 1 || got[0].Content != "openapi: 3.1.2\n" {
		t.Errorf("the files of the second revision: %q %v, want its one file", got, err)
	}
	if got, err := store.Files(t.Context(), sibling, third.ID); err != nil || len(got) != 1 || got[0].Content != "openapi: 3.1.2\n# of the sibling\n" {
		t.Errorf("the files of the revision of the sibling: %q %v, want its one file", got, err)
	}

	elsewhere := func(change func(*domain.AssetRef)) domain.AssetRef {
		changed := asset
		change(&changed)
		return changed
	}
	for name, read := range map[string]struct {
		asset    domain.AssetRef
		revision string
	}{
		"a revision nobody recorded":         {asset, ids.NewUUID()},
		"the revision of another asset":      {asset, third.ID},
		"the revision under another asset":   {sibling, first.ID},
		"the revision in another workspace":  {elsewhere(func(a *domain.AssetRef) { a.WorkspaceID = ids.NewUUID() }), first.ID},
		"the revision under another product": {elsewhere(func(a *domain.AssetRef) { a.ProductID = ids.NewUUID() }), first.ID},
	} {
		if got, err := store.Files(t.Context(), read.asset, read.revision); err != nil || len(got) != 0 {
			t.Errorf("%s: %q %v, want no file", name, got, err)
		}
	}
}
