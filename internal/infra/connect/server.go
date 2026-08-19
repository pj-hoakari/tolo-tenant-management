// Package connect provides the Connect HTTP transport for this service.
package connect

import (
	"fmt"
	"log"
	"net/http"

	connectrpc "connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
)

func NewHandler(tenantService application.TenantUseCases) (http.Handler, error) {
	return NewHandlerWithJWKSURL(tenantService, jwks.DefaultInternalJWKSURL)
}

func NewHandlerWithJWKSURL(tenantService application.TenantUseCases, jwksURL string) (http.Handler, error) {
	return NewHandlerWithValidator(tenantService, jwks.NewJWKSValidator(jwksURL, internalJWTIssuer, internalJWTAudience))
}

func NewHandlerWithValidator(tenantService application.TenantUseCases, validator JWTValidator) (http.Handler, error) {
	// The caller sits behind the API Gateway, so an incoming trace context is
	// trusted and continued instead of being demoted to a span link.
	tracing, err := otelconnect.NewInterceptor(otelconnect.WithTrustRemote())
	if err != nil {
		return nil, fmt.Errorf("create tracing interceptor: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	path, handler := tenantv1connect.NewTenantServiceHandlerWithAuthz(
		NewService(tenantService),
		newTenantAuthzVerifier(validator),
		connectrpc.WithInterceptors(tracing, newTenantPublicIDInterceptor(validator)),
	)
	mux.Handle(path, handler)

	return mux, nil
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		log.Printf("healthz response write: %v", err)
	}
}
