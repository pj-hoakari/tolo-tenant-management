package connect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

type testInternalJWTs struct {
	tenantAccess string
	registration string
	service      string
	jwks         jwtgen.JWKS
}

var (
	testTokensOnce sync.Once
	testTokens     testInternalJWTs
	testTokensErr  error
)

func internalJWTs(t *testing.T) testInternalJWTs {
	t.Helper()
	testTokensOnce.Do(func() {
		configs := []jwtgen.Config{
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "tenant_access", TenantID: "test-tenant-id", Scope: "tenant_access tenant.write events.read events.write", KeyID: "tenant-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "registration", Scope: "tenant.register", KeyID: "registration-key", TTL: time.Hour},
			{Issuer: internalJWTIssuer, Audience: internalJWTAudience, TokenUse: "service", KeyID: "service-key", TTL: time.Hour},
		}

		outputs := make([]jwtgen.Output, len(configs))
		for i, config := range configs {
			outputs[i], testTokensErr = jwtgen.Generate(config)
			if testTokensErr != nil {
				return
			}

			testTokens.jwks.Keys = append(testTokens.jwks.Keys, outputs[i].JWKS.Keys...)
		}

		testTokens.tenantAccess = "Bearer " + outputs[0].Token
		testTokens.registration = "Bearer " + outputs[1].Token
		testTokens.service = "Bearer " + outputs[2].Token
	})

	if testTokensErr != nil {
		t.Fatalf("generate test internal JWTs: %v", testTokensErr)
	}

	return testTokens
}

func newJWKSStub(t *testing.T, jwks jwtgen.JWKS) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return server
}
