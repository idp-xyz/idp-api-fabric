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

// AudienceConfigs stores which audiences a product documents its APIs for,
// in which environments, in productgovernance.product_audience_config.
//
// A configuration is inserted when it is added, and changed when its entry
// changes and when it is removed; no statement deletes one. Every read is of
// the configurations in force: a removed row is a record, and nothing is
// decided from it.
type AudienceConfigs struct {
	framework *bentopostgres.DB
}

// NewAudienceConfigs builds the store on the framework database.
func NewAudienceConfigs(framework *bentopostgres.DB) *AudienceConfigs {
	return &AudienceConfigs{framework: framework}
}

// A product documents its APIs for an audience in an environment at most
// once at a time: the index of the configurations in force takes no second
// one. As with a key that is taken, the statement then inserts nothing
// instead of failing, so the transaction it runs in is not lost.
const addAudienceConfig = `
INSERT INTO productgovernance.product_audience_config
    (audience_config_id, product_id, audience, environment, docs_url, audience_config_version,
     created_at, created_by_issuer, created_by_subject, updated_at, updated_by_issuer, updated_by_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (product_id, audience, environment) WHERE removed_at IS NULL DO NOTHING`

// Add stores a new configuration in the transaction ctx carries.
func (s *AudienceConfigs) Add(ctx context.Context, config domain.AudienceConfig) error {
	if !config.InForce() {
		return errors.New("add an audience configuration: a configuration is added in force and removed afterwards")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("add an audience configuration: %w", err)
	}
	inserted, err := executor.Exec(ctx, addAudienceConfig,
		string(config.ID), string(config.ProductID), string(config.Audience), string(config.Environment),
		nullable(config.DocsURL), config.Version,
		config.CreatedAt, config.CreatedBy.Issuer, config.CreatedBy.Subject,
		config.UpdatedAt, config.UpdatedBy.Issuer, config.UpdatedBy.Subject)
	if err != nil {
		return fmt.Errorf("add an audience configuration: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		return ports.ErrAudienceConfigExists
	}
	return nil
}

// nullable is what the column holds for an entry: NULL for none.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// The columns of a configuration in force. The removal is not among them: a
// row that carries one is never read.
const audienceConfigInForce = `
SELECT audience_config_id, product_id, audience, environment, docs_url, audience_config_version,
       created_at, created_by_issuer, created_by_subject, updated_at, updated_by_issuer, updated_by_subject
FROM productgovernance.product_audience_config
WHERE removed_at IS NULL`

// scanAudienceConfig reads one row of audienceConfigInForce.
func scanAudienceConfig(row interface{ Scan(dest ...any) error }) (domain.AudienceConfig, error) {
	var config domain.AudienceConfig
	var id, productID, audience, environment string
	var docsURL *string
	err := row.Scan(&id, &productID, &audience, &environment, &docsURL, &config.Version,
		&config.CreatedAt, &config.CreatedBy.Issuer, &config.CreatedBy.Subject,
		&config.UpdatedAt, &config.UpdatedBy.Issuer, &config.UpdatedBy.Subject)
	if err != nil {
		return domain.AudienceConfig{}, err
	}
	config.ID, config.ProductID = domain.AudienceConfigID(id), domain.ProductID(productID)
	config.Audience, config.Environment = domain.Audience(audience), domain.Environment(environment)
	if docsURL != nil {
		config.DocsURL = *docsURL
	}
	return config, nil
}

// The product is part of the condition, not something compared afterwards:
// a configuration of another product is simply not there.
const findAudienceConfig = audienceConfigInForce + ` AND product_id = $1 AND audience_config_id = $2`

// Find returns the configuration with the identifier if it is in force and
// belongs to the product.
func (s *AudienceConfigs) Find(ctx context.Context, productID domain.ProductID, id domain.AudienceConfigID) (domain.AudienceConfig, bool, error) {
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return domain.AudienceConfig{}, false, fmt.Errorf("find an audience configuration: %w", err)
	}
	return foundAudienceConfig(reader.QueryRow(ctx, findAudienceConfig, string(productID), string(id)), "find an audience configuration")
}

// A transaction that finds the row locked waits for the one that holds it,
// and then reads the row as that one left it: changed, at its new version,
// or removed, and then not at all.
const lockAudienceConfig = findAudienceConfig + `
FOR UPDATE`

// Lock returns the configuration with the identifier if it is in force and
// belongs to the product, locked for the transaction ctx carries.
func (s *AudienceConfigs) Lock(ctx context.Context, productID domain.ProductID, id domain.AudienceConfigID) (domain.AudienceConfig, bool, error) {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return domain.AudienceConfig{}, false, fmt.Errorf("lock an audience configuration: %w", err)
	}
	return foundAudienceConfig(executor.QueryRow(ctx, lockAudienceConfig, string(productID), string(id)), "lock an audience configuration")
}

// foundAudienceConfig reads the one row a statement of a single
// configuration returns, if it returns one.
func foundAudienceConfig(row interface{ Scan(dest ...any) error }, what string) (domain.AudienceConfig, bool, error) {
	config, err := scanAudienceConfig(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AudienceConfig{}, false, nil
	}
	if err != nil {
		return domain.AudienceConfig{}, false, fmt.Errorf("%s: %w", what, err)
	}
	return config, true, nil
}

// The configurations in force of one product, oldest first. The product is
// part of the condition, as in a single read, and a row that carries a
// removal is never read. A change of the entry leaves a row where it is.
const listAudienceConfigs = audienceConfigInForce + ` AND product_id = $4 AND (created_at, audience_config_id) > ` + after + `
ORDER BY created_at, audience_config_id
LIMIT $3`

// List returns one page of the configurations in force of the product,
// oldest first.
func (s *AudienceConfigs) List(ctx context.Context, productID domain.ProductID, page ports.Page) ([]domain.AudienceConfig, error) {
	configs, err := list(ctx, s.framework, listAudienceConfigs, scanAudienceConfig, page, string(productID))
	if err != nil {
		return nil, fmt.Errorf("list the audience configurations of a product: %w", err)
	}
	return configs, nil
}

// A change is stored over the version it was made from, and only while the
// configuration is in force: of two changes made from one version, one
// changes the row and the other finds nothing to change. Nothing but the
// entry, who changed it, when, and the version is written.
const changeAudienceConfig = `
UPDATE productgovernance.product_audience_config
SET docs_url = $4, updated_at = $5, updated_by_issuer = $6, updated_by_subject = $7, audience_config_version = $3
WHERE product_id = $1 AND audience_config_id = $2 AND removed_at IS NULL AND audience_config_version = $3 - 1`

// Change stores, in the transaction ctx carries, the new entry of a
// configuration.
func (s *AudienceConfigs) Change(ctx context.Context, changed domain.AudienceConfig) error {
	if !changed.InForce() {
		return errors.New("change an audience configuration: the configuration says it was removed")
	}
	if changed.Version < 2 {
		return errors.New("change an audience configuration: the configuration is at the version it was added at")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("change an audience configuration: %w", err)
	}
	updated, err := executor.Exec(ctx, changeAudienceConfig,
		string(changed.ProductID), string(changed.ID), changed.Version,
		nullable(changed.DocsURL), changed.UpdatedAt, changed.UpdatedBy.Issuer, changed.UpdatedBy.Subject)
	if err != nil {
		return fmt.Errorf("change an audience configuration: %w", err)
	}
	if updated.RowsAffected() == 0 {
		return ports.ErrAudienceConfigGone
	}
	return nil
}

// A removal is stored over the version it was made from, as a change is.
// Nothing but the removal and the version is written: what the
// configuration was, and who changed it last, stays as it was.
const removeAudienceConfig = `
UPDATE productgovernance.product_audience_config
SET removed_at = $4, removed_by_issuer = $5, removed_by_subject = $6, audience_config_version = $3
WHERE product_id = $1 AND audience_config_id = $2 AND removed_at IS NULL AND audience_config_version = $3 - 1`

// Remove stores, in the transaction ctx carries, that the configuration was
// removed.
func (s *AudienceConfigs) Remove(ctx context.Context, removed domain.AudienceConfig) error {
	if removed.InForce() {
		return errors.New("remove an audience configuration: the configuration does not say when it was removed")
	}
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("remove an audience configuration: %w", err)
	}
	updated, err := executor.Exec(ctx, removeAudienceConfig,
		string(removed.ProductID), string(removed.ID), removed.Version,
		removed.RemovedAt, removed.RemovedBy.Issuer, removed.RemovedBy.Subject)
	if err != nil {
		return fmt.Errorf("remove an audience configuration: %w", err)
	}
	if updated.RowsAffected() == 0 {
		return ports.ErrAudienceConfigGone
	}
	return nil
}
