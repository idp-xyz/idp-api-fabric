package application

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// ErrAuditorNotIndependent reports an appointment that would make the same
// account audit a product and answer for it, or audit a product and stand
// above it: an auditor of a product neither owns it nor administers the
// platform, and whoever audits a product is not named an owner of it.
var ErrAuditorNotIndependent = errors.New("an auditor of a product neither owns it nor administers the platform")

// ruleNotIndependent refuses what would put two duties in one hand
// (DENY-06, separation of duties), as the decision of a review by whoever
// took part in it and the clearance of a block by whoever placed it are
// refused.
const ruleNotIndependent = "DENY-06"

// appointedByAdministrator reports whether the role is one the owners of a
// product neither grant nor end. Whoever audits a product is appointed and
// dismissed by a platform administrator, so that nobody who is audited picks
// who audits it, or sends it away. An administrator reads nothing of the
// product by appointing one.
func appointedByAdministrator(role domain.Role) bool {
	return role == domain.RoleInternalAuditor
}

// grantedByOwnersAlone reports whether the role is one a platform
// administrator does not name: the security roles of a product are its
// owners' to hand out.
func grantedByOwnersAlone(role domain.Role) bool {
	return role == domain.RoleSecurityAdmin || role == domain.RoleSecurityReviewer
}

// mayGrant decides who names whom. An owner of the product grants every
// role but the one an administrator appoints, to itself as to anybody. A
// platform administrator that does not own the product names its owners and
// appoints its auditors. An owner acts as one, whatever else it is: an
// administrator that owns the product appoints no auditor of it, and another
// administrator has to.
func mayGrant(owns, administers bool, role domain.Role) bool {
	if owns {
		return !appointedByAdministrator(role)
	}
	return administers && !grantedByOwnersAlone(role)
}

// independent decides the separation of duties of a grant: whether principal
// can be given role in the product without auditing what it answers for.
// It compares accounts, when the grant is made, as every separation of
// duties here does.
//
// An auditor is appointed from those who neither own the product nor
// administer the platform, and an owner is named from those who do not audit
// the product. An assignment that was in force before this rule is left as
// it is.
//
// What the principal holds is read as it is committed when the grant is
// decided, and is not locked. A grant holds the roles of its caller and no
// other row: a revocation locks every owner of the product at once, and a
// grant that held its caller's role and then waited for the principal's
// could wait for a revocation that waits for it. Read so, a revocation that
// has not committed still counts, which refuses where it might have
// admitted, and a grant that is made at the same moment is not seen, with a
// lock or without.
func (s *ProductGovernance) independent(ctx context.Context, productID domain.ProductID, principal domain.Principal, role domain.Role) (bool, error) {
	var excludes domain.Role
	switch {
	case appointedByAdministrator(role):
		if s.deps.Administrators.Includes(principal) {
			return false, nil
		}
		excludes = domain.RoleProductOwner
	case role == domain.RoleProductOwner:
		excludes = domain.RoleInternalAuditor
	default:
		return true, nil
	}
	held, err := s.deps.Roles.RolesOf(ctx, productID, principal)
	if err != nil {
		return false, fmt.Errorf("read the roles of the principal: %w", err)
	}
	return !slices.Contains(held, excludes), nil
}

// appointed reports whether the assignment of the scene is one the caller,
// who does not own the product, reads and ends as a platform administrator:
// it is in force and makes somebody an auditor. An administrator reads and
// ends no other assignment, and one that is not there is to it what one of
// another role is.
func (s *ProductGovernance) appointed(caller domain.Principal, seen scene) bool {
	return s.deps.Administrators.Includes(caller) && seen.assignmentFound && appointedByAdministrator(seen.assignment.Role)
}
