package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestPositiveIntEnv: unset falls back silently; a valid value wins; a
// malformed or non-positive value falls back with a Warn naming the key.
func TestPositiveIntEnv(t *testing.T) {
	const key = "ATLAS_TEST_POSITIVE_INT_ENV"
	cases := []struct {
		name, value string
		want        int
		warn        bool
	}{
		{"unset", "", 7, false},
		{"valid", "42", 42, false},
		{"malformed", "lots", 7, true},
		{"zero", "0", 7, true},
		{"negative", "-3", 7, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(key, c.value)
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			if got := positiveIntEnv(logger, key, 7); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
			if warned := strings.Contains(buf.String(), key); warned != c.warn {
				t.Fatalf("warned = %v, want %v (log: %q)", warned, c.warn, buf.String())
			}
		})
	}
}
