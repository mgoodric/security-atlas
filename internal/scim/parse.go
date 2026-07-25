package scim

import (
	"encoding/json"
	"strings"
)

// normalizeOp lowercases + trims a PatchOp `op` value. RFC 7644 §3.5.2 op
// values are case-insensitive in practice across IdPs (Okta sends
// "replace", Entra sometimes "Replace").
func normalizeOp(op string) string {
	return strings.ToLower(strings.TrimSpace(op))
}

// normalizePath lowercases a PatchOp path / attribute key and strips a SCIM
// schema-URN prefix if the IdP fully-qualified the path (e.g.
// "urn:...:User:active" → "active"). Used to compare against the allow-list.
func normalizePath(path string) string {
	p := strings.TrimSpace(path)
	if i := strings.LastIndex(p, ":"); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToLower(p)
}

// decodeBool reads a JSON value that may be a bare boolean (`true`) or a
// quoted boolean string (`"true"` — some IdPs stringify). Returns (false,
// false) when the value is neither.
func decodeBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// decodeString reads a JSON string value. Returns ("", false) when the value
// is not a string.
func decodeString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
