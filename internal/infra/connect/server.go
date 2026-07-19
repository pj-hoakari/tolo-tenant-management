// Package connect provides the Connect HTTP transport for this service.
package connect

import (
	"log"
	"net/http"

	connectrpc "connectrpc.com/connect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
)

func NewHandler(tenantService application.TenantUseCases) http.Handler {
	return NewHandlerWithJWKSURL(tenantService, jwks.DefaultInternalJWKSURL)
}

func NewHandlerWithJWKSURL(tenantService application.TenantUseCases, jwksURL string) http.Handler {
	return NewHandlerWithValidator(tenantService, jwks.NewJWKSValidator(jwksURL, internalJWTIssuer, internalJWTAudience))
}

func NewHandlerWithValidator(tenantService application.TenantUseCases, validator JWTValidator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	path, handler := tenantv1connect.NewTenantServiceHandlerWithAuthz(
		NewService(tenantService),
		newTenantAuthzVerifier(validator),
		connectrpc.WithInterceptors(newTenantPublicIDInterceptor(validator)),
	)
	mux.Handle(path, handler)

	return mux
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		log.Printf("healthz response write: %v", err)
	}
}
