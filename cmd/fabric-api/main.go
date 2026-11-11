// Command fabric-api serves the IDP API Fabric control plane over HTTP.
//
// It reads a database that fabric-migrate has already migrated and never
// executes DDL: the process refuses to start against a schema it does not
// recognise. It verifies the bearer tokens of one identity provider and
// issues none.
//
// IDP_FABRIC_POSTGRES_DSN is the connection string of the application role:
// the one fabric-migrate grants to, not the one it migrates as. The process
// refuses to start as a role that could change the schema.
//
// IDP_FABRIC_OIDC_ISSUER, IDP_FABRIC_OIDC_AUDIENCE and IDP_FABRIC_OIDC_JWKS_URL
// bind the identity provider: the issuer whose tokens are accepted, the name
// this deployment is addressed by in them, and where the issuer publishes its
// keys. All three are required. On a development machine fabric-devidp stands
// in for the provider and logs the three values to use.
//
// IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS names who administers the platform: a
// JSON array of subjects of that issuer, as in ["alice","ops-bot"]. Only an
// administrator registers a product. Unset, nobody does.
//
// IDP_FABRIC_HTTP_ADDR sets the listen address. The default is loopback only:
// the control plane is private (F02 §4.1), so reaching it from another host is
// a deployment decision, not a default.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bentooutbox "go.idp.xyz/idp-bento-go/postgres/outbox"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	operationsdb "go.idp.xyz/idp-api-fabric/internal/platformoperations/adapters/postgres"
	operations "go.idp.xyz/idp-api-fabric/internal/platformoperations/application"
	operationsdomain "go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	governanceoutbox "go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/outbox"
	governancedb "go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/static"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
	httptransport "go.idp.xyz/idp-api-fabric/internal/transport/http"
)

const (
	defaultAddress  = "127.0.0.1:8080"
	connectTimeout  = 15 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fabric-api failed", "error", err)
		os.Exit(1)
	}
	logger.Info("fabric-api stopped")
}

// settings is what the process is configured with.
type settings struct {
	address string
	// dsn carries credentials. It comes from the environment only and is
	// never echoed.
	dsn      string
	identity identity.Config
	// administrators are the principals that administer the platform.
	administrators []governancedomain.Principal
}

// readSettings reads the configuration from the environment and refuses what
// the process cannot run with. Nothing is defaulted that decides whom the
// process trusts.
func readSettings(getenv func(string) string) (settings, error) {
	configured := settings{
		address: getenv("IDP_FABRIC_HTTP_ADDR"),
		dsn:     getenv("IDP_FABRIC_POSTGRES_DSN"),
		identity: identity.Config{
			Issuer:   getenv("IDP_FABRIC_OIDC_ISSUER"),
			Audience: getenv("IDP_FABRIC_OIDC_AUDIENCE"),
			JWKSURL:  getenv("IDP_FABRIC_OIDC_JWKS_URL"),
		},
	}
	if configured.address == "" {
		configured.address = defaultAddress
	}
	// A control plane that cannot reach its database, or cannot tell who is
	// calling it, must fail to start, not answer /healthz with nothing
	// behind it.
	for name, value := range map[string]string{
		"IDP_FABRIC_POSTGRES_DSN":  configured.dsn,
		"IDP_FABRIC_OIDC_ISSUER":   configured.identity.Issuer,
		"IDP_FABRIC_OIDC_AUDIENCE": configured.identity.Audience,
		"IDP_FABRIC_OIDC_JWKS_URL": configured.identity.JWKSURL,
	} {
		if value == "" {
			return settings{}, fmt.Errorf("%s is required", name)
		}
	}

	if listed := getenv("IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS"); listed != "" {
		var subjects []string
		if err := json.Unmarshal([]byte(listed), &subjects); err != nil {
			return settings{}, errors.New(`IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS is not a JSON array of subjects, as in ["alice","ops-bot"]`)
		}
		for _, subject := range subjects {
			// An administrator is a principal of the one issuer the process
			// trusts. Nobody else could authenticate as one.
			configured.administrators = append(configured.administrators,
				governancedomain.Principal{Issuer: configured.identity.Issuer, Subject: subject})
		}
	}
	return configured, nil
}

func run(logger *slog.Logger) error {
	configured, err := readSettings(os.Getenv)
	if err != nil {
		return err
	}
	verifier, err := identity.NewVerifier(configured.identity)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	database, err := db.Open(connectCtx, configured.dsn)
	if err != nil {
		return err
	}
	defer database.Close()

	handler, err := compose(database, verifier, configured, logger)
	if err != nil {
		return err
	}
	if len(configured.administrators) == 0 {
		logger.Warn("nobody administers the platform: no product can be registered; see IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS")
	}

	listener, err := net.Listen("tcp", configured.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", configured.address, err)
	}
	logger.Info("fabric-api listening", "address", listener.Addr().String(), "build", buildinfo.Current(),
		"issuer", configured.identity.Issuer, "audience", configured.identity.Audience,
		"platform_administrators", len(configured.administrators))
	return serve(ctx, listener, handler)
}

// systemClock is the clock of the machine.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// auditTrail is the audit trail of Platform Operations as Product Governance
// records into it. The two contexts do not know each other: this is where
// the port of one meets the use case of the other (F03 §3.2).
type auditTrail struct {
	record *operations.RecordAudit
}

func (a auditTrail) Record(ctx context.Context, entry governanceports.AuditEntry) error {
	outcome := operationsdomain.OutcomeDenied
	if entry.Allowed {
		outcome = operationsdomain.OutcomeSucceeded
	}
	return a.record.Record(ctx, operations.AuditEntry{
		Actor:         operationsdomain.Actor{Issuer: entry.Actor.Issuer, Subject: entry.Actor.Subject},
		Action:        entry.Action,
		Outcome:       outcome,
		Rule:          entry.Rule,
		ProductID:     string(entry.ProductID),
		ResourceType:  entry.ResourceType,
		ResourceID:    entry.ResourceID,
		CorrelationID: entry.CorrelationID,
	})
}

// compose wires the bounded contexts to the database and to each other, and
// puts them behind the transport.
//
// Every write of a use case goes through the one transaction owner of the
// process, so the rows of a context, the audit record of Platform Operations
// and the event in the outbox of the framework commit together (INV-13).
func compose(database *db.Database, verifier httptransport.TokenVerifier, configured settings, logger *slog.Logger) (http.Handler, error) {
	clock, identifiers := systemClock{}, ids.UUIDs{}

	recordAudit := operations.NewRecordAudit(database, operationsdb.NewAuditRecords(database.Framework), clock, identifiers)

	outbox, err := bentooutbox.NewStore(database.Framework)
	if err != nil {
		return nil, fmt.Errorf("bind the outbox: %w", err)
	}
	events, err := governanceoutbox.NewEvents(outbox, clock, identifiers)
	if err != nil {
		return nil, err
	}
	administrators, err := static.NewAdministrators(configured.administrators)
	if err != nil {
		return nil, fmt.Errorf("IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS: %w", err)
	}
	productGovernance, err := governance.New(governance.Dependencies{
		Transactor:     governancedb.NewTransactions(database.WithinTransaction),
		Products:       governancedb.NewProducts(database.Framework),
		Workspaces:     governancedb.NewWorkspaces(database.Framework),
		Roles:          governancedb.NewRoleAssignments(database.Framework),
		Administrators: administrators,
		Issuers:        static.NewIssuers(configured.identity.Issuer),
		Audit:          auditTrail{record: recordAudit},
		Events:         events,
		Clock:          clock,
		Identifiers:    identifiers,
	})
	if err != nil {
		return nil, err
	}

	return httptransport.NewHandler(httptransport.Config{
		Logger: logger, Build: buildinfo.Current(), Verifier: verifier, Governance: productGovernance,
	})
}

// serve answers on listener until ctx is done, then lets in-flight requests
// finish within shutdownTimeout. It returns nil only for that clean stop.
func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// A request of this API is small. A caller that takes longer than
		// this to send one is holding a connection, not making a request.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()

	select {
	case err := <-serveResult:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
