//go:build integration

// LocalStack KMS round-trip integration test (slice 425).
//
// WHAT THIS CLOSES. Slice 413 shipped the `cosign-kms` signing mode, but
// its integration test (cosign_integration_test.go) uses a LOCAL-KEY
// stand-in: it drives the identical `sign-blob` argv through
// `--key cosign.key`, which CANNOT pass validateKMSRef (a local path is
// not a KMS URI) — so it bypasses Client.SignBlob's real provider path.
// The actual `awskms://` argv in signblob.go is therefore asserted only
// by argv-mirroring, never EXECUTED against a real KMS provider. This
// test drives the REAL Client.SignBlob / Client.VerifyBlob against
// LocalStack KMS via an `awskms:///alias/...` reference — exercising
// validateKMSRef + buildEnv + the production argv end to end — and proves
// the signature actually round-trips through `cosign verify-blob`.
//
// OPT-IN. This test is gated behind ATLAS_COSIGN_KMS_LOCALSTACK=1 plus a
// LocalStack endpoint (ATLAS_COSIGN_KMS_LOCALSTACK_ENDPOINT, default
// http://localhost:4566). When the gate is unset — or cosign / the AWS
// CLI are not on PATH, or LocalStack is unreachable — the test t.Skips
// with a clear note. It NEVER fails for an absent optional dependency, so
// the default `Go · integration` job (no LocalStack) stays green. The
// LocalStack-up leg is wired in CI (.github/workflows/ci.yml, leg B3).
//
// HERMETIC / NO REAL CLOUD. The env gate selects LocalStack explicitly;
// the test reaches only the configured sandbox endpoint and uses
// LocalStack dummy credentials. No real cloud KMS is ever contacted, no
// real key material lives in this file (P0-425-2, P0-425-3).
package cosign

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// LocalStack dummy credentials. LocalStack accepts any non-empty
// access-key/secret pair; these neutral literals are NOT real key
// material and carry NO vendor token prefix (GitGuardian-safe, P0-425-3 /
// AC-7). The secret value doubles as the no-leak sentinel (AC-6): the
// test asserts it never appears in cosign's captured output.
const (
	localstackAccessKey = "test"
	localstackSecretKey = "localstack-dummy-secret-do-not-use" //nolint:gosec // LocalStack dummy, not a real secret
	localstackRegion    = "us-east-1"
	localstackAliasName = "atlas-oscal-425"
)

// kmsLocalstackGate resolves the opt-in env gate. It returns the
// LocalStack endpoint and whether the test should run. When the gate is
// not requested it returns ok=false so the caller t.Skips.
func kmsLocalstackGate() (endpoint string, ok bool) {
	if os.Getenv("ATLAS_COSIGN_KMS_LOCALSTACK") != "1" {
		return "", false
	}
	endpoint = strings.TrimSpace(os.Getenv("ATLAS_COSIGN_KMS_LOCALSTACK_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:4566"
	}
	return endpoint, true
}

// requireLocalstackKMS gates the test: it skips (never fails) unless the
// env gate is set AND cosign + the aws CLI are on PATH AND LocalStack's
// KMS API answers. Every skip carries a note naming the missing piece.
func requireLocalstackKMS(t *testing.T) (endpoint string) {
	t.Helper()
	ep, ok := kmsLocalstackGate()
	if !ok {
		t.Skip("ATLAS_COSIGN_KMS_LOCALSTACK!=1 — skipping LocalStack KMS round-trip " +
			"(opt-in: set ATLAS_COSIGN_KMS_LOCALSTACK=1 with LocalStack up; see docs/runbooks/oscal-signing.md)")
	}
	if _, err := exec.LookPath(DefaultBinary); err != nil {
		t.Skipf("cosign binary not on PATH — skipping LocalStack KMS round-trip: %v", err)
	}
	if _, err := exec.LookPath("aws"); err != nil {
		t.Skipf("aws CLI not on PATH (needed to create the LocalStack KMS key) — skipping: %v", err)
	}
	// Cheap reachability probe: list keys against the endpoint. A failure
	// here means LocalStack is not up — skip, do not fail.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := awsKMS(ctx, t, ep, "list-keys"); err != nil {
		t.Skipf("LocalStack KMS at %s not reachable — skipping LocalStack KMS round-trip: %v\n%s", ep, err, out)
	}
	return ep
}

// awsEnv builds the LocalStack credential + endpoint environment shared by
// the aws CLI calls and (via WithExtraEnvKeys) the cosign subprocess.
func awsEnv(endpoint string) []string {
	return []string{
		"AWS_ACCESS_KEY_ID=" + localstackAccessKey,
		"AWS_SECRET_ACCESS_KEY=" + localstackSecretKey,
		"AWS_REGION=" + localstackRegion,
		"AWS_DEFAULT_REGION=" + localstackRegion,
		// cosign's awskms provider + the aws SDK both honor AWS_ENDPOINT_URL
		// (global) / AWS_ENDPOINT_URL_KMS (service-specific) to redirect to
		// the LocalStack sandbox instead of real AWS.
		"AWS_ENDPOINT_URL=" + endpoint,
		"AWS_ENDPOINT_URL_KMS=" + endpoint,
	}
}

// awsKMS runs an `aws kms <args...>` call against the LocalStack endpoint
// with dummy creds and returns combined output.
func awsKMS(ctx context.Context, t *testing.T, endpoint string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"kms", "--endpoint-url", endpoint, "--output", "text"}, args...)
	cmd := exec.CommandContext(ctx, "aws", full...)
	cmd.Env = append(os.Environ(), awsEnv(endpoint)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// createLocalstackSigningKey creates an ECDSA P-256 SIGN_VERIFY KMS key in
// LocalStack and points an alias at it, returning the cosign awskms://
// reference for that alias. The alias form keeps the cosign URI stable and
// human-readable; cosign resolves the alias against the LocalStack
// endpoint via the forwarded AWS_ENDPOINT_URL env.
func createLocalstackSigningKey(ctx context.Context, t *testing.T, endpoint string) string {
	t.Helper()
	// ECC_NIST_P256 / SIGN_VERIFY is the ECDSA spec cosign's awskms
	// provider signs with; it is what `cosign sign-blob --key awskms://...`
	// expects for a signing key.
	out, err := awsKMS(ctx, t, endpoint,
		"create-key", "--key-spec", "ECC_NIST_P256", "--key-usage", "SIGN_VERIFY",
		"--query", "KeyMetadata.KeyId")
	if err != nil {
		t.Fatalf("LocalStack create-key failed: %v\n%s", err, out)
	}
	keyID := strings.TrimSpace(out)
	if keyID == "" {
		t.Fatalf("LocalStack create-key returned empty KeyId\n%s", out)
	}
	// A uniquely-suffixed alias avoids collisions across reruns within the
	// same LocalStack instance.
	alias := "alias/" + localstackAliasName + "-" + sanitize(t.Name())
	if out, err := awsKMS(ctx, t, endpoint, "create-alias",
		"--alias-name", alias, "--target-key-id", keyID); err != nil {
		// An already-existing alias is fine — repoint it.
		if uerr := updateAlias(ctx, t, endpoint, alias, keyID); uerr != nil {
			t.Fatalf("LocalStack create-alias failed: %v\n%s (update fallback: %v)", err, out, uerr)
		}
	}
	// cosign awskms alias reference: awskms:///alias/<name>
	return "awskms:///" + alias
}

func updateAlias(ctx context.Context, t *testing.T, endpoint, alias, keyID string) error {
	t.Helper()
	_, err := awsKMS(ctx, t, endpoint, "update-alias",
		"--alias-name", alias, "--target-key-id", keyID)
	return err
}

// sanitize makes a test name safe for use inside a KMS alias (alphanumeric
// + a few separators only).
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// capturingRunner wraps execRunner and records every stdout+stderr+arg the
// cosign subprocess emits, so the no-leak assertion (AC-6) can inspect the
// full captured surface after the round-trip.
type capturingRunner struct {
	inner execRunner
	buf   bytes.Buffer
}

func (c *capturingRunner) run(ctx context.Context, bin string, env []string, stdin []byte, args ...string) ([]byte, []byte, error) {
	stdout, stderr, err := c.inner.run(ctx, bin, env, stdin, args...)
	c.buf.WriteString("ARGS: " + strings.Join(args, " ") + "\n")
	c.buf.Write(stdout)
	c.buf.WriteByte('\n')
	c.buf.Write(stderr)
	c.buf.WriteByte('\n')
	return stdout, stderr, err
}

func (c *capturingRunner) captured() string { return c.buf.String() }

// newLocalstackClient builds a cosign Client whose subprocess receives the
// LocalStack AWS_ENDPOINT_URL* env via WithExtraEnvKeys (the DESIGNED
// extension seam — slice 413 D2 — NOT a signblob.go change; P0-425-4). The
// credential env is set on the process for the duration of the test via
// t.Setenv so buildEnv's os.Getenv lookups resolve them.
func newLocalstackClient(t *testing.T, endpoint string) (*Client, *capturingRunner) {
	t.Helper()
	for _, kv := range awsEnv(endpoint) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cr := &capturingRunner{}
	c := New(
		withRunner(cr),
		WithExtraEnvKeys("AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_KMS"),
		WithTimeout(60*time.Second),
	)
	return c, cr
}

// TestIntegration_LocalStackKMS_SignVerifyRoundTrip drives the REAL
// Client.SignBlob / Client.VerifyBlob (the awskms:// provider path in
// signblob.go) against LocalStack KMS and asserts the round-trip verifies
// (AC-1, AC-2). This is the path the slice-413 local-key stand-in could
// not reach.
func TestIntegration_LocalStackKMS_SignVerifyRoundTrip(t *testing.T) {
	endpoint := requireLocalstackKMS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	kmsRef := createLocalstackSigningKey(ctx, t, endpoint)
	client, cr := newLocalstackClient(t, endpoint)

	blob := []byte("atlas-oscal-bundle-digest-localstack-roundtrip")

	// REAL provider path: validateKMSRef accepts awskms://, buildEnv
	// forwards the AWS_* + endpoint allowlist, the production argv runs
	// `cosign sign-blob --key awskms:///alias/... ...` against LocalStack.
	sig, err := client.SignBlob(ctx, kmsRef, blob)
	if err != nil {
		t.Fatalf("Client.SignBlob against LocalStack KMS (%s): %v", kmsRef, err)
	}
	if len(sig) == 0 {
		t.Fatal("SignBlob returned an empty signature")
	}

	// AC-2: round-trip — verify the unmodified blob succeeds.
	if err := client.VerifyBlob(ctx, kmsRef, blob, sig); err != nil {
		t.Fatalf("VerifyBlob of the freshly KMS-signed blob must succeed: %v\ncaptured:\n%s", err, cr.captured())
	}

	// AC-6: no-leak — the LocalStack secret must NOT appear in any captured
	// cosign output. (The dummy secret doubles as the sentinel.)
	assertNoCredentialLeak(t, cr.captured())
}

// TestIntegration_LocalStackKMS_VerifyFailsOnTamper proves tamper-evidence
// through the LocalStack-backed KMS key: a mutated blob must FAIL
// verification against a signature over the original (AC-3).
func TestIntegration_LocalStackKMS_VerifyFailsOnTamper(t *testing.T) {
	endpoint := requireLocalstackKMS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	kmsRef := createLocalstackSigningKey(ctx, t, endpoint)
	client, cr := newLocalstackClient(t, endpoint)

	original := []byte("original-localstack-digest")
	sig, err := client.SignBlob(ctx, kmsRef, original)
	if err != nil {
		t.Fatalf("Client.SignBlob against LocalStack KMS: %v", err)
	}

	// Verify a TAMPERED blob against the original signature — must fail.
	tampered := []byte("TAMPERED-localstack-digest")
	if err := client.VerifyBlob(ctx, kmsRef, tampered, sig); err == nil {
		t.Fatalf("VerifyBlob must FAIL for a tampered blob (tamper-evidence); captured:\n%s", cr.captured())
	}

	// AC-6 again: even on the failure path, no credential leak.
	assertNoCredentialLeak(t, cr.captured())
}

// assertNoCredentialLeak fails if the LocalStack secret appears anywhere in
// the captured cosign output (AC-6). The access key "test" is too generic
// to assert on (it is a common substring), so the sentinel is the secret
// value, which is unique enough to be a meaningful leak signal.
func assertNoCredentialLeak(t *testing.T, captured string) {
	t.Helper()
	// Guard the assertion against vacuity: the capturing runner always
	// records at least the argv line (and cosign emits a stderr
	// deprecation warning on sign), so an empty buffer means the runner
	// was never exercised and the no-leak check would be meaningless.
	if strings.TrimSpace(captured) == "" {
		t.Fatal("AC-6 no-leak: captured cosign output is empty — the no-leak check would be vacuous")
	}
	if strings.Contains(captured, localstackSecretKey) {
		t.Errorf("AC-6 no-leak: LocalStack secret material appeared in captured cosign output")
	}
}
