package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type Store struct{ db *bentopostgres.DB }

func NewStore(db *bentopostgres.DB) *Store { return &Store{db: db} }

const targetFields = `target_id, product_id, audience_config_id, portal_key, audience, environment, block_generation, created_at`

func scanTarget(row pgx.Row) (domain.Target, error) {
	var t domain.Target
	err := row.Scan(&t.ID, &t.ProductID, &t.AudienceConfigID, &t.PortalKey, &t.Audience, &t.Environment, &t.BlockGeneration, &t.CreatedAt)
	return t, err
}

func (s *Store) PutTarget(ctx context.Context, target domain.Target) (domain.Target, bool, error) {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return domain.Target{}, false, err
	}
	const insert = `INSERT INTO publication.target
    (target_id, product_id, audience_config_id, portal_key, audience, environment, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (product_id, audience_config_id, portal_key) DO NOTHING`
	result, err := tx.Exec(ctx, insert, target.ID, target.ProductID, target.AudienceConfigID, target.PortalKey,
		target.Audience, target.Environment, target.CreatedAt)
	if err != nil {
		return domain.Target{}, false, fmt.Errorf("record publication target: %w", err)
	}
	if result.RowsAffected() == 1 {
		return target, true, nil
	}
	const byScope = `SELECT ` + targetFields + ` FROM publication.target
WHERE product_id = $1 AND audience_config_id = $2 AND portal_key = $3`
	existing, err := scanTarget(tx.QueryRow(ctx, byScope, target.ProductID, target.AudienceConfigID, target.PortalKey))
	return existing, false, err
}

func (s *Store) Target(ctx context.Context, productID, targetID string, lock ports.TargetLock) (domain.Target, bool, error) {
	query := `SELECT ` + targetFields + ` FROM publication.target WHERE product_id = $1 AND target_id = $2`
	switch lock {
	case ports.HoldTarget:
		query += ` FOR SHARE`
	case ports.EditBlock:
		query += ` FOR UPDATE`
	}
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return domain.Target{}, false, err
	}
	target, err := scanTarget(read.QueryRow(ctx, query, productID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Target{}, false, nil
	}
	return target, err == nil, err
}

func (s *Store) Targets(ctx context.Context, productID string, after *ports.TargetPosition, limit int) ([]domain.Target, error) {
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	const query = `SELECT ` + targetFields + ` FROM publication.target
WHERE product_id = $1 AND ($3::timestamptz IS NULL OR (created_at, target_id) > ($3, $4::uuid))
ORDER BY created_at, target_id LIMIT $2`
	var at *time.Time
	var id *string
	if after != nil {
		at, id = &after.At, &after.ID
	}
	rows, err := read.Query(ctx, query, productID, limit, at, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Target, error) { return scanTarget(row) })
}

func (s *Store) Slot(ctx context.Context, request domain.Request, locked bool) (domain.Slot, bool, error) {
	query := `SELECT slot_id, product_id, target_id, api_asset_id, coalesce(active_release_id::text, ''), generation
FROM publication.slot WHERE product_id = $1 AND target_id = $2 AND api_asset_id = $3`
	if locked {
		query += ` FOR UPDATE`
	}
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return domain.Slot{}, false, err
	}
	var slot domain.Slot
	err = read.QueryRow(ctx, query, request.ProductID, request.TargetID, request.AssetID).Scan(
		&slot.ID, &slot.ProductID, &slot.TargetID, &slot.AssetID, &slot.ActiveReleaseID, &slot.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Slot{}, false, nil
	}
	return slot, err == nil, err
}

func (s *Store) EnsureSlot(ctx context.Context, slot domain.Slot) error {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const insert = `INSERT INTO publication.slot (slot_id, product_id, target_id, api_asset_id)
VALUES ($1, $2, $3, $4) ON CONFLICT (target_id, api_asset_id) DO NOTHING`
	_, err = tx.Exec(ctx, insert, slot.ID, slot.ProductID, slot.TargetID, slot.AssetID)
	return err
}

const selectOperation = `SELECT operation_id, slot_id, generation, action,
       coalesce(previous_release_id::text, ''), coalesce(release_id::text, ''), reason,
       actor_issuer, actor_subject, occurred_at
FROM publication.operation WHERE slot_id = $1`

func scanOperation(row pgx.Row) (domain.Operation, error) {
	var op domain.Operation
	err := row.Scan(&op.ID, &op.SlotID, &op.Generation, &op.Action, &op.PreviousReleaseID,
		&op.ReleaseID, &op.Reason, &op.By.Issuer, &op.By.Subject, &op.At)
	return op, err
}

func (s *Store) OperationByKey(ctx context.Context, slotID, digest string) (domain.Operation, bool, error) {
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return domain.Operation{}, false, err
	}
	op, err := scanOperation(read.QueryRow(ctx, selectOperation+` AND key_digest = $2`, slotID, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	return op, err == nil, err
}

func (s *Store) Operations(ctx context.Context, slotID string, after int64, limit int) ([]domain.Operation, error) {
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := read.Query(ctx, selectOperation+` AND generation > $2 ORDER BY generation LIMIT $3`, slotID, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Operation, error) { return scanOperation(row) })
}

func (s *Store) PriorRelease(ctx context.Context, slotID, releaseID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM publication.operation WHERE slot_id = $1 AND release_id = $2)`
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	err = read.QueryRow(ctx, query, slotID, releaseID).Scan(&found)
	return found, err
}

func (s *Store) LatestRelease(ctx context.Context, slotID string) (string, error) {
	const query = `SELECT release_id FROM publication.operation
WHERE slot_id = $1 AND release_id IS NOT NULL ORDER BY generation DESC LIMIT 1`
	read, err := s.db.ReadExecutor(ctx)
	if err != nil {
		return "", err
	}
	var id string
	err = read.QueryRow(ctx, query, slotID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return id, err
}

func (s *Store) Apply(ctx context.Context, slot domain.Slot, op domain.Operation, keyDigest string) error {
	tx, err := s.db.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const update = `UPDATE publication.slot SET generation = $3, active_release_id = $4
WHERE slot_id = $1 AND generation = $2`
	updated, err := tx.Exec(ctx, update, slot.ID, slot.Generation, op.Generation, optionalID(op.ReleaseID))
	if err != nil {
		return fmt.Errorf("advance publication slot: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return domain.ErrGeneration
	}
	const insert = `INSERT INTO publication.operation
    (operation_id, slot_id, generation, key_digest, action, previous_release_id, release_id,
     reason, actor_issuer, actor_subject, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err = tx.Exec(ctx, insert, op.ID, slot.ID, op.Generation, keyDigest, op.Action,
		optionalID(op.PreviousReleaseID), optionalID(op.ReleaseID), op.Reason, op.By.Issuer, op.By.Subject, op.At)
	return err
}

func optionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
