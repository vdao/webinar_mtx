package webutil

import (
	"encoding/json"
	"testing"
)

func TestGetPath(t *testing.T) {
	var payload any
	if err := json.Unmarshal([]byte(`{
		"object": {
			"metadata": {"email": "a@b.com", "product": "pro"},
			"paid": true,
			"amount": 1490.5,
			"count": 42
		},
		"items": [{"price": {"code": "x1"}}, {"price": {"code": "x2"}}]
	}`), &payload); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"object.metadata.email", "a@b.com", true},
		{"object.metadata.product", "pro", true},
		{"object.paid", "true", true},
		{"object.amount", "1490.5", true},
		{"object.count", "42", true},
		{"items.0.price.code", "x1", true},
		{"items.1.price.code", "x2", true},
		{"items.2.price.code", "", false},
		{"object.missing", "", false},
		{"object.metadata.email.deep", "", false},
		{"", "", false},
		{"items.x", "", false},
	}
	for _, c := range cases {
		got, ok := GetPath(payload, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("GetPath(%q) = %q,%v want %q,%v", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeAndValidateEmail(t *testing.T) {
	if NormalizeEmail("  User@Example.COM ") != "user@example.com" {
		t.Error("normalize failed")
	}
	valid := []string{"a@b.co", "first.last+tag@sub.domain.org"}
	invalid := []string{"", "no-at", "a@b", "@b.com", "a b@c.com", "a@b.c@d.com"}
	for _, e := range valid {
		if !ValidEmail(e) {
			t.Errorf("%q should be valid", e)
		}
	}
	for _, e := range invalid {
		if ValidEmail(e) {
			t.Errorf("%q should be invalid", e)
		}
	}
}
