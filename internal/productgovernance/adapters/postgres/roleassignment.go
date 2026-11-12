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

// RoleAssignments stores who holds which role in which product, in
// productgovernance.product_role_assignment.
//
// An assignment is inserted when it is granted and changed once, when it is
// revoked; no statement removes one. Every read is of the assignments in
// force: a revoked row is a record, and nothing is decided from it.
type RoleAssignments struct {
	framework *bentopostgres.DB
}

// NewRoleAssignments builds the store on the framework database.
func NewRoleAssignments(framework *bentopostgres.DB) *RoleAssignments {
	return &RoleAssignments{framework: framework}
}

// A principal holds a role in a product at most once at a time: the index of
// the assignments in force takes no second one. As with a key that is taken,
// the statement then inserts nothing instead of failing, so the transaction
// it runs in is not lost.
const addRoleAssignment = `
INSERT INTO productgovernance.product_role_assignment
    (role_assignment_id, product_id, principal_issuer, principal_subject, role,
     role_assignment_version, granted_at, granted_by_issuer, granted_by_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (product_id, principal_issuer, principal_subject, role) WHERE revoked_at IS NULL DO NOTHING`

// Add stores a new assignment in the transaction ctx carries.
func (s *RoleAssignments) Add(ctx context.Context, assignment domain.RoleAssignment) error {
	if !assignment.InForce() {
		return errors.New("add a role assignment: an assignment is added in force and revoked afterwards")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("add a role assignment: %w", err)
	}
	inserted, err := executor.Exec(ctx, addRoleAssignment,
		string(assignment.ID), string(assignment.ProductID),
		assignment.Principal.Issuer, assignment.Principal.Subject, string(assignment.Role),
		assignment.Version, assignment.GrantedAt, assignment.GrantedBy.Issuer, assignment.GrantedBy.Subject)
	if err != nil {
		return fmt.Errorf("add a role assignment: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrRoleAssignmentExists
	}
	return nil
}

// The columns of an assignment in force. The revocation is not among them: a
// row that carries one is never read.
const roleAssignmentInForce = `
SELECT role_assignment_id, product_id, principal_issuer, principal_subject, role,
       role_assignment_version, granted_at, granted_by_issuer, granted_by_subject
FROM productgovernance.product_role_assignment
WHERE revoked_at IS NULL`

// scanRoleAssignment reads one row of roleAssignmentInForce.
func scanRoleAssignment(row interface{ Scan(dest ...any) error }) (domain.RoleAssignment, error) {
	var assignment domain.RoleAssignment
	var id, productID, role string
	err := row.Scan(&id, &productID, &assignment.Principal.Issuer, &assignment.Principal.Subject, &role,
		&assignment.Version, &assignment.GrantedAt, &assignment.GrantedBy.Issuer, &assignment.GrantedBy.Subject)
	if err != nil {
		return domain.RoleAssignment{}, err
	}
	assignment.ID, assignment.ProductID = domain.RoleAssignmentID(id), domain.ProductID(productID)
	assignment.Role = domain.Role(role)
	return assignment, nil
}

// The product is part of the condition, not something compared afterwards:
// an assignment of another product is simply not there.
const findRoleAssignment = roleAssignmentInForce + ` AND product_id = $1 AND role_assignment_id = $2`

// Find returns the assignment with the identifier if it is in force and
// belongs to the product.
func (s *RoleAssignments) Find(ctx context.Context, productID domain.ProductID, id domain.RoleAssignmentID) (domain.RoleAssignment, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.RoleAssignment{}, false, fmt.Errorf("find a role assignment: %w", err)
	}
	assignment, err := scanRoleAssignment(reader.QueryRow(ctx, findRoleAssignment, string(productID), string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RoleAssignment{}, false, nil
	}
	if err != nil {
		return domain.RoleAssignment{}, false, fmt.Errorf("find a role assignment: %w", err)
	}
	return assignment, true, nil
}

// The assignments in force of one product, oldest first. The product is part
// of the condition, as in a single read, and a row that carries a revocation
// is never read: who held a role once is in no list.
const listRoleAssignments = roleAssignmentInForce + ` AND product_id = $4 AND (granted_at, role_assignment_id) > ` + after + `
ORDER BY granted_at, role_assignment_id
LIMIT $3`

// List returns one page of the assignments in force of the product, oldest
// first.
func (s *RoleAssignments) List(ctx context.Context, productID domain.ProductID, page ports.Page) ([]domain.RoleAssignment, error) {
	assignments, err := list(ctx, s.framework, listRoleAssignments, scanRoleAssignment, page, string(productID))
	if err != nil {
		return nil, fmt.Errorf("list the role assignments of a product: %w", err)
	}
	return assignments, nil
}

// Only an assignment in force is revoked, so of two revocations at once one
// changes the row and the other finds nothing to change. Nothing but the
// revocation and the version is written: what was granted, to whom and by
// whom stays as it was.
const revokeRoleAssignment = `
UPDATE productgovernance.product_role_assignment
SET revoked_at = $3, revoked_by_issuer = $4, revoked_by_subject = $5, role_assignment_version = $6
WHERE product_id = $1 AND role_assignment_id = $2 AND revoked_at IS NULL`

// Revoke stores, in the transaction ctx carries, that the assignment was
// revoked.
func (s *RoleAssignments) Revoke(ctx context.Context, revoked domain.RoleAssignment) error {
	if revoked.InForce() {
		return errors.New("revoke a role assignment: the assignment does not say when it was revoked")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("revoke a role assignment: %w", err)
	}
	updated, err := executor.Exec(ctx, revokeRoleAssignment,
		string(revoked.ProductID), string(revoked.ID),
		revoked.RevokedAt, revoked.RevokedBy.Issuer, revoked.RevokedBy.Subject, revoked.Version)
	if err != nil {
		return fmt.Errorf("revoke a role assignment: %w", err)
	}
	if updated.RowsAffected() == 0 {
		return ports.ErrRoleAssignmentGone
	}
	return nil
}

// A principal is its issuer and its subject together: the same subject under
// another issuer holds nothing here. A role that was revoked is not held.
const rolesOf = `
SELECT role
FROM productgovernance.product_role_assignment
WHERE product_id = $1 AND principal_issuer = $2 AND principal_subject = $3 AND revoked_at IS NULL
ORDER BY role`

// RolesOf returns the roles the principal holds in the product now.
func (s *RoleAssignments) RolesOf(ctx context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Role, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the roles of a principal: %w", err)
	}
	rows, err := reader.Query(ctx, rolesOf, string(productID), principal.Issuer, principal.Subject)
	if err != nil {
		return nil, fmt.Errorf("read the roles of a principal: %w", err)
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Role, error) {
		var role string
		err := row.Scan(&role)
		return domain.Role(role), err
	})
	if err != nil {
		return nil, fmt.Errorf("read the roles of a principal: %w", err)
	}
	return roles, nil
}

// The owners in force of a product, each row locked until the transaction
// ends. A transaction that finds one locked waits for the one that holds it,
// and then takes the row as that one left it: an owner it revoked is not
// returned. The rows are locked in the order of their identifiers, so two
// transactions that lock the owners of one product wait for each other in
// turn and never for each other at once.
const lockOwners = `
SELECT role_assignment_id
FROM productgovernance.product_role_assignment
WHERE product_id = $1 AND role = $2 AND revoked_at IS NULL
ORDER BY role_assignment_id
FOR UPDATE`

// LockOwners locks the assignments in force that make somebody an owner of
// the product, for the transaction ctx carries, and returns how many they
// are.
//
// The count is of the rows this transaction holds and of nothing else. An
// owner that was added while it waited is not among them, so a revocation
// that decides from the count may take an owner for the last one when
// another was just named. It never takes the last one for one of several:
// what it counted cannot be ended by anybody else before it ends itself.
func (s *RoleAssignments) LockOwners(ctx context.Context, productID domain.ProductID) (int, error) {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return 0, fmt.Errorf("lock the owners of a product: %w", err)
	}
	rows, err := executor.Query(ctx, lockOwners, string(productID), string(domain.RoleProductOwner))
	if err != nil {
		return 0, fmt.Errorf("lock the owners of a product: %w", err)
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("lock the owners of a product: %w", err)
	}
	return len(locked), nil
}
