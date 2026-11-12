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
		// Products, workspaces and role assignments are registered and read.
		// No use case changes or removes one yet.
		{"productgovernance", "product", []string{"SELECT", "INSERT"}},
		{"productgovernance", "product_role_assignment", []string{"SELECT", "INSERT"}},
		{"productgovernance", "workspace", []string{"SELECT", "INSERT"}},
		// A membership is granted, read and ended. Ending it is an UPDATE,
		// and a trigger of the table refuses every other one. No statement
		// removes a membership.
		{"productgovernance", "workspace_membership", []string{"SELECT", "INSERT", "UPDATE"}},
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
