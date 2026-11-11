package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// historyDDL records what was actually applied. Beside the migration ID it
// keeps what the framework asks a consumer's history to keep: the canonical
// checksum of the template, the framework version and the schema the step was
// rendered for, so that a deployed database can be traced back to immutable
// artifacts.
//
// sequence is the order of application in this database. A step that failed
// and rolled back leaves a gap in it.
const historyDDL = `
CREATE TABLE IF NOT EXISTS ` + SchemaHistory + `.applied_migration (
    migration_id       text        NOT NULL PRIMARY KEY,
    sequence           bigint      GENERATED ALWAYS AS IDENTITY,
    origin             text        NOT NULL,
    canonical_checksum text        NOT NULL,
    framework_version  text,
    target_schema      text        NOT NULL,
    applied_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT applied_migration_origin_known
        CHECK (origin IN ('FRAMEWORK', 'FABRIC')),
    CONSTRAINT applied_migration_framework_version_present
        CHECK (origin <> 'FRAMEWORK' OR framework_version IS NOT NULL)
)`

// ErrChecksumDrift reports that an applied migration no longer matches the
// artifact this build carries. The run stops: it neither applies the step again
// nor accepts a rewritten template.
var ErrChecksumDrift = errors.New("migrate: an applied migration differs from the artifact this build carries")

// ErrUnknownMigration reports that the database records a migration this build
// does not carry: a newer build migrated it, or an applied migration was
// renamed or removed. A build cannot vouch for a step it has no artifact for,
// so the run stops instead of applying its own plan around it.
var ErrUnknownMigration = errors.New("migrate: the database records a migration this build does not carry")

// runLockKey names the session-level advisory lock that serializes migration
// runs on one database. What a run applies is decided by the history it read,
// so no other run may extend that history meanwhile. A session lock goes with
// its connection: a run that dies does not keep it.
const runLockKey = SchemaHistory + ".run"

// runLockReleaseTimeout bounds giving the lock back after the run's own
// context has ended.
const runLockReleaseTimeout = 5 * time.Second

// Run applies to the database behind conn the steps of the Fabric plan that
// database does not record yet. The caller owns the connection, its
// credentials and the deployment window; no application process reaches
// anything here.
//
// Whether a step is applied is decided by its ID, never by its position, so a
// step added in the middle of the plan is still applied to a database that
// already holds the steps after it. Each step commits together with its
// history row: a run that stops half way leaves every committed step recorded
// and nothing of the step it stopped in.
func Run(ctx context.Context, conn *pgx.Conn, logger *slog.Logger) error {
	plan, err := Plan()
	if err != nil {
		return err
	}
	return apply(ctx, conn, logger, plan)
}

func apply(ctx context.Context, conn *pgx.Conn, logger *slog.Logger, plan []Step) error {
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, runLockKey); err != nil {
		return fmt.Errorf("migrate: take the run lock: %w", err)
	}
	defer releaseRunLock(ctx, conn)

	if err := prepare(ctx, conn); err != nil {
		return err
	}
	recorded, err := readHistory(ctx, conn)
	if err != nil {
		return err
	}
	if err := verifyNoDrift(recorded, plan); err != nil {
		return err
	}

	applied := 0
	for _, step := range plan {
		if _, done := recorded[step.ID]; done {
			continue
		}
		logger.Info("applying migration", "migration", step.ID, "schema", step.Schema)
		if err := applyStep(ctx, conn, step); err != nil {
			return err
		}
		applied++
	}
	logger.Info("migration plan applied",
		"steps", len(plan), "applied_now", applied, "framework_version", FrameworkVersion)
	return nil
}

// releaseRunLock gives the run lock back on a context of its own, so that a
// run whose context was cancelled still returns it. If even that fails, the
// lock goes when the connection closes.
func releaseRunLock(ctx context.Context, conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLockReleaseTimeout)
	defer cancel()
	_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, runLockKey)
}

func prepare(ctx context.Context, conn *pgx.Conn) error {
	for _, schema := range Schemas() {
		// Schema names are constants of this package, never input.
		if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
			return fmt.Errorf("migrate: create schema %s: %w", schema, err)
		}
	}
	if _, err := conn.Exec(ctx, historyDDL); err != nil {
		return fmt.Errorf("migrate: create the migration history: %w", err)
	}
	return nil
}

// readHistory returns the recorded checksum of every applied migration.
func readHistory(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx,
		`SELECT migration_id, canonical_checksum FROM `+SchemaHistory+`.applied_migration`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read the migration history: %w", err)
	}

	recorded := make(map[string]string)
	var id, checksum string
	if _, err := pgx.ForEachRow(rows, []any{&id, &checksum}, func() error {
		recorded[id] = checksum
		return nil
	}); err != nil {
		return nil, fmt.Errorf("migrate: read the migration history: %w", err)
	}
	return recorded, nil
}

// verifyNoDrift compares every recorded migration with the artifact this build
// carries. A framework template that was copied and edited, a Fabric migration
// rewritten after it was applied and a recorded migration without an artifact
// all fail here.
func verifyNoDrift(recorded map[string]string, plan []Step) error {
	artifacts := make(map[string]string, len(plan))
	for _, step := range plan {
		artifacts[step.ID] = step.Checksum
	}
	// In ID order, so that the same history always reports the same migration.
	for _, id := range slices.Sorted(maps.Keys(recorded)) {
		checksum, carried := artifacts[id]
		if !carried {
			return fmt.Errorf("%w: %s", ErrUnknownMigration, id)
		}
		if recorded[id] != checksum {
			return fmt.Errorf("%w: %s recorded %s, artifact %s", ErrChecksumDrift, id, recorded[id], checksum)
		}
	}
	return nil
}

// applyStep runs one step and records it in the same transaction.
func applyStep(ctx context.Context, conn *pgx.Conn, step Step) (err error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin %s: %w", step.ID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if _, err := tx.Exec(ctx, step.SQL); err != nil {
		return fmt.Errorf("migrate: apply %s: %w", step.ID, err)
	}

	var frameworkVersion *string
	if step.Origin == OriginFramework {
		frameworkVersion = &step.FrameworkVersion
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+SchemaHistory+`.applied_migration
			(migration_id, origin, canonical_checksum, framework_version, target_schema)
		 VALUES ($1, $2, $3, $4, $5)`,
		step.ID, string(step.Origin), step.Checksum, frameworkVersion, step.Schema); err != nil {
		return fmt.Errorf("migrate: record %s: %w", step.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit %s: %w", step.ID, err)
	}
	return nil
}
