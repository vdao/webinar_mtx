// Package auth implements the admin session cookie and a small in-memory
// rate limiter for login code requests.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AdminCookieName is the admin session cookie.
const AdminCookieName = "wm_admin"

// AdminTTL is how long an admin login lasts.
const AdminTTL = 12 * time.Hour

// NewAdminToken returns a signed cookie value valid for AdminTTL.
func NewAdminToken(secret []byte) string {
	expires := time.Now().Add(AdminTTL).Unix()
	return strconv.FormatInt(expires, 10) + "." + sign(secret, expires)
}

// ValidAdminToken verifies a cookie value.
func ValidAdminToken(secret []byte, value string) bool {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return false
	}
	expected := sign(secret, expires)
	return hmac.Equal([]byte(expected), []byte(parts[1]))
}

func sign(secret []byte, expires int64) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("admin:"))
	mac.Write([]byte(strconv.FormatInt(expires, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// SetAdminCookie writes the session cookie.
func SetAdminCookie(w http.ResponseWriter, r *http.Request, secret []byte) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    NewAdminToken(secret),
		Path:     "/admin",
		MaxAge:   int(AdminTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearAdminCookie invalidates the session cookie.
func ClearAdminCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: AdminCookieName, Value: "", Path: "/admin", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// IsAdmin checks the request for a valid admin session.
func IsAdmin(r *http.Request, secret []byte) bool {
	c, err := r.Cookie(AdminCookieName)
	if err != nil {
		return false
	}
	return ValidAdminToken(secret, c.Value)
}

// RoomCookieName returns the per-room participant session cookie name.
func RoomCookieName(slug string) string {
	return "wm_room_" + slug
}

// SetRoomCookie stores the participant token for one room.
func SetRoomCookie(w http.ResponseWriter, r *http.Request, slug, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     RoomCookieName(slug),
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearRoomCookie drops the participant token.
func ClearRoomCookie(w http.ResponseWriter, slug string) {
	http.SetCookie(w, &http.Cookie{
		Name: RoomCookieName(slug), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	return r != nil && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https")
}
