// Package static answers two questions of Product Governance from the
// configuration a process starts with: who administers the platform, and
// which identity providers it trusts.
//
// Both belong to the deployment, not to any product. They are fixed for the
// life of the process and nothing a request carries can change them.
package static

import (
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// Administrators are the principals that administer the platform (AC-08).
//
// The list comes from the deployment until an identity provider is bound that
// can say who they are. An empty list is a platform nobody administers: no
// product can be registered.
type Administrators struct {
	principals []domain.Principal
}

// NewAdministrators accepts the principals that administer the platform. One
// that cannot name a principal is refused: it would be an administrator
// nobody could ever be, most likely a mistake in the configuration.
func NewAdministrators(principals []domain.Principal) (Administrators, error) {
	for _, principal := range principals {
		if err := principal.Validate(); err != nil {
			return Administrators{}, err
		}
	}
	return Administrators{principals: slices.Clone(principals)}, nil
}

// Includes reports whether principal administers the platform. The issuer
// and the subject both have to match.
func (a Administrators) Includes(principal domain.Principal) bool {
	return slices.Contains(a.principals, principal)
}

// Count is the number of administrators.
func (a Administrators) Count() int { return len(a.principals) }

// Issuers are the identity providers whose principals can authenticate.
type Issuers struct {
	issuers []string
}

// NewIssuers accepts the issuers the process verifies tokens of.
func NewIssuers(issuers ...string) Issuers {
	return Issuers{issuers: slices.Clone(issuers)}
}

// Trusts reports whether a principal of issuer can authenticate.
func (i Issuers) Trusts(issuer string) bool {
	return issuer != "" && slices.Contains(i.issuers, issuer)
}
