package application

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

// PartnerAccess binds one partner target to a live grant authority and an
// artifact reader that rechecks the product, the configuration of the target
// and the release it serves.
type PartnerAccess struct {
	ProductID     string
	TargetID      string
	ProductActive func(ctx context.Context, productID string) (bool, error)
	Allowed       func(context.Context, ports.PartnerGrantRequest) (bool, error)
	Read          func(ctx context.Context, request domain.Request, target domain.Target, releaseID string) (ports.Artifact, error)
}

func (p *PartnerAccess) matches(target domain.Target) bool {
	return p != nil && target.Audience == "partner" && p.ProductID == target.ProductID && p.TargetID == target.ID
}

// PartnerArtifact reads an asset at the configured site. The grant check
// runs outside the database transaction. A second target read orders the
// artifact read against AccessBlock while Read holds the current audience
// configuration.
func (s *Publication) PartnerArtifact(ctx context.Context, caller domain.Principal, assetID, correlationID string) (ports.Artifact, error) {
	partner := s.deps.Partner
	if partner == nil {
		return ports.Artifact{}, ErrNoVisibleArtifact
	}
	request := domain.Request{
		Caller: caller, ProductID: partner.ProductID, TargetID: partner.TargetID, AssetID: assetID, CorrelationID: correlationID,
	}
	target, err := s.target(ctx, request, ports.Unlocked)
	switch {
	case errors.Is(err, ErrTargetNotFound):
		return ports.Artifact{}, s.refusePartnerRead(ctx, request, ErrNoVisibleArtifact)
	case err != nil:
		return ports.Artifact{}, err
	case !partner.matches(target):
		return ports.Artifact{}, s.refusePartnerRead(ctx, request, ErrNoVisibleArtifact)
	}

	allowed, err := partner.Allowed(ctx, ports.PartnerGrantRequest{
		Caller: caller, ProductID: request.ProductID, TargetID: request.TargetID,
		AssetID: assetID, Environment: target.Environment,
	})
	if err != nil {
		return ports.Artifact{}, err
	}
	if !allowed {
		return ports.Artifact{}, s.refusePartnerRead(ctx, request, ErrNoVisibleArtifact)
	}

	var artifact ports.Artifact
	err = s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		current, err := s.target(tx, request, ports.HoldTarget)
		switch {
		case errors.Is(err, ErrTargetNotFound):
			return ErrNoVisibleArtifact
		case err != nil:
			return err
		case !partner.matches(current) || current.Environment != target.Environment || current.AudienceConfigID != target.AudienceConfigID:
			return ErrNoVisibleArtifact
		}
		slot, err := s.slot(tx, request, false)
		if err != nil {
			return err
		}
		if slot.ActiveReleaseID == "" {
			return ErrNoVisibleArtifact
		}
		blocked, err := s.deps.Store.HasBlock(tx, current, assetID, slot.ActiveReleaseID)
		if err != nil {
			return err
		}
		if blocked {
			return ErrNoVisibleArtifact
		}
		artifact, err = partner.Read(tx, request, current, slot.ActiveReleaseID)
		return err
	})
	return artifact, s.refusePartnerRead(ctx, request, err)
}

func (s *Publication) refusePartnerRead(ctx context.Context, request domain.Request, err error) error {
	return s.refused(ctx, request, "publication.download", "PublicationSlot", err)
}
