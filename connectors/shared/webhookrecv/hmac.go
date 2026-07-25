// Package webhookrecv owns the vendor-agnostic idioms shared by every
// source-side connector webhook receiver (github / hris / pagerduty today; an
// MDM #557 receiver next). Slice 656 factors the three independently-built
// receivers onto this package: the bounded gosec-G112 http.Server constructor,
// the graceful Serve(ctx) shutdown, the MaxBytesReader body cap, the
// verify-first handler skeleton, and a parameterizable constant-time
// HMAC-SHA256 verifier core.
//
// Invariant #3 (CLAUDE.md): the platform-side wire surface is ALWAYS push. This
// package is part of a CONNECTOR, not the platform — it adds NO inbound API to
// internal/api/. It is purely the SOURCE-side machinery a connector uses to
// receive a vendor's webhook before re-emitting via Push.
//
// The package is intentionally GENERIC: it carries no connector domain type
// (no worker.RawWorker, no incidents.Incident). Each connector keeps a thin
// vendor adapter (its header scheme, its payload parser, its record builder)
// and configures this package declaratively.
package webhookrecv

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// ErrUnsigned is returned when a delivery carries no signature in the configured
// header (or no recognizable signature once the scheme prefix is required). It is
// the dominant rejection: an anonymous POST to a receiver is unsigned and stops
// here, before any record is built.
var ErrUnsigned = errors.New("webhookrecv: delivery carries no signature")

// ErrBadSignature is returned when a signature is present but does not match the
// HMAC computed over the raw body with the configured secret (a forged or
// tampered delivery). For a multi-signature header, it is returned only when at
// least one recognizable signature was present and NONE matched.
var ErrBadSignature = errors.New("webhookrecv: signature does not match")

// Encoding is how a vendor encodes the HMAC-SHA256 digest in its signature
// header value.
type Encoding int

const (
	// EncodingHex is a lowercase hex digest (GitHub, Rippling).
	EncodingHex Encoding = iota
	// EncodingHexUpper is an uppercase hex digest (BambooHR's documented scheme).
	EncodingHexUpper
	// EncodingBase64 is a standard base64 digest.
	EncodingBase64
)

// HMACConfig parameterizes the constant-time HMAC-SHA256-over-raw-body verifier
// core shared by every connector. It captures the only axes on which the three
// connectors' signature schemes differ:
//
//   - Header: the request header carrying the signature
//     (X-Hub-Signature-256 / X-Rippling-Signature / X-PagerDuty-Signature).
//   - Prefix: a literal scheme prefix stripped before the compare
//     ("sha256=" for GitHub, "v1=" for PagerDuty, "" for the bare HRIS digest).
//   - Encoding: hex / hex-upper / base64.
//   - Multi: when true the header value is a comma-separated list of signatures
//     and the delivery is accepted iff ANY listed signature matches (PagerDuty's
//     signing-secret rotation). When false the header value is a single
//     signature.
//
// The secret is held by the caller and passed to Verify per call so the config
// itself never holds the secret (a stray %v on a config cannot leak it).
type HMACConfig struct {
	// Header is the request header carrying the vendor's signature.
	Header string
	// Prefix is stripped from a signature before comparison (e.g. "sha256=" or
	// "v1="). Empty means the value is the bare digest. When Multi is true the
	// prefix doubles as the scheme marker: a comma-separated entry that does not
	// carry the prefix is ignored (an unknown scheme), not treated as a digest.
	Prefix string
	// Encoding selects how the digest is encoded in the header.
	Encoding Encoding
	// Multi accepts a comma-separated list of signatures, matching ANY.
	Multi bool
}

// Verify recomputes HMAC-SHA256(body, secret) and constant-time compares it
// against the signature(s) the configured header carries. It returns nil iff the
// delivery is authentic, ErrUnsigned when the header carries no usable signature,
// and ErrBadSignature when a signature is present but none matches.
//
// The compare is constant-time via hmac.Equal — for hex/base64 encodings the
// decoded digest bytes are compared; a malformed encoding fails before the
// timing-sensitive compare (so a malformed entry can never be an oracle). The
// secret never appears in the returned error.
func (c HMACConfig) Verify(secret, body []byte, header http.Header) error {
	raw := strings.TrimSpace(header.Get(c.Header))
	if raw == "" {
		return ErrUnsigned
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)

	if c.Multi {
		return c.verifyMulti(raw, want)
	}
	return c.verifySingle(raw, want)
}

// verifySingle handles the single-signature header: strip the (optional) prefix,
// decode per the encoding, constant-time compare.
func (c HMACConfig) verifySingle(raw string, want []byte) error {
	got := strings.TrimPrefix(raw, c.Prefix)
	if c.match(got, want) {
		return nil
	}
	return ErrBadSignature
}

// verifyMulti handles the comma-separated multi-signature header (PagerDuty
// rotation): accept iff ANY listed signature carrying the scheme prefix matches.
// An entry not carrying the prefix is an unknown scheme and is ignored. If no
// entry carries the prefix the header is treated as unsigned.
func (c HMACConfig) verifyMulti(raw string, want []byte) error {
	sawSignature := false
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if c.Prefix != "" && !strings.HasPrefix(part, c.Prefix) {
			// Unknown scheme (e.g. a future v2 alongside v1); skip it.
			continue
		}
		sawSignature = true
		got := strings.TrimPrefix(part, c.Prefix)
		if c.match(got, want) {
			return nil
		}
	}
	if !sawSignature {
		return ErrUnsigned
	}
	return ErrBadSignature
}

// match constant-time compares one supplied signature token (already
// prefix-stripped) against the wanted raw digest, decoding per the encoding.
//
// For hex/base64 the supplied token is decoded to bytes and compared against the
// wanted digest bytes; a malformed token fails (returns false) before any
// timing-sensitive compare. For hex-upper, both sides are upper-cased and the
// encoded strings are compared in constant time (mirroring the HRIS BambooHR
// scheme byte-for-byte).
func (c HMACConfig) match(got string, want []byte) bool {
	switch c.Encoding {
	case EncodingHexUpper:
		wantEnc := strings.ToUpper(hex.EncodeToString(want))
		gotEnc := strings.ToUpper(got)
		return hmac.Equal([]byte(gotEnc), []byte(wantEnc))
	case EncodingBase64:
		gotBytes, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			return false
		}
		return hmac.Equal(gotBytes, want)
	default: // EncodingHex
		gotBytes, err := hex.DecodeString(got)
		if err != nil {
			return false
		}
		return hmac.Equal(gotBytes, want)
	}
}

// Sign returns a single signature value for body under secret in the config's
// encoding, with the config's prefix prepended. Used by connectors' tests to
// construct signed fixtures; production receivers only verify inbound
// deliveries, never sign outbound ones.
func (c HMACConfig) Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sum := mac.Sum(nil)
	var enc string
	switch c.Encoding {
	case EncodingHexUpper:
		enc = strings.ToUpper(hex.EncodeToString(sum))
	case EncodingBase64:
		enc = base64.StdEncoding.EncodeToString(sum)
	default:
		enc = hex.EncodeToString(sum)
	}
	return c.Prefix + enc
}
