package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// writeBundle writes a minimal control bundle directory whose control.yaml
// carries the given scf_anchor_id and (when non-empty) scf_release.
func writeBundle(t *testing.T, anchor, release string) string {
	t.Helper()
	dir := t.TempDir()
	y := "bundle_schema_version: \"1\"\n" +
		"bundle_id: release_check\n" +
		"title: Release check\n" +
		"scf_anchor_id: " + anchor + "\n" +
		"implementation_type: manual_attested\n"
	if release != "" {
		y += "scf_release: " + release + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "control.yaml"), []byte(y), 0o600); err != nil {
		t.Fatalf("write control.yaml: %v", err)
	}
	return dir
}

func runValidate(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetArgs(append([]string{"controls", "validate"}, args...))
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("controls validate %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// #1770: validate reports the declared release and flags a code whose
// meaning changed in a later loaded release. GOV-01 is renamed in release
// B; IAC-06 is unchanged.
func TestControlsValidate_ReportsReleaseAndFlagsChangedCode(t *testing.T) {
	catA := scfreleasetest.CatalogPath("scf-release-a.json")
	catB := scfreleasetest.CatalogPath("scf-release-b.json")

	// Catalogs given newest first: ordering is by release_date.
	out := runValidate(t, writeBundle(t, "GOV-01", scfreleasetest.ReleaseA), "--scf-catalog", catB, "--scf-catalog", catA)
	if !strings.Contains(out, "scf_release="+scfreleasetest.ReleaseA+"\n") {
		t.Errorf("declared release not reported:\n%s", out)
	}
	want := `FLAG scf_anchor_id GOV-01 changed in SCF release test-1770-b: renamed (title "Cybersecurity & Data Protection Governance Program" -> "Cybersecurity & Data Privacy Governance Program")`
	if !strings.Contains(out, want) {
		t.Errorf("renamed code not flagged; want %q in:\n%s", want, out)
	}

	out = runValidate(t, writeBundle(t, "IAC-06", scfreleasetest.ReleaseA), "--scf-catalog", catA, "--scf-catalog", catB)
	if strings.Contains(out, "FLAG") {
		t.Errorf("unchanged IAC-06 flagged:\n%s", out)
	}

	for code, kind := range map[string]string{"CRY-03": "split", "AST-04": "reused", "AST-09": "renumbered", "PRM-07": "withdrawn"} {
		out = runValidate(t, writeBundle(t, code, scfreleasetest.ReleaseA), "--scf-catalog", catA, "--scf-catalog", catB)
		if !strings.Contains(out, "FLAG scf_anchor_id "+code+" changed in SCF release test-1770-b: "+kind) {
			t.Errorf("%s not flagged as %s:\n%s", code, kind, out)
		}
	}

	// Declaring the later release: nothing after it to compare against.
	out = runValidate(t, writeBundle(t, "GOV-01", scfreleasetest.ReleaseB), "--scf-catalog", catA, "--scf-catalog", catB)
	if strings.Contains(out, "FLAG") {
		t.Errorf("code flagged against an earlier release:\n%s", out)
	}
}

func TestControlsValidate_ReleaseWarnings(t *testing.T) {
	catA := scfreleasetest.CatalogPath("scf-release-a.json")

	out := runValidate(t, writeBundle(t, "IAC-06", ""))
	if !strings.Contains(out, "scf_release=(none) WARN") {
		t.Errorf("missing release not warned:\n%s", out)
	}
	out = runValidate(t, writeBundle(t, "IAC-06", "nope"), "--scf-catalog", catA)
	if !strings.Contains(out, "WARN scf_release nope is not among the --scf-catalog releases") {
		t.Errorf("unknown release not warned:\n%s", out)
	}
	out = runValidate(t, writeBundle(t, "CRY-03.1", scfreleasetest.ReleaseA), "--scf-catalog", catA)
	if !strings.Contains(out, "WARN scf_anchor_id CRY-03.1 is not in SCF release test-1770-a") {
		t.Errorf("code missing from release not warned:\n%s", out)
	}
}

// #2818: a query with no evidence_kind is warned on, and validation passes.
func TestControlsValidate_WarnsOnQueryWithoutEvidenceKind(t *testing.T) {
	dir := writeBundle(t, "IAC-06", "2025.2")
	path := filepath.Join(dir, "control.yaml")
	y, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	y = append(y, []byte("evidence_queries:\n"+
		"  - id: kinded\n    language: jsonpath\n    evidence_kind: okta.mfa_policy.v1\n    expression: \"$.a\"\n"+
		"  - id: kindless\n    language: jsonpath\n    expression: \"$.b\"\n")...)
	if err := os.WriteFile(path, y, 0o600); err != nil {
		t.Fatal(err)
	}
	out := runValidate(t, dir)
	if !strings.Contains(out, "WARN evidence_queries[1] (kindless) declares no evidence_kind") {
		t.Errorf("kindless query not warned:\n%s", out)
	}
	if strings.Contains(out, "(kinded)") {
		t.Errorf("kinded query warned:\n%s", out)
	}
}
