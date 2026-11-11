// Package migrate owns the Fabric migration plan and applies it. The plan joins
// the framework's immutable migration assets with Fabric's own migrations into
// one ordered history, and the runner records what was actually applied.
//
// Nothing here runs when an application process starts (F07 §5.3): an operator
// or a deployment job runs fabric-migrate before deploying.
package migrate

import (
	"fmt"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"
)

// The schemas the migration job creates before it applies the plan. The
// framework's technical tables stay in a schema of their own, so a Fabric
// migration never touches them.
const (
	SchemaBento   = "bento"
	SchemaHistory = "fabric_migration"
)

// FrameworkModule and FrameworkVersion name the exact framework candidate the
// framework steps come from. The version is recorded beside every framework
// step so that a schema can be traced back to one immutable artifact. That
// only holds while it equals the version the build resolves, so the consumer
// contract test compares the two.
const (
	FrameworkModule  = "go.idp.xyz/idp-bento-go"
	FrameworkVersion = "v0.1.0-rc.2"
)

// Origin tells a migration the framework owns from one Fabric owns. Only the
// framework owns its technical tables; only Fabric owns the rest.
type Origin string

const (
	OriginFramework Origin = "FRAMEWORK"
	OriginFabric    Origin = "FABRIC"
)

// Step is one migration of the plan.
type Step struct {
	ID       string
	Origin   Origin
	Schema   string
	Checksum string
	// FrameworkVersion is set on framework steps only.
	FrameworkVersion string
	SQL              string
}

// Plan returns the ordered migrations Fabric applies. The framework's technical
// tables come first; Fabric has no tables of its own yet, and its migrations
// follow the framework steps once the first persistent slice lands.
func Plan() ([]Step, error) {
	assets := bentopostgres.Migrations()
	steps := make([]Step, 0, len(assets))

	for _, asset := range assets {
		// The framework renders its own template: Fabric never copies or
		// edits it, and the checksum recorded is the template's.
		sql, err := bentopostgres.RenderMigration(asset.ID, SchemaBento)
		if err != nil {
			return nil, fmt.Errorf("migrate: render framework migration %s: %w", asset.ID, err)
		}
		steps = append(steps, Step{
			ID:               "framework/" + asset.ID,
			Origin:           OriginFramework,
			Schema:           SchemaBento,
			Checksum:         asset.Checksum,
			FrameworkVersion: FrameworkVersion,
			SQL:              sql,
		})
	}
	return steps, nil
}

// Schemas returns the schemas the migration job creates before it applies the
// plan. The framework never creates its own, and the role an application
// process connects with never holds the privilege to.
func Schemas() []string {
	return []string{SchemaHistory, SchemaBento}
}
