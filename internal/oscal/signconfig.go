package oscal

import (
	"fmt"
	"strings"
)

// Environment variables that select and configure the signing mode
// (ADR-0010 / slice 413). Centralized here so the CLI, the server, and
// config-check all resolve the mode identically.
const (
	// EnvSigningMode explicitly selects the signing mode
	// (embedded-ed25519 | cosign-kms). When unset, the mode is inferred:
	// a set EnvKMSRef implies cosign-kms, otherwise embedded-ed25519.
	EnvSigningMode = "ATLAS_OSCAL_SIGNING_MODE"
	// EnvKMSRef is the cosign KMS key reference for cosign-kms mode
	// (e.g. awskms:///alias/atlas-oscal).
	EnvKMSRef = "ATLAS_COSIGN_KMS_REF"
	// EnvCosignBinary overrides the cosign binary path (default: resolve
	// "cosign" on PATH).
	EnvCosignBinary = "ATLAS_COSIGN_BINARY"
	// EnvAllowEmbedded, when "true", permits an explicit downgrade to the
	// embedded mode even where a stronger mode was requested. It does NOT
	// silently downgrade — the resolver only honors it when the operator
	// has asked for embedded or left the mode unset. It exists so a
	// connected deployment can fall back to the air-gap-safe mode without
	// editing the primary mode variable.
	EnvAllowEmbedded = "ATLAS_OSCAL_ALLOW_EMBEDDED"
	// EnvSigningKey is the existing hex ed25519 key for embedded mode
	// (unchanged from slice 030). Honored by the server wiring.
	EnvSigningKey = "OSCAL_SIGNING_KEY"
)

// SigningConfig is the resolved signing configuration: which mode, and
// the mode-specific parameters. Build it with ResolveSigningConfig from a
// (possibly faked) env lookup, so resolution is unit-testable.
type SigningConfig struct {
	// Mode is the resolved signing mode. Never empty after a successful
	// ResolveSigningConfig.
	Mode Mode
	// KMSRef is the cosign KMS key reference (set iff Mode == ModeCosignKMS).
	KMSRef string
	// CosignBinary is the cosign binary path/name (empty → "cosign" on PATH).
	CosignBinary string
}

// ResolveSigningConfig determines the signing mode from configuration.
// lookup is os.LookupEnv-shaped (returns value, present); pass a fake in
// tests. Resolution rules (ADR-0010 default table):
//
//   - If ATLAS_OSCAL_SIGNING_MODE is set, it is authoritative (and
//     validated). cosign-kms then requires ATLAS_COSIGN_KMS_REF.
//   - Else if ATLAS_COSIGN_KMS_REF is set, the mode is inferred as
//     cosign-kms (a connected operator who configured a KMS key).
//   - Else the mode is embedded-ed25519 — the air-gap-safe default
//     (P0-413-2). This is what the docker-compose self-host deployment
//     gets with no extra configuration.
//
// cosign-keyless is rejected here (P0-413-1 — Phase 1 does not implement
// it; slice 414 owns it).
func ResolveSigningConfig(lookup func(string) (string, bool)) (SigningConfig, error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}

	cfg := SigningConfig{CosignBinary: get(EnvCosignBinary)}
	kmsRef := get(EnvKMSRef)
	explicit := Mode(get(EnvSigningMode))

	switch explicit {
	case "":
		// Inferred. A configured KMS ref implies cosign-kms; otherwise the
		// air-gap-safe embedded default.
		if kmsRef != "" {
			cfg.Mode = ModeCosignKMS
			cfg.KMSRef = kmsRef
			return cfg, nil
		}
		cfg.Mode = ModeEmbeddedEd25519
		return cfg, nil
	case ModeEmbeddedEd25519:
		cfg.Mode = ModeEmbeddedEd25519
		return cfg, nil
	case ModeCosignKMS:
		if kmsRef == "" {
			return SigningConfig{}, fmt.Errorf("%w=cosign-kms requires %s to be set",
				errBadSigningMode, EnvKMSRef)
		}
		cfg.Mode = ModeCosignKMS
		cfg.KMSRef = kmsRef
		return cfg, nil
	case ModeCosignKeyless:
		return SigningConfig{}, fmt.Errorf("%w: cosign-keyless is not available in this build "+
			"(Phase 1 / slice 413 ships embedded-ed25519 + cosign-kms; keyless is slice 414)", errBadSigningMode)
	default:
		return SigningConfig{}, fmt.Errorf("%w: unknown %s=%q (valid: embedded-ed25519, cosign-kms)",
			errBadSigningMode, EnvSigningMode, explicit)
	}
}

// errBadSigningMode is the sentinel wrapped by ResolveSigningConfig
// failures.
var errBadSigningMode = fmt.Errorf("oscal: invalid signing configuration")

// Describe returns a one-line human summary of the resolved config, used
// by the CLI config-check output.
func (c SigningConfig) Describe() string {
	switch c.Mode {
	case ModeCosignKMS:
		bin := c.CosignBinary
		if bin == "" {
			bin = "cosign (PATH)"
		}
		return fmt.Sprintf("mode=cosign-kms key_ref=%s cosign=%s", c.KMSRef, bin)
	default:
		return fmt.Sprintf("mode=%s (hermetic; no external dependency)", c.Mode)
	}
}
