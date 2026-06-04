// Package cosign is a conservative wrapper around the upstream `cosign`
// binary's `sign-blob` and `verify-blob` subcommands, used by the OSCAL
// export pipeline's `cosign-kms` signing mode (slice 413 / ADR-0010
// Phase 1).
//
// "Conservative" is load-bearing here — this code shells out to an
// external binary on a security-critical path (signing of audit-export
// bundles), so the wrapper is deliberately defensive:
//
//   - Injectable exec boundary. All process execution goes through the
//     unexported `runner` interface; production uses `execRunner` (a
//     thin `os/exec` shim), tests inject a fake. The cosign binary is a
//     swappable dependency and every branch is unit-testable without it.
//   - Explicit timeouts. Every invocation runs under a caller-supplied
//     context; `Client` additionally enforces a per-call ceiling so a
//     hung subprocess cannot wedge an export indefinitely.
//   - Curated env allowlist. The cosign subprocess does NOT inherit the
//     caller's full environment. Only an explicit allowlist (cloud-KMS
//     credential vars + PATH + HOME) is forwarded — see envAllowlist.
//   - No shell interpolation. Arguments are passed as an []string argv
//     to exec; there is no `sh -c` and no string concatenation of
//     untrusted input. The only operator-influenced value (the KMS key
//     reference) is validated and passed as a discrete argv element.
//   - Structured error mapping. cosign's exit codes / absence are mapped
//     to typed Go errors (ErrCosignNotFound, ErrSignFailed,
//     ErrVerifyFailed, ErrTimeout) so callers can branch without parsing
//     stderr.
//
// Scope (Phase 1, P0-413-1): KMS-backed signing/verification only. NO
// Fulcio, Rekor, OIDC, or keyless code lives here — that is slice 414.
package cosign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Typed errors returned by Client. Callers branch on these with
// errors.Is; the underlying cosign stderr is wrapped for diagnostics but
// never parsed for control flow.
var (
	// ErrCosignNotFound means the cosign binary is not on PATH (or the
	// configured BinaryPath does not resolve). The wrapper surfaces this
	// as a clear, actionable error rather than letting exec panic — the
	// operator's fix is "install cosign or switch to embedded-ed25519".
	ErrCosignNotFound = errors.New("cosign: binary not found on PATH (install cosign, or use the embedded-ed25519 signing mode)")

	// ErrSignFailed wraps any non-zero exit from `cosign sign-blob`.
	ErrSignFailed = errors.New("cosign: sign-blob failed")

	// ErrVerifyFailed wraps any non-zero exit from `cosign verify-blob`
	// (the signature did not validate, or the inputs were rejected).
	ErrVerifyFailed = errors.New("cosign: verify-blob failed")

	// ErrTimeout is returned when an invocation exceeds the deadline.
	ErrTimeout = errors.New("cosign: invocation timed out")

	// ErrBadConfig is returned by config validation (empty/malformed KMS
	// reference, etc.) before any subprocess is spawned.
	ErrBadConfig = errors.New("cosign: invalid configuration")
)

// runner is the injectable exec boundary. Production wires execRunner;
// tests inject a fake so every Client branch is exercised without the
// real cosign binary. stdin carries the blob to sign/verify; stdout
// carries the produced signature (sign) and is unused on verify.
type runner interface {
	run(ctx context.Context, bin string, env []string, stdin []byte, args ...string) (stdout []byte, stderr []byte, err error)
}

// execRunner is the production runner: a thin os/exec shim. It performs
// NO argument interpolation — args are passed straight to exec.Command,
// which does not invoke a shell.
type execRunner struct{}

func (execRunner) run(ctx context.Context, bin string, env []string, stdin []byte, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return []byte(outBuf.String()), []byte(errBuf.String()), err
}

// DefaultTimeout bounds a single cosign invocation. KMS round-trips are
// network calls; a few seconds is generous and a hung call must not
// block an export.
const DefaultTimeout = 30 * time.Second

// DefaultBinary is the cosign binary name resolved against PATH when no
// explicit path is configured.
const DefaultBinary = "cosign"

// Client invokes the cosign binary for KMS-backed blob signing and
// verification. Construct with New; it is safe for concurrent use (it
// holds no mutable state).
type Client struct {
	bin     string
	timeout time.Duration
	run     runner
	// extraEnv lets a deployment inject additional credential env vars
	// (looked up from the caller's environment) beyond the static
	// allowlist — e.g. a non-standard cloud-SDK variable. It is keys
	// only; values are read from os.Getenv at call time so secrets never
	// live on the Client.
	extraEnvKeys []string
}

// Option configures a Client.
type Option func(*Client)

// WithBinary overrides the cosign binary path/name (default "cosign",
// resolved on PATH).
func WithBinary(path string) Option {
	return func(c *Client) {
		if path != "" {
			c.bin = path
		}
	}
}

// WithTimeout overrides the per-invocation timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithExtraEnvKeys adds environment variable NAMES (not values) to the
// forwarded allowlist. Use for deployment-specific credential vars not
// in the static allowlist. Values are read from the process env at call
// time.
func WithExtraEnvKeys(keys ...string) Option {
	return func(c *Client) { c.extraEnvKeys = append(c.extraEnvKeys, keys...) }
}

// withRunner is unexported — only tests use it to inject a fake runner.
func withRunner(r runner) Option {
	return func(c *Client) { c.run = r }
}

// New builds a Client with conservative defaults (cosign on PATH,
// DefaultTimeout, the static env allowlist).
func New(opts ...Option) *Client {
	c := &Client{
		bin:     DefaultBinary,
		timeout: DefaultTimeout,
		run:     execRunner{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// envAllowlist is the static set of environment variable NAMES forwarded
// into the cosign subprocess. The caller's full environment is NOT
// inherited — only these (when present) plus any WithExtraEnvKeys are
// passed. The list is intentionally minimal and documented:
//
//   - PATH / HOME: cosign needs PATH to find sub-tools and HOME for the
//     cloud-SDK default credential/config search paths.
//   - AWS_*: AWS KMS provider credentials (static keys, STS session
//     token, region, profile, and the container/web-identity credential
//     paths used by IRSA / ECS task roles).
//   - GOOGLE_*/CLOUDSDK_*/GCLOUD_*: GCP KMS provider (application-default
//     credentials file + active project/config).
//   - AZURE_*: Azure Key Vault provider (service-principal client/tenant
//     /secret and the federated-token path used by workload identity).
//   - VAULT_*: HashiCorp Vault transit provider (addr + token).
//
// Anything not on this list (and not added via WithExtraEnvKeys) is
// withheld from the subprocess — the wrapper does not leak the atlas
// process's database DSN, OAuth signing keys, etc. into cosign.
var envAllowlist = []string{
	"PATH",
	"HOME",
	// AWS KMS
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_REGION",
	"AWS_DEFAULT_REGION",
	"AWS_PROFILE",
	"AWS_SHARED_CREDENTIALS_FILE",
	"AWS_CONFIG_FILE",
	"AWS_ROLE_ARN",
	"AWS_WEB_IDENTITY_TOKEN_FILE",
	"AWS_CONTAINER_CREDENTIALS_FULL_URI",
	"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	// GCP KMS
	"GOOGLE_APPLICATION_CREDENTIALS",
	"GOOGLE_CLOUD_PROJECT",
	"CLOUDSDK_CONFIG",
	"CLOUDSDK_CORE_PROJECT",
	"GCLOUD_PROJECT",
	// Azure Key Vault
	"AZURE_TENANT_ID",
	"AZURE_CLIENT_ID",
	"AZURE_CLIENT_SECRET",
	"AZURE_FEDERATED_TOKEN_FILE",
	"AZURE_AUTHORITY_HOST",
	// HashiCorp Vault transit
	"VAULT_ADDR",
	"VAULT_TOKEN",
}

// buildEnv constructs the curated subprocess environment from the
// allowlist plus any extra keys, reading values from the supplied lookup
// (os.Getenv in production; a fake in tests). Only set (non-empty)
// variables are forwarded.
func (c *Client) buildEnv(lookup func(string) string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(envAllowlist)+len(c.extraEnvKeys))
	add := func(key string) {
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if v := lookup(key); v != "" {
			out = append(out, key+"="+v)
		}
	}
	for _, k := range envAllowlist {
		add(k)
	}
	for _, k := range c.extraEnvKeys {
		add(k)
	}
	return out
}

// classifyRunErr maps a runner error + stderr to a typed wrapper error.
// A *exec.Error (binary not found) becomes ErrCosignNotFound; a context
// deadline becomes ErrTimeout; everything else becomes base (sign or
// verify) with stderr attached for diagnostics.
func classifyRunErr(ctx context.Context, base error, runErr error, stderr []byte) error {
	if runErr == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded || errors.Is(runErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrTimeout, runErr)
	}
	var execErr *exec.Error
	if errors.As(runErr, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return ErrCosignNotFound
	}
	if errors.Is(runErr, exec.ErrNotFound) {
		return ErrCosignNotFound
	}
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = runErr.Error()
	}
	return fmt.Errorf("%w: %s", base, msg)
}

// validateKMSRef performs a cheap, offline well-formedness check on a KMS
// key reference. It does NOT reach the network — reachability is a
// separate concern (see Client.CheckKMSRef). It rejects empty refs and
// refs that are obviously not a cosign KMS URI.
//
// cosign KMS references are provider-scheme URIs, e.g.:
//
//	awskms:///arn:aws:kms:us-east-1:111122223333:key/abcd-...
//	awskms:///alias/atlas-oscal
//	gcpkms://projects/p/locations/l/keyRings/r/cryptoKeys/k
//	azurekms://vault-name.vault.azure.net/keys/key-name
//	hashivault://transit-key
func validateKMSRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("%w: empty KMS key reference", ErrBadConfig)
	}
	knownSchemes := []string{"awskms://", "gcpkms://", "azurekms://", "hashivault://", "k8s://"}
	for _, s := range knownSchemes {
		if strings.HasPrefix(ref, s) {
			return nil
		}
	}
	return fmt.Errorf("%w: KMS reference %q does not use a known cosign provider scheme "+
		"(awskms://, gcpkms://, azurekms://, hashivault://)", ErrBadConfig, ref)
}

// Available reports whether the cosign binary resolves on PATH (or at the
// configured BinaryPath). It is a cheap pre-flight used by config-check
// and by the signer's mode selection. It does not run cosign.
func (c *Client) Available() bool {
	if strings.ContainsAny(c.bin, "/\\") {
		// Explicit path: stat it.
		info, err := os.Stat(c.bin)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(c.bin)
	return err == nil
}
