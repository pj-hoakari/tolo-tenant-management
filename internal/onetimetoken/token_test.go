package onetimetoken

import "testing"

func TestNew(t *testing.T) {
	t.Parallel()

	token, hash, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if len(token) != 43 {
		t.Errorf("token length = %d, want 43 (32 random bytes, base64url)", len(token))
	}

	if !hash.Matches(token) {
		t.Error("Matches(token) = false, want true")
	}

	if hash.Matches(token + "x") {
		t.Error("Matches(tampered token) = true, want false")
	}

	if got := HashOf(token); got != hash {
		t.Error("HashOf(token) differs from the hash returned at creation")
	}

	other, _, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if other == token {
		t.Error("two generated tokens are equal")
	}
}
