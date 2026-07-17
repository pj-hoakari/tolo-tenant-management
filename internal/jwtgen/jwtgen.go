// Package jwtgen creates ES256 internal JWTs and matching JWKS documents.
package jwtgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Config struct {
	Issuer   string
	Audience string
	TokenUse string
	TenantID string
	Scope    string
	KeyID    string
	TTL      time.Duration
}

type Output struct {
	Token string `json:"token"`
	JWKS  JWKS   `json:"jwks"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type JWK struct {
	KeyType   string `json:"kty"`
	Curve     string `json:"crv"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	X         string `json:"x"`
	Y         string `json:"y"`
}

type claims struct {
	jwt.RegisteredClaims
	TokenUse  string `json:"token_use"`
	Scope     string `json:"scope"`
	ClientID  string `json:"client_id"`
	SourceJTI string `json:"src_jti"`
	TenantID  string `json:"tenant_id,omitempty"`
}

func Generate(config Config) (Output, error) {
	if config.TTL <= 0 {
		return Output{}, fmt.Errorf("ttl must be positive")
	}

	if config.TokenUse != "tenant_access" && config.TokenUse != "service" && config.TokenUse != "registration" {
		return Output{}, fmt.Errorf("unsupported token use %q", config.TokenUse)
	}

	if config.TokenUse == "tenant_access" && strings.TrimSpace(config.TenantID) == "" {
		return Output{}, fmt.Errorf("tenant-id is required for tenant_access")
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Output{}, fmt.Errorf("generate key: %w", err)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Issuer,
			Subject:   "test-subject",
			Audience:  jwt.ClaimStrings{config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(config.TTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        "test-jti",
		},
		TokenUse:  config.TokenUse,
		Scope:     config.Scope,
		ClientID:  "test-client",
		SourceJTI: "test-source-jti",
		TenantID:  config.TenantID,
	})
	token.Header["kid"] = config.KeyID

	signed, err := token.SignedString(privateKey)
	if err != nil {
		return Output{}, fmt.Errorf("sign JWT: %w", err)
	}

	publicKey, err := privateKey.PublicKey.Bytes()
	if err != nil {
		return Output{}, fmt.Errorf("encode public key: %w", err)
	}

	if len(publicKey) != 65 {
		return Output{}, fmt.Errorf("unexpected ES256 public key length %d", len(publicKey))
	}

	return Output{
		Token: signed,
		JWKS: JWKS{Keys: []JWK{{
			KeyType:   "EC",
			Curve:     "P-256",
			KeyID:     config.KeyID,
			Use:       "sig",
			Algorithm: "ES256",
			X:         base64.RawURLEncoding.EncodeToString(publicKey[1:33]),
			Y:         base64.RawURLEncoding.EncodeToString(publicKey[33:]),
		}}},
	}, nil
}
