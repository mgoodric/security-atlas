package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Slice 496 — `controls test` CLI surface. These tests pin the runner's CLI
// contract: a passing bundle exits zero + reports PASS; a bundle whose
// expectation is wrong exits NON-ZERO (AC-5 / P0-496-6); --json emits a
// machine-readable report. No network, no DB (AC-9).

const passingControlYAML = `bundle_schema_version: "1"
bundle_id: cli_pass_test
title: "CLI runner pass bundle"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: all_pass
    language: rego
    expression: |
      package evidence.query
      default result := "fail"
      result := "pass" if {
        count(input.records) > 0
        every r in input.records { r.result == "pass" }
      }
`

const passingCaseYAML = `cases:
  - name: ok
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
`

func writeTestBundle(t *testing.T, control, cases string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "control.yaml"), []byte(control), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "c.yaml"), []byte(cases), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestControlsTest_PassingBundleExitsZero(t *testing.T) {
	dir := writeTestBundle(t, passingControlYAML, passingCaseYAML)
	var out, errb bytes.Buffer
	cmd := newRootCmd()
	cmd.SetArgs([]string{"controls", "test", dir})
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("passing bundle should exit zero, got error: %v (stderr=%s)", err, errb.String())
	}
	if !strings.Contains(out.String(), "PASS  ok") {
		t.Fatalf("expected a PASS line for case ok, got:\n%s", out.String())
	}
}

func TestControlsTest_WrongExpectationExitsNonZero(t *testing.T) {
	// Same query, but the case wrongly expects fail for an all-pass fixture.
	wrongCase := strings.Replace(passingCaseYAML, "expected_state: pass", "expected_state: fail", 1)
	dir := writeTestBundle(t, passingControlYAML, wrongCase)
	var out, errb bytes.Buffer
	cmd := newRootCmd()
	cmd.SetArgs([]string{"controls", "test", dir})
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("AC-5/P0-496-6: a failing test case must make the CLI exit non-zero")
	}
	if !strings.Contains(out.String(), "FAIL  ok") {
		t.Fatalf("expected a FAIL line for case ok, got:\n%s", out.String())
	}
}

func TestControlsTest_JSONOutput(t *testing.T) {
	dir := writeTestBundle(t, passingControlYAML, passingCaseYAML)
	var out, errb bytes.Buffer
	cmd := newRootCmd()
	cmd.SetArgs([]string{"controls", "test", "--json", dir})
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var rep struct {
		BundleID string `json:"bundle_id"`
		Passed   int    `json:"passed"`
		Cases    []struct {
			Name   string `json:"name"`
			Passed bool   `json:"passed"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, out.String())
	}
	if rep.BundleID != "cli_pass_test" || rep.Passed != 1 || len(rep.Cases) != 1 || !rep.Cases[0].Passed {
		t.Fatalf("unexpected JSON report: %+v", rep)
	}
}

func TestControlsTest_NoTestsWarnsButExitsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "control.yaml"), []byte(passingControlYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	cmd := newRootCmd()
	cmd.SetArgs([]string{"controls", "test", dir})
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("a bundle with no tests must exit zero (warning only), got: %v", err)
	}
	if !strings.Contains(errb.String(), "no test cases") {
		t.Fatalf("expected a no-test-cases warning on stderr, got: %q", errb.String())
	}
}
