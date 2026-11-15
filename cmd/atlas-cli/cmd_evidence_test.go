package main

import (
	"bytes"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// #2165: a non-UUID --control that linked to no control warns on stderr; a
// linked push, or a UUID --control, stays quiet.
func TestWarnUnlinked(t *testing.T) {
	const ctrlUUID = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	cases := []struct {
		name     string
		control  string
		linked   string
		wantWarn bool
	}{
		{"bundle id unlinked", "acme_mfa_reqd", "", true},
		{"bundle id linked", "acme_mfa_required", ctrlUUID, false},
		{"uuid control", ctrlUUID, ctrlUUID, false},
		{"uuid control without linkage", ctrlUUID, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnUnlinked(&buf, tc.control, &evidencev1.EvidenceReceipt{LinkedControlId: tc.linked})
			got := buf.String()
			if tc.wantWarn {
				if !strings.Contains(got, "warning:") || !strings.Contains(got, tc.control) {
					t.Fatalf("want a warning naming %q, got %q", tc.control, got)
				}
				return
			}
			if got != "" {
				t.Fatalf("want no warning, got %q", got)
			}
		})
	}
}

// #2165: --insecure is loopback-only for the admin client too, not just the
// SDK push client. The host is parsed, so a look-alike is refused.
func TestNewAdminClientInsecureLoopbackOnly(t *testing.T) {
	t.Cleanup(func() {
		common.endpoint = ""
		common.insecure = false
	})
	common.insecure = true

	for _, ep := range []string{"localhost:50051", "127.0.0.1:50051", "127.3.4.5:50051", "[::1]:50051"} {
		common.endpoint = ep
		_, conn, err := newAdminClient()
		if err != nil {
			t.Fatalf("newAdminClient(%q): unexpected error %v", ep, err)
		}
		_ = conn.Close()
	}

	for _, ep := range []string{"localhost.evil.com:50051", "atlas.example.test:50051", "203.0.113.10:50051", "[2001:db8::1]:50051"} {
		common.endpoint = ep
		_, conn, err := newAdminClient()
		if err == nil {
			_ = conn.Close()
			t.Fatalf("newAdminClient(%q): want loopback refusal, got nil", ep)
		}
		if !strings.Contains(err.Error(), "non-loopback") {
			t.Fatalf("newAdminClient(%q): error %q does not name the loopback rule", ep, err)
		}
	}

	// Without --insecure the TLS path takes any host.
	common.insecure = false
	common.endpoint = "atlas.example.test:443"
	_, conn, err := newAdminClient()
	if err != nil {
		t.Fatalf("TLS newAdminClient: %v", err)
	}
	_ = conn.Close()
}
