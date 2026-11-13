package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// Memberships stores who is a member of which workspace, in
// productgovernance.workspace_membership.
//
// A membership is inserted when it is granted and changed once, when it is
// revoked; no statement removes one. Every read is of the memberships in
// force: a revoked row is a record, and nothing is decided from it.
type Memberships struct {
	framework *bentopostgres.DB
}

// NewMemberships builds the store on the framework database.
func NewMemberships(framework *bentopostgres.DB) *Memberships {
	return &Memberships{framework: framework}
}

// A principal holds a role in a workspace at most once at a time: the index
// of the memberships in force takes no second one. As with a key that is
// taken, the statement then inserts nothing instead of failing, so the
// transaction it runs in is not lost.
const addMembership = `
INSERT INTO productgovernance.workspace_membership
    (membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
     membership_version, granted_at, granted_by_issuer, granted_by_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, principal_issuer, principal_subject, role) WHERE revoked_at IS NULL DO NOTHING`

// Add stores a new membership in the transaction ctx carries.
func (s *Memberships) Add(ctx context.Context, membership domain.Membership) error {
	if !membership.InForce() {
		return errors.New("add a membership: a membership is added in force and revoked afterwards")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("add a membership: %w", err)
	}
	inserted, err := executor.Exec(ctx, addMembership,
		string(membership.ID), string(membership.ProductID), string(membership.WorkspaceID),
		membership.Principal.Issuer, membership.Principal.Subject, string(membership.Role),
		membership.Version, membership.GrantedAt, membership.GrantedBy.Issuer, membership.GrantedBy.Subject)
	if err != nil {
		return fmt.Errorf("add a membership: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrMembershipExists
	}
	return nil
}

// The columns of a membership in force. The revocation is not among them: a
// row that carries one is never read.
const membershipInForce = `
SELECT membership_id, product_id, workspace_id, principal_issuer, principal_subject, role,
       membership_version, granted_at, granted_by_issuer, granted_by_subject
FROM productgovernance.workspace_membership
WHERE revoked_at IS NULL`

// scanMembership reads one row of membershipInForce.
func scanMembership(row interface{ Scan(dest ...any) error }) (domain.Membership, error) {
	var membership domain.Membership
	var id, productID, workspaceID, role string
	err := row.Scan(&id, &productID, &workspaceID, &membership.Principal.Issuer, &membership.Principal.Subject, &role,
		&membership.Version, &membership.GrantedAt, &membership.GrantedBy.Issuer, &membership.GrantedBy.Subject)
	if err != nil {
		return domain.Membership{}, err
	}
	membership.ID, membership.ProductID = domain.MembershipID(id), domain.ProductID(productID)
	membership.WorkspaceID, membership.Role = domain.WorkspaceID(workspaceID), domain.Role(role)
	return membership, nil
}

// The product and the workspace are part of the condition, not something
// compared afterwards: a membership of another workspace is simply not there.
const findMembership = membershipInForce + ` AND product_id = $1 AND workspace_id = $2 AND membership_id = $3`

// Find returns the membership with the identifier if it is in force and
// belongs to the workspace of the product.
func (s *Memberships) Find(ctx context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, id domain.MembershipID) (domain.Membership, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.Membership{}, false, fmt.Errorf("find a membership: %w", err)
	}
	membership, err := scanMembership(reader.QueryRow(ctx, findMembership, string(productID), string(workspaceID), string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, false, nil
	}
	if err != nil {
		return domain.Membership{}, false, fmt.Errorf("find a membership: %w", err)
	}
	return membership, true, nil
}

// The memberships in force of one workspace, oldest first. The product and
// the workspace are part of the condition, as in a single read, and a row
// that carries a revocation is never read: who was a member once is in no
// list.
const listMemberships = membershipInForce + ` AND product_id = $4 AND workspace_id = $5 AND (granted_at, membership_id) > ` + after + `
ORDER BY granted_at, membership_id
LIMIT $3`

// List returns one page of the memberships in force of the workspace of the
// product, oldest first.
func (s *Memberships) List(ctx context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, page ports.Page) ([]domain.Membership, error) {
	memberships, err := list(ctx, s.framework, listMemberships, scanMembership, page, string(productID), string(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("list the memberships of a workspace: %w", err)
	}
	return memberships, nil
}

// Only a membership in force is revoked, so of two revocations at once one
// changes the row and the other finds nothing to change. Nothing but the
// revocation and the version is written: what was granted, to whom and by
// whom stays as it was.
const revokeMembership = `
UPDATE productgovernance.workspace_membership
SET revoked_at = $4, revoked_by_issuer = $5, revoked_by_subject = $6, membership_version = $7
WHERE product_id = $1 AND workspace_id = $2 AND membership_id = $3 AND revoked_at IS NULL`

// Revoke stores, in the transaction ctx carries, that the membership was
// revoked.
func (s *Memberships) Revoke(ctx context.Context, revoked domain.Membership) error {
	if revoked.InForce() {
		return errors.New("revoke a membership: the membership does not say when it was revoked")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("revoke a membership: %w", err)
	}
	updated, err := executor.Exec(ctx, revokeMembership,
		string(revoked.ProductID), string(revoked.WorkspaceID), string(revoked.ID),
		revoked.RevokedAt, revoked.RevokedBy.Issuer, revoked.RevokedBy.Subject, revoked.Version)
	if err != nil {
		return fmt.Errorf("revoke a membership: %w", err)
	}
	if updated.RowsAffected() == 0 {
		return ports.ErrMembershipGone
	}
	return nil
}

// A principal is its issuer and its subject together, and what it holds in
// one product says nothing about another.
const membershipsHeldBy = membershipInForce + ` AND product_id = $1 AND principal_issuer = $2 AND principal_subject = $3
ORDER BY workspace_id, role`

// HeldBy returns the memberships the principal holds in the workspaces of
// the product now.
func (s *Memberships) HeldBy(ctx context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Membership, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the memberships of a principal: %w", err)
	}
	rows, err := reader.Query(ctx, membershipsHeldBy, string(productID), principal.Issuer, principal.Subject)
	if err != nil {
		return nil, fmt.Errorf("read the memberships of a principal: %w", err)
	}
	memberships, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Membership, error) {
		return scanMembership(row)
	})
	if err != nil {
		return nil, fmt.Errorf("read the memberships of a principal: %w", err)
	}
	return memberships, nil
}
