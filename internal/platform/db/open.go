// Package db opens the Fabric connection pool and binds the framework database
// to it. An application process only ever reads the schema here: it never runs
// DDL, creates a schema or repairs a missing object (F07 §5.3), and it refuses
// to run as a superuser, as the owner of a Fabric table or as a role that may
// create objects in a Fabric schema (F02 §3.2).
package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
)

// ErrDSNMissing reports that no connection string was given. The DSN carries
// credentials and is accepted from the environment only.
var ErrDSNMissing = errors.New("db: a connection string is required")

// ErrNotMigrated reports that the database does not hold what this build's
// migration plan produces. A process must not run on a database an operator
// has not migrated.
var ErrNotMigrated = errors.New("db: the database is not migrated for this build; run fabric-migrate -apply first")

// ErrOverprivileged reports that the role the process connects with can do
// more than a Fabric process may: it is a superuser, bypasses row-level
// security, owns a Fabric table or may create objects in a Fabric schema. The
// usual cause is starting a process with the migrating role's DSN.
var ErrOverprivileged = errors.New("db: the application role holds privileges a Fabric process must not have")

// ErrNotGranted reports that the role the process connects with lacks a
// privilege this build needs. Failing at start is better than failing on the
// first write.
var ErrNotGranted = errors.New("db: the application role lacks privileges this build needs; run fabric-migrate -apply -grant-to <role>")

// Database is the Fabric pool together with the framework database bound to
// the framework schema. Transactions, the outbox and the inbox all go through
// Framework, so they share one transaction owner.
type Database struct {
	Pool      *pgxpool.Pool
	Framework *bentopostgres.DB
}

// Open connects as the application role, verifies that role and the schema,
// and returns the handles. It fails when the role is over-privileged in one of
// the ways ErrOverprivileged names, when it lacks a privilege of
// migrate.ApplicationPrivileges, when a framework object is missing or
// altered, and when the history does not record a step of this build's
// migration plan.
func Open(ctx context.Context, dsn string) (*Database, error) {
	if dsn == "" {
		return nil, ErrDSNMissing
	}

	config, err := PoolConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse the connection string: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("db: open the pool: %w", err)
	}
	framework, err := verify(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Database{Pool: pool, Framework: framework}, nil
}

// verify runs the start-up checks in the order that gives an operator the most
// direct answer: can the server be reached, is this the right role, is the
// schema there, is it the schema of this build.
func verify(ctx context.Context, pool *pgxpool.Pool) (*bentopostgres.DB, error) {
	// The pool connects lazily. Connecting here keeps a server that cannot be
	// reached apart from a schema that does not match.
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := checkRole(ctx, pool); err != nil {
		return nil, err
	}
	if err := checkPrivileges(ctx, pool); err != nil {
		return nil, err
	}

	framework, err := bentopostgres.NewDB(pool, bentopostgres.WithSchema(migrate.SchemaBento))
	if err != nil {
		return nil, fmt.Errorf("db: bind the framework database: %w", err)
	}
	if err := framework.CheckSchema(ctx); err != nil {
		if errors.Is(err, bentopostgres.ErrSchemaIncompatible) {
			return nil, fmt.Errorf("%w: %w", ErrNotMigrated, err)
		}
		return nil, fmt.Errorf("db: check the framework schema: %w", err)
	}
	if err := checkHistory(ctx, pool); err != nil {
		return nil, err
	}
	return framework, nil
}

// Close releases the pool.
func (d *Database) Close() {
	d.Pool.Close()
}

// WithinTransaction runs fn in a transaction and commits when fn returns nil.
// If ctx already carries a transaction, fn joins it and nothing is committed
// here: a failure of fn then fails the whole transaction. Statements reach
// the transaction through the context fn receives.
//
// It has the shape the ports of the bounded contexts declare, so that every
// context is wired with the same transaction owner without importing the
// framework.
func (d *Database) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.Framework.Transactor().WithinTransaction(ctx, fn)
}

// PoolConfig parses dsn into the configuration every Fabric pool is opened
// with, so that a test reads the database the way a process does.
func PoolConfig(dsn string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.AfterConnect = readTimestampsInUTC
	return config, nil
}

// readTimestampsInUTC makes a connection hand back every timestamptz, alone or
// in an array, in UTC. pgx otherwise decodes into time.Local, and a timestamp
// a read surface serializes would carry the host's offset. The instant is the
// same either way; this pins the zone it is written in (F03 §1.3).
func readTimestampsInUTC(_ context.Context, conn *pgx.Conn) error {
	timestamptz := &pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}}
	conn.TypeMap().RegisterType(timestamptz)
	conn.TypeMap().RegisterType(&pgtype.Type{Name: "_timestamptz", OID: pgtype.TimestamptzArrayOID,
		Codec: &pgtype.ArrayCodec{ElementType: timestamptz}})
	return nil
}

// checkRole refuses a role whose attributes put it above every privilege
// check of the database.
func checkRole(ctx context.Context, pool *pgxpool.Pool) error {
	var superuser, bypassesRLS bool
	if err := pool.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&superuser, &bypassesRLS); err != nil {
		return fmt.Errorf("db: read the attributes of the application role: %w", err)
	}
	switch {
	case superuser:
		return fmt.Errorf("%w: it is a superuser", ErrOverprivileged)
	case bypassesRLS:
		return fmt.Errorf("%w: it bypasses row-level security", ErrOverprivileged)
	}
	return nil
}

// tableAccess reads, for one table, what the application role holds. It goes
// through the catalog by name and then by OID, so it answers for a table the
// role has no access to instead of failing on it. The last column lists the
// privileges of $3 the role does not hold.
const tableAccess = `
SELECT has_schema_privilege(current_user, n.oid, 'USAGE'),
       has_schema_privilege(current_user, n.oid, 'CREATE'),
       pg_has_role(current_user, c.relowner, 'USAGE'),
       ARRAY(SELECT p FROM unnest($3::text[]) AS p
             WHERE NOT has_table_privilege(current_user, c.oid, p))
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'r'`

// checkPrivileges requires every privilege of migrate.ApplicationPrivileges
// and refuses a role that owns one of those tables or may create objects in
// their schemas.
//
// It does not look for further table privileges: a role that was also granted
// DELETE on bento.outbox by hand still passes.
func checkPrivileges(ctx context.Context, pool *pgxpool.Pool) error {
	var missing []string

	for _, table := range migrate.ApplicationPrivileges() {
		name := table.Schema + "." + table.Table

		var usesSchema, createsInSchema, owns bool
		var lacking []string
		err := pool.QueryRow(ctx, tableAccess, table.Schema, table.Table, table.Privileges).
			Scan(&usesSchema, &createsInSchema, &owns, &lacking)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s does not exist", ErrNotMigrated, name)
		}
		if err != nil {
			return fmt.Errorf("db: read the privileges on %s: %w", name, err)
		}

		switch {
		case owns:
			return fmt.Errorf("%w: it owns %s", ErrOverprivileged, name)
		case createsInSchema:
			return fmt.Errorf("%w: it may create objects in schema %s", ErrOverprivileged, table.Schema)
		}
		if usage := "USAGE on schema " + table.Schema; !usesSchema && !slices.Contains(missing, usage) {
			missing = append(missing, usage)
		}
		for _, privilege := range lacking {
			missing = append(missing, privilege+" on "+name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrNotGranted, strings.Join(missing, ", "))
	}
	return nil
}

// checkHistory requires the history fabric-migrate keeps to record every step
// of this build's plan, applied from the artifact this build carries. It reads
// the plan rather than a list of objects, so a step is covered the day it is
// added, even one that only adds a column.
//
// Steps the history records beyond the plan come from a newer build and are
// accepted: the schema only grew, and an older process must still be able to
// restart during a rollout.
func checkHistory(ctx context.Context, pool *pgxpool.Pool) error {
	plan, err := migrate.Plan()
	if err != nil {
		return fmt.Errorf("db: build the migration plan: %w", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT migration_id, canonical_checksum FROM `+migrate.SchemaHistory+`.applied_migration`)
	if err != nil {
		return fmt.Errorf("db: read the migration history: %w", err)
	}
	recorded := make(map[string]string)
	var id, checksum string
	if _, err := pgx.ForEachRow(rows, []any{&id, &checksum}, func() error {
		recorded[id] = checksum
		return nil
	}); err != nil {
		return fmt.Errorf("db: read the migration history: %w", err)
	}

	for _, step := range plan {
		checksum, present := recorded[step.ID]
		if !present {
			return fmt.Errorf("%w: migration %s is not recorded", ErrNotMigrated, step.ID)
		}
		if checksum != step.Checksum {
			return fmt.Errorf("%w: migration %s was applied from another artifact (recorded %s, this build carries %s)",
				ErrNotMigrated, step.ID, checksum, step.Checksum)
		}
	}
	return nil
}
