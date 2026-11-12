// Package oidc implements admin login via an OIDC provider
// (Authorization Code flow with PKCE).
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/vdao/webinar_mtx/internal/config"
)

// Manager drives the OIDC login flow.
type Manager struct {
	cfg      config.OIDCConfig
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// New discovers the provider (fail-fast with a few retries, the IdP may boot
// slower than the app in compose).
func New(ctx context.Context, cfg config.OIDCConfig) (*Manager, error) {
	var provider *oidc.Provider
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		provider, err = oidc.NewProvider(ctx, cfg.IssuerURL)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", cfg.IssuerURL, err)
	}
	return &Manager{
		cfg:      cfg,
		provider: provider,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

// AuthRequest is everything needed to start and finish one login attempt.
type AuthRequest struct {
	State string
	Nonce string
	PKCE  string
}

// Begin generates state/nonce/PKCE and the provider authorization URL.
func (m *Manager) Begin() (AuthRequest, string, error) {
	req := AuthRequest{
		State: randomToken(),
		Nonce: randomToken(),
		PKCE:  randomToken(),
	}
	url := m.oauth.AuthCodeURL(req.State,
		oauth2.SetAuthURLParam("nonce", req.Nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(req.PKCE)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return req, url, nil
}

// Identity is the verified user info extracted from the ID token.
type Identity struct {
	Email  string   // best-effort, for display and logs
	Groups []string // claim "groups": admin access is granted by group membership
}

// Complete exchanges the code, verifies the ID token and returns the
// authenticated identity (email + groups claims).
func (m *Manager) Complete(ctx context.Context, req AuthRequest, code string) (Identity, error) {
	var ident Identity
	tok, err := m.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", req.PKCE))
	if err != nil {
		return ident, fmt.Errorf("code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return ident, fmt.Errorf("no id_token in the token response")
	}
	idToken, err := m.verifier.Verify(ctx, rawID)
	if err != nil {
		return ident, fmt.Errorf("verify id_token: %w", err)
	}
	if idToken.Nonce != req.Nonce {
		return ident, fmt.Errorf("nonce mismatch")
	}
	var claims struct {
		Email             string   `json:"email"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return ident, err
	}
	ident.Groups = claims.Groups
	ident.Email = strings.ToLower(strings.TrimSpace(claims.Email))
	if ident.Email == "" {
		ident.Email = strings.ToLower(strings.TrimSpace(claims.PreferredUsername))
	}
	return ident, nil
}

func randomToken() string {
	buf := make([]byte, 32)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func pkceChallenge(verifier string) string {
	sum := sha256Sum(verifier)
	return base64.RawURLEncoding.EncodeToString(sum)
}
