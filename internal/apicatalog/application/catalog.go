package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

var (
	ErrForbidden         = errors.New("the caller may not perform this catalog action")
	ErrProductNotFound   = errors.New("no such product")
	ErrWorkspaceNotFound = errors.New("no such workspace in the product")
	ErrAssetNotFound     = errors.New("no such API asset in the visible scope")
	ErrProductSuspended  = errors.New("the product is suspended")
	ErrRefusalNotAudited = errors.New("an API catalog refusal could not be audited")
)

const (
	actionCreate    = "asset.create"
	actionRead      = "asset.read"
	actionSearch    = "asset.search"
	maintainerRule  = "AC-11"
	denialRule      = "DENY-03"
	defaultPageSize = 50
	maxPageSize     = 100
)

type Dependencies struct {
	Transactions ports.Transactor
	Scopes       ports.Scopes
	Assets       ports.Assets
	Audit        func(context.Context, ports.AuditEntry) error
	Events       ports.Events
	Clock        ports.Clock
	Identifiers  ports.Identifiers
}

type Catalog struct{ deps Dependencies }

func New(deps Dependencies) (*Catalog, error) {
	if deps.Transactions == nil || deps.Scopes == nil || deps.Assets == nil || deps.Audit == nil ||
		deps.Events == nil || deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("API Catalog requires transactions, product scope, asset storage, audit, events, a clock and identifiers")
	}
	return &Catalog{deps: deps}, nil
}

type Request struct {
	Caller        domain.Principal
	ProductID     domain.ProductID
	CorrelationID string
}

type RegisterAsset struct {
	Request
	WorkspaceID domain.WorkspaceID
	Key         string
	DisplayName string
	DomainKey   string
	Kind        domain.Kind
}

func (c *Catalog) RegisterAsset(ctx context.Context, command RegisterAsset) (domain.Asset, error) {
	if err := command.Request.validate(); err != nil {
		return domain.Asset{}, err
	}
	if !domain.ValidIdentifier(command.WorkspaceID) {
		return domain.Asset{}, ErrWorkspaceNotFound
	}
	var registered domain.Asset
	var refusal error
	err := c.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		scope, err := c.deps.Scopes.Registration(tx, command.ProductID, command.WorkspaceID, command.Caller)
		if err != nil {
			return err
		}
		if !scope.Exists {
			return ErrProductNotFound
		}
		switch {
		case !scope.Known:
			refusal = ErrProductNotFound
		case !scope.WorkspaceExists || !scope.CanRead(command.WorkspaceID):
			refusal = ErrWorkspaceNotFound
		case !scope.Maintainer:
			refusal = ErrForbidden
		}
		if refusal != nil {
			return refusal
		}
		if !scope.Active {
			return ErrProductSuspended
		}
		registered, err = domain.Register(domain.AssetID(c.deps.Identifiers.NewID()), command.ProductID, command.WorkspaceID,
			command.Key, command.DisplayName, command.DomainKey, command.Kind, command.Caller, c.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := c.deps.Assets.Add(tx, registered); err != nil {
			return err
		}
		if err := c.deps.Audit(tx, command.Request.entry(actionCreate, string(registered.ID), maintainerRule, true)); err != nil {
			return err
		}
		return c.deps.Events.Registered(tx, registered, command.CorrelationID)
	})
	if refusal != nil {
		return domain.Asset{}, c.refuse(ctx, command.Request, actionCreate, "", refusal)
	}
	if err != nil {
		return domain.Asset{}, fmt.Errorf("register an API asset: %w", err)
	}
	return registered, nil
}

type GetAsset struct {
	Request
	AssetID domain.AssetID
}

func (c *Catalog) GetAsset(ctx context.Context, query GetAsset) (domain.Asset, error) {
	scope, err := c.readScope(ctx, query.Request, actionRead)
	if err != nil {
		return domain.Asset{}, err
	}
	asset, err := c.AssetInProduct(ctx, query.ProductID, query.AssetID)
	if err != nil {
		return domain.Asset{}, err
	}
	if !scope.CanRead(asset.WorkspaceID) {
		return domain.Asset{}, c.refuse(ctx, query.Request, actionRead, string(asset.ID), ErrAssetNotFound)
	}
	return asset, nil
}

// AssetInProduct verifies the scope of an asset for a use case that already
// authorized the product, such as placing an emergency block. It does not
// grant Catalog read access to its caller.
func (c *Catalog) AssetInProduct(ctx context.Context, product domain.ProductID, id domain.AssetID) (domain.Asset, error) {
	if !domain.ValidIdentifier(product) || !domain.ValidIdentifier(id) {
		return domain.Asset{}, ErrAssetNotFound
	}
	asset, found, err := c.deps.Assets.Find(ctx, product, id)
	if err != nil {
		return domain.Asset{}, err
	}
	if !found {
		return domain.Asset{}, ErrAssetNotFound
	}
	return asset, nil
}

type ListAssets struct {
	Request
	WorkspaceID domain.WorkspaceID
	Kind        domain.Kind
	DomainKey   string
	Query       string
	PageSize    int
	After       *ports.Position
}

type Page struct {
	Items []domain.Asset
	Next  *ports.Position
}

func (c *Catalog) ListAssets(ctx context.Context, query ListAssets) (Page, error) {
	size := query.PageSize
	if size == 0 {
		size = defaultPageSize
	}
	if size < 1 || size > maxPageSize ||
		query.After != nil && (query.After.At.IsZero() || !domain.ValidIdentifier(query.After.ID)) ||
		query.WorkspaceID != "" && !domain.ValidIdentifier(query.WorkspaceID) ||
		query.Kind != "" && !query.Kind.Valid() ||
		query.DomainKey != "" && !domain.ValidKey(query.DomainKey) ||
		!utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 120 ||
		strings.ContainsFunc(query.Query, unicode.IsControl) {
		return Page{}, domain.ErrInvalid
	}
	scope, err := c.readScope(ctx, query.Request, actionSearch)
	if err != nil {
		return Page{}, err
	}
	if !scope.Owner && len(scope.Workspaces) == 0 {
		return Page{}, c.refuse(ctx, query.Request, actionSearch, "", ErrForbidden)
	}
	items, err := c.deps.Assets.List(ctx, query.ProductID, ports.Selection{
		All: scope.Owner, Workspaces: scope.Workspaces, Workspace: query.WorkspaceID,
		Kind: query.Kind, DomainKey: query.DomainKey, Query: query.Query, After: query.After, Limit: size + 1,
	})
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(items) > size {
		last := items[size-1]
		page.Items = items[:size]
		page.Next = &ports.Position{At: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (c *Catalog) readScope(ctx context.Context, request Request, action string) (ports.ProductScope, error) {
	if err := request.validate(); err != nil {
		return ports.ProductScope{}, err
	}
	scope, err := c.deps.Scopes.Product(ctx, request.ProductID, request.Caller)
	if err != nil {
		return ports.ProductScope{}, err
	}
	if !scope.Exists {
		return ports.ProductScope{}, ErrProductNotFound
	}
	if !scope.Known {
		return ports.ProductScope{}, c.refuse(ctx, request, action, "", ErrProductNotFound)
	}
	return scope, nil
}

func (r Request) validate() error {
	if r.Caller.Issuer == "" || r.Caller.Subject == "" {
		return ErrForbidden
	}
	if !domain.ValidIdentifier(r.ProductID) {
		return ErrProductNotFound
	}
	return nil
}

func (r Request) entry(action, id, rule string, allowed bool) ports.AuditEntry {
	return ports.AuditEntry{
		Actor: r.Caller, ProductID: r.ProductID, ResourceID: id, CorrelationID: r.CorrelationID,
		Action: action, Rule: rule, Allowed: allowed,
	}
}

// A refusal records its own audit fact after any write transaction ends.
func (c *Catalog) refuse(ctx context.Context, request Request, action, id string, cause error) error {
	if err := c.deps.Audit(ctx, request.entry(action, id, denialRule, false)); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}
