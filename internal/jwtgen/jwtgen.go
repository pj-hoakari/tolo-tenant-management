// Package jwtgen creates ES256 internal JWTs and matching JWKS documents.
package jwtgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	TokenUseTenantAccess = "tenant_access"
	TokenUseService      = "service"
	TokenUseRegistration = "registration"
)

// Config describes the internal JWT to mint.
//
// The claim set follows the origin of the token (internal_jwt.md):
//   - tenant_access and registration are entrance conversions and carry scope
//     and src_jti. tenant_access additionally carries tenant_id.
//   - service with OriginSub is a user-origin re-issue: it carries scope,
//     src_jti, origin_sub, and optionally tenant_id copied from the context.
//   - service without OriginSub is a machine-origin issue: it carries none of
//     scope, src_jti, origin_sub, or tenant_id.
type Config struct {
	Issuer         string
	Audience       string
	TokenUse       string
	TenantPublicID string
	Scope          string
	OriginSub      string
	KeyID          string
	TTL            time.Duration
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
	ClientID  string `json:"client_id"`
	Txn       string `json:"txn"`
	Scope     string `json:"scope,omitempty"`
	SourceJTI string `json:"src_jti,omitempty"`
	OriginSub string `json:"origin_sub,omitempty"`
	// TenantPublicID is serialized as tenant_id. It is the tenant's
	// 16-character hexadecimal public ID.
	TenantPublicID string `json:"tenant_id,omitempty"`
}

func Generate(config Config) (Output, error) {
	if config.TTL <= 0 {
		return Output{}, errors.New("ttl must be positive")
	}

	if err := validateConfig(config); err != nil {
		return Output{}, err
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Output{}, fmt.Errorf("generate key: %w", err)
	}

	txn, err := uuid.NewV7()
	if err != nil {
		return Output{}, fmt.Errorf("generate txn: %w", err)
	}

	now := time.Now()
	tokenClaims := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Issuer,
			Subject:   "test-subject",
			Audience:  jwt.ClaimStrings{config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(config.TTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        "test-jti",
		},
		TokenUse:       config.TokenUse,
		ClientID:       "test-client",
		Txn:            txn.String(),
		Scope:          "",
		SourceJTI:      "",
		OriginSub:      "",
		TenantPublicID: "",
	}

	if config.TokenUse != TokenUseService || config.OriginSub != "" {
		tokenClaims.Scope = config.Scope
		tokenClaims.SourceJTI = "test-source-jti"
		tokenClaims.OriginSub = config.OriginSub
		tokenClaims.TenantPublicID = config.TenantPublicID
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, tokenClaims)
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

func validateConfig(config Config) error {
	hasTenant := strings.TrimSpace(config.TenantPublicID) != ""
	hasScope := strings.TrimSpace(config.Scope) != ""
	hasOriginSub := strings.TrimSpace(config.OriginSub) != ""

	switch config.TokenUse {
	case TokenUseTenantAccess:
		if !hasTenant {
			return errors.New("tenant-public-id is required for tenant_access")
		}

		if !hasScope {
			return errors.New("scope is required for tenant_access")
		}

		if hasOriginSub {
			return errors.New("origin-sub is only valid for service")
		}
	case TokenUseRegistration:
		if hasTenant {
			return errors.New("registration must not carry tenant-public-id")
		}

		if !hasScope {
			return errors.New("scope is required for registration")
		}

		if hasOriginSub {
			return errors.New("origin-sub is only valid for service")
		}
	case TokenUseService:
		if hasOriginSub {
			if !hasScope {
				return errors.New("scope is required for a user-origin service token")
			}

			return nil
		}

		if hasScope || hasTenant {
			return errors.New("a machine-origin service token carries neither scope nor tenant-public-id; set origin-sub for a user-origin token")
		}
	default:
		return fmt.Errorf("unsupported token use %q", config.TokenUse)
	}

	return nil
}
