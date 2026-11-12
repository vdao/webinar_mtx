package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdao/webinar_mtx/internal/config"
)

// fakeIdP is a minimal OIDC provider for tests: discovery, JWKS and a token
// endpoint issuing a signed ID token.
type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	email  string
	groups []string
	nonce  string // nonce expected in the issued token
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{key: key, email: "admin@example.com", groups: []string{"users", "admin"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/jwks.json",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": "AQAB",
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at","token_type":"Bearer","id_token":%q}`, p.signIDToken(t))
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIdP) signIDToken(t *testing.T) string {
	t.Helper()
	header := base64URL(map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	claims := map[string]any{
		"iss":    p.srv.URL,
		"aud":    "cid",
		"sub":    "u1",
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
		"email":  p.email,
		"groups": p.groups,
	}
	if p.nonce != "" {
		claims["nonce"] = p.nonce
	}
	payload := base64URL(claims)
	signing := []byte(header + "." + payload)
	digest := sha256.Sum256(signing)
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(signing) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func base64URL(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestManagerFlow(t *testing.T) {
	idp := newFakeIdP(t)
	m, err := New(context.Background(), config.OIDCConfig{
		IssuerURL: idp.srv.URL, ClientID: "cid", ClientSecret: "cs",
		RedirectURL: "http://app/admin/oidc/callback",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req, authURL, err := m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"code_challenge_method=S256", "state=" + req.State, "nonce=" + req.Nonce} {
		if !strings.Contains(authURL, want) {
			t.Errorf("authURL missing %s", want)
		}
	}

	// Happy path: the IdP embeds the correct nonce into the token.
	idp.nonce = req.Nonce
	ident, err := m.Complete(context.Background(), req, "code123")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if ident.Email != "admin@example.com" {
		t.Fatalf("email = %q", ident.Email)
	}
	if len(ident.Groups) != 2 || ident.Groups[0] != "users" || ident.Groups[1] != "admin" {
		t.Fatalf("groups = %v", ident.Groups)
	}

	// A token without the admin group still verifies — access control is
	// the caller's decision (see web.adminAllowed).
	idp.groups = nil
	ident, err = m.Complete(context.Background(), req, "code123")
	if err != nil {
		t.Fatalf("Complete without groups: %v", err)
	}
	if ident.Groups != nil {
		t.Fatalf("groups = %v, want nil", ident.Groups)
	}

	// Replay with a wrong nonce must fail.
	idp.nonce = "other-nonce"
	if _, err := m.Complete(context.Background(), req, "code123"); err == nil {
		t.Error("nonce mismatch must fail verification")
	}
}

func TestNewRetriesDiscovery(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound) // wrong path anyway; not used further
	}))
	defer srv.Close()
	_, err := New(context.Background(), config.OIDCConfig{IssuerURL: srv.URL, ClientID: "c"})
	if err == nil {
		t.Fatal("expected discovery failure for a broken provider")
	}
	if attempts < 3 {
		t.Fatalf("expected retries, got %d attempts", attempts)
	}
}
