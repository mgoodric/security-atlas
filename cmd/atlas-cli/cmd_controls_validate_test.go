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

// runValidateErr runs controls validate and returns its output and error.
func runValidateErr(args ...string) (string, error) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetArgs(append([]string{"controls", "validate"}, args...))
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return out.String(), err
}

// writeBundleUnder writes <parent>/<name>/control.yaml with the given
// scf_anchor_id (omitted when empty, as anchor_fit: none) and scf_release
// (omitted when empty).
func writeBundleUnder(t *testing.T, parent, name, anchor, release string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	y := "bundle_schema_version: \"1\"\n" +
		"bundle_id: " + name + "\n" +
		"title: " + name + "\n" +
		"implementation_type: manual_attested\n"
	if anchor == "" {
		y += "anchor_fit: none\nanchor_fit_note: no SCF control fits\n"
	} else {
		y += "scf_anchor_id: " + anchor + "\n"
	}
	if release != "" {
		y += "scf_release: " + release + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "control.yaml"), []byte(y), 0o600); err != nil {
		t.Fatalf("write control.yaml: %v", err)
	}
	return dir
}

// #1774: validate fails a bundle whose scf_anchor_id code has no
// scf_release, with the fix in the message.
func TestControlsValidate_MissingReleaseFails(t *testing.T) {
	dir := writeBundle(t, "IAC-06", "")
	out, err := runValidateErr(dir)
	if err == nil {
		t.Fatalf("validate passed a bundle with no scf_release:\n%s", out)
	}
	for _, want := range []string{"declare no scf_release", "scf_release: test-2026.3", dir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
	if !strings.Contains(out, "scf_release=(none) ERROR") {
		t.Errorf("bundle report does not flag the missing release:\n%s", out)
	}
}

// #1774: over a directory, validate lists every bundle missing scf_release
// in one run, not just the first. A bundle declaring one, one naming its
// anchor by UUID and an unanchored one need none.
func TestControlsValidate_DirectoryListsEveryBundleMissingRelease(t *testing.T) {
	parent := t.TempDir()
	missingA := writeBundleUnder(t, parent, "a_missing", "IAC-06", "")
	_ = writeBundleUnder(t, parent, "b_declared", "IAC-06", scfreleasetest.ReleaseA)
	missingC := writeBundleUnder(t, parent, "c_missing", "GOV-01", "")
	_ = writeBundleUnder(t, parent, "d_uuid", "6f1c2a7e-0000-4000-8000-0000000017a4", "")
	_ = writeBundleUnder(t, parent, "e_unanchored", "", "")

	out, err := runValidateErr(parent)
	if err == nil {
		t.Fatalf("validate passed a directory with bundles missing scf_release:\n%s", out)
	}
	if !strings.Contains(err.Error(), "2 bundle(s)") {
		t.Errorf("error %q does not count 2 bundles", err)
	}
	for _, want := range []string{missingA, missingC} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not list %s", err, want)
		}
	}
	for _, ok := range []string{"b_declared", "d_uuid", "e_unanchored"} {
		if strings.Contains(err.Error(), ok) {
			t.Errorf("error %q lists %s, which needs no scf_release", err, ok)
		}
	}
	// Every bundle was still validated and reported.
	if n := strings.Count(out, "OK bundle_id="); n != 5 {
		t.Errorf("reported %d bundles; want 5:\n%s", n, out)
	}
}

func TestControlsValidate_ReleaseWarnings(t *testing.T) {
	catA := scfreleasetest.CatalogPath("scf-release-a.json")

	out := runValidate(t, writeBundle(t, "IAC-06", "nope"), "--scf-catalog", catA)
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

// #2882: a fixture record without evidence_kind, in a bundle whose queries
// read two kinds, reaches both queries in the test and neither live.
func TestControlsValidate_WarnsOnKindlessFixtureRecordInTwoKindBundle(t *testing.T) {
	dir := writeBundle(t, "IAC-06", "2025.2")
	path := filepath.Join(dir, "control.yaml")
	y, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	y = append(y, []byte("evidence_queries:\n"+
		"  - id: one\n    language: jsonpath\n    evidence_kind: okta.mfa_policy.v1\n    expression: \"$.a\"\n"+
		"  - id: two\n    language: jsonpath\n    evidence_kind: manual.upload.v1\n    expression: \"$.b\"\n")...)
	if err := os.WriteFile(path, y, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := "cases:\n" +
		"  - name: kinded\n    expected_state: pass\n    records:\n      - result: pass\n        evidence_kind: manual.upload.v1\n" +
		"  - name: kindless\n    expected_state: pass\n    records:\n      - result: pass\n        evidence_kind: manual.upload.v1\n      - result: pass\n"
	if err := os.WriteFile(filepath.Join(dir, "tests", "t.yaml"), []byte(cases), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runValidate(t, dir)
	if !strings.Contains(out, `WARN test case "kindless": records[1] omit evidence_kind`) {
		t.Errorf("kindless fixture record not warned:\n%s", out)
	}
	if strings.Contains(out, `"kinded"`) {
		t.Errorf("kinded case warned:\n%s", out)
	}
}
