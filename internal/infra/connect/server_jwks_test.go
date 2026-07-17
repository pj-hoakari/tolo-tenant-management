package connect

import (
	"net/http"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
)

func newTestHandler(t *testing.T, service application.TenantUseCases) http.Handler {
	t.Helper()
	jwtServer := newJWKSStub(t, internalJWTs(t).jwks)

	return NewHandlerWithJWKSURL(service, jwtServer.URL)
}
