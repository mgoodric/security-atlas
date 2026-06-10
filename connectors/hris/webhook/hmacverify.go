package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// ErrUnsigned is returned when a delivery carries no signature header. It is the
// dominant rejection: an anonymous POST to the receiver is unsigned and stops
// here, before any record is built.
var ErrUnsigned = errors.New("webhook: delivery carries no signature")

// ErrBadSignature is returned when a signature is present but does not match the
// HMAC computed over the body with the subscription secret (a forged or
// tampered delivery).
var ErrBadSignature = errors.New("webhook: signature does not match")

// SigEncoding is how a vendor encodes the HMAC digest in its signature header.
type SigEncoding int

const (
	// EncodingHex is a lowercase hex digest (Rippling's scheme).
	EncodingHex SigEncoding = iota
	// EncodingHexUpper is an uppercase hex digest (BambooHR's documented scheme).
	EncodingHexUpper
)

// HMACVerifier verifies a delivery by recomputing HMAC-SHA256 over the raw body
// with a per-subscription shared secret and comparing it, in constant time,
// against the digest the vendor placed in HeaderName. It is the reusable core
// both HRIS verifiers (and the future PagerDuty 540 / MDM 557 receivers) build
// on. The secret never appears in a log line or the HTTP response.
//
// Some vendors prefix the header value (e.g. "sha256="); Prefix is stripped
// before the compare. Encoding selects hex casing.
type HMACVerifier struct {
	// HeaderName is the request header carrying the vendor's signature.
	HeaderName string
	// Prefix is stripped from the header value before comparison (e.g.
	// "sha256="). Empty means the header value is the bare digest.
	Prefix string
	// Encoding selects how the digest is encoded in the header.
	Encoding SigEncoding
	// secret is the per-subscription shared secret. Unexported so a stray %v
	// cannot leak it.
	secret []byte
	// vendor is the source HRIS for attribution.
	vendor worker.HRIS
}

// NewHMACVerifier builds an HMACVerifier. secret is the per-subscription shared
// secret (from the connector's environment, never a flag, never logged).
func NewHMACVerifier(vendor worker.HRIS, secret, headerName, prefix string, enc SigEncoding) *HMACVerifier {
	return &HMACVerifier{
		HeaderName: headerName,
		Prefix:     prefix,
		Encoding:   enc,
		secret:     []byte(secret),
		vendor:     vendor,
	}
}

// Vendor implements Verifier.
func (v *HMACVerifier) Vendor() worker.HRIS { return v.vendor }

// Verify implements Verifier: recompute HMAC-SHA256(body, secret) and
// constant-time compare against the header digest. Returns ErrUnsigned when the
// header is absent and ErrBadSignature when it does not match.
func (v *HMACVerifier) Verify(body []byte, header http.Header) error {
	got := strings.TrimSpace(header.Get(v.HeaderName))
	if got == "" {
		return ErrUnsigned
	}
	got = strings.TrimPrefix(got, v.Prefix)

	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	sum := mac.Sum(nil)

	var want string
	switch v.Encoding {
	case EncodingHexUpper:
		want = strings.ToUpper(hex.EncodeToString(sum))
		got = strings.ToUpper(got)
	default:
		want = hex.EncodeToString(sum)
		got = strings.ToLower(got)
	}
	// Constant-time compare; defends against a timing oracle on the digest.
	if !hmac.Equal([]byte(got), []byte(want)) {
		return ErrBadSignature
	}
	return nil
}
