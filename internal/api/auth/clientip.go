// Slice 162 — client-IP + User-Agent capture helpers for session create.
// Slice 466 — replaces the blunt TRUST_FORWARDED_HEADERS boolean with a
// TRUSTED_PROXY_CIDRS allowlist + a right-to-left X-Forwarded-For walk.
//
// The HTTP handlers that issue sessions (LocalLogin, OIDCCallback) need a
// stable way to extract the caller's IP and User-Agent in a form safe to
// persist. Two concerns drive the helpers' shape:
//
//   - P0-162-2: do not trust X-Forwarded-For blindly. RFC 7239 + OWASP
//     IP-spoofing guidance both say the header is forgeable by any client
//     unless an intermediary trusted-proxy layer strips and re-issues it.
//     Slice 465's TRUST_FORWARDED_HEADERS boolean was a blunt instrument:
//     setting it trusted the LEFT-most XFF IP unconditionally, so a client
//     not actually behind a header-scrubbing proxy could forge its source
//     IP. Slice 466 replaces it with a CIDR allowlist (TRUSTED_PROXY_CIDRS):
//     the resolver walks X-Forwarded-For right-to-left and accepts a hop
//     only while the immediately-connecting peer is within an allowed CIDR,
//     stopping at the first untrusted hop (the real client). A client-forged
//     XFF prefix is ignored because the forging client's own peer address is
//     not in any trusted CIDR.
//   - DoS guard: real User-Agent strings stay under a few hundred bytes;
//     truncation at the session-store layer (MaxUserAgentBytes) caps the
//     persisted value at 512 bytes. The helper here is the read-side.
//     The XFF walk stops at the first untrusted hop and caps the trusted
//     hops it steps over (maxForwardedHops), so a pathological
//     multi-kilobyte header cannot turn the per-request walk into a CPU
//     sink (threat-model D).
//
// Operator note: in a v1 single-VM docker-compose deployment, the platform
// process binds directly to the public TLS port and TRUSTED_PROXY_CIDRS
// should stay unset (no proxy in front) — the resolver then returns the
// direct TCP peer, byte-identical to the slice-162 default. In
// K8s-Ingress / reverse-proxy deployments, set TRUSTED_PROXY_CIDRS to the
// CIDR(s) the proxy connects from.
package auth

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// trustedProxyCIDRsEnv is the canonical allowlist env var (slice 466).
	// Comma-separated CIDRs (e.g. "10.0.0.0/8,192.168.0.0/16"). Empty/unset
	// ⇒ no proxy is trusted ⇒ the direct TCP peer is the client IP.
	trustedProxyCIDRsEnv = "TRUSTED_PROXY_CIDRS"

	// trustForwardedHeadersEnv is the slice-465 boolean, retained as a
	// DEPRECATED back-compat alias (slice 466 D1). When set to "1" AND
	// TRUSTED_PROXY_CIDRS is unset, it maps to "trust any proxy"
	// (0.0.0.0/0 + ::/0) so existing deployments keep working — but it
	// emits a one-time deprecation warning at boot. TRUSTED_PROXY_CIDRS
	// always takes precedence when set.
	trustForwardedHeadersEnv = "TRUST_FORWARDED_HEADERS"

	// maxForwardedHops caps how many trusted-proxy hops (the socket peer
	// included) the X-Forwarded-For walk steps over (threat-model D — DoS
	// via a pathological header). Untrusted entries never count: the walk
	// stops at the first one. A request legitimately passes through a
	// handful of proxies; 50 is far beyond any real topology while bounding
	// the per-request CPU cost. Matches MAX_FORWARDED_HOPS in
	// web/peer-chain.cjs.
	maxForwardedHops = 50
)

// trustedProxies is the process-wide, validated set of CIDRs whose member
// addresses are treated as trusted proxies during the X-Forwarded-For walk.
// It is parsed once at startup (InitTrustedProxiesFromEnv) and read without
// further locking on the request hot path. A nil/empty set means "trust no
// proxy" — the fail-safe default.
var (
	trustedProxies   []*net.IPNet
	trustedProxiesMu sync.RWMutex
)

// trustAnyProxyCIDRs is the back-compat expansion of TRUST_FORWARDED_HEADERS=1:
// every IPv4 and IPv6 address is treated as a trusted proxy. This reproduces
// the slice-465 "trust the forwarded header" posture for operators who have
// not yet migrated to TRUSTED_PROXY_CIDRS.
var trustAnyProxyCIDRs = []string{"0.0.0.0/0", "::/0"}

// InitTrustedProxiesFromEnv parses TRUSTED_PROXY_CIDRS (or the deprecated
// TRUST_FORWARDED_HEADERS alias) into the process-wide trusted-proxy set.
// It is called once at server startup. On a malformed CIDR it returns an
// error so the caller can fail loud at boot (AC-1) rather than silently
// trusting/ignoring headers per-request.
//
// Precedence (slice 466 D1):
//
//  1. If TRUSTED_PROXY_CIDRS is set (non-empty after trimming), it is the
//     authoritative allowlist. A malformed entry is a boot error.
//  2. Else if TRUST_FORWARDED_HEADERS=1, expand to trust-any-proxy and
//     return a non-nil *deprecation* sentinel via deprecated=true so the
//     caller can log a warning. (The set is still installed.)
//  3. Else the set is empty ⇒ direct peer IP for every request.
//
// The deprecated return is informational; err is the load-bearing signal.
func InitTrustedProxiesFromEnv() (deprecated bool, err error) {
	raw := strings.TrimSpace(os.Getenv(trustedProxyCIDRsEnv))
	if raw != "" {
		nets, perr := parseCIDRList(raw)
		if perr != nil {
			return false, perr
		}
		setTrustedProxies(nets)
		return false, nil
	}
	if os.Getenv(trustForwardedHeadersEnv) == "1" {
		nets, perr := parseCIDRList(strings.Join(trustAnyProxyCIDRs, ","))
		if perr != nil {
			// Statically valid; this never fires, but keep the contract honest.
			return false, perr
		}
		setTrustedProxies(nets)
		return true, nil
	}
	setTrustedProxies(nil)
	return false, nil
}

// parseCIDRList parses a comma-separated CIDR list into IPNets. An empty or
// whitespace-only entry is rejected (a stray trailing comma is a config
// typo worth surfacing). Returns a descriptive error naming the offending
// token so the operator can fix the env var.
func parseCIDRList(raw string) ([]*net.IPNet, error) {
	parts := strings.Split(raw, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if tok == "" {
			return nil, &cidrParseError{token: p, reason: "empty CIDR entry (stray comma?)"}
		}
		_, ipNet, err := net.ParseCIDR(tok)
		if err != nil {
			return nil, &cidrParseError{token: tok, reason: err.Error()}
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
}

// cidrParseError names the offending token for a fail-loud boot message.
type cidrParseError struct {
	token  string
	reason string
}

func (e *cidrParseError) Error() string {
	return "TRUSTED_PROXY_CIDRS: invalid entry " + strconv.Quote(e.token) + ": " + e.reason
}

func setTrustedProxies(nets []*net.IPNet) {
	trustedProxiesMu.Lock()
	trustedProxies = nets
	trustedProxiesMu.Unlock()
}

func loadTrustedProxies() []*net.IPNet {
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	return trustedProxies
}

// isTrustedProxy reports whether ip is within any configured trusted-proxy
// CIDR. An empty set always returns false (trust nobody).
func isTrustedProxy(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the request's caller IP, suitable for persistence on a
// session row. Strategy (slice 466):
//
//  1. Determine the direct TCP peer from r.RemoteAddr.
//  2. If no trusted-proxy CIDRs are configured, return the peer (the
//     fail-safe default — byte-identical to the slice-162/465-unset path).
//  3. Otherwise walk X-Forwarded-For right-to-left starting from the peer:
//     while the current connecting address is a trusted proxy, step to the
//     next XFF entry to the left; stop and return the first address whose
//     connecting peer is NOT trusted (the real client). A client-forged
//     XFF prefix is never reached because the walk stops at the first
//     untrusted hop.
//  4. On any parse error, return "" — the store layer converts that to
//     SQL NULL. We never invent or guess an IP.
//
// The returned string is the canonical text form (IPv4 dotted-quad or IPv6
// colon-separated). Callers persist it verbatim; the column is TEXT (slice
// 162 D1).
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	peer := peerIP(r.RemoteAddr)
	if peer == nil {
		return ""
	}

	nets := loadTrustedProxies()
	if len(nets) == 0 {
		// No proxy trusted — the direct peer is the client. XFF ignored.
		return peer.String()
	}

	// The connecting peer must itself be trusted before we consult XFF at
	// all; if a direct (untrusted) client sets X-Forwarded-For, the peer is
	// not in a trusted CIDR and we return the peer immediately.
	if !isTrustedProxy(peer, nets) {
		return peer.String()
	}

	// Every X-Forwarded-For line, in order: a proxy that adds its own line
	// instead of appending to the client's leaves the real client on the
	// last line, which Header.Get never reads (#2272).
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	// Walk right-to-left. `candidate` starts as the peer (already known
	// trusted). Each hop from the right is the address that connected to
	// the current candidate; we keep walking left only while that address
	// is itself a trusted proxy. Only trusted steps count toward
	// maxForwardedHops, the peer being the first, as in the web preload
	// (web/peer-chain.cjs, #2077). An untrusted hop ends the walk at once,
	// so however many entries a client forges in front of a trusted proxy,
	// the right-most untrusted one (the real client) is returned.
	candidate := peer
	trustedSteps := 1
	for {
		hop, rest, ok := lastForwardedHop(xff)
		if !ok {
			// Every hop was a trusted proxy (or the header was empty).
			// Return the left-most candidate we walked to (or the peer).
			return candidate.String()
		}
		xff = rest
		ip := net.ParseIP(hop)
		if ip == nil {
			// Malformed entry — stop; return the last good (trusted-side)
			// candidate, which is the closest address we can attribute.
			return candidate.String()
		}
		candidate = ip
		if !isTrustedProxy(ip, nets) {
			// First untrusted hop — this is the real client.
			return candidate.String()
		}
		trustedSteps++
		if trustedSteps > maxForwardedHops {
			// Cap reached. Everything to the right was written by trusted
			// proxies, so this trusted address is still attributable.
			return candidate.String()
		}
	}
}

// ClientIP is the exported entrypoint for other packages (e.g.
// internal/api/admindemo, which keys its per-IP rate limiter on the same
// resolved address) so the trusted-proxy walk lives in exactly one place.
// It shares the process-wide trusted-proxy set installed by
// InitTrustedProxiesFromEnv.
func ClientIP(r *http.Request) string {
	return clientIP(r)
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

// peerIP extracts the IP from a RemoteAddr ("host:port" or a bare host).
// Returns nil when it cannot be parsed.
func peerIP(remoteAddr string) net.IP {
	if remoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr without a port (e.g. tests using a Unix socket).
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// lastForwardedHop pops the right-most trimmed, non-empty entry off an
// X-Forwarded-For value and returns it with the remainder to its left;
// ok is false when no entry remains. Empty and whitespace-only entries
// are skipped so a stray comma does not abort the walk early. Popping
// from the right means the walk never splits the client-written part of
// the header it does not reach (threat-model D).
func lastForwardedHop(xff string) (hop, rest string, ok bool) {
	for xff != "" {
		i := strings.LastIndexByte(xff, ',')
		tok := strings.TrimSpace(xff[i+1:])
		if i < 0 {
			xff = ""
		} else {
			xff = xff[:i]
		}
		if tok != "" {
			return tok, xff, true
		}
	}
	return "", "", false
}
