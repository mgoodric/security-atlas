package oidc

import "testing"

// #1944: only an explicit false (bool or string) reads as unverified; an
// absent or malformed claim is unknown (nil).
func TestParseEmailVerified(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
		want *bool
	}{
		{"absent", nil, nil},
		{"bool true", true, ptr(true)},
		{"bool false", false, ptr(false)},
		{"string true", "true", ptr(true)},
		{"string false", "false", ptr(false)},
		{"garbage string", "yes please", nil},
		{"number", float64(1), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseEmailVerified(tc.in)
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil || *got != *tc.want:
				t.Errorf("parseEmailVerified(%v) = %v, want %v", tc.in, deref(got), deref(tc.want))
			}
		})
	}
}

func ptr(b bool) *bool { return &b }

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
