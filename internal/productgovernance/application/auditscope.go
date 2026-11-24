package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// AuditScope is what Product Governance knows of a caller for the reading of
// the audit records of one product. Platform Operations owns the records and
// decides from these facts; knowing that a product exists conveys no right to
// read what was recorded about it.
type AuditScope struct {
	// Found is whether there is such a product.
	Found bool
	// Known is whether the caller may know that there is: it holds something
	// in the product, or administers the platform.
	Known bool
	// Auditor is whether the caller holds the role of an auditor of the
	// product now. An owner, a member and a platform administrator are none
	// by being one.
	Auditor bool
}

// ReadAuditScope reads what the caller is to the product, for the reading of
// its audit records.
//
// What the caller holds is read under a shared lock on its role assignments,
// so the transaction ctx carries has to be the one that reads the records
// too: a revocation that commits first is reflected here, and one that comes
// after waits until that transaction ends. A role that was revoked grants
// nothing from that moment on, on every page.
func (s *ProductGovernance) ReadAuditScope(ctx context.Context, caller domain.Principal, productID domain.ProductID) (AuditScope, error) {
	scope, err := s.ReadCatalogScope(ctx, caller, productID)
	if err != nil || !scope.Known {
		return AuditScope{Found: scope.Found}, err
	}
	held, err := s.deps.Roles.LockHeldRoles(ctx, productID, caller)
	if err != nil {
		return AuditScope{}, err
	}
	return AuditScope{Found: true, Known: true, Auditor: slices.Contains(held, domain.RoleInternalAuditor)}, nil
}
