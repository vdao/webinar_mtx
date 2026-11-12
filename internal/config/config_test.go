package config

import (
	"testing"
)

func TestLoadOIDCAndS3(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("ADMIN_EMAIL", "Admin@Example.COM")
	t.Setenv("ADMIN_PASSWORD", "pw")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC.Enabled {
		t.Error("OIDC must be disabled by default")
	}
	if cfg.S3.Configured() {
		t.Error("S3 must not be configured by default")
	}
	if cfg.RecordingsDir != "/recordings" {
		t.Errorf("RecordingsDir = %q", cfg.RecordingsDir)
	}
	// admin email is lowercased
	if cfg.AdminEmail != "admin@example.com" {
		t.Errorf("AdminEmail = %q", cfg.AdminEmail)
	}
	// default admin group
	if cfg.OIDC.AdminGroup != "admin" {
		t.Errorf("default AdminGroup = %q, want admin", cfg.OIDC.AdminGroup)
	}

	t.Setenv("OIDC_ADMIN_GROUP", "webinar-admins")
	t.Setenv("OIDC_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Error("OIDC without issuer/client_id must fail")
	}
	t.Setenv("OIDC_ISSUER_URL", "https://idp.example.com/realm/main/")
	t.Setenv("OIDC_CLIENT_ID", "webinar")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OIDC.Enabled || cfg.OIDC.IssuerURL != "https://idp.example.com/realm/main" {
		t.Errorf("OIDC = %+v", cfg.OIDC)
	}
	if cfg.OIDC.AdminGroup != "webinar-admins" {
		t.Errorf("AdminGroup = %q, want webinar-admins", cfg.OIDC.AdminGroup)
	}
	if want := "http://localhost:8080/admin/oidc/callback"; cfg.OIDC.RedirectURL != want {
		t.Errorf("RedirectURL = %q, want %q", cfg.OIDC.RedirectURL, want)
	}

	// S3 configured flag
	t.Setenv("S3_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("S3_SECURE", "false")
	t.Setenv("S3_BUCKET", "rec")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("S3_PUBLIC_URL", "http://127.0.0.1:9000/rec/")
	t.Setenv("S3_PREFIX", "/recordings/")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.S3.Configured() {
		t.Fatal("S3 should be configured")
	}
	if cfg.S3.Secure {
		t.Error("S3_SECURE=false must disable TLS")
	}
	if cfg.S3.PublicURL != "http://127.0.0.1:9000/rec" {
		t.Errorf("PublicURL = %q (trailing slash must be trimmed)", cfg.S3.PublicURL)
	}
	if cfg.S3.Prefix != "recordings" {
		t.Errorf("Prefix = %q (slashes must be trimmed)", cfg.S3.Prefix)
	}
}
