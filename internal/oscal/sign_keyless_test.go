package oscal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- fakes -------------------------------------------------------------

// fakeKeylessBackend stands in for the cosign keyless wrapper with NO real
// Fulcio/Rekor: SignBlobKeyless records the blob and returns a canned cert
// + signature + Rekor index; VerifyBlobKeyless checks the blob/signature
// match what was "signed". This exercises the KeylessSigner / adapter /
// dispatch / manifest logic with no binary and no network.
type fakeKeylessBackend struct {
	certPEM   string
	rekorIdx  int64
	signed    map[string][]byte // signature -> blob signed
	signErr   error
	verifyErr error
	signCalls int
	verCalls  int
}

func newFakeKeylessBackend(certPEM string) *fakeKeylessBackend {
	return &fakeKeylessBackend{certPEM: certPEM, rekorIdx: 7, signed: map[string][]byte{}}
}

func (f *fakeKeylessBackend) SignBlobKeyless(_ context.Context, blob []byte, _, _, _ string) ([]byte, string, int64, error) {
	f.signCalls++
	if f.signErr != nil {
		return nil, "", 0, f.signErr
	}
	sig := []byte("fake-keyless-sig")
	cp := make([]byte, len(blob))
	copy(cp, blob)
	f.signed[string(sig)] = cp
	return sig, f.certPEM, f.rekorIdx, nil
}

func (f *fakeKeylessBackend) VerifyBlobKeyless(_ context.Context, blob, signature []byte, _, _, _, _ string) error {
	f.verCalls++
	if f.verifyErr != nil {
		return f.verifyErr
	}
	prev, ok := f.signed[string(signature)]
	if !ok || string(prev) != string(blob) {
		return errors.New("fake: blob does not match what was signed")
	}
	return nil
}

// fakeTokenSource yields a canned OIDC token (or an error / empty token to
// exercise those branches).
type fakeTokenSource struct {
	token string
	err   error
	calls int
}

func (f *fakeTokenSource) IdentityToken(_ context.Context) (string, error) {
	f.calls++
	return f.token, f.err
}

// --- test cert ---------------------------------------------------------

const (
	testKeylessIdentity = "https://atlas.example.com/client:oscal-signer"
	testKeylessIssuer   = "https://atlas.example.com/oauth"
	testFulcioURL       = "https://fulcio.atlas.internal"
	testRekorURL        = "https://rekor.atlas.internal"
)

// makeFulcioLikeCert produces a PEM cert with a URI SAN (the signing
// identity) and the Fulcio "Issuer (V2)" extension (DER UTF8String) so the
// production certIdentityAndIssuer parser is exercised against a real,
// correctly-encoded cert — no live Fulcio needed.
func makeFulcioLikeCert(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	u, _ := url.Parse(testKeylessIdentity)
	issuerExtVal, err := asn1.Marshal(testKeylessIssuer)
	if err != nil {
		t.Fatalf("marshal issuer ext: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "atlas-oscal-signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{u},
		ExtraExtensions: []pkix.Extension{
			{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}, Value: issuerExtVal},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func newKeylessSignerForTest(t *testing.T, backend *fakeKeylessBackend, ts IdentityTokenSource) *KeylessSigner {
	t.Helper()
	adapter := NewCosignKeylessAdapter(backend)
	signer, err := NewKeylessSigner(adapter, ts, testFulcioURL, testRekorURL)
	if err != nil {
		t.Fatalf("NewKeylessSigner: %v", err)
	}
	return signer
}

// --- tests -------------------------------------------------------------

func TestKeylessSigner_SignAndVerifyRoundTrips(t *testing.T) {
	t.Parallel()
	cert := makeFulcioLikeCert(t)
	backend := newFakeKeylessBackend(cert)
	ts := &fakeTokenSource{token: "header.payload.sig"}
	signer := newKeylessSignerForTest(t, backend, ts)

	b := testBundle(t)
	sig, err := signer.SignBundle(context.Background(), b)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	b.Signature = sig

	if sig.Mode != ModeCosignKeyless {
		t.Errorf("mode = %q, want %q", sig.Mode, ModeCosignKeyless)
	}
	if sig.Algorithm != "cosign-keyless" {
		t.Errorf("algorithm = %q, want cosign-keyless", sig.Algorithm)
	}
	if sig.Keyless == nil {
		t.Fatal("keyless attestation must be recorded")
	}
	if sig.Keyless.RekorLogIndex != 7 {
		t.Errorf("rekor_log_index = %d, want 7", sig.Keyless.RekorLogIndex)
	}
	if sig.Keyless.CertificateIdentity != testKeylessIdentity {
		t.Errorf("cert identity = %q, want %q (parsed from the cert SAN)", sig.Keyless.CertificateIdentity, testKeylessIdentity)
	}
	if sig.Keyless.CertificateOIDCIssuer != testKeylessIssuer {
		t.Errorf("cert issuer = %q, want %q (parsed from the Fulcio issuer ext)", sig.Keyless.CertificateOIDCIssuer, testKeylessIssuer)
	}
	if sig.Keyless.FulcioURL != testFulcioURL || sig.Keyless.RekorURL != testRekorURL {
		t.Errorf("manifest must record the operator's private Sigstore endpoints, got fulcio=%q rekor=%q", sig.Keyless.FulcioURL, sig.Keyless.RekorURL)
	}
	if sig.PublicKey != "" || sig.KeyRef != "" {
		t.Errorf("keyless must not carry an embedded public key or KMS ref, got pub=%q ref=%q", sig.PublicKey, sig.KeyRef)
	}
	if ts.calls != 1 {
		t.Errorf("identity token source called %d times, want 1", ts.calls)
	}

	verifier := NewCosignKeylessAdapter(backend)
	if err := VerifyBundleWithCosign(context.Background(), b, kvOnly{verifier}); err != nil {
		t.Errorf("VerifyBundleWithCosign on a keyless bundle: %v", err)
	}
}

// kvOnly is a keyless-capable verifier passed to VerifyBundleWithCosign
// (whose param is the kms CosignVerifier). Its VerifyBlob (the kms surface)
// is a no-op that fails closed — it is never reached for a keyless bundle,
// since the dispatch type-asserts to CosignKeylessVerifier and calls
// VerifyBlobKeyless. This proves the keyless dispatch path works through a
// verifier whose ONLY real capability is keyless.
type kvOnly struct{ v CosignKeylessVerifier }

func (k kvOnly) VerifyBlob(_ context.Context, _ string, _, _ []byte) error {
	return ErrCosignVerifierRequired
}

func (k kvOnly) VerifyBlobKeyless(ctx context.Context, req KeylessVerifyRequest) error {
	return k.v.VerifyBlobKeyless(ctx, req)
}

func TestKeylessSigner_DetectsTamperBeforeCosign(t *testing.T) {
	t.Parallel()
	backend := newFakeKeylessBackend(makeFulcioLikeCert(t))
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "tok"})
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	b.Signature = sig
	b.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"tampered":true}`))
	verCallsBefore := backend.verCalls
	if err := VerifyBundleWithCosign(context.Background(), b, kvOnly{NewCosignKeylessAdapter(backend)}); err == nil {
		t.Fatal("VerifyBundleWithCosign must fail on a tampered member")
	}
	if backend.verCalls != verCallsBefore {
		t.Error("digest mismatch must reject before invoking cosign verify")
	}
}

func TestKeylessSigner_VerifyFailsOnBadSignature(t *testing.T) {
	t.Parallel()
	backend := newFakeKeylessBackend(makeFulcioLikeCert(t))
	backend.verifyErr = errors.New("cosign: signature did not verify")
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "tok"})
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	b.Signature = sig
	if err := VerifyBundleWithCosign(context.Background(), b, kvOnly{NewCosignKeylessAdapter(backend)}); err == nil {
		t.Fatal("VerifyBundleWithCosign must fail when cosign rejects the signature")
	}
}

func TestNewKeylessSigner_Validation(t *testing.T) {
	t.Parallel()
	adapter := NewCosignKeylessAdapter(newFakeKeylessBackend("cert"))
	ts := &fakeTokenSource{token: "tok"}
	if _, err := NewKeylessSigner(nil, ts, testFulcioURL, testRekorURL); err == nil {
		t.Error("must reject a nil client")
	}
	if _, err := NewKeylessSigner(adapter, nil, testFulcioURL, testRekorURL); err == nil {
		t.Error("must reject a nil token source")
	}
	if _, err := NewKeylessSigner(adapter, ts, "", testRekorURL); !errors.Is(err, ErrKeylessNotConfigured) {
		t.Errorf("missing fulcio: err = %v, want ErrKeylessNotConfigured", err)
	}
	if _, err := NewKeylessSigner(adapter, ts, testFulcioURL, "  "); !errors.Is(err, ErrKeylessNotConfigured) {
		t.Errorf("blank rekor: err = %v, want ErrKeylessNotConfigured", err)
	}
}

func TestKeylessSigner_RejectsEmptyBundle(t *testing.T) {
	t.Parallel()
	signer := newKeylessSignerForTest(t, newFakeKeylessBackend("cert"), &fakeTokenSource{token: "tok"})
	if _, err := signer.SignBundle(context.Background(), &Bundle{}); err == nil {
		t.Error("SignBundle must reject an empty bundle")
	}
}

func TestKeylessSigner_TokenSourceErrors(t *testing.T) {
	t.Parallel()
	// Token source returns an error.
	s1 := newKeylessSignerForTest(t, newFakeKeylessBackend("cert"), &fakeTokenSource{err: errors.New("AS unreachable")})
	if _, err := s1.SignBundle(context.Background(), testBundle(t)); err == nil {
		t.Error("SignBundle must surface a token-source error")
	}
	// Token source returns an empty token.
	s2 := newKeylessSignerForTest(t, newFakeKeylessBackend("cert"), &fakeTokenSource{token: "  "})
	if _, err := s2.SignBundle(context.Background(), testBundle(t)); err == nil {
		t.Error("SignBundle must reject an empty token")
	}
}

func TestKeylessSigner_SignPropagatesCosignError(t *testing.T) {
	t.Parallel()
	backend := newFakeKeylessBackend("cert")
	backend.signErr = errors.New("fulcio: 503")
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "tok"})
	if _, err := signer.SignBundle(context.Background(), testBundle(t)); err == nil {
		t.Fatal("SignBundle must surface a cosign sign error")
	}
}

func TestKeylessSigner_RejectsEmptyCert(t *testing.T) {
	t.Parallel()
	// Backend returns an empty cert → sign must fail (a keyless signature
	// with no cert is unusable).
	signer := newKeylessSignerForTest(t, newFakeKeylessBackend(""), &fakeTokenSource{token: "tok"})
	if _, err := signer.SignBundle(context.Background(), testBundle(t)); err == nil {
		t.Error("SignBundle must reject a sign that produced no certificate")
	}
}

func TestVerifyCosignKeyless_RequiresVerifier(t *testing.T) {
	t.Parallel()
	backend := newFakeKeylessBackend(makeFulcioLikeCert(t))
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "tok"})
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	b.Signature = sig

	// nil verifier → fail closed.
	if err := VerifyBundleWithCosign(context.Background(), b, nil); !errors.Is(err, ErrCosignVerifierRequired) {
		t.Fatalf("nil verifier: err = %v, want ErrCosignVerifierRequired", err)
	}
	// A verifier that only implements the kms surface must NOT silently pass
	// a keyless bundle — it fails closed.
	if err := VerifyBundleWithCosign(context.Background(), b, newFakeCosign()); !errors.Is(err, ErrCosignVerifierRequired) {
		t.Fatalf("kms-only verifier on a keyless bundle: err = %v, want ErrCosignVerifierRequired", err)
	}
	// The embedded-only VerifyBundle must also refuse a keyless bundle.
	if err := VerifyBundle(b); !errors.Is(err, ErrCosignVerifierRequired) {
		t.Fatalf("VerifyBundle on a keyless bundle: err = %v, want ErrCosignVerifierRequired", err)
	}
}

func TestVerifyCosignKeyless_MalformedFields(t *testing.T) {
	t.Parallel()
	backend := newFakeKeylessBackend(makeFulcioLikeCert(t))
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "tok"})
	good, _ := signer.SignBundle(context.Background(), testBundle(t))

	cases := map[string]func(s Signature) Signature{
		"nil attestation":  func(s Signature) Signature { s.Keyless = nil; return s },
		"empty cert":       func(s Signature) Signature { c := *s.Keyless; c.Certificate = ""; s.Keyless = &c; return s },
		"empty identity":   func(s Signature) Signature { c := *s.Keyless; c.CertificateIdentity = ""; s.Keyless = &c; return s },
		"empty issuer":     func(s Signature) Signature { c := *s.Keyless; c.CertificateOIDCIssuer = ""; s.Keyless = &c; return s },
		"empty signature":  func(s Signature) Signature { s.Signature = ""; return s },
		"bad algorithm":    func(s Signature) Signature { s.Algorithm = "ed25519"; return s },
		"malformed digest": func(s Signature) Signature { s.Digest = "not-hex"; return s },
	}
	for name, mutate := range cases {
		b := testBundle(t)
		// deep-ish copy of the good signature
		sig := good
		b.Signature = mutate(sig)
		if err := VerifyBundleWithCosign(context.Background(), b, kvOnly{NewCosignKeylessAdapter(backend)}); err == nil {
			t.Errorf("%s: VerifyBundleWithCosign must fail", name)
		}
	}
}

func TestCertIdentityAndIssuer(t *testing.T) {
	t.Parallel()
	id, iss := certIdentityAndIssuer(makeFulcioLikeCert(t))
	if id != testKeylessIdentity {
		t.Errorf("identity = %q, want %q", id, testKeylessIdentity)
	}
	if iss != testKeylessIssuer {
		t.Errorf("issuer = %q, want %q", iss, testKeylessIssuer)
	}
	// Garbage input → empty, not a panic.
	if id, iss := certIdentityAndIssuer("not a pem"); id != "" || iss != "" {
		t.Errorf("garbage cert should yield empty, got id=%q iss=%q", id, iss)
	}
	if id, iss := certIdentityAndIssuer("-----BEGIN CERTIFICATE-----\nbm90LWRlcg==\n-----END CERTIFICATE-----"); id != "" || iss != "" {
		t.Errorf("undecodable DER should yield empty, got id=%q iss=%q", id, iss)
	}
}

func TestCertIdentityAndIssuer_LegacyIssuerExtension(t *testing.T) {
	t.Parallel()
	// A cert using the legacy 57264.1.1 issuer extension (raw string, not
	// DER UTF8String) must still resolve.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	u, _ := url.Parse(testKeylessIdentity)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{u},
		ExtraExtensions: []pkix.Extension{
			{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}, Value: []byte("https://legacy.issuer")},
		},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	id, iss := certIdentityAndIssuer(certPEM)
	if id != testKeylessIdentity {
		t.Errorf("identity = %q, want %q", id, testKeylessIdentity)
	}
	if iss != "https://legacy.issuer" {
		t.Errorf("legacy issuer = %q, want https://legacy.issuer", iss)
	}
}

func TestResolveMode_Keyless(t *testing.T) {
	t.Parallel()
	if got := ResolveMode(ModeCosignKeyless); got != ModeCosignKeyless {
		t.Errorf("ResolveMode(keyless) = %q, want keyless", got)
	}
}

// TestKeylessManifest_OmittedForOtherModes is the load-bearing
// backward-compat assertion: a non-keyless Signature marshals WITHOUT a
// `keyless` key (omitempty), so pre-414 and kms/embedded manifests are
// byte-identical to before this slice.
func TestKeylessManifest_OmittedForOtherModes(t *testing.T) {
	t.Parallel()
	signer, _ := NewEphemeralSigner()
	b := testBundle(t)
	sig, _ := signer.SignBundle(b)
	if sig.Keyless != nil {
		t.Fatal("embedded signature must not carry a keyless attestation")
	}
	js, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(js), "keyless") {
		t.Errorf("embedded signature JSON must omit the keyless key, got %s", js)
	}
}
