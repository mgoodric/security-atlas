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

func TestResolveSigningConfig_KeylessRequiresPrivateEndpoints(t *testing.T) {
	t.Parallel()
	// Keyless selected but no Fulcio/Rekor configured → error (opt-in is not
	// satisfied). This is the P0-414-3 guard: keyless is unavailable without
	// the operator's PRIVATE Sigstore endpoints.
	cases := []map[string]string{
		{EnvSigningMode: string(ModeCosignKeyless)},
		{EnvSigningMode: string(ModeCosignKeyless), EnvFulcioURL: "https://fulcio.internal"},
		{EnvSigningMode: string(ModeCosignKeyless), EnvRekorURL: "https://rekor.internal"},
	}
	for i, c := range cases {
		_, err := ResolveSigningConfig(envFake(c))
		if err == nil {
			t.Errorf("case %d: cosign-keyless without both private endpoints must error", i)
		}
	}
}

func TestResolveSigningConfig_KeylessOptIn(t *testing.T) {
	t.Parallel()
	cfg, err := ResolveSigningConfig(envFake(map[string]string{
		EnvSigningMode:       string(ModeCosignKeyless),
		EnvFulcioURL:         "https://fulcio.atlas.internal",
		EnvRekorURL:          "https://rekor.atlas.internal",
		EnvKeylessIdentity:   "https://atlas.example/client:oscal-signer",
		EnvKeylessOIDCIssuer: "https://atlas.example/oauth",
	}))
	if err != nil {
		t.Fatalf("ResolveSigningConfig (keyless opt-in): %v", err)
	}
	if cfg.Mode != ModeCosignKeyless {
		t.Errorf("mode = %q, want cosign-keyless", cfg.Mode)
	}
	if cfg.FulcioURL != "https://fulcio.atlas.internal" || cfg.RekorURL != "https://rekor.atlas.internal" {
		t.Errorf("private endpoints not recorded: fulcio=%q rekor=%q", cfg.FulcioURL, cfg.RekorURL)
	}
	if cfg.KeylessIdentity == "" || cfg.KeylessOIDCIssuer == "" {
		t.Errorf("optional identity/issuer hints not recorded: id=%q iss=%q", cfg.KeylessIdentity, cfg.KeylessOIDCIssuer)
	}
}

func TestResolveSigningConfig_KeylessIsNeverInferred(t *testing.T) {
	t.Parallel()
	// A configured Fulcio/Rekor must NOT silently flip the default to keyless
	// (P0-414-2 / P0-414-3 — keyless is opt-in, never inferred). With no
	// explicit mode and no KMS ref, the default stays embedded even if the
	// private-Sigstore endpoints happen to be set.
	cfg, err := ResolveSigningConfig(envFake(map[string]string{
		EnvFulcioURL: "https://fulcio.atlas.internal",
		EnvRekorURL:  "https://rekor.atlas.internal",
	}))
	if err != nil {
		t.Fatalf("ResolveSigningConfig: %v", err)
	}
	if cfg.Mode != ModeEmbeddedEd25519 {
		t.Errorf("keyless must never be inferred — default stayed %q, want embedded-ed25519", cfg.Mode)
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
	kl := SigningConfig{Mode: ModeCosignKeyless, FulcioURL: "https://f", RekorURL: "https://r"}
	d := kl.Describe()
	if !strings.Contains(d, "cosign-keyless") || !strings.Contains(d, "https://f") || !strings.Contains(d, "opt-in") {
		t.Errorf("keyless describe = %q", d)
	}
}
