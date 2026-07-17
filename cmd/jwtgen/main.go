// Command jwtgen creates an ES256 internal JWT and matching JWKS document.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

func main() {
	issuer := flag.String("issuer", "api-gateway", "internal JWT issuer")
	audience := flag.String("audience", "tolo-tenant-management", "internal JWT audience")
	tokenUse := flag.String("token-use", "tenant_access", "token use: tenant_access, service, or registration")
	tenantID := flag.String("tenant-id", "", "tenant ID claim (required for tenant_access)")
	scope := flag.String("scope", "events.read", "space-delimited scopes")
	kid := flag.String("kid", "test-key", "JWK key ID")
	ttl := flag.Duration("ttl", 2*time.Minute, "token lifetime")

	flag.Parse()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer: *issuer, Audience: *audience, TokenUse: *tokenUse,
		TenantID: *tenantID, Scope: *scope, KeyID: *kid, TTL: *ttl,
	})
	if err != nil {
		log.Print("jwtgen: ", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(output); err != nil {
		log.Print("jwtgen: encode output: ", err)
		os.Exit(1)
	}
}
