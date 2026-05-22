package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestOAuthIssueClient_RequiresName covers the cobra Args check that
// the command rejects a bare invocation. The DATABASE_URL env-var
// path is exercised in the integration test (requires a real
// Postgres); here we only verify the argument-validation surface.
func TestOAuthIssueClient_RequiresName(t *testing.T) {
	cmd := newOAuthCmd()
	cmd.SetArgs([]string{"issue-client"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing name arg")
	}
	if !strings.Contains(err.Error(), "arg") {
		t.Errorf("error = %q, want it to mention args", err)
	}
}

// TestOAuthIssueClient_MissingDatabaseURL covers the env-var check
// that runs after argument validation. A missing DATABASE_URL must
// produce a non-zero exit (returned as a non-nil error from
// Execute).
func TestOAuthIssueClient_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	cmd := newOAuthCmd()
	cmd.SetArgs([]string{"issue-client", "test-client"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error = %q, want it to mention DATABASE_URL", err)
	}
}
