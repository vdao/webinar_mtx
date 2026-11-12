// Package webutil holds small shared helpers.
package webutil

import (
	"strconv"
	"strings"
)

// GetPath extracts a value from a decoded JSON payload (map[string]any /
// []any) by a dot-separated path, e.g. "object.metadata.email" or
// "items.0.price". Returns ("", false) when any segment is missing.
func GetPath(payload any, path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	var cur any = payload
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return "", false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(v) {
				return "", false
			}
			cur = v[idx]
		default:
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(v), true
	case nil:
		return "", false
	default:
		return "", false
	}
}
