package cosign

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// keylessFakeRunner is a fake exec boundary for the keyless path. Unlike
// the kms fakeRunner, the keyless sign path reads the issued signature +
// certificate from the files cosign is told to write (--output-signature /
// --output-certificate), so this fake parses those argv flags and writes
// the canned sig/cert there — faithfully standing in for a Fulcio that
// issued a cert and a cosign that captured it, with NO real Sigstore.
type keylessFakeRunner struct {
	wantStderr   []byte
	wantErr      error
	sigContents  string
	certContents string

	gotArgs  []string
	gotStdin []byte
	gotEnv   []string
	calls    int
}

func (f *keylessFakeRunner) run(_ context.Context, _ string, env []string, stdin []byte, args ...string) ([]byte, []byte, error) {
	f.calls++
	f.gotArgs = args
	f.gotStdin = stdin
	f.gotEnv = env
	if f.wantErr != nil {
		return nil, f.wantStderr, f.wantErr
	}
	// Emulate cosign writing the captured signature + cert to the files it
	// was told to use.
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--output-signature":
			_ = os.WriteFile(args[i+1], []byte(f.sigContents), 0o600)
		case "--output-certificate":
			_ = os.WriteFile(args[i+1], []byte(f.certContents), 0o600)
		}
	}
	return nil, f.wantStderr, nil
}

const (
	testFulcio = "https://fulcio.atlas.internal"
	testRekor  = "https://rekor.atlas.internal"
)

func TestSignBlobKeyless_HappyPath(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{
		sigContents:  "BASE64KEYLESSSIG==",
		certContents: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		wantStderr:   []byte("Using signing certificate...\ntlog entry created with index: 4242\n"),
	}
	c := New(withRunner(fr))
	out, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob:          []byte("digest-bytes"),
		IdentityToken: "header.payload.sig",
		FulcioURL:     testFulcio,
		RekorURL:      testRekor,
	})
	if err != nil {
		t.Fatalf("SignBlobKeyless: %v", err)
	}
	if string(out.Signature) != "BASE64KEYLESSSIG==" {
		t.Errorf("signature = %q", out.Signature)
	}
	if !strings.Contains(out.CertificatePEM, "BEGIN CERTIFICATE") {
		t.Errorf("certificate = %q", out.CertificatePEM)
	}
	if out.RekorLogIndex != 4242 {
		t.Errorf("rekor log index = %d, want 4242", out.RekorLogIndex)
	}
	// The blob is on stdin; the token must NOT appear on the argv (it is
	// passed by file).
	joined := strings.Join(fr.gotArgs, " ")
	for _, want := range []string{"sign-blob", "--fulcio-url", testFulcio, "--rekor-url", testRekor, "--identity-token", "--tlog-upload=true"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "header.payload.sig") {
		t.Error("identity token must be passed by FILE, never on the argv")
	}
	if string(fr.gotStdin) != "digest-bytes" {
		t.Errorf("blob must be on stdin, got %q", fr.gotStdin)
	}
}

func TestSignBlobKeyless_RequiresPrivateEndpoints(t *testing.T) {
	t.Parallel()
	cases := map[string]KeylessSignParams{
		"missing fulcio":  {Blob: []byte("b"), IdentityToken: "t", RekorURL: testRekor},
		"missing rekor":   {Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio},
		"non-http fulcio": {Blob: []byte("b"), IdentityToken: "t", FulcioURL: "fulcio.local", RekorURL: testRekor},
	}
	for name, p := range cases {
		fr := &keylessFakeRunner{}
		c := New(withRunner(fr))
		_, err := c.SignBlobKeyless(context.Background(), p)
		if !errors.Is(err, ErrKeylessConfig) {
			t.Errorf("%s: err = %v, want ErrKeylessConfig", name, err)
		}
		if fr.calls != 0 {
			t.Errorf("%s: must not spawn cosign on a config error", name)
		}
	}
}

func TestSignBlobKeyless_RejectsEmptyBlobAndToken(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{}
	c := New(withRunner(fr))
	if _, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	}); !errors.Is(err, ErrBadConfig) {
		t.Errorf("empty blob: err = %v, want ErrBadConfig", err)
	}
	if _, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), FulcioURL: testFulcio, RekorURL: testRekor,
	}); !errors.Is(err, ErrKeylessConfig) {
		t.Errorf("empty token: err = %v, want ErrKeylessConfig", err)
	}
	if fr.calls != 0 {
		t.Error("must not spawn cosign on input validation failures")
	}
}

func TestSignBlobKeyless_PropagatesCosignError(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{wantErr: errors.New("exit 1"), wantStderr: []byte("fulcio: connection refused")}
	c := New(withRunner(fr))
	_, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	})
	if !errors.Is(err, ErrSignFailed) {
		t.Fatalf("err = %v, want ErrSignFailed", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should carry cosign stderr, got %v", err)
	}
}

func TestSignBlobKeyless_BinaryNotFound(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{wantErr: &exec.Error{Name: "cosign", Err: exec.ErrNotFound}}
	c := New(withRunner(fr))
	_, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	})
	if !errors.Is(err, ErrCosignNotFound) {
		t.Fatalf("err = %v, want ErrCosignNotFound", err)
	}
}

func TestSignBlobKeyless_EmptyOutputsFail(t *testing.T) {
	t.Parallel()
	// cosign "succeeds" but writes no signature → ErrSignFailed.
	fr := &keylessFakeRunner{sigContents: "", certContents: "cert"}
	c := New(withRunner(fr))
	if _, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	}); !errors.Is(err, ErrSignFailed) {
		t.Errorf("empty signature: err = %v, want ErrSignFailed", err)
	}
	// cosign writes a sig but no cert → ErrSignFailed.
	fr2 := &keylessFakeRunner{sigContents: "sig", certContents: ""}
	c2 := New(withRunner(fr2))
	if _, err := c2.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	}); !errors.Is(err, ErrSignFailed) {
		t.Errorf("empty cert: err = %v, want ErrSignFailed", err)
	}
}

func TestSignBlobKeyless_MissingRekorIndexIsNotFatal(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{sigContents: "sig", certContents: "cert", wantStderr: []byte("no index line here")}
	c := New(withRunner(fr))
	out, err := c.SignBlobKeyless(context.Background(), KeylessSignParams{
		Blob: []byte("b"), IdentityToken: "t", FulcioURL: testFulcio, RekorURL: testRekor,
	})
	if err != nil {
		t.Fatalf("SignBlobKeyless should not fail on a missing index: %v", err)
	}
	if out.RekorLogIndex != -1 {
		t.Errorf("rekor index = %d, want -1 (not captured)", out.RekorLogIndex)
	}
}

func TestVerifyBlobKeyless_HappyPath(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{}
	c := New(withRunner(fr))
	err := c.VerifyBlobKeyless(context.Background(), KeylessVerifyParams{
		Blob:                  []byte("digest-bytes"),
		Signature:             []byte("BASE64SIG=="),
		CertificatePEM:        "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----",
		CertificateIdentity:   "https://atlas.example/client:oscal-signer",
		CertificateOIDCIssuer: "https://atlas.example/oauth",
		RekorURL:              testRekor,
	})
	if err != nil {
		t.Fatalf("VerifyBlobKeyless: %v", err)
	}
	joined := strings.Join(fr.gotArgs, " ")
	for _, want := range []string{"verify-blob", "--certificate", "--certificate-identity", "https://atlas.example/client:oscal-signer", "--certificate-oidc-issuer", "--rekor-url", testRekor} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
	if string(fr.gotStdin) != "digest-bytes" {
		t.Errorf("blob must be on stdin, got %q", fr.gotStdin)
	}
}

func TestVerifyBlobKeyless_Validation(t *testing.T) {
	t.Parallel()
	full := KeylessVerifyParams{
		Blob:                  []byte("b"),
		Signature:             []byte("sig"),
		CertificatePEM:        "cert",
		CertificateIdentity:   "id",
		CertificateOIDCIssuer: "iss",
		RekorURL:              testRekor,
	}
	cases := map[string]func(KeylessVerifyParams) KeylessVerifyParams{
		"missing rekor":    func(p KeylessVerifyParams) KeylessVerifyParams { p.RekorURL = ""; return p },
		"empty blob":       func(p KeylessVerifyParams) KeylessVerifyParams { p.Blob = nil; return p },
		"empty signature":  func(p KeylessVerifyParams) KeylessVerifyParams { p.Signature = nil; return p },
		"empty cert":       func(p KeylessVerifyParams) KeylessVerifyParams { p.CertificatePEM = ""; return p },
		"missing identity": func(p KeylessVerifyParams) KeylessVerifyParams { p.CertificateIdentity = ""; return p },
		"missing issuer":   func(p KeylessVerifyParams) KeylessVerifyParams { p.CertificateOIDCIssuer = ""; return p },
	}
	for name, mutate := range cases {
		fr := &keylessFakeRunner{}
		c := New(withRunner(fr))
		if err := c.VerifyBlobKeyless(context.Background(), mutate(full)); err == nil {
			t.Errorf("%s: VerifyBlobKeyless must fail", name)
		}
		if fr.calls != 0 {
			t.Errorf("%s: must not spawn cosign on a validation failure", name)
		}
	}
}

func TestVerifyBlobKeyless_PropagatesFailure(t *testing.T) {
	t.Parallel()
	fr := &keylessFakeRunner{wantErr: errors.New("exit 1"), wantStderr: []byte("certificate identity mismatch")}
	c := New(withRunner(fr))
	err := c.VerifyBlobKeyless(context.Background(), KeylessVerifyParams{
		Blob: []byte("b"), Signature: []byte("sig"), CertificatePEM: "cert",
		CertificateIdentity: "id", CertificateOIDCIssuer: "iss", RekorURL: testRekor,
	})
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("err = %v, want ErrVerifyFailed", err)
	}
	if !strings.Contains(err.Error(), "identity mismatch") {
		t.Errorf("error should carry cosign stderr, got %v", err)
	}
}

func TestParseRekorLogIndex(t *testing.T) {
	t.Parallel()
	cases := map[string]int64{
		"tlog entry created with index: 0\n":                0,
		"prefix\ntlog entry created with index: 99\nsuffix": 99,
		"tlog entry created with index: 12345 (extra)":      12345,
		"no marker at all":                                  -1,
		"tlog entry created with index: notanumber":         -1,
		"": -1,
	}
	for in, want := range cases {
		if got := parseRekorLogIndex([]byte(in)); got != want {
			t.Errorf("parseRekorLogIndex(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestValidateKeylessEndpoints(t *testing.T) {
	t.Parallel()
	if err := validateKeylessEndpoints("https://f", "https://r"); err != nil {
		t.Errorf("valid https endpoints rejected: %v", err)
	}
	if err := validateKeylessEndpoints("http://f", "http://r"); err != nil {
		t.Errorf("valid http endpoints rejected: %v", err)
	}
	if err := validateKeylessEndpoints("", "https://r"); !errors.Is(err, ErrKeylessConfig) {
		t.Errorf("empty fulcio: err = %v", err)
	}
	if err := validateKeylessEndpoints("ftp://f", "https://r"); !errors.Is(err, ErrKeylessConfig) {
		t.Errorf("non-http fulcio: err = %v", err)
	}
}

func TestWritePrivateTemp(t *testing.T) {
	t.Parallel()
	path, cleanup, err := writePrivateTemp("atlas-test-*.tmp", []byte("secret"))
	if err != nil {
		t.Fatalf("writePrivateTemp: %v", err)
	}
	defer cleanup()
	got, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatalf("read temp: %v", err)
	}
	if string(got) != "secret" {
		t.Errorf("contents = %q, want secret", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup must remove the temp file")
	}
}

func TestWritePrivateTemp_CreateError(t *testing.T) {
	t.Parallel()
	// A pattern containing a path separator forces os.CreateTemp to fail
	// (it rejects patterns with separators), exercising the error branch.
	_, _, err := writePrivateTemp("nonexistent-dir/sub/atlas-*.tmp", []byte("x"))
	if err == nil {
		t.Error("writePrivateTemp must surface a CreateTemp error")
	}
}

func TestWritePrivateTemp_NoContents(t *testing.T) {
	t.Parallel()
	// The nil-contents branch (the sign path creates empty output files).
	path, cleanup, err := writePrivateTemp("atlas-empty-*.tmp", nil)
	if err != nil {
		t.Fatalf("writePrivateTemp(nil): %v", err)
	}
	defer cleanup()
	got, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %q", got)
	}
}
