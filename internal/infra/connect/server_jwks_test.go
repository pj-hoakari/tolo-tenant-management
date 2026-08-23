package connect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

// jwksRegistry serves a mutable set of JWKs so tests can add a tenant_access
// token minted for a specific tenant public ID after registration.
type jwksRegistry struct {
	mu   sync.Mutex
	keys []jwtgen.JWK
}

func (r *jwksRegistry) add(keys ...jwtgen.JWK) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.keys = append(r.keys, keys...)
}

func (r *jwksRegistry) document() jwtgen.JWKS {
	r.mu.Lock()
	defer r.mu.Unlock()

	return jwtgen.JWKS{Keys: append([]jwtgen.JWK(nil), r.keys...)}
}

// newDynamicTestHandler builds a handler whose JWKS endpoint can grow at
// runtime, returning the registry so tests can register tokens minted for the
// tenant public ID of a tenant created by the test.
func newDynamicTestHandler(t *testing.T, service application.TenantUseCases) (http.Handler, *jwksRegistry) {
	t.Helper()

	registry := &jwksRegistry{}
	registry.add(internalJWTs(t).jwks.Keys...)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(registry.document()); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	settings := DefaultJWTSettings()
	settings.JWKSURL = server.URL

	handler, err := NewHandlerWithValidator(service, freshJWKSValidator(settings))
	if err != nil {
		t.Fatalf("NewHandlerWithValidator() error = %v", err)
	}

	return handler, registry
}

// freshJWKSValidator validates every request with a newly built JWKS
// validator. The real one caches the document and rate limits the refresh an
// unknown kid triggers, so a key a test registers after the first request
// would stay invisible for the length of that cooldown. These tests are about
// the transport, not about the cache; internal/jwks covers the cache itself.
type freshJWKSValidator JWTSettings

func (v freshJWKSValidator) Claims(ctx context.Context, authorization string) (jwks.InternalJWTClaims, error) {
	return jwks.NewJWKSValidator(v.JWKSURL, v.Issuer, v.Audience).Claims(ctx, authorization)
}

// mintServiceToken issues a user-origin service internal JWT whose tenant_id
// claim is tenantPublicID, as the Service Gateway re-issues it for a call made
// on behalf of a user inside that tenant.
func mintServiceToken(t *testing.T, registry *jwksRegistry, tenantPublicID string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:         internalJWTIssuer,
		Audience:       internalJWTAudience,
		TokenUse:       jwtgen.TokenUseService,
		TenantPublicID: tenantPublicID,
		Scope:          "events.read",
		OriginSub:      "user-" + tenantPublicID,
		KeyID:          "service-key-" + tenantPublicID,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("generate service token: %v", err)
	}

	registry.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}

// mintTenantAccessToken issues a tenant_access internal JWT whose tenant_id
// claim is tenantPublicID and registers its signing key so the handler can
// verify it. This mirrors the Service Gateway issuing a token bound to the
// tenant the caller is authenticated for.
func mintTenantAccessToken(t *testing.T, registry *jwksRegistry, tenantPublicID string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:         internalJWTIssuer,
		Audience:       internalJWTAudience,
		TokenUse:       "tenant_access",
		TenantPublicID: tenantPublicID,
		Scope:          "tenant.write events.read events.write",
		KeyID:          "tenant-key-" + tenantPublicID,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("generate tenant access token: %v", err)
	}

	registry.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}
