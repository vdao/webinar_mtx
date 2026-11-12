package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/vdao/webinar_mtx/internal/auth"
	"github.com/vdao/webinar_mtx/internal/oidc"
)

// oidcCookieName carries the in-flight login attempt (state/nonce/PKCE).
const oidcCookieName = "wm_oidc"

// oidcAuth is the cookie payload for one login attempt.
type oidcAuth struct {
	State string `json:"state"`
	Nonce string `json:"nonce"`
	PKCE  string `json:"pkce"`
}

func signOIDC(secret []byte, a oidcAuth) (string, error) {
	payload, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyOIDC(secret []byte, cookie string) (oidcAuth, bool) {
	var a oidcAuth
	enc, sig, ok := cutLast(cookie, '.')
	if !ok {
		return a, false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return a, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return a, false
	}
	if json.Unmarshal(payload, &a) != nil {
		return a, false
	}
	return a, true
}

func cutLast(s string, sep byte) (string, string, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// adminOIDCStart redirects the admin to the OIDC provider.
func (s *Server) adminOIDCStart(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	req, url, err := s.OIDC.Begin()
	if err != nil {
		s.renderError(w, r, 500, "Не удалось начать авторизацию")
		return
	}
	value, err := signOIDC(s.Cfg.AppSecret, oidcAuth{State: req.State, Nonce: req.Nonce, PKCE: req.PKCE})
	if err != nil {
		s.renderError(w, r, 500, "Внутренняя ошибка")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookieName,
		Value:    value,
		Path:     "/admin",
		MaxAge:   int((5 * time.Minute).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// adminOIDCCallback finishes the OIDC login and issues the admin session.
func (s *Server) adminOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(oidcCookieName)
	if err != nil || c.Value == "" {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	stored, ok := verifyOIDC(s.Cfg.AppSecret, c.Value)
	if !ok {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	if state := r.URL.Query().Get("state"); state == "" || state != stored.State {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	if errStr := r.URL.Query().Get("error"); errStr != "" {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}

	// Clear the one-time login cookie regardless of the outcome.
	http.SetCookie(w, &http.Cookie{Name: oidcCookieName, Value: "", Path: "/admin", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode})

	email, err := s.OIDC.Complete(r.Context(), oidc.AuthRequest{
		State: stored.State, Nonce: stored.Nonce, PKCE: stored.PKCE,
	}, code)
	if err != nil {
		flashRedirect(w, r, "/admin/login", "badlogin", true)
		return
	}
	if !adminAllowed(s.Cfg.AdminEmails, email) {
		s.renderError(w, r, http.StatusForbidden,
			"Пользователь "+email+" не имеет доступа к админке.")
		return
	}
	auth.SetAdminCookie(w, r, s.Cfg.AppSecret)
	http.Redirect(w, r, "/admin/webinars", http.StatusSeeOther)
}

func adminAllowed(allowlist []string, email string) bool {
	for _, e := range allowlist {
		if e == email {
			return true
		}
	}
	return false
}
