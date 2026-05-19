// Slice 162 — client-IP + User-Agent capture helpers for session create.
//
// The HTTP handlers that issue sessions (LocalLogin, OIDCCallback) need a
// stable way to extract the caller's IP and User-Agent in a form safe to
// persist. Two concerns drive the helpers' shape:
//
//   - P0-162-2: do not trust X-Forwarded-For blindly. RFC 7239 + OWASP
//     IP-spoofing guidance both say the header is forgeable by any client
//     unless an intermediary trusted-proxy layer strips and re-issues it.
//     We surface the cooked IP (RemoteAddr-derived) by default and only
//     consult the forwarded header when TRUST_FORWARDED_HEADERS=1 is set
//     in the environment — an opt-in operator gesture that says "I run
//     a reverse proxy that scrubs untrusted X-Forwarded-For values".
//   - DoS guard: real User-Agent strings stay under a few hundred bytes;
//     truncation at the session-store layer (MaxUserAgentBytes) caps the
//     persisted value at 512 bytes. The helper here is the read-side.
//
// Operator note: in a v1 single-VM docker-compose deployment, the platform
// process binds directly to the public TLS port and TRUST_FORWARDED_HEADERS
// should stay unset (no proxy in front). In K8s-Ingress deployments, a
// future operator-docs page (out of scope here) will document setting the
// env var iff the Ingress is configured to scrub incoming X-Forwarded-For.
package auth

import (
	"net"
	"net/http"
	"os"
)

// trustForwardedHeadersEnv is the env var that opts a deployment into
// honoring X-Forwarded-For for session-create IP capture. Any value other
// than "1" leaves the header ignored.
const trustForwardedHeadersEnv = "TRUST_FORWARDED_HEADERS"

// clientIP returns the request's caller IP, suitable for persistence on a
// session row. Strategy:
//
//  1. If TRUST_FORWARDED_HEADERS=1, parse the first IP from X-Forwarded-For
//     (RFC 7239 left-most-is-client convention). When the header is
//     present-but-malformed, fall through to RemoteAddr rather than fail
//     the whole login flow on header parse.
//  2. Otherwise (default), strip the port from r.RemoteAddr.
//  3. On any parse error, return "" — the store layer converts that to
//     SQL NULL. We never invent or guess an IP.
//
// The returned string is the canonical text form (IPv4 dotted-quad or IPv6
// colon-separated). Callers persist it verbatim; the column is TEXT (slice
// 162 D1).
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if os.Getenv(trustForwardedHeadersEnv) == "1" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := firstForwardedIP(xff); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port (e.g. tests using a Unix socket) — try as-is.
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// userAgent returns the request's User-Agent header, ready for persistence.
// The store layer truncates to MaxUserAgentBytes; this helper just surfaces
// the raw header (empty string when absent — store converts to SQL NULL).
func userAgent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.UserAgent()
}

// firstForwardedIP parses the leftmost IP from an X-Forwarded-For header
// value. RFC 7239 lists IPs comma-separated, client-most-first. Returns ""
// on parse failure so the caller falls through to RemoteAddr (per RFC 7239:
// "Implementations are advised to ignore headers they do not understand").
func firstForwardedIP(xff string) string {
	for i := 0; i < len(xff); i++ {
		if xff[i] == ',' {
			xff = xff[:i]
			break
		}
	}
	// Trim leading/trailing whitespace without importing strings (cheap inline).
	for len(xff) > 0 && (xff[0] == ' ' || xff[0] == '\t') {
		xff = xff[1:]
	}
	for len(xff) > 0 && (xff[len(xff)-1] == ' ' || xff[len(xff)-1] == '\t') {
		xff = xff[:len(xff)-1]
	}
	if ip := net.ParseIP(xff); ip != nil {
		return ip.String()
	}
	return ""
}
