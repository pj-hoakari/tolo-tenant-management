package jwks

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/jwtgen"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestJWKSValidatorCachesFetchedKey(t *testing.T) {
	generated, err := jwtgen.Generate(jwtgen.Config{
		Issuer: "api-gateway", Audience: "tenant-management", TokenUse: "tenant_access",
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
	validator := newJWKSValidator("https://jwks.example.test/keys", "api-gateway", "tenant-management", client)

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
