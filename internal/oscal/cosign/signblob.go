package cosign

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// SignBlob signs the given blob with the KMS-held key referenced by
// kmsRef, returning the detached signature bytes (cosign emits a
// base64-encoded signature on stdout). The blob is fed to cosign on
// stdin; nothing untrusted is interpolated into the argv.
//
// The argv is fixed and operator-controlled end to end:
//
//		cosign sign-blob --key <kmsRef> --yes \
//		    --use-signing-config=false --tlog-upload=false -
//
//	  - --key <kmsRef>: the validated KMS provider URI.
//	  - --yes: non-interactive (no confirmation prompt).
//	  - --use-signing-config=false: do NOT fetch a TUF signing-config —
//	    this is the cosign-v3 flag that keeps the operation fully offline
//	    of the Sigstore public infra (no Fulcio/Rekor service discovery).
//	  - --tlog-upload=false: do NOT upload to a transparency log (P0-413-1
//	    — no Rekor). Deprecated-but-honored in v3; kept to document intent
//	    alongside --use-signing-config=false which is what enforces it.
//	  - the trailing "-": read the blob from stdin (no temp file, no path
//	    interpolation). With no --bundle/--output-signature, cosign writes
//	    the base64 detached signature to stdout.
//
// KMS signing is a pure key operation; Phase 1 never touches Fulcio,
// Rekor, or an OIDC identity.
func (c *Client) SignBlob(ctx context.Context, kmsRef string, blob []byte) ([]byte, error) {
	if err := validateKMSRef(kmsRef); err != nil {
		return nil, err
	}
	if len(blob) == 0 {
		return nil, fmt.Errorf("%w: refusing to sign an empty blob", ErrBadConfig)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{
		"sign-blob",
		"--key", kmsRef,
		"--yes",
		"--use-signing-config=false",
		"--tlog-upload=false",
		"-",
	}
	stdout, stderr, err := c.run.run(ctx, c.bin, c.buildEnv(os.Getenv), blob, args...)
	if wrapped := classifyRunErr(ctx, ErrSignFailed, err, stderr); wrapped != nil {
		return nil, wrapped
	}
	sig := strings.TrimSpace(string(stdout))
	if sig == "" {
		return nil, fmt.Errorf("%w: cosign produced an empty signature", ErrSignFailed)
	}
	return []byte(sig), nil
}

// VerifyBlob verifies a detached signature over blob using the cosign KMS
// key reference (the verifier can also use an exported public key file;
// the KMS ref works for both signing and verification). Returns nil iff
// the signature is valid.
//
// argv (fixed, server-controlled):
//
//	cosign verify-blob --key <keyRef> --signature <tmpSigPath> \
//	    --insecure-ignore-tlog=true -
//
// The signature is written to a private temp file because cosign's
// --signature flag takes a path (it does not read the signature from
// stdin while the blob also comes from stdin). The blob is fed on stdin.
// --insecure-ignore-tlog=true is correct for Phase 1: we never upload to
// Rekor, so there is no tlog entry to require (the name is cosign's; it
// means "do not require a transparency-log entry", which matches the
// KMS-only, no-Rekor design — P0-413-1).
func (c *Client) VerifyBlob(ctx context.Context, keyRef string, blob, signature []byte) error {
	if err := validateKMSRef(keyRef); err != nil {
		return err
	}
	if len(blob) == 0 {
		return fmt.Errorf("%w: refusing to verify against an empty blob", ErrBadConfig)
	}
	if len(signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrVerifyFailed)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	sigFile, err := os.CreateTemp("", "atlas-oscal-sig-*.sig")
	if err != nil {
		return fmt.Errorf("%w: create temp signature file: %v", ErrVerifyFailed, err)
	}
	sigPath := sigFile.Name()
	defer func() { _ = os.Remove(sigPath) }()
	if _, err := sigFile.Write(signature); err != nil {
		_ = sigFile.Close()
		return fmt.Errorf("%w: write temp signature file: %v", ErrVerifyFailed, err)
	}
	if err := sigFile.Close(); err != nil {
		return fmt.Errorf("%w: close temp signature file: %v", ErrVerifyFailed, err)
	}

	args := []string{
		"verify-blob",
		"--key", keyRef,
		"--signature", sigPath,
		"--insecure-ignore-tlog=true",
		"-",
	}
	_, stderr, runErr := c.run.run(ctx, c.bin, c.buildEnv(os.Getenv), blob, args...)
	return classifyRunErr(ctx, ErrVerifyFailed, runErr, stderr)
}

// CheckKMSRef validates the KMS reference and probes whether cosign can
// reach/use it, used by the CLI `config-check`. It runs a real cosign
// sign over a tiny probe blob — the cheapest operation that exercises the
// full KMS credential + permission path without writing anything durable.
// A nil return means "cosign is present and the configured KMS key is
// usable for signing right now". Offline-well-formedness failures return
// ErrBadConfig without spawning cosign.
func (c *Client) CheckKMSRef(ctx context.Context, kmsRef string) error {
	if err := validateKMSRef(kmsRef); err != nil {
		return err
	}
	if !c.Available() {
		return ErrCosignNotFound
	}
	// Sign a fixed probe blob; success proves credentials + key-use
	// permission. The signature is discarded.
	_, err := c.SignBlob(ctx, kmsRef, []byte("atlas-oscal-config-check-probe"))
	if err != nil {
		return fmt.Errorf("KMS key %q is configured but not usable: %w", kmsRef, err)
	}
	return nil
}
