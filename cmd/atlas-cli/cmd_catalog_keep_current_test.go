package main

import (
	"bytes"
	"strings"
	"testing"
)

// #3061: deploy/docker/bootstrap/bootstrap.sh loads the bundled sample with
// `atlas-cli catalog import-scf --keep-current` on an install whose
// SCF_CATALOG is its own release. Without the flag declared, that line fails
// with "unknown flag" and the bootstrap stops before the crosswalk.
func TestCatalogImportSCF_KeepCurrentFlagParses(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{
		"catalog", "import-scf", "--keep-current",
		"--dsn", "postgres://unused@127.0.0.1:1/unused",
		"/nonexistent/scf.json",
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	// The catalog load fails (the path does not exist), after flag parsing.
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error (catalog path is bogus), got nil")
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("--keep-current not declared: %v", err)
	}
	if !strings.Contains(err.Error(), "/nonexistent/scf.json") {
		t.Errorf("err = %v; want the catalog read failure", err)
	}
}
