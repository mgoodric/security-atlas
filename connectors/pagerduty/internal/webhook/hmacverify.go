// Package webhook is the SOURCE-SIDE PagerDuty v3 webhook receiver. It
// establishes the event-driven (`subscribe`) profile for the PagerDuty connector
// (slice 540): a long-lived HTTP server that runs INSIDE the connector process,
// receives a PagerDuty v3 webhook delivery (incident lifecycle events), VERIFIES
// the X-PagerDuty-Signature HMAC BEFORE doing any work, maps the delivery's
// SUMMARY fields to the same pagerduty.incident_summary.v1 record (via the shared
// pdrecord builder, UNCHANGED), and emits it through the existing Push API.
//
// Invariant #3 (CLAUDE.md): the platform-side wire surface is ALWAYS push. This
// receiver is part of the CONNECTOR, not the platform — it adds NO inbound API to
// internal/api/. `subscribe` describes how the connector retrieves data FROM the
// source (a webhook PagerDuty POSTs to this process); the record still leaves the
// connector via Push, exactly as the pull profile does.
//
// Dominant new threat (slice 540, STRIDE Spoofing): anyone can POST to a public
// webhook receiver. The signature is verified BEFORE any record is built or
// pushed; an unsigned, forged, or wrong-signature delivery is rejected with 401
// and never produces a record. The body is size-bounded before it is read, so an
// oversized delivery cannot exhaust memory (STRIDE DoS).
//
// Over-collection guard (P0-489-3, unchanged): the receiver maps the SUMMARY
// fields the webhook `data` block carries — id / number / status / urgency /
// service / timestamps — and the decode struct has NO title / description / body
// field BY CONSTRUCTION, so the incident free-text never enters memory as
// connector data even though the v3 payload includes it.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// HeaderSignature is PagerDuty's v3 webhook signature header. Its value is one or
// more comma-separated `v1=<hexdigest>` signatures (more than one during a
// signing-secret rotation). The receiver accepts the delivery if ANY listed v1
// signature matches the HMAC-SHA256 over the raw body keyed by the per-
// subscription signing secret.
const HeaderSignature = "X-PagerDuty-Signature"

// sigScheme is the only signature scheme PagerDuty v3 emits today: "v1=" hex.
const sigScheme = "v1="

// ErrUnsigned is returned when a delivery carries no X-PagerDuty-Signature header
// (or no recognizable v1= signature). It is the dominant rejection: an anonymous
// POST to the receiver is unsigned and stops here, before any record is built.
var ErrUnsigned = errors.New("pagerduty/webhook: delivery carries no v1 signature")

// ErrBadSignature is returned when one or more v1 signatures are present but NONE
// matches the HMAC computed over the body with the subscription signing secret (a
// forged or tampered delivery).
var ErrBadSignature = errors.New("pagerduty/webhook: no signature matches")

// HMACVerifier verifies a PagerDuty v3 delivery by recomputing
// HMAC-SHA256(rawBody, signingSecret) and comparing it, in constant time, against
// each `v1=<hex>` signature in the X-PagerDuty-Signature header. PagerDuty lists
// MULTIPLE v1 signatures during a secret rotation (one per active secret); we hold
// the active secret and accept if it matches ANY listed signature. The signing
// secret never appears in a log line or the HTTP response (it is unexported so a
// stray %v cannot leak it).
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
	raw := strings.TrimSpace(header.Get(HeaderSignature))
	if raw == "" {
		return ErrUnsigned
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	want := mac.Sum(nil)

	sawSignature := false
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, sigScheme) {
			// Ignore unknown schemes; PagerDuty may add a v2 alongside v1.
			continue
		}
		sawSignature = true
		got, err := hex.DecodeString(strings.TrimPrefix(part, sigScheme))
		if err != nil {
			// Malformed hex in this entry; keep checking the others.
			continue
		}
		// Constant-time compare; defends against a timing oracle on the digest.
		if hmac.Equal(got, want) {
			return nil
		}
	}
	if !sawSignature {
		return ErrUnsigned
	}
	return ErrBadSignature
}

// Sign returns a single-signature X-PagerDuty-Signature header value for body
// under secret (the canonical `v1=<hex>` form). Used by TESTS ONLY to construct a
// signed delivery fixture; the receiver never signs outbound deliveries (it only
// verifies inbound ones).
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return sigScheme + hex.EncodeToString(mac.Sum(nil))
}
