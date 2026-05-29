package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
)

// fakeRotator is an in-memory keyRotator for testing doKeyRotation's
// orchestration without touching the filesystem.
type fakeRotator struct {
	kid        string
	rotateErr  error
	pruneErr   error
	pruned     []string
	rotateCnt  int
	pruneCalls int
}

func (f *fakeRotator) Rotate(_ context.Context) error {
	if f.rotateErr != nil {
		return f.rotateErr
	}
	f.rotateCnt++
	// Advance the KeyID to a strictly-later value to mimic a real rotate.
	f.kid = f.kid + "-r"
	return nil
}

func (f *fakeRotator) Prune(_ context.Context, _ time.Time) ([]string, error) {
	f.pruneCalls++
	if f.pruneErr != nil {
		return nil, f.pruneErr
	}
	return f.pruned, nil
}

func (f *fakeRotator) Get(_ context.Context) (keystore.SigningKey, []keystore.VerificationKey, error) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return keystore.SigningKey{KeyID: f.kid, Key: priv}, nil, nil
}

// TestDoKeyRotation_EmitsAuditLineWithKIDTransition covers AC-7: a
// rotation writes a structured audit line carrying the previous + new
// signing KeyIDs and the rotation event name.
func TestDoKeyRotation_EmitsAuditLineWithKIDTransition(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	f := &fakeRotator{kid: "20260101T000000Z", pruned: []string{"20200101T000000Z"}}

	doKeyRotation(context.Background(), f, logger)

	if f.rotateCnt != 1 {
		t.Fatalf("expected 1 rotate, got %d", f.rotateCnt)
	}
	if f.pruneCalls != 1 {
		t.Fatalf("expected 1 prune, got %d", f.pruneCalls)
	}
	out := buf.String()
	if !strings.Contains(out, "key_rotation") {
		t.Fatalf("audit line missing event name:\n%s", out)
	}
	if !strings.Contains(out, "previous_signing_kid=20260101T000000Z") {
		t.Fatalf("audit line missing previous kid:\n%s", out)
	}
	if !strings.Contains(out, "new_signing_kid=20260101T000000Z-r") {
		t.Fatalf("audit line missing new kid:\n%s", out)
	}
	if !strings.Contains(out, "actor=scheduler") {
		t.Fatalf("audit line missing actor:\n%s", out)
	}
}

// TestDoKeyRotation_NeverLogsKeyMaterial covers P0-366-1: the audit +
// prune log output contains no PEM markers / private-key material.
func TestDoKeyRotation_NeverLogsKeyMaterial(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	f := &fakeRotator{kid: "20260101T000000Z", pruned: []string{"20200101T000000Z"}}

	doKeyRotation(context.Background(), f, logger)

	out := buf.String()
	for _, marker := range []string{"PRIVATE KEY", "BEGIN", "-----"} {
		if strings.Contains(out, marker) {
			t.Fatalf("P0-366-1 violation: log contains %q:\n%s", marker, out)
		}
	}
}

// TestDoKeyRotation_RotateFailureIsFailSafe covers the fail-safe path:
// when Rotate errors, the cycle aborts WITHOUT pruning (the prior key
// stays active).
func TestDoKeyRotation_RotateFailureIsFailSafe(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	f := &fakeRotator{kid: "20260101T000000Z", rotateErr: errors.New("disk full")}

	doKeyRotation(context.Background(), f, logger)

	if f.pruneCalls != 0 {
		t.Fatalf("prune must NOT run when rotate fails, got %d calls", f.pruneCalls)
	}
	if !strings.Contains(buf.String(), "rotate failed") {
		t.Fatalf("expected rotate-failure log line:\n%s", buf.String())
	}
}

// TestKeyRotationInterval_DefaultAndOverride covers the env-var parsing
// for the rotation cadence.
func TestKeyRotationInterval_DefaultAndOverride(t *testing.T) {
	t.Setenv("ATLAS_KEY_ROTATION_INTERVAL", "")
	if got := keyRotationInterval(); got != defaultKeyRotationInterval {
		t.Fatalf("empty env should yield default %s, got %s", defaultKeyRotationInterval, got)
	}
	t.Setenv("ATLAS_KEY_ROTATION_INTERVAL", "720h")
	if got := keyRotationInterval(); got != 720*time.Hour {
		t.Fatalf("override should yield 720h, got %s", got)
	}
	t.Setenv("ATLAS_KEY_ROTATION_INTERVAL", "garbage")
	if got := keyRotationInterval(); got != defaultKeyRotationInterval {
		t.Fatalf("invalid env should fall back to default %s, got %s", defaultKeyRotationInterval, got)
	}
}
