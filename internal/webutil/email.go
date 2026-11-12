package webutil

import "net/mail"

// NormalizeEmail trims and lowercases an email address.
func NormalizeEmail(s string) string {
	return trimLower(s)
}

// ValidEmail reports whether s is a plausible email address: parseable by
// net/mail and with a dotted domain (catches common typos like "a@b").
func ValidEmail(s string) bool {
	if len(s) == 0 || len(s) > 254 {
		return false
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	at := lastIndexByte(a.Address, '@')
	if at < 0 {
		return false
	}
	domain := a.Address[at+1:]
	return containsByte(domain, '.') && !hasSpace(domain)
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func containsByte(s string, b byte) bool { return lastIndexByte(s, b) >= 0 }

func hasSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return true
		}
	}
	return false
}
