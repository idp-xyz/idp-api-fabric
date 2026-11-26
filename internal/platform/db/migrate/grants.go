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
// statement that needs it, not before, and leaves with the last one, for
// RetiredPrivileges.
func ApplicationPrivileges() []TablePrivileges {
	return []TablePrivileges{
		{SchemaBento, "outbox", []string{"SELECT", "INSERT", "UPDATE"}},
		{SchemaBento, "outbox_partition", []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
		{SchemaBento, "inbox", []string{"SELECT", "INSERT", "UPDATE"}},
		// A process reads the history when it starts, to refuse a database
		// that was not migrated for its build.
		{SchemaHistory, "applied_migration", []string{"SELECT"}},
		// The audit trail is append-only for a process: it records, and reads
		// back for the auditors of a product (AC-29), and cannot change or
		// remove what it recorded (FR-062). No UPDATE, no DELETE: those are
		// the privileges that would make it a record that can be rewritten.
		{"platformoperations", "audit_record", []string{"SELECT", "INSERT"}},
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
		// A revision is recorded and read back, and no statement changes or
		// removes one. Its status moved along its review until CR-0001
		// retired reviews: the statuses of before are read only.
		{"contractregistry", "contract_revision", []string{"SELECT", "INSERT"}},
		// A check run is evidence: the policy it was held to, what it
		// concluded of each rule and what it found are recorded once and
		// read back. No statement changes or removes any of it.
		{"policyreview", "policy_snapshot", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_run", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_rule_outcome", []string{"SELECT", "INSERT"}},
		{"policyreview", "check_finding", []string{"SELECT", "INSERT"}},
		// CR-0001 retired reviews, and those of before are read only: their
		// requests, scopes, conclusions and the waivers of approvals are
		// read back, and no statement writes any of them.
		{"policyreview", "review_request", []string{"SELECT"}},
		{"policyreview", "review_scope", []string{"SELECT"}},
		{"policyreview", "review_conclusion", []string{"SELECT"}},
		{"policyreview", "review_waiver", []string{"SELECT"}},
		// Ready releases, artifact bytes and publication operations are append-only.
		{"contractregistry", "release_artifact", []string{"SELECT", "INSERT"}},
		{"contractregistry", "contract_release", []string{"SELECT", "INSERT"}},
		{"publication", "target", []string{"SELECT", "INSERT", "UPDATE"}},
		{"publication", "slot", []string{"SELECT", "INSERT", "UPDATE"}},
		{"publication", "operation", []string{"SELECT", "INSERT"}},
		{"publication", "access_block", []string{"SELECT", "INSERT", "UPDATE"}},
	}
}

// RetiredPrivileges returns what builds before this one granted the
// application role and this build no longer needs. GRANT only adds, so a role
// an earlier build granted keeps such a privilege until it is revoked: Grant
// revokes these. A privilege that leaves ApplicationPrivileges is listed
// here, with why.
func RetiredPrivileges() []TablePrivileges {
	return []TablePrivileges{
		// CR-0001 retired reviews: no statement records one any more.
		{"policyreview", "review_request", []string{"INSERT"}},
		{"policyreview", "review_scope", []string{"INSERT"}},
		{"policyreview", "review_conclusion", []string{"INSERT"}},
		{"policyreview", "review_waiver", []string{"INSERT"}},
		// CR-0001 retired reviews, the one thing that moved the status of a revision.
		{"contractregistry", "contract_revision", []string{"UPDATE"}},
	}
}

// Grant gives role the privileges of ApplicationPrivileges: USAGE on their
// schemas and the listed privileges on each table. It then revokes from role
// the privileges of RetiredPrivileges, which an earlier build may have
// granted it. Both happen in one transaction, and granting twice changes
// nothing.
//
// It revokes nothing else: a privilege somebody granted by hand stays, as the
// start check of a process does not look for one either. A process of a
// build that still needs a retired privilege fails once Grant has run, so
// those processes are stopped first. Grant works inside the Fabric schemas
// only. It creates no role, and whether the role may connect to the database
// is for whoever provisions the server to decide.
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
	// Revoking what role does not hold does nothing, so this is repeatable
	// too.
	for _, table := range RetiredPrivileges() {
		statement := `REVOKE ` + strings.Join(table.Privileges, ", ") +
			` ON ` + table.Schema + `.` + table.Table + ` FROM ` + grantee
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: revoke on %s.%s from %s: %w", table.Schema, table.Table, role, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit the grants: %w", err)
	}
	return nil
}
