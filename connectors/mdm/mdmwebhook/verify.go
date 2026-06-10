package mdmwebhook

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// errBadCredential is returned by both vendor Verifiers when a delivery's
// credential is absent or does not match. It is the shared webhookrecv
// ErrBadSignature sentinel so the receiver and the skeleton agree; the skeleton
// maps any non-nil Verify error to a bare 401 (no detail leak).
var errBadCredential = webhookrecv.ErrBadSignature

// SharedSecretVerifier authenticates a delivery by a constant-time compare of a
// configured shared secret against the value carried in a configured request
// header. It is the Jamf scheme (D-JAMF): Jamf Pro webhooks do NOT HMAC-sign the
// body — the operator configures a static credential on the webhook (a Basic-auth
// header or a custom shared-secret header), and Jamf replays it verbatim on every
// delivery. We hold the configured credential and compare it in constant time.
//
// The secret is held unexported so a stray %v cannot leak it. An empty/absent
// header value is rejected (ErrUnsigned semantics — the shared skeleton maps any
// non-nil error to a bare 401, no detail leak).
type SharedSecretVerifier struct {
	header string
	secret []byte
}

// NewSharedSecretVerifier builds a SharedSecretVerifier. header is the request
// header carrying the operator-configured credential (e.g. "Authorization" for a
// Basic header, or a custom "X-Jamf-Webhook-Secret"); secret is the exact value
// to require. Both are read from the connector environment, never a flag, never
// logged.
func NewSharedSecretVerifier(header, secret string) *SharedSecretVerifier {
	return &SharedSecretVerifier{header: header, secret: []byte(secret)}
}

// Verify returns nil iff the configured header carries exactly the configured
// secret (constant-time compare). body is ignored — Jamf does not sign the body;
// the credential lives entirely in the header. A missing or mismatched credential
// returns webhookrecv.ErrBadSignature so the skeleton rejects with 401 BEFORE any
// record is built.
func (v *SharedSecretVerifier) Verify(_ []byte, header http.Header) error {
	got := strings.TrimSpace(header.Get(v.header))
	if subtle.ConstantTimeCompare([]byte(got), v.secret) == 1 {
		return nil
	}
	return errBadCredential
}

// ClientStateVerifier authenticates a Microsoft Graph change notification by a
// constant-time compare of the per-delivery `clientState` value (carried in the
// notification body) against the configured secret (D-INTUNE-2). Graph echoes the
// clientState the connector set when it created the subscription; a forged
// delivery cannot know it. This is NOT a body-HMAC — Graph does not sign the body
// — so it is a custom one-method Verifier over the parsed clientState. The
// shared webhookrecv.Verifier interface (one method) fits it exactly.
//
// The secret is held unexported so a stray %v cannot leak it.
type ClientStateVerifier struct {
	extract func(body []byte) (string, bool)
	secret  []byte
}

// NewClientStateVerifier builds a ClientStateVerifier. extract pulls the
// clientState out of the delivery body (the Intune adapter supplies the Graph
// notification-batch shape); secret is the value to require. A delivery whose
// clientState is absent or does not match every notification is rejected.
func NewClientStateVerifier(secret string, extract func(body []byte) (string, bool)) *ClientStateVerifier {
	return &ClientStateVerifier{extract: extract, secret: []byte(secret)}
}

// Verify returns nil iff the body's clientState matches the configured secret
// (constant-time). A body with no clientState, or one that does not match,
// returns errBadCredential so the skeleton rejects with 401 BEFORE any record is
// built.
func (v *ClientStateVerifier) Verify(body []byte, _ http.Header) error {
	got, ok := v.extract(body)
	if !ok {
		return errBadCredential
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), v.secret) == 1 {
		return nil
	}
	return errBadCredential
}
