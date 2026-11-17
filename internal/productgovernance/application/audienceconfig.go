package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// ConfigureAudience is what making a product document its APIs for an
// audience in an environment needs.
type ConfigureAudience struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	Audience      string
	Environment   string
	// DocsURL is where the audience enters the documentation. Empty says
	// that nobody said where yet.
	DocsURL string
}

// ConfigureAudience makes a product document its APIs for an audience in an
// environment (FR-005). Only an owner of that product may: its portal is
// the owner's to configure (F06 §2). Whoever else may know the product
// exists is told it may not; to anyone else there is no such product. A
// platform administrator registers a product and configures none of its
// audiences.
//
// A product starts with no configuration, and documents its APIs for an
// audience in an environment once at a time: a second configuration of the
// pair is refused with ports.ErrAudienceConfigExists. A suspended product
// takes none (INV-01).
//
// All audience writes lock the caller's assignments before writing or
// locking a configuration. Either the write commits before revocation, or
// the revocation wins and the write is refused. The configuration, audit
// fact and event commit together (INV-13).
func (s *ProductGovernance) ConfigureAudience(ctx context.Context, command ConfigureAudience) (domain.AudienceConfig, error) {
	if err := known(command.Caller); err != nil {
		return domain.AudienceConfig{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.AudienceConfig{}, ErrProductNotFound
	}

	var config domain.AudienceConfig
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.surveyAudienceConfig(ctx, command.Caller, productID, "", s.deps.Roles.LockHeldRoles)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, audienceConfigEntry(command.Caller, ActionAudienceConfigure, productID, seen, command.CorrelationID))
			return refused.err
		}

		id, err := domain.ParseAudienceConfigID(s.deps.Identifiers.NewID())
		if err != nil {
			return fmt.Errorf("generate the identifier of the audience configuration: %w", err)
		}
		config, err = domain.ConfigureAudience(id, seen.product, domain.Audience(command.Audience),
			domain.Environment(command.Environment), command.DocsURL, command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.deps.AudienceConfigs.Add(ctx, config); err != nil {
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionAudienceConfigure, Allowed: true, Rule: ruleProductOwner,
			ProductID: productID, ResourceType: resourceAudienceConfig, ResourceID: string(config.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.AudienceConfigChanged(ctx, config, command.CorrelationID)
	})
	if refused != nil {
		return domain.AudienceConfig{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.AudienceConfig{}, fmt.Errorf("configure the audience: %w", err)
	}
	return config, nil
}

// audienceConfigEntry is the record of a refused attempt on the audiences of
// a product. It names the configuration the request names if there is one
// in force.
func audienceConfigEntry(caller domain.Principal, action string, productID domain.ProductID, seen scene, correlationID string) ports.AuditEntry {
	entry := ports.AuditEntry{
		Actor: caller, Action: action, Rule: ruleNoRole,
		ProductID: productID, ResourceType: resourceAudienceConfig, CorrelationID: correlationID,
	}
	if seen.configFound {
		entry.ResourceID = string(seen.config.ID)
	}
	return entry
}

// GetAudienceConfig asks for one audience configuration of one product.
type GetAudienceConfig struct {
	Caller           domain.Principal
	CorrelationID    string
	ProductID        string
	AudienceConfigID string
}

// GetAudienceConfig returns a configuration in force to whoever may read its
// product: a platform administrator and whoever holds something in it. For
// whom a product documents its APIs, and in which environments, is part of
// what the product is, as much as its name. Who may read the documentation
// itself is decided where it is published.
//
// To anyone else the product does not exist. Whoever may read it is told
// that there is no such configuration when none in force has the
// identifier.
func (s *ProductGovernance) GetAudienceConfig(ctx context.Context, query GetAudienceConfig) (domain.AudienceConfig, error) {
	if err := known(query.Caller); err != nil {
		return domain.AudienceConfig{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return domain.AudienceConfig{}, ErrProductNotFound
	}
	// An identifier that is none names nothing. The rest is read all the
	// same: what the caller is told depends on it.
	configID, _ := domain.ParseAudienceConfigID(query.AudienceConfigID)

	seen, err := s.surveyAudienceConfig(ctx, query.Caller, productID, configID, s.deps.Roles.RolesOf)
	if err != nil {
		return domain.AudienceConfig{}, err
	}

	if !seen.productFound {
		return domain.AudienceConfig{}, ErrProductNotFound
	}
	if !s.knows(seen.standing, query.Caller) {
		return domain.AudienceConfig{}, s.refuse(ctx, refusal{ErrProductNotFound,
			audienceConfigEntry(query.Caller, ActionAudienceConfigRead, productID, seen, query.CorrelationID)})
	}
	if !seen.configFound {
		return domain.AudienceConfig{}, ErrAudienceConfigNotFound
	}
	return seen.config, nil
}

// ChangeAudienceConfig is what changing where an audience enters the
// documentation needs.
type ChangeAudienceConfig struct {
	Caller           domain.Principal
	CorrelationID    string
	ProductID        string
	AudienceConfigID string
	// DocsURL is the new entry. Empty takes the entry away.
	DocsURL string
	// Version is the version of the configuration the change was made
	// from: the one the caller read (F04 §11).
	Version int64
}

// ChangeAudienceConfig changes where an audience enters the documentation of
// an environment (FR-005). Only an owner of the product may, and whoever does
// not own it is answered as for a configuration, whatever the request says.
// A suspended product takes no change (INV-01).
//
// A change is made from the version of the configuration the caller read. If
// the configuration is at another version by then, somebody changed it in
// the meantime, and the change is refused with ErrVersionMismatch. The
// configuration is locked while the change is decided and stored, so of two
// changes made from one version the second is refused. A change to the entry
// the configuration has already changes nothing: the configuration is
// returned as it is, at its version, and nothing is recorded or announced.
//
// The audience and the environment of a configuration never change: another
// pair is another configuration.
//
// The decision is made inside the transaction of the write. The change, the
// audit fact and the event commit together or not at all (INV-13).
func (s *ProductGovernance) ChangeAudienceConfig(ctx context.Context, command ChangeAudienceConfig) (domain.AudienceConfig, error) {
	if err := known(command.Caller); err != nil {
		return domain.AudienceConfig{}, err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return domain.AudienceConfig{}, ErrProductNotFound
	}
	configID, _ := domain.ParseAudienceConfigID(command.AudienceConfigID)

	var changed domain.AudienceConfig
	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.surveyAudienceConfig(ctx, command.Caller, productID, configID, s.deps.Roles.LockHeldRoles)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, audienceConfigEntry(command.Caller, ActionAudienceConfigChange, productID, seen, command.CorrelationID))
			return refused.err
		}
		config, err := s.lockAudienceConfig(ctx, productID, seen)
		if err != nil {
			return err
		}
		if command.Version != config.Version {
			return ErrVersionMismatch
		}

		changed, err = config.ChangeDocsURL(seen.product, command.DocsURL, command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if changed == config {
			return nil
		}
		if err := s.deps.AudienceConfigs.Change(ctx, changed); err != nil {
			if errors.Is(err, ports.ErrAudienceConfigGone) {
				// It is not at the version it was locked at any more, which
				// only a writer that did not lock it first could do.
				return ErrVersionMismatch
			}
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionAudienceConfigChange, Allowed: true, Rule: ruleProductOwner,
			ProductID: productID, ResourceType: resourceAudienceConfig, ResourceID: string(changed.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.AudienceConfigChanged(ctx, changed, command.CorrelationID)
	})
	if refused != nil {
		return domain.AudienceConfig{}, s.refuse(ctx, *refused)
	}
	if err != nil {
		return domain.AudienceConfig{}, fmt.Errorf("change the audience configuration: %w", err)
	}
	return changed, nil
}

// lockAudienceConfig locks the configuration the request names, for the
// transaction ctx carries, and returns it as it is once it is locked. What
// another transaction committed while this one waited is in it: a change at
// its new version, a removal as no configuration at all.
func (s *ProductGovernance) lockAudienceConfig(ctx context.Context, productID domain.ProductID, seen scene) (domain.AudienceConfig, error) {
	if !seen.configFound {
		return domain.AudienceConfig{}, ErrAudienceConfigNotFound
	}
	config, found, err := s.deps.AudienceConfigs.Lock(ctx, productID, seen.config.ID)
	if err != nil {
		return domain.AudienceConfig{}, fmt.Errorf("lock the audience configuration: %w", err)
	}
	if !found {
		return domain.AudienceConfig{}, ErrAudienceConfigNotFound
	}
	return config, nil
}

// RemoveAudienceConfig names the audience configuration to remove.
type RemoveAudienceConfig struct {
	Caller           domain.Principal
	CorrelationID    string
	ProductID        string
	AudienceConfigID string
}

// RemoveAudienceConfig ends an audience configuration: from the moment it
// commits, the product documents nothing for the audience in the
// environment, until the pair is configured again (FR-005). Only an owner of
// the product may, and whoever does not own it is answered as for a
// configuration. A suspended product takes no new configuration; what it has
// can still be removed.
//
// The removed configuration is kept, with who removed it and when. One that
// is not in force is not found, whoever removed it, so a removal that is
// sent twice finds nothing to do the second time. The configuration is
// locked while the removal is decided and stored: a change at the same time
// is either stored before it and removed with it, or finds it removed.
//
// The decision is made inside the transaction of the write. The removal, the
// audit fact and the event commit together or not at all (INV-13).
func (s *ProductGovernance) RemoveAudienceConfig(ctx context.Context, command RemoveAudienceConfig) error {
	if err := known(command.Caller); err != nil {
		return err
	}
	productID, err := domain.ParseProductID(command.ProductID)
	if err != nil {
		return ErrProductNotFound
	}
	configID, _ := domain.ParseAudienceConfigID(command.AudienceConfigID)

	var refused *refusal
	err = s.deps.Transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		seen, err := s.surveyAudienceConfig(ctx, command.Caller, productID, configID, s.deps.Roles.LockHeldRoles)
		if err != nil {
			return err
		}
		if !seen.productFound {
			return ErrProductNotFound
		}
		if !seen.standing.Owns() {
			refused = s.notOwner(seen.standing, audienceConfigEntry(command.Caller, ActionAudienceConfigRemove, productID, seen, command.CorrelationID))
			return refused.err
		}
		config, err := s.lockAudienceConfig(ctx, productID, seen)
		if err != nil {
			return err
		}

		removed, err := config.Remove(command.Caller, s.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.deps.AudienceConfigs.Remove(ctx, removed); err != nil {
			if errors.Is(err, ports.ErrAudienceConfigGone) {
				// It was changed or removed between the lock and the write,
				// which only a writer that did not lock it first could do.
				// This removal leaves no record of its own.
				return ErrAudienceConfigNotFound
			}
			return err
		}
		if err := s.deps.Audit.Record(ctx, ports.AuditEntry{
			Actor: command.Caller, Action: ActionAudienceConfigRemove, Allowed: true, Rule: ruleProductOwner,
			ProductID: productID, ResourceType: resourceAudienceConfig, ResourceID: string(removed.ID),
			CorrelationID: command.CorrelationID,
		}); err != nil {
			return err
		}
		return s.deps.Events.AudienceConfigChanged(ctx, removed, command.CorrelationID)
	})
	if refused != nil {
		return s.refuse(ctx, *refused)
	}
	if err != nil {
		return fmt.Errorf("remove the audience configuration: %w", err)
	}
	return nil
}
