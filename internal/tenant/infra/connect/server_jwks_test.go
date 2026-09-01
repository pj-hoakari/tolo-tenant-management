package connect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"

	infraconnect "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
)

// testRefreshCooldown lets a key a test registers after the first request be
// picked up at once. jwks.Config reads a non-positive duration as "use the
// default", so the shortest cooldown a test can ask for is one nanosecond.
// These tests are about the transport, not about the cache; the jwks package
// covers the cache itself.
const testRefreshCooldown = time.Nanosecond

// jwksRegistry serves a mutable set of JWKs so tests can add a tenant_access
// token minted for a specific tenant public ID after registration. Every key
// it hands out has its own kid, because a JWKS naming one kid twice is refused
// as a whole.
type jwksRegistry struct {
	mu     sync.Mutex
	keys   []internaljwt.JWK
	minted int
}

func (r *jwksRegistry) add(keys ...internaljwt.JWK) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.keys = append(r.keys, keys...)
}

func (r *jwksRegistry) document() internaljwt.JWKS {
	r.mu.Lock()
	defer r.mu.Unlock()

	return internaljwt.JWKS{Keys: append([]internaljwt.JWK(nil), r.keys...)}
}

// nextKeyID names the signing key of the next token minted for the registry.
func (r *jwksRegistry) nextKeyID(prefix string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.minted++

	return fmt.Sprintf("%s-%d", prefix, r.minted)
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

	cache, err := jwks.New(jwks.Config{
		URL:             server.URL,
		RefreshCooldown: testRefreshCooldown,
		FailureCooldown: testRefreshCooldown,
	})
	if err != nil {
		t.Fatalf("jwks.New() error = %v", err)
	}

	tokenVerifier, err := verifier.New(internalJWTIssuer, internalJWTAudience, cache)
	if err != nil {
		t.Fatalf("verifier.New() error = %v", err)
	}

	handler, err := infraconnect.NewHandlerWithVerifier(tokenVerifier, Mount(service))
	if err != nil {
		t.Fatalf("NewHandlerWithVerifier() error = %v", err)
	}

	return handler, registry
}

// mintServiceToken issues a user-origin service internal JWT whose tenant_id
// claim is tenantPublicID, as the Service Gateway re-issues it for a call made
// on behalf of a user inside that tenant.
func mintServiceToken(t *testing.T, registry *jwksRegistry, tenantPublicID string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:         internalJWTIssuer,
		Audience:       internalJWTAudience,
		TokenUse:       internaljwt.TokenUseService,
		TenantPublicID: tenantPublicID,
		Scope:          "events.read",
		OriginSub:      "user-" + tenantPublicID,
		KeyID:          registry.nextKeyID("service-key-" + tenantPublicID),
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("generate service token: %v", err)
	}

	registry.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}

// mintRegistrationTokenWithScope issues a registration internal JWT granting
// exactly scope. A registration token carries no tenant context, so the
// procedure's required scopes are all the policy has left to check.
func mintRegistrationTokenWithScope(t *testing.T, registry *jwksRegistry, scope string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:   internalJWTIssuer,
		Audience: internalJWTAudience,
		TokenUse: internaljwt.TokenUseRegistration,
		Scope:    scope,
		KeyID:    registry.nextKeyID("registration-key"),
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("generate registration token: %v", err)
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

	return mintTenantAccessTokenWithScope(t, registry, tenantPublicID, "tenant.write events.read events.write")
}

// mintTenantAccessTokenWithScope issues a tenant_access internal JWT granting
// exactly scope, so a test can present a caller the procedure's required
// scopes are not all granted to.
func mintTenantAccessTokenWithScope(t *testing.T, registry *jwksRegistry, tenantPublicID, scope string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:         internalJWTIssuer,
		Audience:       internalJWTAudience,
		TokenUse:       internaljwt.TokenUseTenantAccess,
		TenantPublicID: tenantPublicID,
		Scope:          scope,
		KeyID:          registry.nextKeyID("tenant-key-" + tenantPublicID),
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("generate tenant access token: %v", err)
	}

	registry.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}
