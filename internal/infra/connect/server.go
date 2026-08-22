// Package connect provides the Connect HTTP transport for this service.
package connect

import (
	"fmt"
	"log/slog"
	"net/http"

	connectrpc "connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
)

// JWTSettings locates the Service Gateway's JWKS and names the issuer and
// audience every internal JWT must carry.
type JWTSettings struct {
	JWKSURL  string
	Issuer   string
	Audience string
}

// DefaultJWTSettings returns the settings for the Docker Compose setup.
func DefaultJWTSettings() JWTSettings {
	return JWTSettings{
		JWKSURL:  jwks.DefaultInternalJWKSURL,
		Issuer:   jwks.DefaultInternalJWTIssuer,
		Audience: jwks.DefaultInternalJWTAudience,
	}
}

// Mount registers one more Connect service on the mux. The validator and the
// interceptors are the ones TenantService runs with, so every service in the
// process verifies credentials and establishes request context the same way.
type Mount func(mux *http.ServeMux, validator JWTValidator, interceptors ...connectrpc.Interceptor)

func NewHandler(tenantService application.TenantUseCases, mounts ...Mount) (http.Handler, error) {
	return NewHandlerWithJWTSettings(tenantService, DefaultJWTSettings(), mounts...)
}

func NewHandlerWithJWTSettings(tenantService application.TenantUseCases, settings JWTSettings, mounts ...Mount) (http.Handler, error) {
	return NewHandlerWithValidator(tenantService, jwks.NewJWKSValidator(settings.JWKSURL, settings.Issuer, settings.Audience), mounts...)
}

func NewHandlerWithValidator(tenantService application.TenantUseCases, validator JWTValidator, mounts ...Mount) (http.Handler, error) {
	// The caller sits behind the Service Gateway, so an incoming trace context is
	// trusted and continued instead of being demoted to a span link.
	tracing, err := otelconnect.NewInterceptor(otelconnect.WithTrustRemote())
	if err != nil {
		return nil, fmt.Errorf("create tracing interceptor: %w", err)
	}

	interceptors := []connectrpc.Interceptor{tracing, NewClaimInterceptor(validator)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	path, handler := tenantv1connect.NewTenantServiceHandlerWithAuthz(
		NewService(tenantService),
		newTenantAuthzVerifier(validator),
		connectrpc.WithInterceptors(interceptors...),
	)
	mux.Handle(path, handler)

	for _, mount := range mounts {
		mount(mux, validator, interceptors...)
	}

	return mux, nil
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		slog.ErrorContext(r.Context(), "healthz response write failed", "error", err)
	}
}
