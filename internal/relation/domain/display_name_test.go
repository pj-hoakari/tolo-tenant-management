package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDisplayName(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		name  string
		input string
		want  string
	}{
		{name: "a plain name", input: "Hanako", want: "Hanako"},
		{name: "surrounding whitespace trimmed", input: "  Hanako \t\n", want: "Hanako"},
		{name: "inner whitespace kept", input: "Yamada Hanako", want: "Yamada Hanako"},
		{name: "64 ASCII characters", input: strings.Repeat("a", 64), want: strings.Repeat("a", 64)},
		{name: "64 multibyte characters", input: strings.Repeat("あ", 64), want: strings.Repeat("あ", 64)},
	}

	for _, tt := range accepted {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDisplayName(tt.input)
			if err != nil {
				t.Fatalf("ParseDisplayName(%q) error = %v", tt.input, err)
			}

			if got.String() != tt.want {
				t.Errorf("ParseDisplayName(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}

	rejected := []struct {
		name  string
		input string
		want  error
	}{
		{name: "empty", input: "", want: ErrDisplayNameRequired},
		{name: "whitespace only", input: " \t\n", want: ErrDisplayNameRequired},
		{name: "65 ASCII characters", input: strings.Repeat("a", 65), want: ErrDisplayNameTooLong},
		{name: "65 multibyte characters", input: strings.Repeat("あ", 65), want: ErrDisplayNameTooLong},
	}

	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseDisplayName(tt.input); !errors.Is(err, tt.want) {
				t.Errorf("ParseDisplayName(%q) error = %v, want %v", tt.input, err, tt.want)
			}
		})
	}
}
