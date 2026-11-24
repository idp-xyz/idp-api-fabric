package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

// A list of slots is read a page at a time, in an order that is fixed: by the
// instant the first operation of a slot was accepted, then by the identifier
// of the slot, which tells apart the slots of one instant. A slot is made
// with its first operation, in one transaction, and neither value changes
// afterwards, so a slot keeps its place for as long as it is listed, and a
// page starts behind a position wherever the slots around it are by then.
// What is done to a slot later does not move it. A slot that took no
// operation is no publication and is not listed.
//
// The position is compared as a row, the way the order compares it. A page
// that names no position starts behind one that precedes every slot, so one
// statement reads the first page as it reads the others.
//
// The slots of a target are those of the assets the caller reads: the set is
// a condition of the statement, ahead of the position and the LIMIT, so that
// every page but the last is full. The set is the caller's to give, and an
// empty one is no asset, never every asset. The unique index of the target
// and the asset finds the slots of a target. The slots of an asset are found
// from the targets of its product, a handful, each looked up by that index:
// the slots alone have no index that leads with the asset, and asked of them
// the statement reads every slot of every product.
const slotsOfTarget = `SELECT s.slot_id, s.api_asset_id, coalesce(s.active_release_id::text, ''), s.generation, o.occurred_at
FROM publication.slot s
JOIN publication.operation o ON o.slot_id = s.slot_id AND o.generation = 1
WHERE s.product_id = $1 AND s.target_id = $2 AND s.generation > 0
  AND s.api_asset_id = ANY($3::text[]::uuid[])
  AND (o.occurred_at, s.slot_id) > (coalesce($4, '-infinity'::timestamptz), coalesce($5, '00000000-0000-0000-0000-000000000000'::uuid))
ORDER BY o.occurred_at, s.slot_id
LIMIT $6`

const slotsOfAsset = `SELECT t.target_id, t.product_id, t.audience_config_id, t.portal_key, t.audience, t.environment,
       t.block_generation, t.created_at,
       s.slot_id, s.api_asset_id, coalesce(s.active_release_id::text, ''), s.generation, o.occurred_at
FROM publication.target t
JOIN publication.slot s ON s.target_id = t.target_id AND s.api_asset_id = $2 AND s.generation > 0
JOIN publication.operation o ON o.slot_id = s.slot_id AND o.generation = 1
WHERE t.product_id = $1
  AND (o.occurred_at, s.slot_id) > (coalesce($3, '-infinity'::timestamptz), coalesce($4, '00000000-0000-0000-0000-000000000000'::uuid))
ORDER BY o.occurred_at, s.slot_id
LIMIT $5`

// slotAfter is the parameters of a position: none for the start of a list.
func slotAfter(after *ports.SlotPosition) (at, id any) {
	if after != nil {
		return after.At, after.ID
	}
	return nil, nil
}

// Slots reads a page of the slots of the target on the assets named. It runs
// in the transaction ctx carries if there is one and on the pool otherwise,
// as every read does.
func (s *Store) Slots(ctx context.Context, target domain.Target, assets []string, after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
	if limit < 1 {
		return nil, errors.New("a page takes at least one row")
	}
	if len(assets) == 0 {
		return nil, nil
	}
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	at, id := slotAfter(after)
	rows, err := read.Query(ctx, slotsOfTarget, target.ProductID, target.ID, assets, at, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.PlacedSlot, error) {
		placed := ports.PlacedSlot{Target: target}
		slot := &placed.Slot
		slot.ProductID, slot.TargetID = target.ProductID, target.ID
		err := row.Scan(&slot.ID, &slot.AssetID, &slot.ActiveReleaseID, &slot.Generation, &placed.Since)
		placed.Since = placed.Since.UTC()
		return placed, err
	})
}

// AssetSlots reads a page of the slots of the asset on the targets of its
// product, each with its target.
func (s *Store) AssetSlots(ctx context.Context, productID, assetID string, after *ports.SlotPosition, limit int) ([]ports.PlacedSlot, error) {
	if limit < 1 {
		return nil, errors.New("a page takes at least one row")
	}
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	at, id := slotAfter(after)
	rows, err := read.Query(ctx, slotsOfAsset, productID, assetID, at, id, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.PlacedSlot, error) {
		var placed ports.PlacedSlot
		target, slot := &placed.Target, &placed.Slot
		err := row.Scan(&target.ID, &target.ProductID, &target.AudienceConfigID, &target.PortalKey, &target.Audience,
			&target.Environment, &target.BlockGeneration, &target.CreatedAt,
			&slot.ID, &slot.AssetID, &slot.ActiveReleaseID, &slot.Generation, &placed.Since)
		slot.ProductID, slot.TargetID = target.ProductID, target.ID
		placed.Since = placed.Since.UTC()
		return placed, err
	})
}
