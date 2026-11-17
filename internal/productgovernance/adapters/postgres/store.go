// Package postgres stores the facts Product Governance owns in the
// productgovernance schema.
//
// A write runs in the transaction its context carries; the framework hands
// out no executor for one outside it. A read runs in that transaction if
// there is one and on the pool otherwise.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	bentoapplication "go.idp.xyz/idp-bento-go/application"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// Transactions is the transaction boundary of the use cases: the database of
// the process, with the one failure the use cases have to tell apart named
// in their own terms.
type Transactions struct {
	within func(ctx context.Context, fn func(ctx context.Context) error) error
}

// NewTransactions wraps the transaction boundary of the process, as
// db.Database.WithinTransaction provides it.
func NewTransactions(within func(ctx context.Context, fn func(ctx context.Context) error) error) Transactions {
	return Transactions{within: within}
}

// WithinTransaction runs fn in a transaction. A commit that may or may not
// have happened is reported as ports.ErrOutcomeUncertain.
func (t Transactions) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	err := t.within(ctx, fn)
	if errors.Is(err, bentoapplication.ErrCommitUncertain) {
		return fmt.Errorf("%w: %w", ports.ErrOutcomeUncertain, err)
	}
	return err
}

// Products stores products in productgovernance.product.
type Products struct {
	framework *bentopostgres.DB
}

// NewProducts builds the store on the framework database, the owner of the
// transactions of a Fabric process.
func NewProducts(framework *bentopostgres.DB) *Products {
	return &Products{framework: framework}
}

// A key that is taken makes the statement insert nothing, instead of failing
// it: a failed statement would also fail the transaction it runs in.
const addProduct = `
INSERT INTO productgovernance.product
    (product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
     aggregate_version, created_at, created_by_issuer, created_by_subject,
     updated_at, updated_by_issuer, updated_by_subject, brand_name, brand_logo_url, brand_primary_color)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (product_key) DO NOTHING`

// Add stores a new product in the transaction ctx carries.
func (s *Products) Add(ctx context.Context, product domain.Product) error {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("add a product: %w", err)
	}
	inserted, err := executor.Exec(ctx, addProduct,
		string(product.ID), product.Key, product.DisplayName, string(product.Status),
		product.Owner.IdentityProvider, product.Owner.Organization,
		product.Version, product.CreatedAt, product.CreatedBy.Issuer, product.CreatedBy.Subject,
		product.UpdatedAt, product.UpdatedBy.Issuer, product.UpdatedBy.Subject,
		product.Brand.Name, nullable(product.Brand.LogoURL), nullable(product.Brand.PrimaryColor))
	if err != nil {
		return fmt.Errorf("add a product: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrProductKeyTaken
	}
	return nil
}

// The columns of a product. A row a build before the brand wrote says
// nothing of a brand or of who changed the product last: such a product is
// as it was registered, under its display name, and its registrant changed
// it last.
const selectProduct = `
SELECT product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
       aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at,
       coalesce(updated_by_issuer, created_by_issuer), coalesce(updated_by_subject, created_by_subject),
       coalesce(brand_name, display_name), brand_logo_url, brand_primary_color
FROM productgovernance.product`

// scanProduct reads one row of selectProduct.
func scanProduct(row interface{ Scan(dest ...any) error }) (domain.Product, error) {
	var product domain.Product
	var id, status string
	var logoURL, primaryColor *string
	err := row.Scan(&id, &product.Key, &product.DisplayName, &status,
		&product.Owner.IdentityProvider, &product.Owner.Organization,
		&product.Version, &product.CreatedAt, &product.CreatedBy.Issuer, &product.CreatedBy.Subject, &product.UpdatedAt,
		&product.UpdatedBy.Issuer, &product.UpdatedBy.Subject,
		&product.Brand.Name, &logoURL, &primaryColor)
	if err != nil {
		return domain.Product{}, err
	}
	product.ID, product.Status = domain.ProductID(id), domain.ProductStatus(status)
	if logoURL != nil {
		product.Brand.LogoURL = *logoURL
	}
	if primaryColor != nil {
		product.Brand.PrimaryColor = *primaryColor
	}
	return product, nil
}

const findProduct = selectProduct + `
WHERE product_id = $1`

// Find returns the product with the identifier, if there is one.
func (s *Products) Find(ctx context.Context, id domain.ProductID) (domain.Product, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("find a product: %w", err)
	}
	product, err := scanProduct(reader.QueryRow(ctx, findProduct, string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("find a product: %w", err)
	}
	return product, true, nil
}

// A transaction that finds the row locked waits for the one that holds it,
// and then reads the row as that one left it, at its new version.
//
// The lock is the one a change of the brand takes itself. It keeps another
// change of the product out and lets everything else in: a workspace, a role
// assignment or an audience configuration of the product needs the product
// to stay, not to stay as it is, and is added while the lock is held.
const lockProduct = findProduct + `
FOR NO KEY UPDATE`

// Lock returns the product with the identifier, if there is one, locked for
// the transaction ctx carries.
func (s *Products) Lock(ctx context.Context, id domain.ProductID) (domain.Product, bool, error) {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("lock a product: %w", err)
	}
	product, err := scanProduct(executor.QueryRow(ctx, lockProduct, string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("lock a product: %w", err)
	}
	return product, true, nil
}

// A change is stored over the version it was made from: of two changes made
// from one version, one changes the row and the other finds nothing to
// change. Nothing but the brand, who changed it, when, and the version is
// written.
const changeProductBrand = `
UPDATE productgovernance.product
SET brand_name = $3, brand_logo_url = $4, brand_primary_color = $5,
    updated_at = $6, updated_by_issuer = $7, updated_by_subject = $8, aggregate_version = $2
WHERE product_id = $1 AND aggregate_version = $2 - 1`

// ChangeBrand stores, in the transaction ctx carries, the new brand of a
// product.
func (s *Products) ChangeBrand(ctx context.Context, changed domain.Product) error {
	if changed.Version < 2 {
		return errors.New("change the brand of a product: the product is at the version it was registered at")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("change the brand of a product: %w", err)
	}
	updated, err := executor.Exec(ctx, changeProductBrand,
		string(changed.ID), changed.Version,
		changed.Brand.Name, nullable(changed.Brand.LogoURL), nullable(changed.Brand.PrimaryColor),
		changed.UpdatedAt, changed.UpdatedBy.Issuer, changed.UpdatedBy.Subject)
	if err != nil {
		return fmt.Errorf("change the brand of a product: %w", err)
	}
	if updated.RowsAffected() == 0 {
		return ports.ErrProductChanged
	}
	return nil
}

// Every product, oldest first.
const listProducts = selectProduct + `
WHERE (created_at, product_id) > ` + after + `
ORDER BY created_at, product_id
LIMIT $3`

// List returns one page of all the products, oldest first.
func (s *Products) List(ctx context.Context, page ports.Page) ([]domain.Product, error) {
	products, err := list(ctx, s.framework, listProducts, scanProduct, page)
	if err != nil {
		return nil, fmt.Errorf("list the products: %w", err)
	}
	return products, nil
}

// The products a principal holds something in: a role assignment in force,
// or a membership in force of one of the workspaces. A principal is its
// issuer and its subject together, and what was revoked is not held.
//
// What the principal holds is found by the principal, across the products,
// and the products are read from that: somebody holds something in a few
// products, however many there are. A product in which it holds several
// things is listed once.
const listProductsFor = selectProduct + `
WHERE product_id IN (
        SELECT product_id FROM productgovernance.product_role_assignment
        WHERE principal_issuer = $4 AND principal_subject = $5 AND revoked_at IS NULL
        UNION ALL
        SELECT product_id FROM productgovernance.workspace_membership
        WHERE principal_issuer = $4 AND principal_subject = $5 AND revoked_at IS NULL)
  AND (created_at, product_id) > ` + after + `
ORDER BY created_at, product_id
LIMIT $3`

// ListFor returns one page of the products the principal holds something in
// now, oldest first.
func (s *Products) ListFor(ctx context.Context, principal domain.Principal, page ports.Page) ([]domain.Product, error) {
	products, err := list(ctx, s.framework, listProductsFor, scanProduct, page, principal.Issuer, principal.Subject)
	if err != nil {
		return nil, fmt.Errorf("list the products of a principal: %w", err)
	}
	return products, nil
}

// Workspaces stores workspaces in productgovernance.workspace.
type Workspaces struct {
	framework *bentopostgres.DB
}

// NewWorkspaces builds the store on the framework database.
func NewWorkspaces(framework *bentopostgres.DB) *Workspaces {
	return &Workspaces{framework: framework}
}

const addWorkspace = `
INSERT INTO productgovernance.workspace
    (workspace_id, product_id, workspace_key, display_name, status,
     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (product_id, workspace_key) DO NOTHING`

// Add stores a new workspace in the transaction ctx carries.
func (s *Workspaces) Add(ctx context.Context, workspace domain.Workspace) error {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("add a workspace: %w", err)
	}
	inserted, err := executor.Exec(ctx, addWorkspace,
		string(workspace.ID), string(workspace.ProductID), workspace.Key, workspace.DisplayName, string(workspace.Status),
		workspace.Version, workspace.CreatedAt, workspace.CreatedBy.Issuer, workspace.CreatedBy.Subject, workspace.UpdatedAt)
	if err != nil {
		return fmt.Errorf("add a workspace: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrWorkspaceKeyTaken
	}
	return nil
}

// The columns of a workspace.
const selectWorkspace = `
SELECT workspace_id, product_id, workspace_key, display_name, status,
       aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at
FROM productgovernance.workspace`

// scanWorkspace reads one row of selectWorkspace.
func scanWorkspace(row interface{ Scan(dest ...any) error }) (domain.Workspace, error) {
	var workspace domain.Workspace
	var id, productID, status string
	err := row.Scan(&id, &productID, &workspace.Key, &workspace.DisplayName, &status,
		&workspace.Version, &workspace.CreatedAt, &workspace.CreatedBy.Issuer, &workspace.CreatedBy.Subject, &workspace.UpdatedAt)
	if err != nil {
		return domain.Workspace{}, err
	}
	workspace.ID, workspace.ProductID = domain.WorkspaceID(id), domain.ProductID(productID)
	workspace.Status = domain.WorkspaceStatus(status)
	return workspace, nil
}

// The product is part of the condition, not something compared afterwards: a
// workspace of another product is simply not there.
const findWorkspace = selectWorkspace + `
WHERE product_id = $1 AND workspace_id = $2`

// Find returns the workspace with the identifier if it belongs to the
// product.
func (s *Workspaces) Find(ctx context.Context, productID domain.ProductID, id domain.WorkspaceID) (domain.Workspace, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.Workspace{}, false, fmt.Errorf("find a workspace: %w", err)
	}
	workspace, err := scanWorkspace(reader.QueryRow(ctx, findWorkspace, string(productID), string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Workspace{}, false, nil
	}
	if err != nil {
		return domain.Workspace{}, false, fmt.Errorf("find a workspace: %w", err)
	}
	return workspace, true, nil
}

// The workspaces of one product, oldest first. As in a single read, the
// product is part of the condition: a workspace of another product is simply
// not there.
const listWorkspaces = selectWorkspace + `
WHERE product_id = $4 AND (created_at, workspace_id) > ` + after + `
ORDER BY created_at, workspace_id
LIMIT $3`

// List returns one page of the workspaces of the product, oldest first.
func (s *Workspaces) List(ctx context.Context, productID domain.ProductID, page ports.Page) ([]domain.Workspace, error) {
	workspaces, err := list(ctx, s.framework, listWorkspaces, scanWorkspace, page, string(productID))
	if err != nil {
		return nil, fmt.Errorf("list the workspaces of a product: %w", err)
	}
	return workspaces, nil
}

// The workspaces of one product a principal is a member of: those in which
// it holds a membership in force, in whichever role. A workspace in which it
// holds two roles is listed once, and one in which its membership was
// revoked is not listed.
//
// Materialize the member's distinct workspace IDs before applying the page
// limit, then look each workspace up by its primary key. The inner LIMIT 1
// keeps this a bounded lookup rather than an ordered scan of the product.
// Otherwise the
// planner can walk workspace_list_order and probe a membership for every
// workspace, hoping to fill the page. At the end of a sparse member's list
// there are fewer than Limit matches, so it probes the rest of the product.
// This work scales with the member's visible set instead of an early page
// being fast and the last one scanning thousands of unrelated workspaces.
const listWorkspacesFor = `WITH visible_workspaces AS MATERIALIZED (
    SELECT DISTINCT workspace_id FROM productgovernance.workspace_membership
    WHERE product_id = $4 AND principal_issuer = $5 AND principal_subject = $6 AND revoked_at IS NULL)
SELECT visible.* FROM visible_workspaces AS membership
CROSS JOIN LATERAL (` + selectWorkspace + `
    WHERE product_id = $4 AND workspace_id = membership.workspace_id LIMIT 1) AS visible
WHERE (visible.created_at, visible.workspace_id) > ` + after + `
ORDER BY visible.created_at, visible.workspace_id
LIMIT $3`

// ListFor returns one page of the workspaces of the product the principal is
// a member of now, oldest first.
func (s *Workspaces) ListFor(ctx context.Context, productID domain.ProductID, principal domain.Principal, page ports.Page) ([]domain.Workspace, error) {
	workspaces, err := list(ctx, s.framework, listWorkspacesFor, scanWorkspace, page,
		string(productID), principal.Issuer, principal.Subject)
	if err != nil {
		return nil, fmt.Errorf("list the workspaces of a principal: %w", err)
	}
	return workspaces, nil
}
