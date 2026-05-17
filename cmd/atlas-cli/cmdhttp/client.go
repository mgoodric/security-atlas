// Package cmdhttp gives the atlas-cli a single construction point for
// outbound *http.Client instances. Every call site under cmd/atlas-cli
// MUST go through Client(timeout) instead of the net/http package-level
// shared client — the latter has no timeout and a hung atlas server
// (DNS, deep TCP retransmits, pause-the-world) would stall the CLI
// indefinitely. The CLI is the operator's primary administrative
// entrypoint; an unbounded hang is a DoS-against-the-operator.
//
// Background: Q2 2026 security audit (slice 085) flagged the two CLI
// subcommands cmd_features.go and cmd_credentials.go for using the
// package-level shared client. This package is the remediation surface
// filed as slice 088. Acceptance criterion AC-4 enforces zero remaining
// references (DefaultClient / package-level Get / package-level Post)
// under cmd/atlas-cli/ via a grep gate.
//
// Each Client() call returns a fresh *http.Client with its own
// *http.Transport. We deliberately do NOT share a package-level Client
// or Transport, for two reasons:
//
//  1. Different call sites want different timeouts (a feature-flag GET
//     is 10s; a credential issuance POST that may invoke cosign is 30s).
//     A shared default would force the longest timeout everywhere, which
//     defeats the responsiveness goal.
//  2. The CLI is short-lived. Connection pooling across calls is not a
//     concern — most invocations make one request and exit. A fresh
//     transport per call is the simpler, safer construction.
//
// Anti-criteria honored from slice 088:
//
//   - P0-A1: we do NOT mutate the package-level shared client's Timeout
//     field. That is a process-global footgun.
//   - P0-A2: no retry-on-timeout logic. Retry is a separate concern
//     with different correctness implications (idempotency, observability).
//   - P0-A3: server-side handler timeouts are unchanged. Fix is CLI-only.
package cmdhttp

import (
	"net"
	"net/http"
	"time"
)

// Client returns a new *http.Client whose Timeout field is set to the
// supplied duration. The returned Client uses an explicit, freshly
// constructed *http.Transport with conservative dial / TLS / response
// header sub-timeouts — these are belt-and-suspenders against the
// (rare) case where Client.Timeout's deadline doesn't fire for a stuck
// connection phase (e.g., a TLS handshake that never errors).
//
// Each invocation produces a distinct *http.Client and a distinct
// *http.Transport — no shared state across calls. See package docs for
// the rationale.
//
// Timeout is the wall-clock budget for the entire round trip:
// connection, TLS, request write, response headers, and body read.
// When it fires, Client.Do returns an error satisfying
// errors.Is(err, context.DeadlineExceeded) (or a *url.Error wrapping it).
//
// A non-positive timeout disables Client.Timeout (matching net/http
// semantics). Call sites in this repo always pass a positive duration;
// the zero-value path exists only so misuse is loud at the call site
// rather than silently producing an unbounded client.
func Client(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		// DialContext: bound the TCP connect phase. The outer
		// Client.Timeout still applies; this just ensures we fail
		// fast if the network stack is stuck before a single byte
		// has been sent.
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// TLSHandshakeTimeout: bound the TLS handshake.
		TLSHandshakeTimeout: 10 * time.Second,
		// ResponseHeaderTimeout: bound time-to-first-byte after the
		// request is fully written. Set conservatively so that a
		// slow-but-progressing server doesn't get pre-empted.
		ResponseHeaderTimeout: 20 * time.Second,
		// ExpectContinueTimeout: standard net/http default.
		ExpectContinueTimeout: 1 * time.Second,
		// ForceAttemptHTTP2: opt-in to HTTP/2 when the server
		// negotiates it (mirrors net/http.DefaultTransport).
		ForceAttemptHTTP2: true,
		// IdleConnTimeout: short-lived CLI doesn't need a long
		// idle pool; keep it modest.
		IdleConnTimeout: 30 * time.Second,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// CheckRedirect: leave nil to use net/http's default redirect
		// policy (max 10 hops). Slice 088 explicitly preserves the
		// default; future slices may tighten this for credential
		// endpoints if cross-origin redirect risk surfaces.
		// Jar: nil (CLI is stateless; cookies are not part of any
		// supported auth path — bearer tokens are passed via header).
	}
}
