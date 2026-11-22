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
	const bound, routedKind = "sast.scan_result.v1", "github.repo_protection.v2"
	cases := []struct {
		name    string
		kind    string
		control string
		linked  string
		want    string // "" for silence, else the prefix of the stderr line
	}{
		{"bundle id unlinked", bound, "acme_mfa_reqd", "", "warning:"},
		{"bundle id linked", bound, "acme_mfa_required", ctrlUUID, ""},
		{"uuid control", bound, ctrlUUID, ctrlUUID, ""},
		{"uuid control without linkage", bound, ctrlUUID, "", ""},
		// #2931: a routed kind never links, so an unlinked receipt is not a
		// mistyped bundle ID. Naming a control gets a note that it is
		// provenance only; omitting it stays quiet.
		{"routed kind with bundle id", routedKind, "soc2_cc8_1_change_management", "", "note:"},
		{"routed kind with uuid", routedKind, ctrlUUID, "", "note:"},
		{"routed kind without control", routedKind, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnUnlinked(&buf, tc.kind, tc.control, &evidencev1.EvidenceReceipt{LinkedControlId: tc.linked})
			got := buf.String()
			if tc.want == "" {
				if got != "" {
					t.Fatalf("want no output, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, tc.want) || !strings.Contains(got, tc.control) {
				t.Fatalf("want a %q line naming %q, got %q", tc.want, tc.control, got)
			}
			if tc.want == "note:" && strings.Contains(got, "warning") {
				t.Fatalf("routed kind must not warn, got %q", got)
			}
		})
	}
}

// #2931: --control is required for a bound kind and optional for a routed
// one, which reaches its controls by evidence type.
func TestValidatePushFlagsControlRequiredOnlyForBoundKinds(t *testing.T) {
	base := func(kind string) evidencePushFlags {
		return evidencePushFlags{
			kind: kind, scopeJSON: "{}", observedAt: "2026-11-22T00:00:00Z",
			resultStr: "pass", payloadRef: "{}", idempotencyKey: "k", actorID: "a",
		}
	}
	f := base("sast.scan_result.v1")
	if err := validatePushFlags(&f); err == nil || !strings.Contains(err.Error(), "--control") {
		t.Fatalf("bound kind without --control: err = %v, want one naming --control", err)
	}
	f = base("github.repo_protection.v2")
	if err := validatePushFlags(&f); err != nil {
		t.Fatalf("routed kind without --control: err = %v, want nil", err)
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
