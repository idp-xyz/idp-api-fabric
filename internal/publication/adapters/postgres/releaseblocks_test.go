package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

// The blocks of a release are those that name it, on any target of its
// product, while they are not cleared. A block of a whole target, of the
// asset, or of another release of the asset is none of them, and neither is
// one of another product. While the read is in its transaction every target
// of the product is held, so a block cannot be placed on any of them until
// the transaction ends, and the read refuses to run outside a transaction.
func TestTheBlocksOfAReleaseAreThoseThatNameItOnAnyTargetOfItsProduct(t *testing.T) {
	f := newSlotStore(t)
	owner := pgtest.Connect(t, f.database.Migrator)
	serving, other := f.target(f.product, "engineering"), f.target(f.product, "preview")
	elsewhere := f.target(ids.NewUUID(), "engineering")
	asset, release, another := ids.NewUUID(), ids.NewUUID(), ids.NewUUID()
	security := domain.Principal{Issuer: "https://issuer.example", Subject: "security"}

	generations := map[string]int64{}
	place := func(target domain.Target, assetID, releaseID string) domain.AccessBlock {
		t.Helper()
		generations[target.ID]++
		block := domain.AccessBlock{
			ID: ids.NewUUID(), ProductID: target.ProductID, TargetID: target.ID, AssetID: assetID, ReleaseID: releaseID,
			Generation: generations[target.ID], Reason: "under audit", CreatedBy: security, CreatedAt: at(int(generations[target.ID])),
		}
		f.within(func(ctx context.Context) error {
			_, _, err := f.store.PutBlock(ctx, block)
			return err
		})
		return block
	}
	blocked := func(when string, want bool) {
		t.Helper()
		var got bool
		f.within(func(ctx context.Context) (err error) {
			got, err = f.store.ReleaseBlocked(ctx, f.product, asset, release)
			return err
		})
		if got != want {
			t.Errorf("%s: the release is blocked %v, want %v", when, got, want)
		}
	}

	blocked("no block", false)
	place(serving, "", "")
	place(serving, asset, "")
	place(serving, asset, another)
	place(elsewhere, asset, release)
	blocked("blocks of the target, of the asset, of another release and of another product", false)

	block := place(other, asset, release)
	blocked("a block of the release on the other target", true)

	// The read holds every target of the product: neither can be locked to
	// place a block until its transaction ends, and a target of another
	// product can.
	f.within(func(ctx context.Context) error {
		if _, err := f.store.ReleaseBlocked(ctx, f.product, asset, release); err != nil {
			return err
		}
		for _, target := range []domain.Target{serving, other} {
			_, err := owner.Exec(t.Context(), `SELECT 1 FROM publication.target WHERE target_id = $1 FOR UPDATE NOWAIT`, target.ID)
			var failure *pgconn.PgError
			if !errors.As(err, &failure) || failure.Code != "55P03" {
				t.Errorf("the target %s locked for update while the blocks are read: %v, want it held", target.PortalKey, err)
			}
		}
		if _, err := owner.Exec(t.Context(), `SELECT 1 FROM publication.target WHERE target_id = $1 FOR UPDATE NOWAIT`, elsewhere.ID); err != nil {
			t.Errorf("a target of another product is held: %v", err)
		}
		return nil
	})

	generations[other.ID]++
	block.ClearGeneration, block.ClearedAt, block.ClearReason = generations[other.ID], at(10), "the host was renamed"
	block.ClearedBy = domain.Principal{Issuer: security.Issuer, Subject: "clearer"}
	f.within(func(ctx context.Context) error { return f.store.ClearBlock(ctx, block) })
	blocked("the block cleared", false)

	if _, err := f.store.ReleaseBlocked(t.Context(), f.product, asset, release); err == nil {
		t.Error("the blocks of a release were read outside a transaction, where nothing is held")
	}
}
