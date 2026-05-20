// Slice 139 — unit tests for the vendor-export email-masking helper.
//
// Per P0-A11 the helper MUST handle every input shape without
// panicking. The table below enumerates the documented edge cases.

package adminvendors

import (
	"strings"
	"testing"
)

func TestMaskEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Slice 139 D1 happy path — strip local-part, keep domain.
		{
			name: "standard email",
			in:   "alice@example.com",
			want: "*@example.com",
		},
		{
			name: "long subdomain",
			in:   "matt+ops@grc.security-atlas.example.com",
			want: "*@grc.security-atlas.example.com",
		},
		// Empty / no-`@` — return empty (P0-A11 + slice 139 D1 docs).
		// Returning empty rather than echoing the input is the
		// no-leak choice: if a non-email identifier somehow reached
		// the cell, we don't want it on the wire either.
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "no @ symbol",
			in:   "just-a-username",
			want: "",
		},
		{
			name: "no @ symbol — UUID-shaped",
			in:   "550e8400-e29b-41d4-a716-446655440000",
			want: "",
		},
		// Multiple-@ — use the LAST `@`, never panic.
		{
			name: "multiple @ symbols — last wins",
			in:   "weird@thing@example.com",
			want: "*@example.com",
		},
		{
			name: "three @ symbols",
			in:   "a@b@c@final.tld",
			want: "*@final.tld",
		},
		// Trailing `@` — no domain to surface, return empty.
		{
			name: "trailing @ — no domain",
			in:   "alice@",
			want: "",
		},
		// Leading `@` — empty local-part, domain still leaks safely
		// as `*@domain` (the `*` is the same masked-marker we use for
		// non-empty local-parts, so the wire shape is stable).
		{
			name: "leading @ — empty local-part",
			in:   "@example.com",
			want: "*@example.com",
		},
		// Domain-only nuances — single-label domains are valid (e.g.
		// `localhost`), and we don't try to validate.
		{
			name: "single-label domain",
			in:   "ops@localhost",
			want: "*@localhost",
		},
		// Whitespace within domain is preserved verbatim — we are
		// not the validator. The CSV encoder's OWASP injection
		// sanitizer handles any leading-formula concern at a
		// different layer.
		{
			name: "whitespace inside domain",
			in:   "bob@weird domain.tld",
			want: "*@weird domain.tld",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := MaskEmail(tc.in)
			if got != tc.want {
				t.Errorf("MaskEmail(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMaskEmailNeverLeaksLocalPart is the constitutional check: for
// every standard-shape email input, the local-part MUST NOT appear in
// the output. Independent of the canonical shape — a guard against
// regressions that accidentally emit the local-part anywhere in the
// result.
func TestMaskEmailNeverLeaksLocalPart(t *testing.T) {
	inputs := []struct {
		in        string
		localPart string
	}{
		{"alice@example.com", "alice"},
		{"matt+ops@grc.security-atlas.example.com", "matt+ops"},
		{"VeryLongLocalPart123_with-stuff@tenant.example", "VeryLongLocalPart123_with-stuff"},
		{"a.b.c@x.y.z", "a.b.c"},
	}
	for _, tc := range inputs {
		out := MaskEmail(tc.in)
		// out must not contain the local-part anywhere.
		if out == tc.localPart || containsLocalPart(out, tc.localPart) {
			t.Errorf("MaskEmail(%q) = %q leaks local-part %q", tc.in, out, tc.localPart)
		}
	}
}

func containsLocalPart(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestMaskEmailDoesNotPanic asserts the panic-free guarantee
// (P0-A11) across a few intentionally weird inputs. If any future
// refactor introduces an out-of-bounds index, this test catches it.
func TestMaskEmailDoesNotPanic(t *testing.T) {
	inputs := []string{
		"",
		"@",
		"@@",
		"@@@",
		"@@@@",
		"a@",
		"@b",
		"a@b",
		"a@b@",
		"@a@b",
		strings.Repeat("@", 1000),
	}
	for _, in := range inputs {
		// Calling MaskEmail must not panic. If it does, the test
		// process crashes; if it doesn't, we just verify the call
		// returns something (a panic would never reach this line).
		_ = MaskEmail(in)
	}
}
