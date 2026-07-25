// Slice 413 — `oscal sign | verify | config-check` subcommands.
//
// These operate on an on-disk OSCAL export bundle directory (manifest.json
// + member files) plus the deployment's signing-mode configuration. They
// are the operator/auditor-facing surface for the ADR-0010 Phase-1 signing
// modes:
//
//   - sign         (re)signs a bundle with the configured mode.
//   - verify       verifies a bundle, dispatching on the manifest's mode
//     (embedded-ed25519 in-process; cosign-kms via cosign
//     verify-blob). Backward-compatible: a pre-413 manifest
//     with no mode field verifies as embedded.
//   - config-check reports the resolved signing mode and whether its
//     prerequisites are met (cosign present; KMS key usable).
//
// Phase 1 is kms + embedded only — keyless is slice 414.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/oscal/cosign"
)

func newOscalSignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oscal",
		Short: "sign, verify, and check signing config for OSCAL export bundles",
		Long: `Sign and verify OSCAL audit-export bundles, and check the signing
configuration. Two Phase-1 signing modes (ADR-0010):

  embedded-ed25519  in-process ed25519 detached signature (air-gap default)
  cosign-kms        cosign sign-blob with a cloud-KMS key (stock-verifiable)

The mode is selected by ATLAS_OSCAL_SIGNING_MODE (or inferred: a set
ATLAS_COSIGN_KMS_REF implies cosign-kms; otherwise embedded-ed25519).`,
	}
	cmd.AddCommand(newOscalSignSubCmd())
	cmd.AddCommand(newOscalVerifyCmd())
	cmd.AddCommand(newOscalConfigCheckCmd())
	return cmd
}

// lineWriter is a tiny io.Writer-ish helper that records the first write
// error so a sequence of output lines can be emitted and the error
// checked once (errcheck-clean, matching the project's convention without
// an `if err` after every line).
type lineWriter struct {
	w   io.Writer
	err error
}

func (l *lineWriter) printf(format string, args ...any) {
	if l.err != nil {
		return
	}
	_, l.err = fmt.Fprintf(l.w, format, args...)
}

// cosignClientFromConfig builds a cosign.Client honoring the configured
// binary override.
func cosignClientFromConfig(cfg oscal.SigningConfig) *cosign.Client {
	var opts []cosign.Option
	if cfg.CosignBinary != "" {
		opts = append(opts, cosign.WithBinary(cfg.CosignBinary))
	}
	return cosign.New(opts...)
}

// EnvKeylessIdentityToken is the OIDC token the CLI uses for cosign-keyless
// signing. The server mints atlas's scoped `client:oscal-signer` token via
// slice 188's client_credentials grant and plumbs it programmatically; the
// operator-run CLI instead supplies the token out of band via this env var
// (e.g. obtained from `atlas-cli auth token --client oscal-signer`).
const EnvKeylessIdentityToken = "ATLAS_COSIGN_IDENTITY_TOKEN"

// envIdentityTokenSource satisfies oscal.IdentityTokenSource by reading the
// operator-supplied OIDC token from the environment. It is the CLI's token
// source; the server uses a slice-188-backed source instead.
type envIdentityTokenSource struct{}

func (envIdentityTokenSource) IdentityToken(_ context.Context) (string, error) {
	tok := os.Getenv(EnvKeylessIdentityToken)
	if tok == "" {
		return "", fmt.Errorf("cosign-keyless requires an OIDC identity token in %s "+
			"(atlas's client:oscal-signer token; see ADR-0016)", EnvKeylessIdentityToken)
	}
	return tok, nil
}

// keylessBackend is the flat shim that lets cosign.Client satisfy the
// oscal package's keyless backend contract (oscal does not import the
// cosign subpackage's param types). The oscal.CosignKeylessAdapter wraps
// this to produce the package's CosignKeylessSigner/Verifier.
type keylessBackend struct{ c *cosign.Client }

func (k keylessBackend) SignBlobKeyless(ctx context.Context, blob []byte, identityToken, fulcioURL, rekorURL string) ([]byte, string, int64, error) {
	out, err := k.c.SignBlobKeyless(ctx, cosign.KeylessSignParams{
		Blob:          blob,
		IdentityToken: identityToken,
		FulcioURL:     fulcioURL,
		RekorURL:      rekorURL,
	})
	if err != nil {
		return nil, "", 0, err
	}
	return out.Signature, out.CertificatePEM, out.RekorLogIndex, nil
}

func (k keylessBackend) VerifyBlobKeyless(ctx context.Context, blob, signature []byte, certPEM, certIdentity, certOIDCIssuer, rekorURL string) error {
	return k.c.VerifyBlobKeyless(ctx, cosign.KeylessVerifyParams{
		Blob:                  blob,
		Signature:             signature,
		CertificatePEM:        certPEM,
		CertificateIdentity:   certIdentity,
		CertificateOIDCIssuer: certOIDCIssuer,
		RekorURL:              rekorURL,
	})
}

// keylessAdapterFromConfig builds the oscal keyless signer/verifier adapter
// from the resolved signing config.
func keylessAdapterFromConfig(cfg oscal.SigningConfig) *oscal.CosignKeylessAdapter {
	return oscal.NewCosignKeylessAdapter(keylessBackend{c: cosignClientFromConfig(cfg)})
}

// combinedVerifier satisfies BOTH oscal.CosignVerifier (kms) and
// oscal.CosignKeylessVerifier so a single value handed to
// VerifyBundleWithCosign can verify whichever cosign mode the bundle
// recorded. The CLI `verify` command does not know the bundle's mode until
// it reads the manifest, so it passes this superset verifier.
type combinedVerifier struct {
	kms     *cosign.Client
	keyless *oscal.CosignKeylessAdapter
}

func newCombinedVerifier(cfg oscal.SigningConfig) combinedVerifier {
	return combinedVerifier{kms: cosignClientFromConfig(cfg), keyless: keylessAdapterFromConfig(cfg)}
}

func (v combinedVerifier) VerifyBlob(ctx context.Context, keyRef string, blob, signature []byte) error {
	return v.kms.VerifyBlob(ctx, keyRef, blob, signature)
}

func (v combinedVerifier) VerifyBlobKeyless(ctx context.Context, req oscal.KeylessVerifyRequest) error {
	return v.keyless.VerifyBlobKeyless(ctx, req)
}

func newOscalSignSubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign <bundle-dir>",
		Short: "sign an OSCAL bundle directory with the configured signing mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			cfg, err := oscal.ResolveSigningConfig(os.LookupEnv)
			if err != nil {
				return err
			}
			b, err := oscal.ReadBundle(dir)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			var sig oscal.Signature
			switch cfg.Mode {
			case oscal.ModeCosignKMS:
				client := cosignClientFromConfig(cfg)
				signer, sErr := oscal.NewKMSSigner(client, cfg.KMSRef)
				if sErr != nil {
					return sErr
				}
				sig, err = signer.SignBundle(ctx, b)
			case oscal.ModeCosignKeyless:
				adapter := keylessAdapterFromConfig(cfg)
				signer, sErr := oscal.NewKeylessSigner(adapter, envIdentityTokenSource{}, cfg.FulcioURL, cfg.RekorURL)
				if sErr != nil {
					return sErr
				}
				sig, err = signer.SignBundle(ctx, b)
			default: // embedded-ed25519
				signer, sErr := embeddedSignerFromEnv()
				if sErr != nil {
					return sErr
				}
				sig, err = signer.SignBundle(b)
			}
			if err != nil {
				return fmt.Errorf("sign bundle: %w", err)
			}
			b.Signature = sig
			if _, err := b.WriteBundle(dir); err != nil {
				return fmt.Errorf("write signed bundle: %w", err)
			}
			lw := &lineWriter{w: cmd.OutOrStdout()}
			lw.printf("Signed %s with mode=%s\n", dir, sig.Mode)
			if sig.KeyRef != "" {
				lw.printf("  key_ref: %s\n", sig.KeyRef)
			}
			lw.printf("  digest:  %s\n", sig.Digest)
			return lw.err
		},
	}
	return cmd
}

// embeddedSignerFromEnv mirrors the server's OSCAL_SIGNING_KEY handling so
// the CLI signs with the same key when one is configured; otherwise it
// generates an ephemeral key (the public half travels in the manifest).
func embeddedSignerFromEnv() (*oscal.Signer, error) {
	raw := os.Getenv(oscal.EnvSigningKey)
	if raw == "" {
		return oscal.NewEphemeralSigner()
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid hex: %w", oscal.EnvSigningKey, err)
	}
	return oscal.NewSigner(ed25519.PrivateKey(key))
}

func newOscalVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <bundle-dir>",
		Short: "verify an OSCAL bundle, dispatching on its recorded signing mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			b, err := oscal.ReadBundle(dir)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			// VerifyBundleWithCosign handles every mode: embedded in-process,
			// cosign-kms + cosign-keyless via the cosign binary. The combined
			// verifier satisfies both the kms and keyless surfaces so it works
			// whatever mode the manifest records. Keyless verify does NOT require
			// keyless to be the locally-configured mode — an auditor verifies a
			// keyless bundle from the cert/issuer/rekor recorded in its manifest.
			cfg, _ := oscal.ResolveSigningConfig(os.LookupEnv)
			verifier := newCombinedVerifier(cfg)
			if err := oscal.VerifyBundleWithCosign(ctx, b, verifier); err != nil {
				return fmt.Errorf("verification FAILED: %w", err)
			}
			mode := oscal.ResolveMode(b.Signature.Mode)
			lw := &lineWriter{w: cmd.OutOrStdout()}
			lw.printf("OK: %s verifies (mode=%s)\n", dir, mode)
			return lw.err
		},
	}
	return cmd
}

func newOscalConfigCheckCmd() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{
		Use:   "config-check",
		Short: "report the resolved OSCAL signing mode and its prerequisites",
		Long: `Reports which signing mode the current environment resolves to and
whether its prerequisites are met. For cosign-kms it checks that the cosign
binary is present and (with --probe) that the configured KMS key is usable
for signing right now.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := oscal.ResolveSigningConfig(os.LookupEnv)
			if err != nil {
				return err
			}
			lw := &lineWriter{w: cmd.OutOrStdout()}
			lw.printf("signing config: %s\n", cfg.Describe())

			switch cfg.Mode {
			case oscal.ModeCosignKMS:
				client := cosignClientFromConfig(cfg)
				if !client.Available() {
					return fmt.Errorf("cosign-kms selected but cosign binary not found "+
						"(install cosign, set %s, or switch to embedded-ed25519)", oscal.EnvCosignBinary)
				}
				lw.printf("  cosign binary:  present\n")
				if probe {
					ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
					defer cancel()
					if err := client.CheckKMSRef(ctx, cfg.KMSRef); err != nil {
						return fmt.Errorf("KMS key check FAILED: %w", err)
					}
					lw.printf("  KMS key:        usable (probe sign succeeded)\n")
				} else {
					lw.printf("  KMS key:        not probed (pass --probe to test a live sign)\n")
				}
			case oscal.ModeCosignKeyless:
				client := cosignClientFromConfig(cfg)
				if !client.Available() {
					return fmt.Errorf("cosign-keyless selected but cosign binary not found "+
						"(install cosign, set %s, or switch to embedded-ed25519)", oscal.EnvCosignBinary)
				}
				lw.printf("  cosign binary:    present\n")
				lw.printf("  fulcio (private): %s\n", cfg.FulcioURL)
				lw.printf("  rekor (private):  %s\n", cfg.RekorURL)
				if _, terr := (envIdentityTokenSource{}).IdentityToken(cmd.Context()); terr != nil {
					lw.printf("  identity token:   NOT set (set %s before signing)\n", EnvKeylessIdentityToken)
				} else {
					lw.printf("  identity token:   present\n")
				}
				lw.printf("  note:             opt-in; targets an operator-run PRIVATE Sigstore only (ADR-0016)\n")
			default:
				lw.printf("  prerequisites:  none (hermetic, air-gap-safe)\n")
			}
			lw.printf("config-check: OK\n")
			return lw.err
		},
	}
	cmd.Flags().BoolVar(&probe, "probe", false, "for cosign-kms, perform a live test sign against the KMS key")
	return cmd
}
