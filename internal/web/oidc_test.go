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

func TestAdminAllowedByGroup(t *testing.T) {
	if !adminAllowed([]string{"users", "admin", "ops"}, "admin") {
		t.Error("member of the admin group must be allowed")
	}
	if adminAllowed([]string{"users", "ops"}, "admin") {
		t.Error("stranger must be denied")
	}
	if adminAllowed(nil, "admin") {
		t.Error("no groups at all must be denied")
	}
	if adminAllowed([]string{"Admin"}, "admin") {
		t.Error("group names must match exactly (case-sensitive)")
	}
	if !adminAllowed([]string{"webinar-admins"}, "webinar-admins") {
		t.Error("custom group name must work")
	}
}
