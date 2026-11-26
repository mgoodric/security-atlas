package control

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReanchorManifest_SetsAnchorAndReleaseKeepsTheRest(t *testing.T) {
	t.Parallel()
	in := `bundle_schema_version: "1"
bundle_id: acme_mfa
title: "MFA everywhere"
scf_anchor_id: IAC-06
implementation_type: manual_attested
`
	out, err := reanchorManifest([]byte(in), "IAC-60", "2025.2")
	if err != nil {
		t.Fatalf("reanchorManifest: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse output: %v\n%s", err, out)
	}
	want := map[string]any{
		"bundle_schema_version": "1", "bundle_id": "acme_mfa", "title": "MFA everywhere",
		"scf_anchor_id": "IAC-60", "scf_release": "2025.2", "implementation_type": "manual_attested",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v; want %#v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("keys = %v; want %d keys", got, len(want))
	}
	// The result is a manifest the upload path accepts.
	if _, err := FinalizeBundleForHTTP(out); err != nil {
		t.Errorf("rewritten manifest does not parse as a bundle: %v", err)
	}
}

func TestReanchorManifest_ReplacesAnExistingRelease(t *testing.T) {
	t.Parallel()
	out, err := reanchorManifest([]byte("scf_anchor_id: GOV-01\nscf_release: \"2025.1\"\n"), "GOV-10", "2025.2")
	if err != nil {
		t.Fatalf("reanchorManifest: %v", err)
	}
	if strings.Count(string(out), "scf_release") != 1 || !strings.Contains(string(out), "scf_release: \"2025.2\"") {
		t.Errorf("output:\n%s", out)
	}
}

func TestReanchorManifest_RejectsNonMapping(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"- a\n- b\n", "just text\n", "{unclosed"} {
		if _, err := reanchorManifest([]byte(in), "X-01", "r"); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

func TestCheckDecider(t *testing.T) {
	t.Parallel()
	if err := checkDecider("  ", ""); !errors.Is(err, ErrRemapDeciderRequired) {
		t.Errorf("blank decider: %v", err)
	}
	if err := checkDecider("u", strings.Repeat("x", maxRemapReason+1)); !errors.Is(err, ErrRemapReasonTooLong) {
		t.Errorf("long reason: %v", err)
	}
	if err := checkDecider("u", strings.Repeat("x", maxRemapReason)); err != nil {
		t.Errorf("max-length reason: %v", err)
	}
}
