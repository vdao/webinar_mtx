package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// RFC 7636 appendix B test vector for the S256 challenge.
func TestPKCEChallenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	sum := sha256.Sum256([]byte(verifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != want {
		t.Fatalf("pkce challenge = %q, want %q", got, want)
	}
}

func TestRandomToken(t *testing.T) {
	a, b := randomToken(), randomToken()
	if len(a) != 43 { // 32 bytes → base64url without padding
		t.Errorf("token length = %d, want 43", len(a))
	}
	if a == b {
		t.Error("tokens must not repeat")
	}
}
