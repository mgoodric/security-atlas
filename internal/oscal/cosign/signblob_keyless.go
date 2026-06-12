package cosign

// Keyless (Fulcio + Rekor) blob signing/verification for the OSCAL
// export pipeline's `cosign-keyless` mode (slice 414 / 368b, per
// ADR-0016). This file is the keyless counterpart to signblob.go's
// KMS-backed path.
//
// SCOPE (load-bearing — ADR-0016): keyless here targets an OPERATOR-RUN
// PRIVATE Sigstore stack (Fulcio + Rekor whose trust root the operator
// controls), federating atlas's scoped `client:oscal-signer` OIDC
// identity. It NEVER targets public Fulcio/Rekor for the runtime OSCAL
// surface (options a/d rejected — P0-414-3). The Fulcio/Rekor URLs are
// therefore REQUIRED inputs, validated to be non-empty before any
// subprocess spawns; there is no public-Sigstore default.
//
// The wrapper keeps the same conservative posture as the kms path: an
// injectable exec boundary (the runner interface), explicit timeouts, a
// curated env allowlist (no inheritance of the atlas process env), no
// shell interpolation (fixed argv), and structured error mapping.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// KeylessSignParams is the input to a keyless sign-blob invocation.
type KeylessSignParams struct {
	// Blob is the bytes to sign (the OSCAL bundle digest). Fed to cosign on
	// stdin; never interpolated into the argv.
	Blob []byte
	// IdentityToken is atlas's scoped `client:oscal-signer` OIDC token
	// (slice 188). It is passed to cosign via a private temp file referenced
	// by --identity-token, not on the argv (tokens must not land in process
	// listings).
	IdentityToken string
	// FulcioURL / RekorURL are the operator's PRIVATE Sigstore endpoints
	// (REQUIRED — no public default; ADR-0016 / P0-414-3).
	FulcioURL string
	RekorURL  string
}

// KeylessSignOutput is what a keyless sign produces: the detached
// signature, the issued PEM certificate, and the Rekor log index.
type KeylessSignOutput struct {
	Signature      []byte
	CertificatePEM string
	RekorLogIndex  int64
}

// KeylessVerifyParams is the input to a keyless verify-blob invocation.
type KeylessVerifyParams struct {
	Blob                  []byte
	Signature             []byte
	CertificatePEM        string
	CertificateIdentity   string
	CertificateOIDCIssuer string
	// RekorURL is the operator's PRIVATE Rekor (used for the inclusion-proof
	// check). REQUIRED.
	RekorURL string
}

// ErrKeylessConfig is returned by keyless config validation (missing
// Fulcio/Rekor URL, empty token, etc.) before any subprocess spawns.
var ErrKeylessConfig = errors.New("cosign: invalid keyless configuration")

// validateKeylessEndpoints rejects empty/obviously-malformed private
// Sigstore endpoints. It does NOT reach the network. Per ADR-0016 the
// endpoints are operator-supplied private URLs; we require an http(s)
// scheme and a non-empty host, and we do NOT special-case (nor permit a
// silent fallback to) the public Sigstore endpoints — there is no default.
func validateKeylessEndpoints(fulcioURL, rekorURL string) error {
	for name, u := range map[string]string{"fulcio": fulcioURL, "rekor": rekorURL} {
		u = strings.TrimSpace(u)
		if u == "" {
			return fmt.Errorf("%w: %s URL is required (operator-run PRIVATE Sigstore; ADR-0016)", ErrKeylessConfig, name)
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("%w: %s URL %q must be an http(s) URL", ErrKeylessConfig, name, u)
		}
	}
	return nil
}

// SignBlobKeyless signs blob via the cosign keyless flow against the
// operator's PRIVATE Fulcio + Rekor, returning the signature, the issued
// certificate, and the Rekor log index.
//
// argv (fixed, server-controlled):
//
//		cosign sign-blob --yes \
//		    --fulcio-url <fulcio> --rekor-url <rekor> \
//		    --identity-token <tokenFile> \
//		    --output-signature <sigFile> --output-certificate <certFile> \
//		    --use-signing-config=false --tlog-upload=true -
//
//	  - --fulcio-url / --rekor-url: the operator's PRIVATE endpoints. There is
//	    no default; absent these the call is rejected offline.
//	  - --identity-token <tokenFile>: atlas's oscal-signer OIDC token, passed
//	    by FILE (not on the argv) so it never appears in a process listing.
//	  - --output-signature / --output-certificate: capture the detached
//	    signature + the Fulcio cert to private temp files (cleaner than
//	    parsing stdout, which cosign also uses for human logs).
//	  - --use-signing-config=false: do NOT fetch a TUF signing-config from a
//	    well-known location — the operator supplies the endpoints explicitly.
//	  - --tlog-upload=true: upload to the operator's Rekor (the transparency
//	    log IS the keyless value). The Rekor log index is parsed from cosign's
//	    stderr.
//	  - trailing "-": read the blob from stdin.
func (c *Client) SignBlobKeyless(ctx context.Context, p KeylessSignParams) (KeylessSignOutput, error) {
	if err := validateKeylessEndpoints(p.FulcioURL, p.RekorURL); err != nil {
		return KeylessSignOutput{}, err
	}
	if len(p.Blob) == 0 {
		return KeylessSignOutput{}, fmt.Errorf("%w: refusing to sign an empty blob", ErrBadConfig)
	}
	if strings.TrimSpace(p.IdentityToken) == "" {
		return KeylessSignOutput{}, fmt.Errorf("%w: empty identity token", ErrKeylessConfig)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tokenFile, cleanupToken, err := writePrivateTemp("atlas-oscal-token-*.jwt", []byte(p.IdentityToken))
	if err != nil {
		return KeylessSignOutput{}, fmt.Errorf("%w: %v", ErrKeylessConfig, err)
	}
	defer cleanupToken()
	sigFile, cleanupSig, err := writePrivateTemp("atlas-oscal-keyless-sig-*.sig", nil)
	if err != nil {
		return KeylessSignOutput{}, fmt.Errorf("%w: %v", ErrSignFailed, err)
	}
	defer cleanupSig()
	certFile, cleanupCert, err := writePrivateTemp("atlas-oscal-keyless-cert-*.pem", nil)
	if err != nil {
		return KeylessSignOutput{}, fmt.Errorf("%w: %v", ErrSignFailed, err)
	}
	defer cleanupCert()

	args := []string{
		"sign-blob",
		"--yes",
		"--fulcio-url", p.FulcioURL,
		"--rekor-url", p.RekorURL,
		"--identity-token", tokenFile,
		"--output-signature", sigFile,
		"--output-certificate", certFile,
		"--use-signing-config=false",
		"--tlog-upload=true",
		"-",
	}
	_, stderr, runErr := c.run.run(ctx, c.bin, c.buildEnv(os.Getenv), p.Blob, args...)
	if wrapped := classifyRunErr(ctx, ErrSignFailed, runErr, stderr); wrapped != nil {
		return KeylessSignOutput{}, wrapped
	}

	sig, err := os.ReadFile(sigFile) //nolint:gosec // path is a private temp file we created above
	if err != nil || len(strings.TrimSpace(string(sig))) == 0 {
		return KeylessSignOutput{}, fmt.Errorf("%w: cosign produced no signature", ErrSignFailed)
	}
	cert, err := os.ReadFile(certFile) //nolint:gosec // path is a private temp file we created above
	if err != nil || len(strings.TrimSpace(string(cert))) == 0 {
		return KeylessSignOutput{}, fmt.Errorf("%w: cosign produced no certificate", ErrSignFailed)
	}

	return KeylessSignOutput{
		Signature:      []byte(strings.TrimSpace(string(sig))),
		CertificatePEM: strings.TrimSpace(string(cert)),
		RekorLogIndex:  parseRekorLogIndex(stderr),
	}, nil
}

// VerifyBlobKeyless verifies a keyless signature over blob against the
// recorded Fulcio cert + identity + issuer, using the operator's PRIVATE
// Rekor for the inclusion-proof check. Returns nil iff valid.
//
// argv (fixed, server-controlled):
//
//	cosign verify-blob --signature <sigFile> --certificate <certFile> \
//	    --certificate-identity <id> --certificate-oidc-issuer <iss> \
//	    --rekor-url <rekor> -
func (c *Client) VerifyBlobKeyless(ctx context.Context, p KeylessVerifyParams) error {
	if strings.TrimSpace(p.RekorURL) == "" {
		return fmt.Errorf("%w: rekor URL is required for keyless verification", ErrKeylessConfig)
	}
	if len(p.Blob) == 0 {
		return fmt.Errorf("%w: refusing to verify against an empty blob", ErrBadConfig)
	}
	if len(p.Signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrVerifyFailed)
	}
	if strings.TrimSpace(p.CertificatePEM) == "" {
		return fmt.Errorf("%w: empty certificate", ErrVerifyFailed)
	}
	if p.CertificateIdentity == "" || p.CertificateOIDCIssuer == "" {
		return fmt.Errorf("%w: certificate identity and issuer are required", ErrVerifyFailed)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	sigFile, cleanupSig, err := writePrivateTemp("atlas-oscal-keyless-vsig-*.sig", p.Signature)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerifyFailed, err)
	}
	defer cleanupSig()
	certFile, cleanupCert, err := writePrivateTemp("atlas-oscal-keyless-vcert-*.pem", []byte(p.CertificatePEM))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerifyFailed, err)
	}
	defer cleanupCert()

	args := []string{
		"verify-blob",
		"--signature", sigFile,
		"--certificate", certFile,
		"--certificate-identity", p.CertificateIdentity,
		"--certificate-oidc-issuer", p.CertificateOIDCIssuer,
		"--rekor-url", p.RekorURL,
		"-",
	}
	_, stderr, runErr := c.run.run(ctx, c.bin, c.buildEnv(os.Getenv), p.Blob, args...)
	return classifyRunErr(ctx, ErrVerifyFailed, runErr, stderr)
}

// writePrivateTemp creates a 0600 temp file with the given pattern and
// optional initial contents, returning its path and a cleanup func. Used
// for the OIDC token, the captured signature, and the cert — none of which
// should be passed on the argv (process-listing leak) or left readable by
// other users.
func writePrivateTemp(pattern string, contents []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if len(contents) > 0 {
		if _, err := f.Write(contents); err != nil {
			_ = f.Close()
			cleanup()
			return "", func() {}, err
		}
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// parseRekorLogIndex extracts the Rekor transparency-log index from
// cosign's stderr. cosign logs a line like:
//
//	tlog entry created with index: 12345
//
// We parse that index for the manifest's non-repudiation anchor. A missing
// index is not fatal to the sign (the signature + cert are the primary
// artifacts), so this returns -1 when it cannot find one — the caller
// records that as "no index captured" rather than failing the export.
func parseRekorLogIndex(stderr []byte) int64 {
	const marker = "tlog entry created with index:"
	for _, line := range strings.Split(string(stderr), "\n") {
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len(marker):])
		// Take the leading integer run.
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			continue
		}
		if n, err := strconv.ParseInt(rest[:end], 10, 64); err == nil {
			return n
		}
	}
	return -1
}
