package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

var (
	ErrForbidden        = errors.New("the caller may not work with revisions of this asset")
	ErrProductSuspended = errors.New("the product is suspended")
	// ErrSpecificationMismatch reports a contract of the other language than
	// the asset's: an HTTP asset takes OpenAPI and an event asset AsyncAPI.
	ErrSpecificationMismatch = errors.New("an HTTP asset takes an OpenAPI contract and an event asset an AsyncAPI one")
	ErrRefusalNotAudited     = errors.New("a revision refusal could not be audited")
)

type Dependencies struct {
	Transactions ports.Transactor
	Scopes       ports.Scopes
	Capture      func(string, []domain.SourceFile) (domain.SourceSnapshot, error)
	Revisions    ports.Revisions
	Audit        func(context.Context, ports.AuditEntry) error
	Events       ports.Events
	Clock        ports.Clock
	Identifiers  ports.Identifiers
}

type Registry struct{ deps Dependencies }

func New(deps Dependencies) (*Registry, error) {
	if deps.Transactions == nil || deps.Scopes == nil || deps.Capture == nil || deps.Revisions == nil ||
		deps.Audit == nil || deps.Events == nil || deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("Contract Registry needs transactions, asset scope, source capture, revision storage, audit, events, clock and identifiers")
	}
	return &Registry{deps: deps}, nil
}

type ImportRevision struct {
	ports.ScopeQuery
	Entrypoint string
	Files      []domain.SourceFile
	// Origin is the Git origin the caller declares for the files, or the
	// zero value for an upload. It is recorded as it is said: nothing here
	// reads the repository.
	Origin domain.GitOrigin
}

func (r *Registry) ImportRevision(ctx context.Context, command ImportRevision) (ports.Registration, error) {
	const action = "revision.create"
	scope, err := r.deps.Scopes.Asset(ctx, command.ScopeQuery)
	if err != nil {
		return ports.Registration{}, err
	}
	var registered ports.Registration
	err = r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		active, err := r.requireAuthor(tx, command.Caller, scope.Asset, true)
		if err != nil {
			return err
		}
		if !active {
			return ErrProductSuspended
		}
		if command.Origin.Declared() {
			if err := command.Origin.Validate(); err != nil {
				return err
			}
		}
		source, err := r.deps.Capture(command.Entrypoint, command.Files)
		if err != nil {
			return err
		}
		want := domain.AsyncAPI
		if scope.HTTP {
			want = domain.OpenAPI
		}
		if source.Specification != want {
			return ErrSpecificationMismatch
		}
		registered, err = r.deps.Revisions.Register(tx, domain.Draft{
			ID: r.deps.Identifiers.NewID(), SourceSnapshotID: r.deps.Identifiers.NewID(),
			Asset: scope.Asset, Origin: command.Origin, CapturedAt: r.deps.Clock.Now(), CapturedBy: command.Caller,
		}, source)
		if err != nil || !registered.Created {
			return err
		}
		if err := r.deps.Audit(tx, ports.AuditEntry{
			Query: command.ScopeQuery, RevisionID: registered.Revision.ID, Action: action, Rule: "AC-01", Allowed: true,
		}); err != nil {
			return err
		}
		return r.deps.Events.Registered(tx, registered.Revision, command.CorrelationID)
	})
	if err != nil {
		return ports.Registration{}, r.auditRefusal(ctx, command.ScopeQuery, action, err)
	}
	return registered, nil
}

func (r *Registry) requireAuthor(ctx context.Context, caller domain.Principal, asset domain.AssetRef, importing bool) (bool, error) {
	author, active, err := r.deps.Scopes.Author(ctx, caller, asset, importing)
	if err != nil {
		return false, err
	}
	if !author {
		return false, ErrForbidden
	}
	return active, nil
}

func (r *Registry) auditRefusal(ctx context.Context, query ports.ScopeQuery, action string, cause error) error {
	if !errors.Is(cause, ErrForbidden) {
		return cause
	}
	if err := r.deps.Audit(ctx, ports.AuditEntry{Query: query, Action: action, Rule: "DENY-03"}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}
