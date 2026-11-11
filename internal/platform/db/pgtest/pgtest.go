// Package pgtest gives integration tests PostgreSQL databases of their own,
// reachable as the two roles a deployment has: the one that migrates and the
// one a Fabric process connects with. It is test support: no production
// package imports it.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
)

const (
	// MigratorDSNVariable names the connection string of the role that owns
	// the schema. The tests also create and drop their databases with it.
	MigratorDSNVariable = "IDP_FABRIC_MIGRATOR_DSN"

	// ApplicationDSNVariable names the connection string of the role a Fabric
	// process connects with. It must be another role, one without DDL rights:
	// the tests run under it so that they prove what that role can do.
	ApplicationDSNVariable = "IDP_FABRIC_POSTGRES_DSN"
)

// operationTimeout bounds one administrative operation. It is generous: it
// guards against a statement that hangs, not one that is slow.
const operationTimeout = 60 * time.Second

// Database is a test database, as each of the two roles reaches it.
type Database struct {
	// Migrator is the DSN of the role that owns the schema.
	Migrator string
	// Application is the DSN of the role a Fabric process connects with.
	Application string
	// ApplicationRole is the name of that role.
	ApplicationRole string
}

// FreshDatabase creates an empty database owned by the calling test. The
// database is dropped when the test ends.
//
// Without the DSNs the test is skipped locally and fails in CI: a required
// integration gate must never be silently absent from a pipeline.
func FreshDatabase(t *testing.T) Database {
	t.Helper()

	migratorDSN, applicationDSN, applicationRole := requireRoles(t)
	name := databaseName(t)
	quoted := pgx.Identifier{name}.Sanitize()

	if err := withConnection(migratorDSN, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `CREATE DATABASE `+quoted)
		return err
	}); err != nil {
		t.Fatalf("create the test database: %v", err)
	}
	t.Cleanup(func() {
		// A failed drop is reported: silent orphans pile up, and nobody
		// knows where they came from.
		if err := withConnection(migratorDSN, func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+quoted+` WITH (FORCE)`)
			return err
		}); err != nil {
			t.Errorf("drop the test database %s, leaving an orphan: %v", name, err)
		}
	})

	database := Database{ApplicationRole: applicationRole}
	var err error
	if database.Migrator, err = withDatabase(migratorDSN, name); err != nil {
		t.Fatalf("build the migrator DSN of the test database: %v", err)
	}
	if database.Application, err = withDatabase(applicationDSN, name); err != nil {
		t.Fatalf("build the application DSN of the test database: %v", err)
	}
	return database
}

// MigratedDatabase creates a database owned by the calling test and prepares
// it the way a deployment is prepared: the migrating role applies the Fabric
// plan and grants the application role what a process needs.
func MigratedDatabase(t *testing.T) Database {
	t.Helper()

	database := FreshDatabase(t)
	if err := withConnection(database.Migrator, func(ctx context.Context, conn *pgx.Conn) error {
		if err := migrate.Run(ctx, conn, slog.New(slog.DiscardHandler)); err != nil {
			return err
		}
		return migrate.Grant(ctx, conn, database.ApplicationRole)
	}); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}
	return database
}

// Connect opens a single connection that is closed when the test ends.
func Connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
		defer cancel()
		_ = conn.Close(ctx)
	})
	return conn
}

// requireRoles returns the two DSNs of the test server and the name of the
// application role.
func requireRoles(t *testing.T) (migratorDSN, applicationDSN, applicationRole string) {
	t.Helper()

	migratorDSN, applicationDSN = os.Getenv(MigratorDSNVariable), os.Getenv(ApplicationDSNVariable)
	switch {
	case migratorDSN == "" && applicationDSN == "":
		if os.Getenv("CI") != "" {
			t.Fatalf("%s and %s are required in CI; the PostgreSQL gate must not be skipped",
				MigratorDSNVariable, ApplicationDSNVariable)
		}
		t.Skipf("%s and %s are not set; skipping the PostgreSQL integration gate",
			MigratorDSNVariable, ApplicationDSNVariable)
	case migratorDSN == "" || applicationDSN == "":
		t.Fatalf("%s and %s must be set together: the tests migrate as one role and run as the other",
			MigratorDSNVariable, ApplicationDSNVariable)
	}

	migrator, err := pgx.ParseConfig(migratorDSN)
	if err != nil {
		t.Fatalf("parse %s: %v", MigratorDSNVariable, err)
	}
	application, err := pgx.ParseConfig(applicationDSN)
	if err != nil {
		t.Fatalf("parse %s: %v", ApplicationDSNVariable, err)
	}
	if application.User == migrator.User {
		t.Fatalf("%s and %s name the same role %q; tests run as the migrating role would prove nothing about the application role",
			MigratorDSNVariable, ApplicationDSNVariable, application.User)
	}
	return migratorDSN, applicationDSN, application.User
}

// withConnection runs do on a connection of its own. It does not take the
// test's context, which is already cancelled when a cleanup runs.
func withConnection(dsn string, do func(context.Context, *pgx.Conn) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return do(ctx, conn)
}

func databaseName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("name the test database: %v", err)
	}
	return "fabric_test_" + hex.EncodeToString(suffix)
}

// withDatabase points dsn at another database of the same server. Only the
// database name changes: credentials, sslmode and every other parameter pass
// through as the operator wrote them. The result is parsed back and refused
// unless it names the requested database, so a test is never handed some
// other one.
func withDatabase(dsn, name string) (string, error) {
	if _, err := pgx.ParseConfig(dsn); err != nil {
		return "", err
	}

	var derived string
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		location, err := url.Parse(dsn)
		if err != nil {
			// A url.Error repeats its input, password included.
			return "", errors.New("parse the DSN as a URL")
		}
		// A dbname in the query string wins over the path.
		query := location.Query()
		if query.Has("dbname") || query.Has("database") {
			query.Del("dbname")
			query.Del("database")
			location.RawQuery = query.Encode()
		}
		location.Path, location.RawPath = "/"+name, ""
		derived = location.String()
	} else {
		// In keyword form the last occurrence of a key wins.
		derived = dsn + " dbname='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(name) + "'"
	}

	config, err := pgx.ParseConfig(derived)
	if err != nil {
		return "", err
	}
	if config.Database != name {
		return "", fmt.Errorf("the derived DSN names database %q, not %q", config.Database, name)
	}
	return derived, nil
}
