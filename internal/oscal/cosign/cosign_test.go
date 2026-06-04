package cosign

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeRunner is the injected exec boundary for unit tests. It records the
// call it received and returns canned output/error, so every Client
// branch is exercised without the real cosign binary.
type fakeRunner struct {
	wantStdout []byte
	wantStderr []byte
	wantErr    error

	gotBin   string
	gotEnv   []string
	gotStdin []byte
	gotArgs  []string
	calls    int
}

func (f *fakeRunner) run(_ context.Context, bin string, env []string, stdin []byte, args ...string) ([]byte, []byte, error) {
	f.calls++
	f.gotBin = bin
	f.gotEnv = env
	f.gotStdin = stdin
	f.gotArgs = args
	return f.wantStdout, f.wantStderr, f.wantErr
}

func TestSignBlob_HappyPath(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantStdout: []byte("BASE64SIG==\n")}
	c := New(withRunner(fr))
	sig, err := c.SignBlob(context.Background(), "awskms:///alias/atlas-oscal", []byte("digest-bytes"))
	if err != nil {
		t.Fatalf("SignBlob: %v", err)
	}
	if string(sig) != "BASE64SIG==" {
		t.Errorf("signature = %q, want trimmed BASE64SIG==", sig)
	}
	// argv must be the fixed, non-shell form with the KMS ref as a
	// discrete element and the blob on stdin.
	joined := strings.Join(fr.gotArgs, " ")
	for _, want := range []string{"sign-blob", "--key", "awskms:///alias/atlas-oscal", "--use-signing-config=false", "--tlog-upload=false"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
	if string(fr.gotStdin) != "digest-bytes" {
		t.Errorf("blob must be passed on stdin, got %q", fr.gotStdin)
	}
}

func TestSignBlob_RejectsEmptyBlob(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{}
	c := New(withRunner(fr))
	_, err := c.SignBlob(context.Background(), "awskms:///alias/k", nil)
	if !errors.Is(err, ErrBadConfig) {
		t.Fatalf("err = %v, want ErrBadConfig", err)
	}
	if fr.calls != 0 {
		t.Error("must not spawn cosign for an empty blob")
	}
}

func TestSignBlob_RejectsBadKMSRef(t *testing.T) {
	t.Parallel()
	cases := []string{"", "not-a-uri", "https://example.com/key", "kms:///alias/x"}
	for _, ref := range cases {
		fr := &fakeRunner{}
		c := New(withRunner(fr))
		_, err := c.SignBlob(context.Background(), ref, []byte("blob"))
		if !errors.Is(err, ErrBadConfig) {
			t.Errorf("ref %q: err = %v, want ErrBadConfig", ref, err)
		}
		if fr.calls != 0 {
			t.Errorf("ref %q: must not spawn cosign on a malformed ref", ref)
		}
	}
}

func TestSignBlob_AcceptsAllKnownSchemes(t *testing.T) {
	t.Parallel()
	refs := []string{
		"awskms:///arn:aws:kms:us-east-1:111122223333:key/abcd",
		"gcpkms://projects/p/locations/l/keyRings/r/cryptoKeys/k",
		"azurekms://vault.vault.azure.net/keys/key",
		"hashivault://transit-key",
	}
	for _, ref := range refs {
		fr := &fakeRunner{wantStdout: []byte("SIG==")}
		c := New(withRunner(fr))
		if _, err := c.SignBlob(context.Background(), ref, []byte("b")); err != nil {
			t.Errorf("ref %q: unexpected err %v", ref, err)
		}
	}
}

func TestSignBlob_EmptySignatureOutput(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantStdout: []byte("   \n")}
	c := New(withRunner(fr))
	_, err := c.SignBlob(context.Background(), "awskms:///alias/k", []byte("b"))
	if !errors.Is(err, ErrSignFailed) {
		t.Fatalf("err = %v, want ErrSignFailed for empty signature", err)
	}
}

func TestSignBlob_MapsBinaryNotFound(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantErr: &exec.Error{Name: "cosign", Err: exec.ErrNotFound}}
	c := New(withRunner(fr))
	_, err := c.SignBlob(context.Background(), "awskms:///alias/k", []byte("b"))
	if !errors.Is(err, ErrCosignNotFound) {
		t.Fatalf("err = %v, want ErrCosignNotFound", err)
	}
}

func TestSignBlob_MapsTimeout(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantErr: context.DeadlineExceeded}
	c := New(withRunner(fr), WithTimeout(time.Second))
	_, err := c.SignBlob(context.Background(), "awskms:///alias/k", []byte("b"))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestSignBlob_MapsGenericNonZeroExit(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantStderr: []byte("AccessDenied: kms:Sign"), wantErr: errors.New("exit status 1")}
	c := New(withRunner(fr))
	_, err := c.SignBlob(context.Background(), "awskms:///alias/k", []byte("b"))
	if !errors.Is(err, ErrSignFailed) {
		t.Fatalf("err = %v, want ErrSignFailed", err)
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err must carry stderr diagnostic, got %v", err)
	}
}

func TestVerifyBlob_HappyPath(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{}
	c := New(withRunner(fr))
	err := c.VerifyBlob(context.Background(), "awskms:///alias/k", []byte("blob"), []byte("SIG=="))
	if err != nil {
		t.Fatalf("VerifyBlob: %v", err)
	}
	joined := strings.Join(fr.gotArgs, " ")
	for _, want := range []string{"verify-blob", "--key", "--signature", "--insecure-ignore-tlog=true"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
	if string(fr.gotStdin) != "blob" {
		t.Errorf("blob must be on stdin, got %q", fr.gotStdin)
	}
}

func TestVerifyBlob_RejectsEmptyInputs(t *testing.T) {
	t.Parallel()
	c := New(withRunner(&fakeRunner{}))
	if err := c.VerifyBlob(context.Background(), "awskms:///alias/k", nil, []byte("s")); !errors.Is(err, ErrBadConfig) {
		t.Errorf("empty blob: err = %v, want ErrBadConfig", err)
	}
	if err := c.VerifyBlob(context.Background(), "awskms:///alias/k", []byte("b"), nil); !errors.Is(err, ErrVerifyFailed) {
		t.Errorf("empty sig: err = %v, want ErrVerifyFailed", err)
	}
	if err := c.VerifyBlob(context.Background(), "bad-ref", []byte("b"), []byte("s")); !errors.Is(err, ErrBadConfig) {
		t.Errorf("bad ref: err = %v, want ErrBadConfig", err)
	}
}

func TestVerifyBlob_MapsFailure(t *testing.T) {
	t.Parallel()
	fr := &fakeRunner{wantStderr: []byte("invalid signature"), wantErr: errors.New("exit status 1")}
	c := New(withRunner(fr))
	err := c.VerifyBlob(context.Background(), "awskms:///alias/k", []byte("b"), []byte("s"))
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("err = %v, want ErrVerifyFailed", err)
	}
}

func TestBuildEnv_AllowlistOnly(t *testing.T) {
	t.Parallel()
	c := New(WithExtraEnvKeys("MY_CUSTOM_KMS_TOKEN"))
	// Fake lookup: set a couple of allowlisted vars, the extra key, and a
	// secret that MUST NOT be forwarded.
	lookup := func(k string) string {
		switch k {
		case "PATH":
			return "/usr/bin"
		case "AWS_REGION":
			return "us-east-1"
		case "MY_CUSTOM_KMS_TOKEN":
			return "tok"
		case "DATABASE_URL_APP":
			return "postgres://secret"
		case "OSCAL_SIGNING_KEY":
			return "deadbeef"
		default:
			return ""
		}
	}
	env := c.buildEnv(lookup)
	joined := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/usr/bin", "AWS_REGION=us-east-1", "MY_CUSTOM_KMS_TOKEN=tok"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing allowlisted %q; got:\n%s", want, joined)
		}
	}
	for _, leak := range []string{"DATABASE_URL_APP", "OSCAL_SIGNING_KEY", "postgres://secret", "deadbeef"} {
		if strings.Contains(joined, leak) {
			t.Errorf("env LEAKED non-allowlisted %q; got:\n%s", leak, joined)
		}
	}
}

func TestBuildEnv_OmitsUnsetVars(t *testing.T) {
	t.Parallel()
	c := New()
	env := c.buildEnv(func(string) string { return "" })
	if len(env) != 0 {
		t.Errorf("buildEnv with all-unset lookup must be empty, got %v", env)
	}
}

func TestValidateKMSRef(t *testing.T) {
	t.Parallel()
	good := []string{"awskms:///alias/x", "gcpkms://p", "azurekms://v", "hashivault://k", "k8s://ns/name"}
	for _, g := range good {
		if err := validateKMSRef(g); err != nil {
			t.Errorf("validateKMSRef(%q) = %v, want nil", g, err)
		}
	}
	bad := []string{"", "  ", "file:///key", "alias/x"}
	for _, b := range bad {
		if err := validateKMSRef(b); !errors.Is(err, ErrBadConfig) {
			t.Errorf("validateKMSRef(%q) = %v, want ErrBadConfig", b, err)
		}
	}
}

func TestCheckKMSRef_BadRefShortCircuits(t *testing.T) {
	t.Parallel()
	c := New(withRunner(&fakeRunner{}))
	if err := c.CheckKMSRef(context.Background(), "nope"); !errors.Is(err, ErrBadConfig) {
		t.Fatalf("err = %v, want ErrBadConfig", err)
	}
}

func TestCheckKMSRef_ProbesSign(t *testing.T) {
	t.Parallel()
	// Available() uses real PATH resolution; only run the probe path when
	// cosign is genuinely present, otherwise assert the not-found branch.
	fr := &fakeRunner{wantStdout: []byte("SIG==")}
	c := New(withRunner(fr))
	err := c.CheckKMSRef(context.Background(), "awskms:///alias/k")
	if c.Available() {
		if err != nil {
			t.Fatalf("CheckKMSRef with present cosign + signing runner: %v", err)
		}
		if fr.calls == 0 {
			t.Error("CheckKMSRef must probe-sign when cosign is available")
		}
	} else if !errors.Is(err, ErrCosignNotFound) {
		t.Fatalf("CheckKMSRef without cosign: err = %v, want ErrCosignNotFound", err)
	}
}

func TestNew_OptionsApplied(t *testing.T) {
	t.Parallel()
	c := New(WithBinary("/opt/cosign"), WithTimeout(5*time.Second))
	if c.bin != "/opt/cosign" {
		t.Errorf("bin = %q, want /opt/cosign", c.bin)
	}
	if c.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.timeout)
	}
	// Zero/empty options are ignored (keep defaults).
	d := New(WithBinary(""), WithTimeout(0))
	if d.bin != DefaultBinary || d.timeout != DefaultTimeout {
		t.Errorf("zero options must not override defaults: bin=%q timeout=%v", d.bin, d.timeout)
	}
}

func TestAvailable_ExplicitPath(t *testing.T) {
	t.Parallel()
	// A path that does not exist must report unavailable.
	c := New(WithBinary("/nonexistent/cosign-xyz"))
	if c.Available() {
		t.Error("Available must be false for a missing explicit path")
	}
}
