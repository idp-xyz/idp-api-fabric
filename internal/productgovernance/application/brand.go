package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// ChangeProductBrand is what changing the brand of a product needs.
type ChangeProductBrand struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	// Brand is the brand the product is to have, as a whole: what it leaves
	// empty, the product has none of.
	Brand domain.Brand
	// Version is the version of the product the change was made from: the
	// one the caller read (F04 §11).
	Version int64
}

// ChangeProductBrand changes how a product presents itself to the developers
// it documents its APIs for (FR-005). Only an owner of the product may: its
// portal is the owner's to make up (F06 §2). Whoever else may know the
// product exists is told it may not, whatever the request says; to anyone
// else there is no such product. A platform administrator registers a
// product and gives it no brand. A suspended product takes no change
// (INV-01).
//
// A change is made from the version of the product the caller read. If the
// product is at another version by then, somebody changed it in the
// meantime, and the change is refused with ErrVersionMismatch. The product
// is locked while the change is decided and stored, so of two changes made
// from one version the second is refused. A change to the brand the product
// has already changes nothing: the product is returned as it is, at its
// version, and nothing is recorded or announced.
//
// Like the writes of the audiences, it locks the role assignments of the
// caller before it locks the product: either the change commits before a
// revocation of that ownership, or the revocation wins and the change is
// refused. The change, the audit fact and the event commit together or not
// at all (INV-13).
func (s *ProductGovernance) ChangeProductBrand(ctx context.Context, command ChangeProductBrand) (domain.Product, error) {
	if err := known(command.Caller); err != nil {
		return domain.Product{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.Product{}, ErrProductNotFound
	}

	var changed domain.Product
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.surveyWithRoles(ctx, command.Caller, productID, "", "", s.deps.Roles.LockHeldRoles)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, ports.AuditEntry{
				Actor: command.Caller, Action: ActionProductChangeBrand, Rule: ruleNoRole,
				ProductID: productID, ResourceType: resourceProduct, ResourceID: string(productID),
				CorrelationID: command.CorrelationID,
			})
			return refused.err
		}
		// What another transaction committed while this one waited is in the
		// product once it is locked: a change of the brand, at its version.
		product, found, err := s.deps.Products.Lock(ctx, productID)
		if err != nil {
			return fmt.Errorf("lock the product: %w", err)
		}
		if !found {
			return ErrProductNotFound
		}
		if command.Version != product.Version {
			return ErrVersionMismatch
		}

		changed, err = product.ChangeBrand(command.Brand, command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if changed == product {
			return nil
		}
		if err := s.deps.Products.ChangeBrand(ctx, changed); err != nil {
			if errors.Is(err, ports.ErrProductChanged) {
				// It is not at the version it was locked at any more, which
				// only a writer that did not lock it first could do.
				return ErrVersionMismatch
			}
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionProductChangeBrand, Allowed: true, Rule: ruleProductOwner,
			ProductID: productID, ResourceType: resourceProduct, ResourceID: string(productID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.ProductBrandChanged(ctx, changed, command.CorrelationID)
	})
	if refused != nil {
		return domain.Product{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.Product{}, fmt.Errorf("change the brand of the product: %w", err)
	}
	return changed, nil
}
