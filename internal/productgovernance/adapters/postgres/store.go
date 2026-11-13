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
     aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
		product.Version, product.CreatedAt, product.CreatedBy.Issuer, product.CreatedBy.Subject, product.UpdatedAt)
	if err != nil {
		return fmt.Errorf("add a product: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrProductKeyTaken
	}
	return nil
}

const findProduct = `
SELECT product_id, product_key, display_name, status, owner_identity_provider, owner_organization,
       aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at
FROM productgovernance.product
WHERE product_id = $1`

// Find returns the product with the identifier, if there is one.
func (s *Products) Find(ctx context.Context, id domain.ProductID) (domain.Product, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("find a product: %w", err)
	}
	var product domain.Product
	var productID, status string
	err = reader.QueryRow(ctx, findProduct, string(id)).Scan(
		&productID, &product.Key, &product.DisplayName, &status,
		&product.Owner.IdentityProvider, &product.Owner.Organization,
		&product.Version, &product.CreatedAt, &product.CreatedBy.Issuer, &product.CreatedBy.Subject, &product.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("find a product: %w", err)
	}
	product.ID, product.Status = domain.ProductID(productID), domain.ProductStatus(status)
	return product, true, nil
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

// The product is part of the condition, not something compared afterwards: a
// workspace of another product is simply not there.
const findWorkspace = `
SELECT workspace_id, product_id, workspace_key, display_name, status,
       aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at
FROM productgovernance.workspace
WHERE product_id = $1 AND workspace_id = $2`

// Find returns the workspace with the identifier if it belongs to the
// product.
func (s *Workspaces) Find(ctx context.Context, productID domain.ProductID, id domain.WorkspaceID) (domain.Workspace, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.Workspace{}, false, fmt.Errorf("find a workspace: %w", err)
	}
	var workspace domain.Workspace
	var workspaceID, ownerID, status string
	err = reader.QueryRow(ctx, findWorkspace, string(productID), string(id)).Scan(
		&workspaceID, &ownerID, &workspace.Key, &workspace.DisplayName, &status,
		&workspace.Version, &workspace.CreatedAt, &workspace.CreatedBy.Issuer, &workspace.CreatedBy.Subject, &workspace.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Workspace{}, false, nil
	}
	if err != nil {
		return domain.Workspace{}, false, fmt.Errorf("find a workspace: %w", err)
	}
	workspace.ID, workspace.ProductID = domain.WorkspaceID(workspaceID), domain.ProductID(ownerID)
	workspace.Status = domain.WorkspaceStatus(status)
	return workspace, true, nil
}
