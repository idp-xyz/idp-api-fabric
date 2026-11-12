// Package migrate owns the Fabric migration plan and applies it. The plan joins
// the framework's immutable migration assets with Fabric's own migrations into
// one ordered history, and the runner records what was actually applied.
//
// Nothing here runs when an application process starts (F07 §5.3): an operator
// or a deployment job runs fabric-migrate before deploying.
package migrate

import (
	"fmt"
	"slices"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/migrations"
)

// The schemas this package owns. The framework's technical tables stay in a
// schema of their own, so a Fabric migration never touches them. The tables of
// a bounded context live in a schema named after the context.
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
// tables come first. The tables Fabric owns follow, context by context, from
// the SQL under migrations/ that this build carries.
func Plan() ([]Step, error) {
	steps, err := frameworkSteps()
	if err != nil {
		return nil, err
	}
	fabric, err := fabricSteps(migrations.Files())
	if err != nil {
		return nil, err
	}
	return append(steps, fabric...), nil
}

func frameworkSteps() ([]Step, error) {
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

// schemasOf returns the schemas a run creates before it applies plan: the one
// of the history and the one of every step. Neither the framework nor a
// Fabric migration creates its own schema, and the role an application
// process connects with never holds the privilege to.
func schemasOf(plan []Step) []string {
	schemas := []string{SchemaHistory}
	for _, step := range plan {
		if !slices.Contains(schemas, step.Schema) {
			schemas = append(schemas, step.Schema)
		}
	}
	return schemas
}
