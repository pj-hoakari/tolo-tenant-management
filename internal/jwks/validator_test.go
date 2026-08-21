package jwks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestJWKSValidatorCachesFetchedKey(t *testing.T) {
	generated, err := jwtgen.Generate(jwtgen.Config{
		Issuer: DefaultInternalJWTIssuer, Audience: "tenant-management", TokenUse: "tenant_access",
		TenantPublicID: "0123456789abcdef", Scope: "events.read", KeyID: "key-1", TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	body := []byte(`{"keys":[{"kty":"` + generated.JWKS.Keys[0].KeyType + `","crv":"` + generated.JWKS.Keys[0].Curve + `","kid":"` + generated.JWKS.Keys[0].KeyID + `","use":"` + generated.JWKS.Keys[0].Use + `","alg":"` + generated.JWKS.Keys[0].Algorithm + `","x":"` + generated.JWKS.Keys[0].X + `","y":"` + generated.JWKS.Keys[0].Y + `"}]}`)

	var mu sync.Mutex

	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	validator := newJWKSValidator("https://jwks.example.test/keys", DefaultInternalJWTIssuer, "tenant-management", client)

	for range 2 {
		if _, err := validator.Claims(context.Background(), "Bearer "+generated.Token); err != nil {
			t.Fatalf("Claims() error = %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if got, want := requests, 1; got != want {
		t.Errorf("JWKS requests = %d, want %d", got, want)
	}
}

func TestJWKSValidatorAcceptsBothServiceTokenOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config jwtgen.Config
	}{
		{
			name:   "machine origin",
			config: jwtgen.Config{Issuer: DefaultInternalJWTIssuer, Audience: "tenant-management", TokenUse: "service", KeyID: "key-1", TTL: time.Hour},
		},
		{
			name: "user origin with tenant context",
			config: jwtgen.Config{
				Issuer: DefaultInternalJWTIssuer, Audience: "tenant-management", TokenUse: "service",
				OriginSub: "user-1", Scope: "events.read", TenantPublicID: "0123456789abcdef", KeyID: "key-1", TTL: time.Hour,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			generated, err := jwtgen.Generate(tt.config)
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			validator := newJWKSValidator("https://jwks.example.test/keys", DefaultInternalJWTIssuer, "tenant-management", staticJWKSClient(t, generated.JWKS))

			claims, err := validator.Claims(context.Background(), "Bearer "+generated.Token)
			if err != nil {
				t.Fatalf("Claims() error = %v", err)
			}

			if claims.Txn == "" {
				t.Error("Txn is empty, want a value")
			}

			if got, want := claims.TenantPublicID, tt.config.TenantPublicID; got != want {
				t.Errorf("TenantPublicID = %q, want %q", got, want)
			}
		})
	}
}

func TestValidClaims(t *testing.T) {
	t.Parallel()

	now := jwt.NewNumericDate(time.Now())
	base := func(tokenUse string) InternalJWTClaims {
		return InternalJWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: "user-1", ID: "jti-1", IssuedAt: now, ExpiresAt: now, NotBefore: now},
			TokenUse:         tokenUse,
			ClientID:         "client-1",
			Txn:              "txn-1",
		}
	}
	entrance := func(tokenUse string) InternalJWTClaims {
		claims := base(tokenUse)
		claims.Scope = "events.read"
		claims.SourceJTI = "src-1"

		return claims
	}

	tests := []struct {
		name   string
		mutate func(*InternalJWTClaims)
		claims InternalJWTClaims
		want   bool
	}{
		{name: "tenant_access", claims: entrance("tenant_access"), mutate: func(c *InternalJWTClaims) { c.TenantPublicID = "0123456789abcdef" }, want: true},
		{name: "tenant_access without tenant_id", claims: entrance("tenant_access"), mutate: func(*InternalJWTClaims) {}, want: false},
		{name: "tenant_access without txn", claims: entrance("tenant_access"), mutate: func(c *InternalJWTClaims) { c.TenantPublicID = "0123456789abcdef"; c.Txn = "" }, want: false},
		{name: "event_access", claims: entrance("event_access"), mutate: func(c *InternalJWTClaims) {
			c.TenantPublicID = "0123456789abcdef"
			c.EventPublicID = "fedcba9876543210"
		}, want: true},
		{name: "event_access without event_id", claims: entrance("event_access"), mutate: func(c *InternalJWTClaims) { c.TenantPublicID = "0123456789abcdef" }, want: false},
		{name: "registration", claims: entrance("registration"), mutate: func(*InternalJWTClaims) {}, want: true},
		{name: "registration with tenant_id", claims: entrance("registration"), mutate: func(c *InternalJWTClaims) { c.TenantPublicID = "0123456789abcdef" }, want: false},
		{name: "machine-origin service", claims: base("service"), mutate: func(*InternalJWTClaims) {}, want: true},
		{name: "machine-origin service with tenant_id", claims: base("service"), mutate: func(c *InternalJWTClaims) { c.TenantPublicID = "0123456789abcdef" }, want: false},
		{name: "user-origin service", claims: entrance("service"), mutate: func(c *InternalJWTClaims) { c.OriginSub = "user-1"; c.TenantPublicID = "0123456789abcdef" }, want: true},
		{name: "user-origin service without origin_sub", claims: entrance("service"), mutate: func(*InternalJWTClaims) {}, want: false},
		{name: "unknown token_use", claims: entrance("other"), mutate: func(*InternalJWTClaims) {}, want: false},
		{name: "missing client_id", claims: base("service"), mutate: func(c *InternalJWTClaims) { c.ClientID = "" }, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claims := tt.claims
			tt.mutate(&claims)

			if got := validClaims(claims); got != tt.want {
				t.Errorf("validClaims() = %t, want %t", got, tt.want)
			}
		})
	}
}

// staticJWKSClient serves one JWKS document for every request.
func staticJWKSClient(t *testing.T, document jwtgen.JWKS) *http.Client {
	t.Helper()

	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}

	return &http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
}
