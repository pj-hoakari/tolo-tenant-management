// Package jwks validates internal JWTs using a remote JSON Web Key Set.
package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const DefaultInternalJWKSURL = "http://gateway:8080/.well-known/jwks.json"

const jwksCacheTTL = 5 * time.Minute

// JWKSValidator validates internal JWTs against API Gateway's published keys.
// One instance is shared by authorization and tenant ID extraction.
type JWKSValidator struct {
	url      string
	issuer   string
	audience string
	client   *http.Client
	mu       sync.Mutex
	keys     map[string]*ecdsa.PublicKey
	expiry   time.Time
}

type InternalJWTClaims struct {
	jwt.RegisteredClaims
	TokenUse  string `json:"token_use"`
	Scope     string `json:"scope"`
	ClientID  string `json:"client_id"`
	SourceJTI string `json:"src_jti"`
	// TenantPublicID is carried in the tenant_id JWT claim. Its value is the
	// tenant's 16-character hexadecimal public ID, the same value the proto
	// tenant_id fields carry; the internal UUIDv7 never appears in claims.
	TenantPublicID string `json:"tenant_id"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyType string `json:"kty"`
	Curve   string `json:"crv"`
	KeyID   string `json:"kid"`
	Use     string `json:"use"`
	Alg     string `json:"alg"`
	X       string `json:"x"`
	Y       string `json:"y"`
}

func NewJWKSValidator(url, issuer, audience string) *JWKSValidator {
	return newJWKSValidator(url, issuer, audience, http.DefaultClient)
}

func newJWKSValidator(url, issuer, audience string, client *http.Client) *JWKSValidator {
	if strings.TrimSpace(url) == "" {
		url = DefaultInternalJWKSURL
	}

	return &JWKSValidator{
		url:      url,
		issuer:   issuer,
		audience: audience,
		client:   client,
		mu:       sync.Mutex{},
		keys:     make(map[string]*ecdsa.PublicKey),
		expiry:   time.Time{},
	}
}

func (v *JWKSValidator) Claims(ctx context.Context, authorization string) (InternalJWTClaims, error) {
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return InternalJWTClaims{}, errors.New("invalid authorization header")
	}

	var claims InternalJWTClaims

	parser := jwt.NewParser(
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithIssuer(v.issuer),
		jwt.WithLeeway(30*time.Second),
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
	)

	token, err := parser.ParseWithClaims(parts[1], &claims, func(token *jwt.Token) (any, error) {
		keyID, ok := token.Header["kid"].(string)
		if !ok || strings.TrimSpace(keyID) == "" {
			return nil, errors.New("JWT kid is required")
		}

		return v.key(ctx, keyID)
	})
	if err != nil || !token.Valid || !validClaims(claims) {
		return InternalJWTClaims{}, errors.New("invalid internal JWT")
	}

	return claims, nil
}

func validClaims(claims InternalJWTClaims) bool {
	return strings.TrimSpace(claims.Subject) != "" && strings.TrimSpace(claims.ClientID) != "" && strings.TrimSpace(claims.SourceJTI) != "" && strings.TrimSpace(claims.ID) != "" && claims.IssuedAt != nil && claims.ExpiresAt != nil && claims.NotBefore != nil && strings.TrimSpace(claims.TokenUse) != ""
}

func (v *JWKSValidator) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if key, ok := v.keys[kid]; ok && time.Now().Before(v.expiry) {
		return key, nil
	}

	if err := v.refresh(ctx); err != nil {
		return nil, err
	}

	key, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown JWT key ID %q", kid)
	}

	return key, nil
}

func (v *JWKSValidator) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}

	response, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			return
		}
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: unexpected status %s", response.Status)
	}

	document := jwksDocument{Keys: nil}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*ecdsa.PublicKey, len(document.Keys))
	for _, value := range document.Keys {
		key, err := publicKeyFromJWK(value)
		if err != nil {
			return err
		}

		keys[value.KeyID] = key
	}

	v.keys = keys
	v.expiry = time.Now().Add(jwksCacheTTL)

	return nil
}

func publicKeyFromJWK(value jwk) (*ecdsa.PublicKey, error) {
	if value.KeyType != "EC" || value.Curve != "P-256" || value.Alg != "ES256" || value.Use != "sig" || value.KeyID == "" {
		return nil, errors.New("invalid JWK")
	}

	x, err := base64.RawURLEncoding.DecodeString(value.X)
	if err != nil {
		return nil, fmt.Errorf("decode JWK x: %w", err)
	}

	y, err := base64.RawURLEncoding.DecodeString(value.Y)
	if err != nil {
		return nil, fmt.Errorf("decode JWK y: %w", err)
	}

	encoded := append([]byte{4}, x...)
	encoded = append(encoded, y...)

	publicKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), encoded)
	if err != nil {
		return nil, fmt.Errorf("decode JWK public key: %w", err)
	}

	return publicKey, nil
}
