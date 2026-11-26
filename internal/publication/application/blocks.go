package application

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

var (
	ErrBlockNotFound  = errors.New("no such access block of the target")
	ErrBlockExists    = errors.New("an active access block already covers the scope")
	ErrBlockCleared   = errors.New("the access block was cleared already")
	ErrNotIndependent = errors.New("the author of a block cannot clear it")
	ErrWithdrawFirst  = errors.New("affected releases must be withdrawn before clearing the block")
)

type BlockResult struct {
	Block   domain.AccessBlock
	Created bool
}

func (s *Publication) BlockAccess(ctx context.Context, request domain.Request, releaseID, reason string) (BlockResult, error) {
	if err := domain.ValidateReason(reason); err != nil || (releaseID != "" && request.AssetID == "") {
		return BlockResult{}, domain.ErrInvalid
	}
	const action = "security.block"
	var result BlockResult
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.deps.Authority.Security(tx, request, false); err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.EditBlock)
		if err != nil {
			return err
		}
		candidate := domain.AccessBlock{
			ID: s.deps.IDs.NewID(), ProductID: target.ProductID, TargetID: target.ID,
			AssetID: request.AssetID, ReleaseID: releaseID, Generation: target.BlockGeneration + 1,
			Reason: reason, CreatedBy: request.Caller, CreatedAt: s.now(),
		}
		if err := s.deps.Authority.BlockScope(tx, candidate); err != nil {
			return err
		}
		block, created, err := s.deps.Store.PutBlock(tx, candidate)
		if err != nil {
			return err
		}
		result = BlockResult{Block: block, Created: created}
		if !created {
			if block.CreatedBy == request.Caller && block.Reason == reason {
				return nil
			}
			return ErrBlockExists
		}
		return s.record(tx, request, action, "AC-06", "AccessBlock", block.ID, ports.Event{
			Name: "access_block.created", Aggregate: "access_block", AggregateID: block.ID, AggregateVersion: 1,
			TargetID: target.ID, AssetID: block.AssetID, ReleaseID: block.ReleaseID, BlockGeneration: block.Generation, At: block.CreatedAt,
		})
	})
	return result, s.refused(ctx, request, action, "AccessBlock", err)
}

func (s *Publication) ClearBlock(ctx context.Context, request domain.Request, id, reason string) (domain.AccessBlock, error) {
	if err := domain.ValidateReason(reason); err != nil {
		return domain.AccessBlock{}, err
	}
	const action = "security.unblock"
	var result domain.AccessBlock
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.deps.Authority.Security(tx, request, true); err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.EditBlock)
		if err != nil {
			return err
		}
		block, found, err := s.deps.Store.Block(tx, target, id)
		if err != nil {
			return err
		}
		if !found {
			return ErrBlockNotFound
		}
		if !block.Active() {
			if block.ClearedBy == request.Caller && block.ClearReason == reason {
				result = block
				return nil
			}
			return ErrBlockCleared
		}
		if block.CreatedBy == request.Caller {
			return ErrNotIndependent
		}
		// Clearing a deny cannot make a hidden release visible as a side
		// effect. Restore is a separate CAS command with checks of its own.
		active, err := s.deps.Store.HasActiveRelease(tx, block)
		if err != nil {
			return err
		}
		if active {
			return ErrWithdrawFirst
		}
		block.ClearGeneration = target.BlockGeneration + 1
		block.ClearedAt = s.now()
		if block.ClearedAt.Before(block.CreatedAt) {
			block.ClearedAt = block.CreatedAt
		}
		block.ClearedBy, block.ClearReason = request.Caller, reason
		if err := s.deps.Store.ClearBlock(tx, block); err != nil {
			return err
		}
		result = block
		return s.record(tx, request, action, "AC-07", "AccessBlock", block.ID, ports.Event{
			Name: "access_block.cleared", Aggregate: "access_block", AggregateID: block.ID, AggregateVersion: 2,
			TargetID: target.ID, AssetID: block.AssetID, ReleaseID: block.ReleaseID, BlockGeneration: block.ClearGeneration, At: block.ClearedAt,
		})
	})
	return result, s.refused(ctx, request, action, "AccessBlock", err)
}

type BlockPage struct {
	Items []domain.AccessBlock
	Next  *int64
}

func (s *Publication) GetBlock(ctx context.Context, request domain.Request, id string) (domain.AccessBlock, error) {
	var block domain.AccessBlock
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.deps.Authority.ReadBlocks(tx, request); err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.Unlocked)
		if err != nil {
			return err
		}
		stored, found, err := s.deps.Store.Block(tx, target, id)
		if err == nil && !found {
			return ErrBlockNotFound
		}
		block = stored
		return err
	})
	return block, s.refused(ctx, request, "security.block.read", "AccessBlock", err)
}

func (s *Publication) ListBlocks(ctx context.Context, request domain.Request, after int64, limit int) (BlockPage, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return BlockPage{}, domain.ErrInvalid
	}
	var result BlockPage
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		if err := s.deps.Authority.ReadBlocks(tx, request); err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.Unlocked)
		if err != nil {
			return err
		}
		items, err := s.deps.Store.Blocks(tx, target, after, limit+1)
		if len(items) > limit {
			last := items[limit-1].Generation
			result.Next, items = &last, items[:limit]
		}
		result.Items = items
		return err
	})
	return result, s.refused(ctx, request, "security.block.read", "AccessBlock", err)
}
