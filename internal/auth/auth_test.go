package auth

import (
	"testing"
	"time"
)

func TestAdminToken(t *testing.T) {
	secret := []byte("s3cret")
	tok := NewAdminToken(secret)
	if !ValidAdminToken(secret, tok) {
		t.Error("valid token rejected")
	}
	if ValidAdminToken([]byte("other"), tok) {
		t.Error("token accepted with wrong secret")
	}
	if ValidAdminToken(secret, tok+"x") {
		t.Error("tampered token accepted")
	}
	if ValidAdminToken(secret, "1234567890.deadbeef") {
		t.Error("garbage token accepted")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	if !l.Allow("k") || !l.Allow("k") {
		t.Fatal("first two events should pass")
	}
	if l.Allow("k") {
		t.Error("third event should be rate limited")
	}
	if !l.Allow("other") {
		t.Error("other key should pass")
	}

	l2 := NewLimiter(100, time.Minute)
	if !l2.AllowAtMostOncePer("ip", 30*time.Second) {
		t.Error("first should pass")
	}
	if l2.AllowAtMostOncePer("ip", 30*time.Second) {
		t.Error("second within interval should be blocked")
	}
}
