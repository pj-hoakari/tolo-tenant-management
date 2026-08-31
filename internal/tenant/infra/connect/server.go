// Package connect provides the Connect HTTP transport for this service.
package connect

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	connectrpc "connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/pj-hoakari/internal-jwt-handling/interceptor"
	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"
	"github.com/pj-hoakari/protoc-gen-authz-go/authz"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
)

// Defaults for verifying internal JWTs. The issuer is the Service Gateway's
// issuer identifier and must match the value the gateway signs with; the
// audience is this service's logical identifier.
const (
	DefaultInternalJWKSURL     = "http://gateway:8080/.well-known/jwks.json"
	DefaultInternalJWTIssuer   = "service-gateway"
	DefaultInternalJWTAudience = "tolo-tenant-management"
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
		JWKSURL:  DefaultInternalJWKSURL,
		Issuer:   DefaultInternalJWTIssuer,
		Audience: DefaultInternalJWTAudience,
	}
}

// AuthInterceptor builds the interceptor that authenticates and authorizes one
// service from its generated policy table.
type AuthInterceptor func(policies authz.Policies) (connectrpc.Interceptor, error)

// Mount registers one more Connect service on the mux. auth builds the
// service's authentication interceptor; interceptors are the ones every
// service in the process runs with (tracing), to be placed before it.
type Mount func(mux *http.ServeMux, auth AuthInterceptor, interceptors ...connectrpc.Interceptor) error

// NewHandlerWithJWTSettings builds the process handler that verifies internal
// JWTs against the JWKS the settings locate.
func NewHandlerWithJWTSettings(tenantService application.TenantUseCases, settings JWTSettings, mounts ...Mount) (http.Handler, error) {
	cache, err := jwks.New(jwks.Config{
		URL:             settings.JWKSURL,
		HTTPClient:      nil,
		CacheTTL:        0,
		RefreshCooldown: 0,
		FailureCooldown: 0,
		FetchTimeout:    0,
		RetryBackoff:    nil,
		MaxDocumentSize: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("create JWKS cache: %w", err)
	}

	tokenVerifier, err := verifier.New(settings.Issuer, settings.Audience, cache)
	if err != nil {
		return nil, fmt.Errorf("create internal JWT verifier: %w", err)
	}

	return NewHandlerWithVerifier(tenantService, tokenVerifier, mounts...)
}

// NewHandlerWithVerifier builds the process handler around a verifier of the
// internal JWT. Every service served by this process is guarded by an
// interceptor built from its own generated policy table, so the credential
// rules stay declared in the proto.
func NewHandlerWithVerifier(tenantService application.TenantUseCases, tokenVerifier interceptor.TokenVerifier, mounts ...Mount) (http.Handler, error) {
	// The caller sits behind the Service Gateway, so an incoming trace context is
	// trusted and continued instead of being demoted to a span link.
	tracing, err := otelconnect.NewInterceptor(otelconnect.WithTrustRemote())
	if err != nil {
		return nil, fmt.Errorf("create tracing interceptor: %w", err)
	}

	auth := func(policies authz.Policies) (connectrpc.Interceptor, error) {
		return interceptor.New(tokenVerifier, policies, interceptor.WithErrorReporter(reportAuthRejection))
	}

	tenantAuth, err := auth(tenantv1connect.TenantServicePolicies)
	if err != nil {
		return nil, fmt.Errorf("create TenantService authentication interceptor: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	// Tracing runs before authentication, so a rejected call is still recorded
	// on the trace it belongs to.
	path, handler := tenantv1connect.NewTenantServiceHandler(
		NewService(tenantService),
		connectrpc.WithInterceptors(tracing, tenantAuth),
	)
	mux.Handle(path, handler)

	for _, mount := range mounts {
		if err := mount(mux, auth, tracing); err != nil {
			return nil, fmt.Errorf("mount service: %w", err)
		}
	}

	return mux, nil
}

// reportAuthRejection logs why a call was refused. The client only ever learns
// the Connect code, so the cause is kept server-side, on the trace of the
// request context.
func reportAuthRejection(ctx context.Context, procedure string, err error) {
	slog.WarnContext(ctx, "internal JWT rejected", "procedure", procedure, "error", err)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		slog.ErrorContext(r.Context(), "healthz response write failed", "error", err)
	}
}
