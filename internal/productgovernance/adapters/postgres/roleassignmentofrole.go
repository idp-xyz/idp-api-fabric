package postgres

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// The assignments in force of one product that grant one role, oldest
// first. It is listRoleAssignments with the role as one more condition, in
// the statement and before the limit: a page is cut from the assignments of
// the role, not from all of them and thinned afterwards, so it holds as many
// as it takes while there are that many, and the position of its last row
// is a position of the list of every role as well.
const listRoleAssignmentsOfRole = roleAssignmentInForce + ` AND product_id = $4 AND role = $5 AND (granted_at, role_assignment_id) > ` + after + `
ORDER BY granted_at, role_assignment_id
LIMIT $3`

// ListOfRole returns one page of the assignments in force of the product
// that grant the role, oldest first.
func (s *RoleAssignments) ListOfRole(ctx context.Context, productID domain.ProductID, role domain.Role, page ports.Page) ([]domain.RoleAssignment, error) {
	assignments, err := list(ctx, s.framework, listRoleAssignmentsOfRole, scanRoleAssignment, page, string(productID), string(role))
	if err != nil {
		return nil, fmt.Errorf("list the role assignments of a product by role: %w", err)
	}
	return assignments, nil
}
