package webhook

import (
	"net/http"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// HeaderSignature is PagerDuty's v3 webhook signature header. Its value is one or
// more comma-separated `v1=<hexdigest>` signatures (more than one during a
// signing-secret rotation). The receiver accepts the delivery if ANY listed v1
// signature matches the HMAC-SHA256 over the raw body keyed by the per-
// subscription signing secret.
const HeaderSignature = "X-PagerDuty-Signature"

// sigScheme is the only signature scheme PagerDuty v3 emits today: "v1=" hex.
const sigScheme = "v1="

// pdHMAC is the shared multi-signature HMAC config for the PagerDuty v3 scheme:
// the X-PagerDuty-Signature header carries comma-separated `v1=<hex>` signatures
// and the delivery is accepted if ANY matches (slice 656 factored core).
var pdHMAC = webhookrecv.HMACConfig{
	Header:   HeaderSignature,
	Prefix:   sigScheme,
	Encoding: webhookrecv.EncodingHex,
	Multi:    true,
}

// ErrUnsigned is returned when a delivery carries no X-PagerDuty-Signature header
// (or no recognizable v1= signature). It is the dominant rejection: an anonymous
// POST to the receiver is unsigned and stops here, before any record is built. It
// is the shared webhookrecv sentinel so the receiver and the skeleton agree.
var ErrUnsigned = webhookrecv.ErrUnsigned

// ErrBadSignature is returned when one or more v1 signatures are present but NONE
// matches the HMAC computed over the body with the subscription signing secret (a
// forged or tampered delivery).
var ErrBadSignature = webhookrecv.ErrBadSignature

// HMACVerifier verifies a PagerDuty v3 delivery by recomputing
// HMAC-SHA256(rawBody, signingSecret) and comparing it, in constant time, against
// each `v1=<hex>` signature in the X-PagerDuty-Signature header (via the shared
// webhookrecv multi-signature core). PagerDuty lists MULTIPLE v1 signatures during
// a secret rotation (one per active secret); we hold the active secret and accept
// if it matches ANY listed signature. The signing secret never appears in a log
// line or the HTTP response (it is unexported so a stray %v cannot leak it).
type HMACVerifier struct {
	// secret is the per-subscription signing secret. Unexported so a stray %v
	// cannot leak it. Sourced from the connector environment, never a flag,
	// never logged.
	secret []byte
}

// NewHMACVerifier builds an HMACVerifier. secret is the per-subscription signing
// secret (read from the connector environment, never a flag, never logged).
func NewHMACVerifier(secret string) *HMACVerifier {
	return &HMACVerifier{secret: []byte(secret)}
}

// Verify returns nil iff the delivery is authentic: HMAC-SHA256(body, secret)
// matches at least one of the `v1=` signatures the header carries (constant-time
// compare). Returns ErrUnsigned when the header carries no v1 signature and
// ErrBadSignature when none of the present v1 signatures matches. Verify is called
// BEFORE the body is parsed or any record is built.
func (v *HMACVerifier) Verify(body []byte, header http.Header) error {
	return pdHMAC.Verify(v.secret, body, header)
}

// Sign returns a single-signature X-PagerDuty-Signature header value for body
// under secret (the canonical `v1=<hex>` form). Used by TESTS ONLY to construct a
// signed delivery fixture; the receiver never signs outbound deliveries (it only
// verifies inbound ones).
func Sign(secret, body []byte) string {
	return pdHMAC.Sign(secret, body)
}
