package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSubdomain(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		name      string
		subdomain string
	}{
		{name: "lowercase letters", subdomain: "acme"},
		{name: "letters, digits and hyphens", subdomain: "acme-2026"},
		{name: "one character", subdomain: "a"},
		{name: "digits only", subdomain: "2026"},
		{name: "63 characters", subdomain: strings.Repeat("a", 63)},
		{name: "15-character hex", subdomain: "0123456789abcde"},
		{name: "17-character hex", subdomain: "0123456789abcdef0"},
		{name: "16 characters that are not all hex", subdomain: "0123456789abcdeg"},
		{name: "hyphens that are not the 3rd and 4th characters", subdomain: "a--b"},
		{name: "a reserved word as a prefix", subdomain: "www2"},
		{name: "s alone", subdomain: "s"},
		{name: "s followed by digits and letters", subdomain: "s1a"},
	}

	for _, tt := range accepted {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSubdomain(tt.subdomain)
			if err != nil {
				t.Fatalf("ParseSubdomain(%q) error = %v", tt.subdomain, err)
			}

			if got.String() != tt.subdomain {
				t.Errorf("ParseSubdomain(%q).String() = %q", tt.subdomain, got.String())
			}
		})
	}

	rejected := []struct {
		name      string
		subdomain string
	}{
		{name: "empty", subdomain: ""},
		{name: "64 characters", subdomain: strings.Repeat("a", 64)},
		{name: "uppercase letters", subdomain: "Acme"},
		{name: "an underscore", subdomain: "acme_2026"},
		{name: "a dot", subdomain: "acme.example"},
		{name: "non-ASCII letters", subdomain: "あくめ"},
		{name: "a leading hyphen", subdomain: "-acme"},
		{name: "a trailing hyphen", subdomain: "acme-"},
		{name: "16-character hex like a public ID", subdomain: "0123456789abcdef"},
		{name: "an IDNA prefix", subdomain: "xn--acme"},
		{name: "hyphens as the 3rd and 4th characters", subdomain: "ab--cd"},
		{name: "reserved www", subdomain: "www"},
		{name: "reserved api", subdomain: "api"},
		{name: "reserved admin", subdomain: "admin"},
		{name: "reserved auth", subdomain: "auth"},
		{name: "a preview slot", subdomain: "s1"},
		{name: "a preview slot with many digits", subdomain: "s0123"},
	}

	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseSubdomain(tt.subdomain); !errors.Is(err, ErrInvalidSubdomain) {
				t.Errorf("ParseSubdomain(%q) error = %v, want %v", tt.subdomain, err, ErrInvalidSubdomain)
			}
		})
	}
}
