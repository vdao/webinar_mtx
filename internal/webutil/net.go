package webutil

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ClientIP extracts the real client IP from a request, honoring a single
// trusted X-Forwarded-For entry (the proxy adds the client address).
func ClientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// SafeQuery returns an escaped query parameter value (for redirects).
func SafeQuery(s string) string { return url.QueryEscape(s) }

// Esc returns an HTML-escaped string.
func Esc(s string) string { return html.EscapeString(s) }
