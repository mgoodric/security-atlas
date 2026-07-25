package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func sign(secret, body string, upper bool) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	s := hex.EncodeToString(mac.Sum(nil))
	if upper {
		return strings.ToUpper(s)
	}
	return s
}

func TestHMACVerifier_HexAcceptsValid(t *testing.T) {
	v := NewHMACVerifier(worker.HRISRippling, "test-webhook-secret", "X-Sig", "", EncodingHex)
	body := []byte(`{"worker_id":"w1"}`)
	h := http.Header{}
	h.Set("X-Sig", sign("test-webhook-secret", string(body), false))
	if err := v.Verify(body, h); err != nil {
		t.Fatalf("valid hex signature rejected: %v", err)
	}
	if v.Vendor() != worker.HRISRippling {
		t.Errorf("Vendor = %q", v.Vendor())
	}
}

func TestHMACVerifier_HexUpperAcceptsValid(t *testing.T) {
	v := NewHMACVerifier(worker.HRISBambooHR, "test-webhook-secret", "X-Sig", "", EncodingHexUpper)
	body := []byte(`{"id":"42"}`)
	h := http.Header{}
	h.Set("X-Sig", sign("test-webhook-secret", string(body), true))
	if err := v.Verify(body, h); err != nil {
		t.Fatalf("valid upper-hex signature rejected: %v", err)
	}
}

func TestHMACVerifier_StripsPrefix(t *testing.T) {
	v := NewHMACVerifier(worker.HRISRippling, "test-webhook-secret", "X-Sig", "sha256=", EncodingHex)
	body := []byte(`payload`)
	h := http.Header{}
	h.Set("X-Sig", "sha256="+sign("test-webhook-secret", string(body), false))
	if err := v.Verify(body, h); err != nil {
		t.Fatalf("prefixed signature rejected: %v", err)
	}
}

func TestHMACVerifier_RejectsUnsigned(t *testing.T) {
	v := NewHMACVerifier(worker.HRISRippling, "test-webhook-secret", "X-Sig", "", EncodingHex)
	if err := v.Verify([]byte("body"), http.Header{}); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("want ErrUnsigned; got %v", err)
	}
}

func TestHMACVerifier_RejectsWrongSecret(t *testing.T) {
	v := NewHMACVerifier(worker.HRISRippling, "test-webhook-secret", "X-Sig", "", EncodingHex)
	body := []byte(`{"worker_id":"w1"}`)
	h := http.Header{}
	h.Set("X-Sig", sign("attacker-secret", string(body), false))
	if err := v.Verify(body, h); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature; got %v", err)
	}
}

func TestHMACVerifier_RejectsTamperedBody(t *testing.T) {
	v := NewHMACVerifier(worker.HRISRippling, "test-webhook-secret", "X-Sig", "", EncodingHex)
	h := http.Header{}
	h.Set("X-Sig", sign("test-webhook-secret", `{"worker_id":"w1"}`, false))
	// Body differs from what was signed.
	if err := v.Verify([]byte(`{"worker_id":"w2"}`), h); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature on tampered body; got %v", err)
	}
}
