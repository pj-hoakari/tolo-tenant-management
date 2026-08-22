// Command jwtgen creates an ES256 internal JWT and matching JWKS document.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

func main() {
	issuer := flag.String("issuer", jwks.DefaultInternalJWTIssuer, "internal JWT issuer (the Service Gateway's issuer identifier)")
	audience := flag.String("audience", jwks.DefaultInternalJWTAudience, "internal JWT audience")
	tokenUse := flag.String("token-use", jwtgen.TokenUseTenantAccess, "token use: tenant_access, service, or registration")
	tenantPublicID := flag.String("tenant-public-id", "", "tenant public ID (16-character hex; required for tenant_access, optional for a user-origin service token)")
	scope := flag.String("scope", "", "space-delimited scopes (required for tenant_access, registration, and a user-origin service token)")
	originSub := flag.String("origin-sub", "", "origin user ID; turns a service token into a user-origin re-issue")
	kid := flag.String("kid", "test-key", "JWK key ID")
	ttl := flag.Duration("ttl", 2*time.Minute, "token lifetime")

	flag.Parse()

	// A local CLI reports its failures to a person on stderr, so plain text
	// is more useful here than the service's structured output.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer: *issuer, Audience: *audience, TokenUse: *tokenUse,
		TenantPublicID: *tenantPublicID, Scope: *scope, OriginSub: *originSub, KeyID: *kid, TTL: *ttl,
	})
	if err != nil {
		logger.Error("generate token failed", "error", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(output); err != nil {
		logger.Error("encode output failed", "error", err)
		os.Exit(1)
	}
}
