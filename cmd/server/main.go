package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	connectinfra "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	dbinfra "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
	"github.com/pj-hoakari/tolo-tenant-management/internal/telemetry"
)

const (
	defaultAddr       = ":8080"
	shutdownTimeout   = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := getenv("SERVER_ADDR", defaultAddr)
	jwksURL := getenv("INTERNAL_JWKS_URL", jwks.DefaultInternalJWKSURL)

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
		log.Printf("tenant-management: tracing enabled for service %q", telemetry.ServiceName())
	}

	db, err := dbinfra.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("tenant-management: close database: %v", err)
		}
	}()

	tenantService := application.NewTenantService(dbinfra.NewPostgresTenantRepository(db))

	handler, err := connectinfra.NewHandlerWithJWKSURL(tenantService, jwksURL)
	if err != nil {
		return fmt.Errorf("build handler: %w", err)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	serveErr := make(chan error, 1)

	go func() {
		log.Printf("tenant-management: server listening on %s", addr)

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
		log.Print("tenant-management: server shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		return httpServer.Shutdown(shutdownCtx)
	}
}

// shutdownTracingWithTimeout flushes pending spans on a fresh context, because
// the run context is already cancelled once the process starts shutting down.
func shutdownTracingWithTimeout(shutdown telemetry.ShutdownFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		log.Printf("tenant-management: shutdown tracing: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
