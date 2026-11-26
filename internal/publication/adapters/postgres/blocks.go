package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

const selectBlock = `SELECT block_id, product_id, target_id, coalesce(api_asset_id::text, ''), coalesce(release_id::text, ''),
       generation, reason, created_by_issuer, created_by_subject, created_at,
       coalesce(clear_generation, 0), coalesce(clear_reason, ''), coalesce(cleared_by_issuer, ''),
       coalesce(cleared_by_subject, ''), cleared_at
FROM publication.access_block WHERE product_id = $1 AND target_id = $2`

func scanBlock(row pgx.Row) (domain.AccessBlock, error) {
	var b domain.AccessBlock
	var clearedAt *time.Time
	err := row.Scan(&b.ID, &b.ProductID, &b.TargetID, &b.AssetID, &b.ReleaseID, &b.Generation,
		&b.Reason, &b.CreatedBy.Issuer, &b.CreatedBy.Subject, &b.CreatedAt, &b.ClearGeneration,
		&b.ClearReason, &b.ClearedBy.Issuer, &b.ClearedBy.Subject, &clearedAt)
	if clearedAt != nil {
		b.ClearedAt = *clearedAt
	}
	return b, err
}

func (s *Store) HasBlock(ctx context.Context, target domain.Target, assetID, releaseID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM publication.access_block
WHERE product_id = $1 AND target_id = $2 AND cleared_at IS NULL
  AND (api_asset_id IS NULL OR api_asset_id = $3) AND (release_id IS NULL OR release_id = $4))`
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	err = read.QueryRow(ctx, query, target.ProductID, target.ID, optionalID(assetID), optionalID(releaseID)).Scan(&found)
	return found, err
}

// ReleaseBlocked takes the holds before it reads the blocks, and in a
// statement of its own: a statement reads the rows committed when it starts,
// so a block that was made or cleared while a hold was waited for is read by
// the second statement and would be missed by one that took the holds too.
func (s *Store) ReleaseBlocked(ctx context.Context, productID, assetID, releaseID string) (bool, error) {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return false, err
	}
	const hold = `SELECT target_id FROM publication.target WHERE product_id = $1 ORDER BY target_id FOR SHARE`
	if _, err := tx.Exec(ctx, hold, productID); err != nil {
		return false, fmt.Errorf("hold the publication targets of the product: %w", err)
	}
	const query = `SELECT EXISTS (SELECT 1 FROM publication.access_block
WHERE product_id = $1 AND api_asset_id = $2 AND release_id = $3 AND cleared_at IS NULL)`
	var found bool
	err = tx.QueryRow(ctx, query, productID, assetID, releaseID).Scan(&found)
	return found, err
}

func (s *Store) Block(ctx context.Context, target domain.Target, id string) (domain.AccessBlock, bool, error) {
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return domain.AccessBlock{}, false, err
	}
	block, err := scanBlock(read.QueryRow(ctx, selectBlock+` AND block_id = $3`, target.ProductID, target.ID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessBlock{}, false, nil
	}
	return block, err == nil, err
}

func (s *Store) Blocks(ctx context.Context, target domain.Target, after int64, limit int) ([]domain.AccessBlock, error) {
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := read.Query(ctx, selectBlock+` AND generation > $3 ORDER BY generation LIMIT $4`, target.ProductID, target.ID, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AccessBlock, error) { return scanBlock(row) })
}

func (s *Store) PutBlock(ctx context.Context, b domain.AccessBlock) (domain.AccessBlock, bool, error) {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return domain.AccessBlock{}, false, err
	}
	const insert = `INSERT INTO publication.access_block
    (block_id, product_id, target_id, api_asset_id, release_id, generation, reason, created_by_issuer, created_by_subject, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (target_id, api_asset_id, release_id) WHERE cleared_at IS NULL DO NOTHING`
	result, err := tx.Exec(ctx, insert, b.ID, b.ProductID, b.TargetID, optionalID(b.AssetID), optionalID(b.ReleaseID),
		b.Generation, b.Reason, b.CreatedBy.Issuer, b.CreatedBy.Subject, b.CreatedAt)
	if err != nil {
		return domain.AccessBlock{}, false, fmt.Errorf("record access block: %w", err)
	}
	if result.RowsAffected() == 1 {
		return b, true, s.advanceBlocks(ctx, b.ProductID, b.TargetID, b.Generation)
	}
	const exact = selectBlock + ` AND cleared_at IS NULL AND api_asset_id IS NOT DISTINCT FROM $3
  AND release_id IS NOT DISTINCT FROM $4`
	prior, err := scanBlock(tx.QueryRow(ctx, exact, b.ProductID, b.TargetID, optionalID(b.AssetID), optionalID(b.ReleaseID)))
	return prior, false, err
}

func (s *Store) HasActiveRelease(ctx context.Context, b domain.AccessBlock) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM publication.slot
WHERE product_id = $1 AND target_id = $2 AND active_release_id IS NOT NULL
  AND ($3::uuid IS NULL OR api_asset_id = $3) AND ($4::uuid IS NULL OR active_release_id = $4))`
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return false, err
	}
	var active bool
	err = read.QueryRow(ctx, query, b.ProductID, b.TargetID, optionalID(b.AssetID), optionalID(b.ReleaseID)).Scan(&active)
	return active, err
}

func (s *Store) ClearBlock(ctx context.Context, b domain.AccessBlock) error {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const clear = `UPDATE publication.access_block
SET clear_generation = $4, clear_reason = $5, cleared_by_issuer = $6, cleared_by_subject = $7, cleared_at = $8
WHERE product_id = $1 AND target_id = $2 AND block_id = $3 AND cleared_at IS NULL`
	result, err := tx.Exec(ctx, clear, b.ProductID, b.TargetID, b.ID, b.ClearGeneration, b.ClearReason,
		b.ClearedBy.Issuer, b.ClearedBy.Subject, b.ClearedAt)
	if err != nil {
		return fmt.Errorf("clear access block: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("the access block disappeared during clearance")
	}
	return s.advanceBlocks(ctx, b.ProductID, b.TargetID, b.ClearGeneration)
}

func (s *Store) advanceBlocks(ctx context.Context, productID, targetID string, generation int64) error {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const update = `UPDATE publication.target SET block_generation = $3
WHERE product_id = $1 AND target_id = $2 AND block_generation = $3 - 1`
	result, err := tx.Exec(ctx, update, productID, targetID, generation)
	if err != nil {
		return fmt.Errorf("advance block generation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("the target block generation changed during the transaction")
	}
	return nil
}
