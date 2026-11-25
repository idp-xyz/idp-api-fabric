package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

var (
	ErrTargetNotFound    = errors.New("no such publication target of the product")
	ErrForbidden         = errors.New("the caller may not perform the publication action")
	ErrProductSuspended  = errors.New("the product does not admit new work")
	ErrUnavailableTarget = errors.New("the target cannot serve publications")
	ErrBlocked           = errors.New("an access block denies publication to the target")
	ErrNoVisibleArtifact = errors.New("no active artifact can be served for this target and asset")
	ErrRefusalNotAudited = errors.New("a publication refusal could not be audited")
)

type Dependencies struct {
	Transactions ports.Transactions
	Store        ports.Store
	Authority    ports.Authority
	Audit        func(context.Context, ports.AuditEntry) error
	Enqueue      func(context.Context, domain.Request, ports.Event) error
	Clock        ports.Clock
	IDs          ports.Identifiers
}

type Publication struct{ deps Dependencies }

func New(deps Dependencies) (*Publication, error) {
	if deps.Transactions == nil || deps.Store == nil || deps.Authority == nil || deps.Audit == nil ||
		deps.Enqueue == nil || deps.Clock == nil || deps.IDs == nil {
		return nil, errors.New("publication needs transactions, storage, authority, audit, outbox, clock and identifiers")
	}
	return &Publication{deps: deps}, nil
}

type TargetRegistration struct {
	Target  domain.Target
	Created bool
}

func (s *Publication) RegisterTarget(ctx context.Context, request domain.Request, configID, portalKey string) (TargetRegistration, error) {
	if err := domain.ValidatePortalKey(portalKey); err != nil {
		return TargetRegistration{}, err
	}
	var result TargetRegistration
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		active, err := s.deps.Authority.TargetManager(tx, request)
		if err != nil {
			return err
		}
		if !active {
			return ErrProductSuspended
		}
		audience, environment, err := s.deps.Authority.Config(tx, request.ProductID, configID)
		if err != nil {
			return err
		}
		target := domain.Target{
			ID: s.deps.IDs.NewID(), ProductID: request.ProductID, AudienceConfigID: configID, PortalKey: portalKey,
			Audience: audience, Environment: environment, CreatedAt: s.now(),
		}
		result.Target, result.Created, err = s.deps.Store.PutTarget(tx, target)
		if err != nil || !result.Created {
			return err
		}
		return s.record(tx, request, "publication.target.create", "AC-09", "PublicationTarget", target.ID, ports.Event{
			Name: "target.created", Aggregate: "publication_target", AggregateID: target.ID,
			AggregateVersion: 1, TargetID: target.ID, At: target.CreatedAt,
		})
	})
	return result, s.refused(ctx, request, "publication.target.create", "PublicationTarget", err)
}

func (s *Publication) GetTarget(ctx context.Context, request domain.Request) (domain.Target, error) {
	if err := s.deps.Authority.ProductKnown(ctx, request); err != nil {
		return domain.Target{}, s.refused(ctx, request, "publication.target.read", "PublicationTarget", err)
	}
	target, err := s.target(ctx, request, ports.Unlocked)
	return target, s.refused(ctx, request, "publication.target.read", "PublicationTarget", err)
}

type TargetPage struct {
	Items []domain.Target
	Next  *ports.TargetPosition
}

func (s *Publication) ListTargets(ctx context.Context, request domain.Request, after *ports.TargetPosition, limit int) (TargetPage, error) {
	if limit < 1 || limit > 100 {
		return TargetPage{}, domain.ErrInvalid
	}
	if err := s.deps.Authority.ProductKnown(ctx, request); err != nil {
		return TargetPage{}, s.refused(ctx, request, "publication.target.read", "PublicationTarget", err)
	}
	items, err := s.deps.Store.Targets(ctx, request.ProductID, after, limit+1)
	if err != nil {
		return TargetPage{}, err
	}
	if len(items) <= limit {
		return TargetPage{Items: items}, nil
	}
	last := items[limit-1]
	return TargetPage{Items: items[:limit], Next: &ports.TargetPosition{At: last.CreatedAt, ID: last.ID}}, nil
}

// GetSlot reads what a slot points at, whether an access block stands
// between that release and whoever asks for its artifact, and whether the
// approval the release rests on still holds. A block is why the artifact is
// not served, and the read that is refused says nothing of it. An approval
// that does not hold takes nothing down: it is why the release would not be
// put on the slot today, and whoever may read the slot is told here.
func (s *Publication) GetSlot(ctx context.Context, request domain.Request) (domain.SlotView, error) {
	var result domain.SlotView
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		access, err := s.asset(tx, request, false)
		if err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.HoldTarget)
		if err != nil {
			return err
		}
		slot, err := s.slot(tx, request, false)
		if err != nil {
			return err
		}
		result, err = s.view(tx, request, access.WorkspaceID, target, slot)
		return err
	})
	return result, s.refused(ctx, request, "publication.read", "PublicationSlot", err)
}

func (s *Publication) ChangeSlot(ctx context.Context, request domain.Request, change domain.Change) (domain.Operation, error) {
	if err := change.Validate(); err != nil {
		return domain.Operation{}, err
	}
	var result domain.Operation
	action, rule, event := operationNames(change.Action)
	key := change.KeyDigest()
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		access, err := s.asset(tx, request, true)
		if err != nil {
			return err
		}
		target, err := s.target(tx, request, ports.HoldTarget)
		if err != nil {
			return err
		}
		slot, err := s.slot(tx, request, true)
		if err != nil {
			return err
		}
		if slot.ID == "" && change.Action == domain.Activate && change.ExpectedGeneration == 0 {
			slot.ID = s.deps.IDs.NewID()
			if err := s.deps.Store.EnsureSlot(tx, slot); err != nil {
				return err
			}
			slot, err = s.slot(tx, request, true)
			if err != nil {
				return err
			}
		}
		if slot.ID != "" {
			previous, found, err := s.deps.Store.OperationByKey(tx, slot.ID, key)
			if err != nil {
				return err
			}
			if found {
				if !previous.Matches(change, request.Caller) {
					return domain.ErrIdempotency
				}
				result = previous
				return nil
			}
		}
		op, err := slot.Operation(change, s.deps.IDs.NewID(), request.Caller, s.now())
		if err != nil {
			return err
		}
		if change.Action != domain.Withdraw {
			if err := s.checkActivation(tx, request, access, target, slot, op); err != nil {
				return err
			}
		}
		if err := s.deps.Store.Apply(tx, slot, op, key); err != nil {
			return err
		}
		result = op
		return s.record(tx, request, action, rule, "PublicationSlot", slot.ID, ports.Event{
			Name: event, Aggregate: "publication_slot", AggregateID: slot.ID, AggregateVersion: op.Generation,
			TargetID: target.ID, AssetID: slot.AssetID, ReleaseID: op.ReleaseID, At: op.At,
		})
	})
	return result, s.refused(ctx, request, action, "PublicationSlot", err)
}

// checkActivation decides whether a release is put on a slot. An activation
// puts it there on an approval that holds for the target now, on a check
// that compared its revision with what the slot served last. A restoration
// returns the slot to a release it served before, which is what a publisher
// falls back to when what is served has to go: the release has to be in the
// history of the slot, and whether its approval still holds is not asked.
// Both take a product that admits work, a target that serves, no access
// block and a publisher who had no part in the revision.
func (s *Publication) checkActivation(ctx context.Context, request domain.Request, access ports.AssetAccess, target domain.Target, slot domain.Slot, op domain.Operation) error {
	if !access.Active {
		return ErrProductSuspended
	}
	if err := s.servingTarget(ctx, target); err != nil {
		return err
	}
	blocked, err := s.deps.Store.HasBlock(ctx, target, slot.AssetID, op.ReleaseID)
	if err != nil {
		return err
	}
	if blocked {
		return ErrBlocked
	}
	var baseline string
	returning := op.Action == domain.Restore
	if returning {
		if slot.ID == "" {
			return domain.ErrNotInHistory
		}
		found, err := s.deps.Store.PriorRelease(ctx, slot.ID, op.ReleaseID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrNotInHistory
		}
	} else {
		baseline, err = s.deps.Store.LatestRelease(ctx, slot.ID)
		if err != nil {
			return err
		}
	}
	return s.deps.Authority.Admission(ctx, request, access.WorkspaceID, target, op.ReleaseID, baseline, returning)
}

type OperationPage struct {
	Items []domain.Operation
	Next  *int64
}

func (s *Publication) ListOperations(ctx context.Context, request domain.Request, after int64, limit int) (OperationPage, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return OperationPage{}, domain.ErrInvalid
	}
	var result OperationPage
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		if _, err := s.asset(tx, request, false); err != nil {
			return err
		}
		if _, err := s.target(tx, request, ports.Unlocked); err != nil {
			return err
		}
		slot, err := s.slot(tx, request, false)
		if err != nil || slot.ID == "" {
			return err
		}
		items, err := s.deps.Store.Operations(tx, slot.ID, after, limit+1)
		if len(items) > limit {
			last := items[limit-1].Generation
			result.Next, items = &last, items[:limit]
		}
		result.Items = items
		return err
	})
	return result, s.refused(ctx, request, "publication.history", "PublicationSlot", err)
}

// ActiveArtifact serves only through the current pointer. The shared target
// lock orders this authorization with block changes while the membership and
// configuration locks order it with revocation. What the pointer names is
// served for as long as it is neither withdrawn nor blocked: an approval
// that ran out, lost its reviewer or was overtaken by the policy and the
// tools since stops the next release from being put there, and takes
// nothing down. The slot says so to whoever reads it.
func (s *Publication) ActiveArtifact(ctx context.Context, request domain.Request) (ports.Artifact, error) {
	var artifact ports.Artifact
	err := s.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		access, err := s.asset(tx, request, false)
		if err != nil {
			return err
		}
		if !access.ArtifactReader {
			return ErrForbidden
		}
		target, err := s.target(tx, request, ports.HoldTarget)
		if err != nil {
			return err
		}
		if !access.Active {
			return ErrNoVisibleArtifact
		}
		if err := s.servingTarget(tx, target); err != nil {
			if errors.Is(err, ErrUnavailableTarget) {
				return ErrNoVisibleArtifact
			}
			return err
		}
		slot, err := s.slot(tx, request, false)
		if err != nil {
			return err
		}
		if slot.ActiveReleaseID == "" {
			return ErrNoVisibleArtifact
		}
		blocked, err := s.deps.Store.HasBlock(tx, target, slot.AssetID, slot.ActiveReleaseID)
		if err != nil {
			return err
		}
		if blocked {
			return ErrNoVisibleArtifact
		}
		artifact, err = s.deps.Authority.Artifact(tx, request, access.WorkspaceID, slot.ActiveReleaseID)
		return err
	})
	return artifact, s.refused(ctx, request, "publication.download", "PublicationSlot", err)
}

// Until the external delivery and authorization adapters are available,
// only an authenticated internal origin can serve a publication.
func (s *Publication) servingTarget(ctx context.Context, target domain.Target) error {
	if target.Audience != "internal" {
		return ErrUnavailableTarget
	}
	audience, environment, err := s.deps.Authority.Config(ctx, target.ProductID, target.AudienceConfigID)
	if err != nil {
		return err
	}
	if audience != target.Audience || environment != target.Environment {
		return ErrUnavailableTarget
	}
	return nil
}

func (s *Publication) asset(ctx context.Context, request domain.Request, publisher bool) (ports.AssetAccess, error) {
	access, err := s.deps.Authority.Asset(ctx, request)
	if err != nil {
		return ports.AssetAccess{}, err
	}
	if !access.Reader || (publisher && !access.Publisher) {
		return ports.AssetAccess{}, ErrForbidden
	}
	return access, nil
}

func (s *Publication) target(ctx context.Context, request domain.Request, lock ports.TargetLock) (domain.Target, error) {
	target, found, err := s.deps.Store.Target(ctx, request.ProductID, request.TargetID, lock)
	if err == nil && !found {
		err = ErrTargetNotFound
	}
	return target, err
}

func (s *Publication) slot(ctx context.Context, request domain.Request, locked bool) (domain.Slot, error) {
	slot, found, err := s.deps.Store.Slot(ctx, request, locked)
	if err == nil && !found {
		slot = domain.Slot{ProductID: request.ProductID, TargetID: request.TargetID, AssetID: request.AssetID}
	}
	return slot, err
}

func operationNames(action domain.Action) (audit, rule, event string) {
	switch action {
	case domain.Withdraw:
		return "publication.withdraw", "AC-05", "slot.withdrawn"
	case domain.Restore:
		return "publication.restore", "AC-04", "slot.restored"
	default:
		return "publication.activate", "AC-04", "slot.activated"
	}
}

func (s *Publication) record(ctx context.Context, request domain.Request, action, rule, resource, id string, event ports.Event) error {
	if err := s.deps.Audit(ctx, ports.AuditEntry{
		Request: request, Action: action, Rule: rule, Resource: resource, ResourceID: id, Allowed: true,
	}); err != nil {
		return err
	}
	return s.deps.Enqueue(ctx, request, event)
}

func (s *Publication) refused(ctx context.Context, request domain.Request, action, resource string, cause error) error {
	if !errors.Is(cause, ErrForbidden) && !errors.Is(cause, ErrBlocked) && !errors.Is(cause, ErrNoVisibleArtifact) &&
		!errors.Is(cause, ErrTargetNotFound) && !errors.Is(cause, ErrNotIndependent) {
		return cause
	}
	rule := "DENY-03"
	if errors.Is(cause, ErrNotIndependent) {
		rule = "DENY-06"
	}
	if err := s.deps.Audit(ctx, ports.AuditEntry{
		Request: request, Action: action, Rule: rule, Resource: resource,
	}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}

func (s *Publication) now() time.Time { return s.deps.Clock.Now().UTC().Truncate(time.Microsecond) }
