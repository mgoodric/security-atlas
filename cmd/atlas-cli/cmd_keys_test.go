package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestKeysList_RendersKeys covers AC-5: `atlas keys list` prints every
// KeyID with its role + age against a keystore directory.
func TestKeysList_RendersKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_KEYSTORE_PATH", dir)

	out := runKeysCmd(t, "list")
	if !strings.Contains(out, "signing") {
		t.Fatalf("expected output to mark a signing key, got:\n%s", out)
	}
}

// TestKeysRotate_AddsKey covers AC-5: `atlas keys rotate` rotates the
// signing key; a subsequent list shows two keys.
func TestKeysRotate_AddsKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_KEYSTORE_PATH", dir)

	// Prime the store with one key.
	_ = runKeysCmd(t, "list")
	rotateOut := runKeysCmd(t, "rotate")
	if !strings.Contains(rotateOut, "rotated") {
		t.Fatalf("expected 'rotated' confirmation, got:\n%s", rotateOut)
	}
	listOut := runKeysCmd(t, "list")
	// Two key rows: count lines that look like key entries (contain the
	// KeyID timestamp marker "Z").
	if strings.Count(listOut, "Z") < 2 {
		t.Fatalf("expected at least 2 keys after rotate, got:\n%s", listOut)
	}
}

// TestKeysPrune_DryRunByDefault covers AC-5 + safety: `atlas keys prune`
// without --confirm is a dry run — it reports what WOULD be pruned but
// removes nothing.
func TestKeysPrune_DryRunByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_KEYSTORE_PATH", dir)

	_ = runKeysCmd(t, "list")
	out := runKeysCmd(t, "prune")
	if !strings.Contains(strings.ToLower(out), "dry-run") {
		t.Fatalf("expected dry-run notice in prune output, got:\n%s", out)
	}
}

func runKeysCmd(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newKeysCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("keys %v: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}
