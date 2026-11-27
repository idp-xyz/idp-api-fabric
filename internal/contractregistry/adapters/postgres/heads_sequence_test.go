package postgres_test

import (
	"context"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// recordCapturedAt records a revision of the asset as record does, with the
// source captured at the instant the test says and not now.
func (f *statusFixture) recordCapturedAt(captured time.Time) domain.Revision {
	f.t.Helper()
	f.count++
	content := "openapi: 3.1.2\ninfo: {title: Shipments, version: v" + string(rune('0'+f.count)) + "}\n"
	snapshot, err := domain.CaptureSource("openapi.yaml", domain.OpenAPI, "3.1.2", []domain.SourceFile{{Path: "openapi.yaml", Content: content}})
	if err != nil {
		f.t.Fatal(err)
	}
	var revision domain.Revision
	err = f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		registered, err := f.store.Register(ctx, domain.Draft{
			ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: f.asset,
			CapturedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "author"}, CapturedAt: captured,
		}, snapshot)
		revision = registered.Revision
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return revision
}

// The head of an asset is the revision of the greatest sequence, and not the
// one that was captured last. The two can differ: the sequence is given under
// the lock of the registration of the asset, and the instant of the capture is
// taken before the lock is, so that of two imports that run together the one
// that takes the lock second may have been captured first. The revisions of
// the asset are listed by when they were captured and the head says where the
// asset stands by the order the revisions were registered in, so here the
// revision that was registered last was captured first.
func TestTheHeadOfAnAssetIsTheRevisionOfTheGreatestSequenceAndNotTheOneCapturedLast(t *testing.T) {
	f, _ := newStatusFixture(t)
	heads := postgres.NewHeads(f.fabric.Framework)
	noon := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	first := f.recordCapturedAt(noon)
	second := f.recordCapturedAt(noon.Add(-time.Hour))
	third := f.recordCapturedAt(noon.Add(-2 * time.Hour))
	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 ||
		!third.CreatedAt.Before(second.CreatedAt) || !second.CreatedAt.Before(first.CreatedAt) {
		t.Fatalf("the revisions are not those the case was made of: sequences %d, %d and %d, captured at %v, %v and %v",
			first.Sequence, second.Sequence, third.Sequence, first.CreatedAt, second.CreatedAt, third.CreatedAt)
	}

	head, present := headsOf(t, heads, f.asset.ProductID, f.asset)[f.asset.AssetID]
	if !present {
		t.Fatal("the asset has no head")
	}
	if head.Revision.ID != third.ID || head.Revision.Sequence != 3 {
		t.Errorf("the head is the revision %s of the sequence %d, captured at %v; want the revision %s of the sequence 3, which was captured first",
			head.Revision.ID, head.Revision.Sequence, head.Revision.CreatedAt, third.ID)
	}
	if read, found, err := f.store.Find(t.Context(), f.asset, head.Revision.ID); err != nil || !found || !sameRevision(head.Revision, read) {
		t.Errorf("the head names the revision %+v, a read of it gives %+v (%v)", head.Revision, read, err)
	}
	if head.Released || head.Release != nil {
		t.Errorf("the head is released %t with the release %+v, want neither: nothing was prepared", head.Released, head.Release)
	}
}
