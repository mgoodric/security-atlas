package webhook

import (
	"net/http"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// ErrUnsigned is returned when a delivery carries no signature header. It is the
// dominant rejection: an anonymous POST to the receiver is unsigned and stops
// here, before any record is built. It is the shared webhookrecv error so the
// receiver and the skeleton agree on the sentinel.
var ErrUnsigned = webhookrecv.ErrUnsigned

// ErrBadSignature is returned when a signature is present but does not match the
// HMAC computed over the body with the subscription secret (a forged or
// tampered delivery).
var ErrBadSignature = webhookrecv.ErrBadSignature

// SigEncoding is how a vendor encodes the HMAC digest in its signature header.
// It mirrors webhookrecv.Encoding so existing call sites keep the HRIS-local
// names (slices 573 / 655) while the verifier core is the shared one.
type SigEncoding = webhookrecv.Encoding

const (
	// EncodingHex is a lowercase hex digest (Rippling's scheme).
	EncodingHex = webhookrecv.EncodingHex
	// EncodingHexUpper is an uppercase hex digest (BambooHR's documented scheme).
	EncodingHexUpper = webhookrecv.EncodingHexUpper
)

// HMACVerifier verifies a delivery by recomputing HMAC-SHA256 over the raw body
// with a per-subscription shared secret and comparing it, in constant time,
// against the digest the vendor placed in its signature header. It is the HRIS
// vendor adapter over the shared webhookrecv.HMACConfig core (slice 656): the
// header name, prefix, and encoding are the only per-vendor axes. The secret
// never appears in a log line or the HTTP response.
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
// constant-time compare against the header digest via the shared core. Returns
// ErrUnsigned when the header is absent and ErrBadSignature when it does not
// match.
func (v *HMACVerifier) Verify(body []byte, header http.Header) error {
	return webhookrecv.HMACConfig{
		Header:   v.HeaderName,
		Prefix:   v.Prefix,
		Encoding: v.Encoding,
	}.Verify(v.secret, body, header)
}
