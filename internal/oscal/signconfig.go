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
	// EnvFulcioURL is the operator-run PRIVATE Fulcio URL for cosign-keyless
	// mode (slice 414 / ADR-0016). Keyless targets an operator-controlled
	// Sigstore ONLY — never public Fulcio (P0-414-3). Required (with
	// EnvRekorURL) before cosign-keyless can be selected.
	EnvFulcioURL = "ATLAS_COSIGN_FULCIO_URL"
	// EnvRekorURL is the operator-run PRIVATE Rekor transparency-log URL for
	// cosign-keyless mode (slice 414 / ADR-0016).
	EnvRekorURL = "ATLAS_COSIGN_REKOR_URL"
	// EnvKeylessIdentity is the expected cosign-keyless certificate identity
	// (the atlas `client:oscal-signer` subject) a VERIFIER asserts. The
	// SIGNER derives the identity from the issued cert; this is recorded for
	// config-check / verification-side use. Optional — when empty the
	// identity recorded in the bundle manifest is used at verify time.
	EnvKeylessIdentity = "ATLAS_COSIGN_KEYLESS_IDENTITY"
	// EnvKeylessOIDCIssuer is the expected cosign-keyless certificate OIDC
	// issuer (this deployment's atlas AS issuer). Same optionality as
	// EnvKeylessIdentity.
	EnvKeylessOIDCIssuer = "ATLAS_COSIGN_KEYLESS_OIDC_ISSUER"
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
	// FulcioURL / RekorURL are the operator-run PRIVATE Sigstore endpoints
	// (set iff Mode == ModeCosignKeyless). NEVER public Fulcio/Rekor
	// (P0-414-3).
	FulcioURL string
	RekorURL  string
	// KeylessIdentity / KeylessOIDCIssuer are the optional expected cert
	// identity + issuer for cosign-keyless (verifier-side hints). Empty when
	// not configured — verification then trusts the identity recorded in the
	// bundle manifest.
	KeylessIdentity   string
	KeylessOIDCIssuer string
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
// cosign-keyless is an OPT-IN mode (slice 414 / ADR-0016): it is reachable
// ONLY when explicitly selected via ATLAS_OSCAL_SIGNING_MODE=cosign-keyless
// AND the operator's PRIVATE Fulcio + Rekor URLs are configured. It is
// NEVER inferred — a configured Fulcio/Rekor does NOT silently flip the
// default. This preserves the air-gap default (embedded-ed25519, P0-414-2)
// and the connected GA default (cosign-kms, P0-414-3): keyless is opt-in,
// not a default flip.
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
		// air-gap-safe embedded default. Keyless is deliberately NOT
		// inferable — it must be explicitly opted into (P0-414-3).
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
		fulcio := get(EnvFulcioURL)
		rekor := get(EnvRekorURL)
		if fulcio == "" || rekor == "" {
			return SigningConfig{}, fmt.Errorf("%w=cosign-keyless requires %s and %s "+
				"(operator-run PRIVATE Sigstore endpoints — keyless targets an operator-controlled "+
				"trust root only, never public Fulcio; see ADR-0016)",
				errBadSigningMode, EnvFulcioURL, EnvRekorURL)
		}
		cfg.Mode = ModeCosignKeyless
		cfg.FulcioURL = fulcio
		cfg.RekorURL = rekor
		cfg.KeylessIdentity = get(EnvKeylessIdentity)
		cfg.KeylessOIDCIssuer = get(EnvKeylessOIDCIssuer)
		return cfg, nil
	default:
		return SigningConfig{}, fmt.Errorf("%w: unknown %s=%q (valid: embedded-ed25519, cosign-kms, cosign-keyless)",
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
	case ModeCosignKeyless:
		bin := c.CosignBinary
		if bin == "" {
			bin = "cosign (PATH)"
		}
		return fmt.Sprintf("mode=cosign-keyless fulcio=%s rekor=%s cosign=%s (opt-in; operator-run PRIVATE Sigstore)",
			c.FulcioURL, c.RekorURL, bin)
	default:
		return fmt.Sprintf("mode=%s (hermetic; no external dependency)", c.Mode)
	}
}
