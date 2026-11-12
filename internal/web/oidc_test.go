package web

import (
	"testing"
)

func TestOIDCCookieRoundtrip(t *testing.T) {
	secret := []byte("topsecret")
	a := oidcAuth{State: "st-1", Nonce: "nn-2", PKCE: "pk-3"}

	value, err := signOIDC(secret, a)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := verifyOIDC(secret, value)
	if !ok || got != a {
		t.Fatalf("roundtrip failed: %v %v", got, ok)
	}

	if _, ok := verifyOIDC([]byte("other"), value); ok {
		t.Error("cookie accepted with wrong secret")
	}
	if _, ok := verifyOIDC(secret, value+"x"); ok {
		t.Error("tampered cookie accepted")
	}
	if _, ok := verifyOIDC(secret, "garbage"); ok {
		t.Error("garbage accepted")
	}
	if _, ok := verifyOIDC(secret, "notb64."); ok {
		t.Error("invalid base64 accepted")
	}
}

func TestAdminAllowed(t *testing.T) {
	list := []string{"a@x.com", "b@y.ru"}
	if !adminAllowed(list, "a@x.com") {
		t.Error("member not allowed")
	}
	if adminAllowed(list, "c@z.com") {
		t.Error("stranger allowed")
	}
	if adminAllowed(list, "A@X.COM") {
		t.Error("emails must be matched case-sensitively after lowercasing")
	}
}
