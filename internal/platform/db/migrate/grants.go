package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// TablePrivileges is what the application role may do on one table.
type TablePrivileges struct {
	Schema     string
	Table      string
	Privileges []string
}

// ApplicationPrivileges returns everything a Fabric process needs from the
// database and nothing else: no DDL, no ownership and no write to the
// migration history (F02 §3.2, F05 §12). It is the one source of the
// application role's grants: Grant applies it, a process checks it when it
// starts, and the integration tests run under it.
//
// The framework rows follow the statements idp-bento-go runs on its technical
// tables. It deletes rows from outbox_partition only.
//
// Every table of a bounded context is listed here, with exactly the
// statements Fabric runs on it today. A privilege is added with the first
// statement that needs it, not before.
func ApplicationPrivileges() []TablePrivileges {
	return []TablePrivileges{
		{SchemaBento, "outbox", []string{"SELECT", "INSERT", "UPDATE"}},
		{SchemaBento, "outbox_partition", []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
		{SchemaBento, "inbox", []string{"SELECT", "INSERT", "UPDATE"}},
		// A process reads the history when it starts, to refuse a database
		// that was not migrated for its build.
		{SchemaHistory, "applied_migration", []string{"SELECT"}},
		// The audit trail is append-only for a process: it cannot read back,
		// change or remove what it recorded (FR-062).
		{"platformoperations", "audit_record", []string{"INSERT"}},
		{"apicatalog", "api_asset", []string{"SELECT", "INSERT"}},
		// A product is registered and read, and its brand is changed.
		// Changing the brand is an UPDATE, and a trigger of the table
		// refuses the role every other one. The privilege is also what locks
		// a product while its brand is changed. No statement removes one.
		{"productgovernance", "product", []string{"SELECT", "INSERT", "UPDATE"}},
		// An audience configuration is added, read, changed and removed.
		// Changing its entry and removing it are UPDATEs, and a trigger of
		// the table refuses every other one. The privilege is also what
		// locks a configuration while it is changed. Removing one keeps its
		// row: no statement deletes a configuration.
		{"productgovernance", "product_audience_config", []string{"SELECT", "INSERT", "UPDATE"}},
		// A role assignment is granted, read and ended. Ending it is an
		// UPDATE, and a trigger of the table refuses every other one. The
		// privilege is also what locks the owners of a product while a
		// revocation counts them. No statement removes a role assignment.
		{"productgovernance", "product_role_assignment", []string{"SELECT", "INSERT", "UPDATE"}},
		// A workspace is created and read. No use case changes or removes
		// one yet.
		{"productgovernance", "workspace", []string{"SELECT", "INSERT"}},
		// A membership is granted, read and ended. Ending it is an UPDATE,
		// and a trigger of the table refuses every other one. No statement
		// removes a membership.
		{"productgovernance", "workspace_membership", []string{"SELECT", "INSERT", "UPDATE"}},
		// A revision references immutable source evidence. This process
		// records the original files and reads them back for whoever may
		// read the revision. No statement changes or removes one.
		{"contractregistry", "source_snapshot", []string{"SELECT", "INSERT"}},
		{"contractregistry", "source_file", []string{"SELECT", "INSERT"}},
		// The status of a revision moves along its review. The table lets
		// an UPDATE do that and nothing else, and no statement removes a
		// revision.
		{"contractregistry", "contract_revision", []string{"SELECT", "INSERT", "UPDATE"}},
		// A check run is evidence: the policy it was held to, what it
		// concluded of each rule and what it found are recorded once and
		// read back. No statement changes or removes any of it.
		{"policyreview", "policy_snapshot", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_run", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_rule_outcome", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_finding", []string{"SELECT", "INSERT"}},
		// A review is asked for once and concluded once: its request, its
		// scope, its conclusion and the waivers of an approval are recorded
		// and read back, and no statement changes or removes any of them.
		{"policyreview", "review_request", []string{"SELECT", "INSERT"}},
		{"policyreview", "review_scope", []string{"SELECT", "INSERT"}},
		{"policyreview", "review_conclusion", []string{"SELECT", "INSERT"}},
		{"policyreview", "review_waiver", []string{"SELECT", "INSERT"}},
		// Ready releases, artifact bytes and publication operations are append-only.
		{"contractregistry", "release_artifact", []string{"SELECT", "INSERT"}},
		{"contractregistry", "contract_release", []string{"SELECT", "INSERT"}},
		{"publication", "target", []string{"SELECT", "INSERT", "UPDATE"}},
		{"publication", "slot", []string{"SELECT", "INSERT", "UPDATE"}},
		{"publication", "operation", []string{"SELECT", "INSERT"}},
		{"publication", "access_block", []string{"SELECT", "INSERT", "UPDATE"}},
	}
}

// Grant gives role the privileges of ApplicationPrivileges: USAGE on their
// schemas and the listed privileges on each table. Granting twice changes
// nothing.
//
// It only adds privileges inside the Fabric schemas. It creates no role, and
// whether the role may connect to the database is for whoever provisions the
// server to decide.
func Grant(ctx context.Context, conn *pgx.Conn, role string) (err error) {
	if role == "" {
		return errors.New("migrate: the application role is not named")
	}
	// Schema, table and privilege names are constants of this package; the
	// role is the only input and is quoted as an identifier.
	grantee := pgx.Identifier{role}.Sanitize()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin the grants: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	usage := make(map[string]bool)
	for _, table := range ApplicationPrivileges() {
		if !usage[table.Schema] {
			usage[table.Schema] = true
			if _, err := tx.Exec(ctx, `GRANT USAGE ON SCHEMA `+table.Schema+` TO `+grantee); err != nil {
				return fmt.Errorf("migrate: grant usage on schema %s to %s: %w", table.Schema, role, err)
			}
		}
		statement := `GRANT ` + strings.Join(table.Privileges, ", ") +
			` ON ` + table.Schema + `.` + table.Table + ` TO ` + grantee
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: grant on %s.%s to %s: %w", table.Schema, table.Table, role, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit the grants: %w", err)
	}
	return nil
}
