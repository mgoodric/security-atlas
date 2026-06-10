package webhookrecv_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// testSecret is a NEUTRAL test string — never a vendor-shaped literal (GitGuardian
// flags vendor-shaped fixtures).
const testSecret = "test-webhookrecv-shared-secret"

func rawHMAC(secret, body string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

func hdr(name, value string) http.Header {
	h := http.Header{}
	if value != "" {
		h.Set(name, value)
	}
	return h
}

func TestHMAC_HexAcceptsValid(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHex}
	body := []byte(`{"a":1}`)
	sig := hex.EncodeToString(rawHMAC(testSecret, string(body)))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
		t.Fatalf("valid hex rejected: %v", err)
	}
}

func TestHMAC_HexUpperAcceptsValid(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHexUpper}
	body := []byte(`{"a":1}`)
	sig := strings.ToUpper(hex.EncodeToString(rawHMAC(testSecret, string(body))))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
		t.Fatalf("valid hex-upper rejected: %v", err)
	}
}

func TestHMAC_Base64AcceptsValid(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingBase64}
	body := []byte(`{"a":1}`)
	sig := base64.StdEncoding.EncodeToString(rawHMAC(testSecret, string(body)))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
		t.Fatalf("valid base64 rejected: %v", err)
	}
}

func TestHMAC_StripsPrefix(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "sha256=", Encoding: webhookrecv.EncodingHex}
	body := []byte(`payload`)
	sig := "sha256=" + hex.EncodeToString(rawHMAC(testSecret, string(body)))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
		t.Fatalf("prefixed signature rejected: %v", err)
	}
}

func TestHMAC_RejectsUnsigned(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHex}
	if err := c.Verify([]byte(testSecret), []byte("body"), http.Header{}); !errors.Is(err, webhookrecv.ErrUnsigned) {
		t.Fatalf("want ErrUnsigned; got %v", err)
	}
}

func TestHMAC_RejectsWrongSecret(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHex}
	body := []byte(`{"a":1}`)
	sig := hex.EncodeToString(rawHMAC("attacker", string(body)))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature; got %v", err)
	}
}

func TestHMAC_RejectsTamperedBody(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHex}
	sig := hex.EncodeToString(rawHMAC(testSecret, `{"a":1}`))
	if err := c.Verify([]byte(testSecret), []byte(`{"a":2}`), hdr("X-Sig", sig)); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature on tampered body; got %v", err)
	}
}

func TestHMAC_RejectsMalformedHex(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingHex}
	if err := c.Verify([]byte(testSecret), []byte("body"), hdr("X-Sig", "zzzz")); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("malformed hex: want ErrBadSignature; got %v", err)
	}
}

func TestHMAC_RejectsMalformedBase64(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Encoding: webhookrecv.EncodingBase64}
	if err := c.Verify([]byte(testSecret), []byte("body"), hdr("X-Sig", "!!!notb64")); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("malformed base64: want ErrBadSignature; got %v", err)
	}
}

// Multi-signature (PagerDuty rotation) variants.

func TestHMAC_MultiAcceptsAnyMatch(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "v1=", Encoding: webhookrecv.EncodingHex, Multi: true}
	body := []byte(`{"a":1}`)
	active := "v1=" + hex.EncodeToString(rawHMAC(testSecret, string(body)))
	old := "v1=" + hex.EncodeToString(rawHMAC("previous", string(body)))
	cases := []string{
		active,
		old + "," + active,
		"v2=deadbeef," + active,
		"  " + active + "  ",
	}
	for _, sig := range cases {
		if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
			t.Fatalf("multi accept %q: %v", sig, err)
		}
	}
}

func TestHMAC_MultiUnknownSchemeOnlyIsUnsigned(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "v1=", Encoding: webhookrecv.EncodingHex, Multi: true}
	if err := c.Verify([]byte(testSecret), []byte("b"), hdr("X-Sig", "v2=deadbeef")); !errors.Is(err, webhookrecv.ErrUnsigned) {
		t.Fatalf("unknown-scheme-only: want ErrUnsigned; got %v", err)
	}
}

func TestHMAC_MultiNoMatchIsBad(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "v1=", Encoding: webhookrecv.EncodingHex, Multi: true}
	body := []byte(`{"a":1}`)
	wrong := "v1=" + hex.EncodeToString(rawHMAC("attacker", string(body)))
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", wrong)); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("multi no-match: want ErrBadSignature; got %v", err)
	}
}

func TestHMAC_MultiMalformedHexEntryIsBad(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "v1=", Encoding: webhookrecv.EncodingHex, Multi: true}
	if err := c.Verify([]byte(testSecret), []byte("b"), hdr("X-Sig", "v1=zzzz")); !errors.Is(err, webhookrecv.ErrBadSignature) {
		t.Fatalf("multi malformed-hex: want ErrBadSignature; got %v", err)
	}
}

// Sign round-trips for every encoding (the helper connectors' tests use).

func TestHMAC_SignRoundTrips(t *testing.T) {
	t.Parallel()
	for _, enc := range []webhookrecv.Encoding{webhookrecv.EncodingHex, webhookrecv.EncodingHexUpper, webhookrecv.EncodingBase64} {
		c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "sha256=", Encoding: enc}
		body := []byte(`round-trip`)
		sig := c.Sign([]byte(testSecret), body)
		if !strings.HasPrefix(sig, "sha256=") {
			t.Fatalf("enc %d: Sign did not prepend prefix: %q", enc, sig)
		}
		if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
			t.Fatalf("enc %d: Sign/Verify round-trip failed: %v", enc, err)
		}
	}
}

func TestHMAC_MultiSignRoundTrips(t *testing.T) {
	t.Parallel()
	c := webhookrecv.HMACConfig{Header: "X-Sig", Prefix: "v1=", Encoding: webhookrecv.EncodingHex, Multi: true}
	body := []byte(`multi`)
	sig := c.Sign([]byte(testSecret), body)
	if err := c.Verify([]byte(testSecret), body, hdr("X-Sig", sig)); err != nil {
		t.Fatalf("multi Sign/Verify round-trip failed: %v", err)
	}
}
