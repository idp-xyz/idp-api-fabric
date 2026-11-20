package application

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// MoveRevision changes the status of a revision along its review, inside the
// transaction of the caller. It fails with ErrRevisionNotFound for a
// revision the asset does not have and with domain.ErrStatusConflict for one
// that is not at a status the move starts from.
//
// It authorizes nothing: who may submit a revision and who may decide on it
// is for Policy & Review to say, which records the review this move belongs
// to in the same transaction. No operation of the API reaches it by itself.
func (r *Registry) MoveRevision(ctx context.Context, asset domain.AssetRef, id, to string) (domain.Revision, error) {
	revision, found, err := r.deps.Revisions.Move(ctx, asset, id, to)
	if err != nil {
		return domain.Revision{}, err
	}
	if !found {
		return domain.Revision{}, ErrRevisionNotFound
	}
	return revision, nil
}

// HoldRevision keeps a revision at the status it has until the transaction
// of the caller ends, and returns it as it is. Nothing moves the revision
// meanwhile, and a second hold waits for the first: what Policy & Review
// decides about a revision without moving it, a review of one that is
// approved already, it decides behind a hold, one request at a time. It
// fails with ErrRevisionNotFound for a revision the asset does not have.
//
// Like MoveRevision it authorizes nothing, and no operation of the API
// reaches it by itself.
func (r *Registry) HoldRevision(ctx context.Context, asset domain.AssetRef, id string) (domain.Revision, error) {
	revision, found, err := r.deps.Revisions.Hold(ctx, asset, id)
	if err != nil {
		return domain.Revision{}, err
	}
	if !found {
		return domain.Revision{}, ErrRevisionNotFound
	}
	return revision, nil
}
