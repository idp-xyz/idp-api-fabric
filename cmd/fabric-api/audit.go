package main

import (
	"context"
	"errors"

	operationsdomain "go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	operationsports "go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// Product Governance says who holds which role in a product; Platform
// Operations owns the audit trail and decides what it lets them read. The two
// contexts do not know each other: this is where the port of one meets the use
// case of the other (F03 §3.2).
type auditScopes struct{ governance *governance.ProductGovernance }

func (s auditScopes) AuditScope(ctx context.Context, caller operationsdomain.Actor, product string) (operationsports.AuditScope, error) {
	scope, err := s.governance.ReadAuditScope(ctx, governancedomain.Principal(caller), governancedomain.ProductID(product))
	if errors.Is(err, governance.ErrProductNotFound) {
		// An identifier that names no product is no product to know.
		return operationsports.AuditScope{}, nil
	}
	return operationsports.AuditScope{Found: scope.Found, Known: scope.Known, Auditor: scope.Auditor}, err
}
