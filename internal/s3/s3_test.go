package s3

import (
	"testing"

	"github.com/vdao/webinar_mtx/internal/config"
)

func TestObjectKeyAndPublicURL(t *testing.T) {
	c := &Client{
		bucket:    "webinars",
		prefix:    "recordings",
		publicURL: "https://cdn.example.com/bucket",
	}
	key := c.ObjectKey("abc123", "2026-11-12_10-00-00-000_+0300.mp4")
	want := "recordings/abc123/2026-11-12_10-00-00-000_+0300.mp4"
	if key != want {
		t.Fatalf("ObjectKey = %q, want %q", key, want)
	}
	// path.Base protects against ../ escapes in file names
	if k := c.ObjectKey("id", "../../etc/passwd"); k != "recordings/id/passwd" {
		t.Errorf("ObjectKey traversal = %q", k)
	}
	url := c.PublicURL(key)
	if url != "https://cdn.example.com/bucket/"+want {
		t.Errorf("PublicURL = %q", url)
	}
}

func TestConfigured(t *testing.T) {
	full := config.S3Config{Endpoint: "s3.amazonaws.com", Bucket: "b", AccessKey: "a", SecretKey: "s", PublicURL: "https://x"}
	if !full.Configured() {
		t.Error("full config should be configured")
	}
	for _, mutate := range []func(*config.S3Config){
		func(c *config.S3Config) { c.Endpoint = "" },
		func(c *config.S3Config) { c.Bucket = "" },
		func(c *config.S3Config) { c.AccessKey = "" },
		func(c *config.S3Config) { c.SecretKey = "" },
		func(c *config.S3Config) { c.PublicURL = "" },
	} {
		c := full
		mutate(&c)
		if c.Configured() {
			t.Error("partial config must not be considered configured")
		}
	}
}
