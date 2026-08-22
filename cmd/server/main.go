package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	connectinfra "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	dbinfra "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
	"github.com/pj-hoakari/tolo-tenant-management/internal/logging"
	relationapplication "github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationconnect "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/connect"
	relationdb "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/telemetry"
)

const (
	defaultAddr       = ":8080"
	defaultLogLevel   = "info"
	shutdownTimeout   = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		// run() installs the default logger itself, so a failure before that
		// point is reported by slog's own handler on stderr instead.
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger, err := newLogger()
	if err != nil {
		return err
	}

	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := getenv("SERVER_ADDR", defaultAddr)
	jwtSettings := connectinfra.JWTSettings{
		JWKSURL:  getenv("INTERNAL_JWKS_URL", jwks.DefaultInternalJWKSURL),
		Issuer:   getenv("INTERNAL_JWT_ISSUER", jwks.DefaultInternalJWTIssuer),
		Audience: getenv("INTERNAL_JWT_AUDIENCE", jwks.DefaultInternalJWTAudience),
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	shutdownTracing, err := telemetry.Setup(ctx)
	if err != nil {
		return fmt.Errorf("setup tracing: %w", err)
	}
	defer shutdownTracingWithTimeout(shutdownTracing)

	if telemetry.Enabled() {
		slog.Info("tracing enabled", "service", telemetry.ServiceName())
	}

	db, err := dbinfra.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("close database failed", "error", err)
		}
	}()

	tenantRepository := dbinfra.NewPostgresTenantRepository(db)
	// The membership repository shares the pool, so the owner membership of
	// ClaimTenantOwnership commits in the tenant repository's transaction.
	membershipRepository := relationdb.NewPostgresMembershipRepository(db)
	// The relation side's Authorizer implements the tenant side's
	// CurrentPermissionChecker port, so the administrative tenant writes
	// re-read the caller's membership in their own transaction.
	tenantService := application.NewTenantService(tenantRepository, tenantRepository, membershipRepository, relationapplication.NewAuthorizer(membershipRepository))
	// The membership repository is also the transactor of the relation use
	// cases, so the caller's current-permission check and the write it guards
	// run in one transaction.
	relationService := relationapplication.NewRelationService(tenantRepository, membershipRepository, membershipRepository)

	handler, err := connectinfra.NewHandlerWithJWTSettings(tenantService, jwtSettings, relationconnect.Mount(relationService))
	if err != nil {
		return fmt.Errorf("build handler: %w", err)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		// net/http reports its own failures (a broken connection, a panic in a
		// handler) through this logger, so it goes to the same structured
		// stream as everything else.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)

	go func() {
		slog.Info("server listening", "addr", addr)

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err

			return
		}

		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		slog.Info("server shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		return httpServer.Shutdown(shutdownCtx)
	}
}

// newLogger builds the process logger from the environment. It is the first
// thing run() does, so that everything the service reports afterwards is
// written in the structure Cloud Logging parses.
func newLogger() (*slog.Logger, error) {
	level, err := logging.ParseLevel(getenv("LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return nil, fmt.Errorf("read LOG_LEVEL: %w", err)
	}

	return logging.NewLogger(os.Stdout, logging.Options{
		Level:     level,
		AddSource: false,
		// Without a project the log entries carry the bare trace ID, so Cloud
		// Logging cannot correlate them with the trace.
		ProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT"),
	}), nil
}

// shutdownTracingWithTimeout flushes pending spans on a fresh context, because
// the run context is already cancelled once the process starts shutting down.
func shutdownTracingWithTimeout(shutdown telemetry.ShutdownFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		slog.Error("shutdown tracing failed", "error", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
