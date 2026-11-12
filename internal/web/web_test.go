package web

import (
	"testing"

	"github.com/vdao/webinar_mtx/internal/config"
)

// TestTemplatesParse ensures every embedded template compiles at startup.
func TestTemplatesParse(t *testing.T) {
	s := &Server{Cfg: &config.Config{}}
	if err := s.parseTemplates(); err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, page := range []string{
		"admin_login.html", "admin_webinars.html", "admin_webinar_edit.html",
		"admin_participants.html", "admin_webhooks.html", "admin_webhook_edit.html",
		"room_join.html", "room_watch.html", "error.html",
	} {
		if s.Tpl.Lookup(page) == nil {
			t.Errorf("template %s not found", page)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	cases := map[string]string{
		"Мой лучший вебинар!": "",
		"Webinar 2026":        "webinar-2026",
		"---":                 "",
		"":                    "",
		"Ok-Slug_42":          "ok-slug-42",
	}
	for in, want := range cases {
		got := uniqueSlug(in)
		if want != "" && got != want {
			t.Errorf("uniqueSlug(%q) = %q, want %q", in, got, want)
		}
		if want == "" && len(got) < 3 {
			t.Errorf("uniqueSlug(%q) = %q, too short", in, got)
		}
	}
	if len(uniqueSlug("")) != 10 {
		t.Errorf("random slug length = %d, want 10", len(uniqueSlug("")))
	}
}

func TestTokenFromQuery(t *testing.T) {
	cases := map[string]string{
		"token=abc":         "abc",
		"a=1&token=xyz&b=2": "xyz",
		"":                  "",
		"stoken=1":          "",
		"token=":            "",
	}
	for q, want := range cases {
		if got := tokenFromQuery(q); got != want {
			t.Errorf("tokenFromQuery(%q) = %q, want %q", q, got, want)
		}
	}
}
