// Package config loads application settings from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// NotifyConfig holds the outgoing registration-notification webhook settings.
type NotifyConfig struct {
	WebhookURL    string // POST target for registration events; empty disables notifications
	WebhookSecret string // shared secret for the X-Webhook-Signature header (optional)
}

// OIDCConfig holds settings for protecting the admin panel with OIDC SSO.
type OIDCConfig struct {
	Enabled      bool
	IssuerURL    string // e.g. https://keycloak.example.com/realms/main
	ClientID     string
	ClientSecret string
	RedirectURL  string // defaults to {APP_URL}/admin/oidc/callback
	AdminGroup   string // claim "groups" entry that grants admin access; default "admin"
}

// S3Config holds settings of an S3-compatible storage for webinar recordings.
type S3Config struct {
	Endpoint  string // host[:port], e.g. s3.amazonaws.com or storage.yandexcloud.net
	Secure    bool   // use TLS to talk to the endpoint
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	PublicURL string // public base URL of the bucket, used to build download links
	Prefix    string // key prefix inside the bucket, e.g. "recordings"
}

// Configured reports whether recording publishing is set up.
func (s S3Config) Configured() bool {
	return s.Endpoint != "" && s.Bucket != "" && s.AccessKey != "" && s.SecretKey != "" && s.PublicURL != ""
}

// Config is the full application configuration.
type Config struct {
	Listen        string // HTTP listen address
	AppURL        string // public base URL of the app (used for links in notifications and admin)
	DatabaseURL   string
	AdminEmail    string // login for the password admin login (when OIDC is off)
	AdminPassword string
	AppSecret     []byte        // HMAC key for signed cookies
	SessionTTL    time.Duration // participant session lifetime
	Notify        NotifyConfig
	OIDC          OIDCConfig

	MtxAPIURL     string // MediaMTX REST API, e.g. http://mediamtx:9997
	MtxWHEPURL    string // MediaMTX WebRTC/WHEP base URL, e.g. http://mediamtx:8889
	RTMPURL       string // public RTMP ingest URL shown to the host, e.g. rtmp://host:1935/w
	RecordingsDir string // where MediaMTX recording files are visible to the app

	S3 S3Config
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		Listen:        getenv("LISTEN", ":8080"),
		AppURL:        strings.TrimRight(getenv("APP_URL", "http://localhost:8080"), "/"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AdminEmail:    strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		SessionTTL:    getduration("SESSION_TTL", 12*time.Hour),
		Notify: NotifyConfig{
			WebhookURL:    strings.TrimSpace(os.Getenv("NOTIFY_WEBHOOK_URL")),
			WebhookSecret: os.Getenv("NOTIFY_WEBHOOK_SECRET"),
		},
		OIDC: OIDCConfig{
			Enabled:      strings.EqualFold(getenv("OIDC_ENABLED", "false"), "true"),
			IssuerURL:    strings.TrimRight(getenv("OIDC_ISSUER_URL", ""), "/"),
			ClientID:     os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURL:  getenv("OIDC_REDIRECT_URL", strings.TrimRight(getenv("APP_URL", "http://localhost:8080"), "/")+"/admin/oidc/callback"),
			AdminGroup:   strings.TrimSpace(getenv("OIDC_ADMIN_GROUP", "admin")),
		},
		MtxAPIURL:     getenv("MTX_API_URL", "http://mediamtx:9997"),
		MtxWHEPURL:    getenv("MTX_WHEP_URL", "http://mediamtx:8889"),
		RTMPURL:       getenv("RTMP_URL", "rtmp://localhost:1935/w"),
		RecordingsDir: getenv("RECORDINGS_DIR", "/recordings"),
		S3: S3Config{
			Endpoint:  getenv("S3_ENDPOINT", ""),
			Secure:    !strings.EqualFold(getenv("S3_SECURE", "true"), "false"),
			Region:    getenv("S3_REGION", ""),
			Bucket:    os.Getenv("S3_BUCKET"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"),
			PublicURL: strings.TrimRight(getenv("S3_PUBLIC_URL", ""), "/"),
			Prefix:    strings.Trim(getenv("S3_PREFIX", "recordings"), "/"),
		},
	}
	if c.OIDC.Enabled {
		if c.OIDC.IssuerURL == "" || c.OIDC.ClientID == "" {
			return nil, fmt.Errorf("OIDC_ENABLED=true requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
		}
	}
	if c.OIDC.AdminGroup == "" {
		c.OIDC.AdminGroup = "admin"
	}
	if c.S3.Prefix == "" {
		c.S3.Prefix = "recordings"
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.AdminEmail == "" || c.AdminPassword == "" {
		return nil, fmt.Errorf("ADMIN_EMAIL and ADMIN_PASSWORD are required")
	}

	if s := strings.TrimSpace(os.Getenv("APP_SECRET")); s != "" {
		c.AppSecret = []byte(s)
	} else {
		// Generate a random secret: sessions and admin login survive until restart only.
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate APP_SECRET: %w", err)
		}
		c.AppSecret = buf
		log.Println("warning: APP_SECRET is not set, generated a temporary one; " +
			"all sessions will be dropped on restart. Set APP_SECRET in production")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("warning: invalid %s=%q, using %d", key, v, def)
	}
	return def
}

func getduration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Printf("warning: invalid %s=%q, using %s", key, v, def)
	}
	return def
}

// RandomHex returns n random bytes hex-encoded.
func RandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(buf)
}
