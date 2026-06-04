package oscal

import (
	"strings"
	"testing"
)

// envFake builds a lookup func from a map for ResolveSigningConfig tests.
func envFake(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestResolveSigningConfig_DefaultsToEmbedded(t *testing.T) {
	t.Parallel()
	// No env → air-gap-safe embedded (P0-413-2).
	cfg, err := ResolveSigningConfig(envFake(nil))
	if err != nil {
		t.Fatalf("ResolveSigningConfig: %v", err)
	}
	if cfg.Mode != ModeEmbeddedEd25519 {
		t.Errorf("default mode = %q, want embedded-ed25519", cfg.Mode)
	}
}

func TestResolveSigningConfig_InfersKMSFromRef(t *testing.T) {
	t.Parallel()
	cfg, err := ResolveSigningConfig(envFake(map[string]string{
		EnvKMSRef: "awskms:///alias/atlas-oscal",
	}))
	if err != nil {
		t.Fatalf("ResolveSigningConfig: %v", err)
	}
	if cfg.Mode != ModeCosignKMS {
		t.Errorf("mode = %q, want cosign-kms (inferred from KMS ref)", cfg.Mode)
	}
	if cfg.KMSRef != "awskms:///alias/atlas-oscal" {
		t.Errorf("kms ref = %q", cfg.KMSRef)
	}
}

func TestResolveSigningConfig_ExplicitEmbeddedIgnoresKMSRef(t *testing.T) {
	t.Parallel()
	// Explicit embedded wins even if a KMS ref happens to be set.
	cfg, err := ResolveSigningConfig(envFake(map[string]string{
		EnvSigningMode: string(ModeEmbeddedEd25519),
		EnvKMSRef:      "awskms:///alias/x",
	}))
	if err != nil {
		t.Fatalf("ResolveSigningConfig: %v", err)
	}
	if cfg.Mode != ModeEmbeddedEd25519 {
		t.Errorf("explicit embedded must win, got %q", cfg.Mode)
	}
}

func TestResolveSigningConfig_ExplicitKMSRequiresRef(t *testing.T) {
	t.Parallel()
	_, err := ResolveSigningConfig(envFake(map[string]string{
		EnvSigningMode: string(ModeCosignKMS),
	}))
	if err == nil {
		t.Fatal("cosign-kms without a KMS ref must error")
	}
	if !strings.Contains(err.Error(), EnvKMSRef) {
		t.Errorf("error must name the missing var, got %v", err)
	}
}

func TestResolveSigningConfig_RejectsKeyless(t *testing.T) {
	t.Parallel()
	_, err := ResolveSigningConfig(envFake(map[string]string{
		EnvSigningMode: string(ModeCosignKeyless),
	}))
	if err == nil {
		t.Fatal("cosign-keyless must be rejected in Phase 1 (P0-413-1)")
	}
}

func TestResolveSigningConfig_RejectsUnknownMode(t *testing.T) {
	t.Parallel()
	_, err := ResolveSigningConfig(envFake(map[string]string{
		EnvSigningMode: "quantum-entanglement",
	}))
	if err == nil {
		t.Fatal("unknown mode must error")
	}
}

func TestSigningConfig_Describe(t *testing.T) {
	t.Parallel()
	kms := SigningConfig{Mode: ModeCosignKMS, KMSRef: "awskms:///alias/k"}
	if !strings.Contains(kms.Describe(), "cosign-kms") || !strings.Contains(kms.Describe(), "awskms:///alias/k") {
		t.Errorf("kms describe = %q", kms.Describe())
	}
	emb := SigningConfig{Mode: ModeEmbeddedEd25519}
	if !strings.Contains(emb.Describe(), "embedded-ed25519") {
		t.Errorf("embedded describe = %q", emb.Describe())
	}
}
