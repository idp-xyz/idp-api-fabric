package db_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

func open(t *testing.T, dsn string) *db.Database {
	t.Helper()

	database, err := db.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// refused opens dsn and returns the error, failing the test when the database
// opens after all.
func refused(t *testing.T, dsn string) error {
	t.Helper()

	database, err := db.Open(t.Context(), dsn)
	if err == nil {
		database.Close()
		t.Fatal("the database opened, want it refused")
	}
	return err
}

// alter runs one statement on a test database as the migrating role, the way
// an operator would.
func alter(t *testing.T, database pgtest.Database, statement string) error {
	t.Helper()

	_, err := pgtest.Connect(t, database.Migrator).Exec(t.Context(), statement)
	return err
}

func mustAlter(t *testing.T, database pgtest.Database, statement string) {
	t.Helper()

	if err := alter(t, database, statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// role is the application role of a test database, quoted for use in SQL.
func role(database pgtest.Database) string {
	return pgx.Identifier{database.ApplicationRole}.Sanitize()
}

func TestOpenRequiresAConnectionString(t *testing.T) {
	if _, err := db.Open(t.Context(), ""); !errors.Is(err, db.ErrDSNMissing) {
		t.Fatalf("got %v, want %v", err, db.ErrDSNMissing)
	}
}

func TestOpenDoesNotCallAnUnreachableServerUnmigrated(t *testing.T) {
	// Nothing listens on port 1 of the loopback interface. An operator who is
	// told to run fabric-migrate when the server is down is sent the wrong way.
	_, err := db.Open(t.Context(), "postgres://fabric@127.0.0.1:1/fabric?sslmode=disable&connect_timeout=5")
	if err == nil || errors.Is(err, db.ErrNotMigrated) {
		t.Fatalf("got %v, want a connection failure that is not %v", err, db.ErrNotMigrated)
	}
}

func TestOpenBindsTheFrameworkAsTheApplicationRole(t *testing.T) {
	database := open(t, pgtest.MigratedDatabase(t).Application)

	if got := database.Framework.Schema(); got != migrate.SchemaBento {
		t.Errorf("the framework is bound to schema %q, want %q", got, migrate.SchemaBento)
	}

	// A write needs an explicit transaction. The framework hands out no
	// executor outside one, rather than falling back to the pool.
	if _, err := database.Framework.RequireExecutor(t.Context()); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	var answer int
	err := database.Framework.Transactor().WithinTransaction(t.Context(), func(ctx context.Context) error {
		executor, err := database.Framework.RequireExecutor(ctx)
		if err != nil {
			return err
		}
		return executor.QueryRow(ctx, `SELECT 42`).Scan(&answer)
	})
	if err != nil || answer != 42 {
		t.Errorf("inside a transaction: got %d and %v, want 42", answer, err)
	}
}

func TestOpenRefusesARoleThatCanDoMoreThanAProcessMay(t *testing.T) {
	t.Run("the migrating role", func(t *testing.T) {
		database := pgtest.MigratedDatabase(t)

		if err := refused(t, database.Migrator); !errors.Is(err, db.ErrOverprivileged) {
			t.Fatalf("got %v, want %v", err, db.ErrOverprivileged)
		}
	})

	t.Run("a role that may create in a Fabric schema", func(t *testing.T) {
		database := pgtest.MigratedDatabase(t)
		mustAlter(t, database, `GRANT CREATE ON SCHEMA bento TO `+role(database))

		if err := refused(t, database.Application); !errors.Is(err, db.ErrOverprivileged) {
			t.Fatalf("got %v, want %v", err, db.ErrOverprivileged)
		}
	})

	t.Run("a role that owns a Fabric table", func(t *testing.T) {
		database := pgtest.MigratedDatabase(t)
		err := alter(t, database, `ALTER TABLE bento.inbox OWNER TO `+role(database))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			t.Skip("handing a table to another role needs a superuser as the migrating role")
		}
		if err != nil {
			t.Fatalf("hand the table over: %v", err)
		}

		if err := refused(t, database.Application); !errors.Is(err, db.ErrOverprivileged) {
			t.Fatalf("got %v, want %v", err, db.ErrOverprivileged)
		}
	})
}

func TestOpenRefusesARoleThatWasGrantedLessThanAProcessNeeds(t *testing.T) {
	t.Run("nothing was granted", func(t *testing.T) {
		database := pgtest.FreshDatabase(t)
		if err := migrate.Run(t.Context(), pgtest.Connect(t, database.Migrator), slog.New(slog.DiscardHandler)); err != nil {
			t.Fatalf("migrate: %v", err)
		}

		if err := refused(t, database.Application); !errors.Is(err, db.ErrNotGranted) {
			t.Fatalf("got %v, want %v", err, db.ErrNotGranted)
		}
	})

	cases := map[string]struct {
		revoke  string
		missing string
	}{
		"a table privilege was revoked":   {`REVOKE UPDATE ON bento.outbox FROM `, "UPDATE on bento.outbox"},
		"the use of a schema was revoked": {`REVOKE USAGE ON SCHEMA fabric_migration FROM `, "USAGE on schema fabric_migration"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			database := pgtest.MigratedDatabase(t)
			mustAlter(t, database, c.revoke+role(database))

			err := refused(t, database.Application)
			if !errors.Is(err, db.ErrNotGranted) || !strings.Contains(err.Error(), c.missing) {
				t.Fatalf("got %v, want %v naming %q", err, db.ErrNotGranted, c.missing)
			}
		})
	}
}

func TestOpenRefusesADatabaseThatDoesNotMatchTheBuild(t *testing.T) {
	cases := map[string]string{
		"nobody migrated it":                      "",
		"the history was dropped":                 `DROP SCHEMA fabric_migration CASCADE`,
		"the history lacks a step of the plan":    `DELETE FROM fabric_migration.applied_migration`,
		"a step came from another artifact":       `UPDATE fabric_migration.applied_migration SET canonical_checksum = 'sha256:rewritten'`,
		"a framework table was altered":           `ALTER TABLE bento.inbox DROP COLUMN failure_code`,
		"the framework schema was dropped":        `DROP SCHEMA bento CASCADE`,
		"a framework index was dropped by hand":   `DROP INDEX bento.outbox_active_recorded_idx`,
		"a framework trigger was dropped by hand": `DROP TRIGGER inbox_settled_before_commit ON bento.inbox`,
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			var database pgtest.Database
			if damage == "" {
				database = pgtest.FreshDatabase(t)
			} else {
				database = pgtest.MigratedDatabase(t)
				mustAlter(t, database, damage)
			}

			if err := refused(t, database.Application); !errors.Is(err, db.ErrNotMigrated) {
				t.Fatalf("got %v, want %v", err, db.ErrNotMigrated)
			}
		})
	}
}

func TestOpenAcceptsStepsOfANewerBuild(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	mustAlter(t, database,
		`INSERT INTO fabric_migration.applied_migration (migration_id, origin, canonical_checksum, target_schema)
		 VALUES ('publication/0001_from_a_newer_build', 'FABRIC', 'sha256:newer', 'publication')`)

	// During a rollout an older process must still be able to restart on a
	// schema a newer build has already grown.
	open(t, database.Application)
}

func TestOpenReadsTimestampsInUTC(t *testing.T) {
	database := open(t, pgtest.MigratedDatabase(t).Application)

	var now time.Time
	if err := database.Pool.QueryRow(t.Context(), `SELECT now()`).Scan(&now); err != nil {
		t.Fatalf("read a timestamp: %v", err)
	}
	if now.Location() != time.UTC {
		t.Errorf("a timestamptz is read in %s, want UTC whatever the host's zone", now.Location())
	}
}
