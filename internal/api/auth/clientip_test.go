// Slice 162 / slice 466 — unit tests for the clientIP + userAgent helpers
// and the TRUSTED_PROXY_CIDRS allowlist + right-to-left X-Forwarded-For walk.
//
// The trusted-proxy set is process-wide state installed by
// InitTrustedProxiesFromEnv. Each test that needs a particular allowlist
// sets TRUSTED_PROXY_CIDRS (or TRUST_FORWARDED_HEADERS) via t.Setenv (which
// auto-cleans) and calls a helper that re-parses + restores the prior set.

package auth

import (
	"net/http"
	"testing"
)

func newReq(t *testing.T, remoteAddr string, headers map[string]string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// withTrustedProxies installs the given CIDR allowlist for the duration of
// the test and restores the previous set on cleanup. An empty cidrs string
// installs the empty (trust-nobody) set.
func withTrustedProxies(t *testing.T, cidrs string) {
	t.Helper()
	prev := loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(prev) })
	if cidrs == "" {
		setTrustedProxies(nil)
		return
	}
	nets, err := parseCIDRList(cidrs)
	if err != nil {
		t.Fatalf("withTrustedProxies(%q): %v", cidrs, err)
	}
	setTrustedProxies(nets)
}

// --- AC-3: empty allowlist ⇒ direct peer IP (today's unset behavior) ---

func TestClientIP_EmptyAllowlistReturnsPeer(t *testing.T) {
	withTrustedProxies(t, "") // no proxy trusted
	r := newReq(t, "203.0.113.4:55320", map[string]string{
		"X-Forwarded-For": "198.51.100.42",
	})
	if got := clientIP(r); got != "203.0.113.4" {
		t.Errorf("clientIP empty-allowlist = %q; want 203.0.113.4 (peer, XFF ignored)", got)
	}
}

// --- AC-2: forged XFF prefix from an UNTRUSTED direct client is ignored ---

func TestClientIP_ForgedPrefixFromUntrustedPeerRejected(t *testing.T) {
	// Allowlist trusts 10.0.0.0/8 proxies. The attacker connects directly
	// (peer 203.0.113.50, NOT in the allowlist) and forges XFF. Because the
	// connecting peer is untrusted, the header is never consulted.
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "203.0.113.50:44100", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	if got := clientIP(r); got != "203.0.113.50" {
		t.Errorf("clientIP forged-from-untrusted-peer = %q; want 203.0.113.50 (peer; forged XFF ignored)", got)
	}
}

// --- AC-2: forged prefix PREPENDED to a legitimate trusted chain ---

func TestClientIP_ForgedPrefixThroughTrustedProxyRejected(t *testing.T) {
	// Real client 198.51.100.42 sits behind trusted proxy 10.0.0.5. The
	// client forges an extra left-most hop (1.1.1.1) to try to spoof its
	// source IP. The walk stops at the first UNTRUSTED hop (198.51.100.42),
	// so the forged 1.1.1.1 is never reached.
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "1.1.1.1, 198.51.100.42",
	})
	if got := clientIP(r); got != "198.51.100.42" {
		t.Errorf("clientIP forged-prefix-through-proxy = %q; want 198.51.100.42 (forged 1.1.1.1 ignored)", got)
	}
}

// --- AC-2: single trusted hop ---

func TestClientIP_SingleTrustedHop(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "198.51.100.42",
	})
	if got := clientIP(r); got != "198.51.100.42" {
		t.Errorf("clientIP single-trusted-hop = %q; want 198.51.100.42", got)
	}
}

// --- AC-2: multiple trusted hops walk through to the real client ---

func TestClientIP_MultipleTrustedHops(t *testing.T) {
	// Two trusted proxies in front (10.0.0.9 then 10.0.0.5 = peer). Walk
	// right-to-left: 10.0.0.9 trusted → keep going; 198.51.100.42 untrusted
	// → that's the client.
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "198.51.100.42, 10.0.0.9",
	})
	if got := clientIP(r); got != "198.51.100.42" {
		t.Errorf("clientIP multiple-trusted-hops = %q; want 198.51.100.42", got)
	}
}

// --- AC-2: all hops trusted ⇒ left-most walked candidate ---

func TestClientIP_AllHopsTrustedReturnsLeftmost(t *testing.T) {
	// Pathological-but-valid: every hop is inside the trusted range. The
	// walk consumes the whole header and returns the left-most entry.
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "10.0.0.1, 10.0.0.2, 10.0.0.3",
	})
	if got := clientIP(r); got != "10.0.0.1" {
		t.Errorf("clientIP all-trusted = %q; want 10.0.0.1 (left-most)", got)
	}
}

// --- AC-5: malformed header entry stops the walk at the last good candidate ---

func TestClientIP_MalformedHeaderStopsAtLastGood(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", map[string]string{
		// Right-most (10.0.0.9) is trusted → step left; next entry is
		// garbage → stop and attribute to the last good candidate (10.0.0.9).
		"X-Forwarded-For": "not-an-ip, 10.0.0.9",
	})
	if got := clientIP(r); got != "10.0.0.9" {
		t.Errorf("clientIP malformed-header = %q; want 10.0.0.9 (stop at malformed)", got)
	}
}

func TestClientIP_TrustedPeerNoHeaderReturnsPeer(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	r := newReq(t, "10.0.0.5:443", nil)
	if got := clientIP(r); got != "10.0.0.5" {
		t.Errorf("clientIP trusted-peer-no-XFF = %q; want 10.0.0.5", got)
	}
}

// --- IPv6 trusted proxy ---

func TestClientIP_IPv6TrustedProxy(t *testing.T) {
	withTrustedProxies(t, "2001:db8::/32")
	r := newReq(t, "[2001:db8::5]:443", map[string]string{
		"X-Forwarded-For": "2001:db8:abcd::99",
	})
	// The forwarded IP is inside the trusted /32, so the walk steps past it;
	// no further hop ⇒ left-most candidate is the forwarded IP itself.
	if got := clientIP(r); got != "2001:db8:abcd::99" {
		t.Errorf("clientIP ipv6-trusted = %q; want 2001:db8:abcd::99", got)
	}
}

func TestClientIP_IPv6RemoteAddrEmptyAllowlist(t *testing.T) {
	withTrustedProxies(t, "")
	r := newReq(t, "[2001:db8::1]:55320", nil)
	if got := clientIP(r); got != "2001:db8::1" {
		t.Errorf("clientIP IPv6 = %q; want 2001:db8::1", got)
	}
}

func TestClientIP_NoPortRemoteAddr(t *testing.T) {
	// Some test rigs set RemoteAddr to a bare host with no port.
	withTrustedProxies(t, "")
	r := newReq(t, "203.0.113.4", nil)
	if got := clientIP(r); got != "203.0.113.4" {
		t.Errorf("clientIP no-port = %q; want 203.0.113.4", got)
	}
}

func TestClientIP_EmptyRemoteAddrReturnsEmpty(t *testing.T) {
	r := newReq(t, "", nil)
	if got := clientIP(r); got != "" {
		t.Errorf("clientIP empty RemoteAddr = %q; want \"\" (store → SQL NULL)", got)
	}
}

func TestClientIP_NilRequestReturnsEmpty(t *testing.T) {
	if got := clientIP(nil); got != "" {
		t.Errorf("clientIP(nil) = %q; want \"\"", got)
	}
}

// --- AC-1: malformed CIDR fails loud at boot ---

func TestInitTrustedProxies_MalformedCIDRFailsLoud(t *testing.T) {
	cases := []string{
		"not-a-cidr",
		"10.0.0.0/8,garbage",
		"10.0.0.0",       // missing prefix length
		"10.0.0.0/8,",    // stray trailing comma → empty entry
		"999.0.0.0/8",    // out-of-range octet
		"2001:db8::/129", // invalid IPv6 prefix length
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Setenv(trustedProxyCIDRsEnv, c)
			t.Setenv(trustForwardedHeadersEnv, "")
			prev := loadTrustedProxies()
			t.Cleanup(func() { setTrustedProxies(prev) })
			if _, err := InitTrustedProxiesFromEnv(); err == nil {
				t.Errorf("InitTrustedProxiesFromEnv(%q) err = nil; want a boot error", c)
			}
		})
	}
}

func TestInitTrustedProxies_ValidCIDRInstalls(t *testing.T) {
	t.Setenv(trustedProxyCIDRsEnv, "10.0.0.0/8, 192.168.0.0/16")
	t.Setenv(trustForwardedHeadersEnv, "")
	prev := loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(prev) })
	dep, err := InitTrustedProxiesFromEnv()
	if err != nil {
		t.Fatalf("InitTrustedProxiesFromEnv err = %v; want nil", err)
	}
	if dep {
		t.Errorf("deprecated = true; want false for explicit TRUSTED_PROXY_CIDRS")
	}
	if n := len(loadTrustedProxies()); n != 2 {
		t.Errorf("installed %d CIDRs; want 2", n)
	}
}

func TestInitTrustedProxies_EmptyInstallsNothing(t *testing.T) {
	t.Setenv(trustedProxyCIDRsEnv, "")
	t.Setenv(trustForwardedHeadersEnv, "")
	prev := loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(prev) })
	dep, err := InitTrustedProxiesFromEnv()
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if dep {
		t.Errorf("deprecated = true; want false")
	}
	if n := len(loadTrustedProxies()); n != 0 {
		t.Errorf("installed %d CIDRs; want 0 (trust nobody)", n)
	}
}

// --- AC-4: TRUST_FORWARDED_HEADERS=1 back-compat alias ---

func TestInitTrustedProxies_BackCompatAliasTrustsAnyProxy(t *testing.T) {
	t.Setenv(trustedProxyCIDRsEnv, "")
	t.Setenv(trustForwardedHeadersEnv, "1")
	prev := loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(prev) })
	dep, err := InitTrustedProxiesFromEnv()
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !dep {
		t.Errorf("deprecated = false; want true (alias should warn)")
	}
	// trust-any: a left-most forwarded IP is honored through any peer.
	r := newReq(t, "203.0.113.50:44100", map[string]string{
		"X-Forwarded-For": "198.51.100.42",
	})
	if got := clientIP(r); got != "198.51.100.42" {
		t.Errorf("back-compat clientIP = %q; want 198.51.100.42 (trust-any-proxy)", got)
	}
}

func TestInitTrustedProxies_CIDRsTakePrecedenceOverAlias(t *testing.T) {
	// Both set: the explicit allowlist wins; the alias is NOT consulted.
	t.Setenv(trustedProxyCIDRsEnv, "10.0.0.0/8")
	t.Setenv(trustForwardedHeadersEnv, "1")
	prev := loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(prev) })
	dep, err := InitTrustedProxiesFromEnv()
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if dep {
		t.Errorf("deprecated = true; want false (CIDRs set → alias ignored)")
	}
	// Untrusted direct peer forging XFF is rejected (allowlist semantics,
	// NOT trust-any).
	r := newReq(t, "203.0.113.50:44100", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	if got := clientIP(r); got != "203.0.113.50" {
		t.Errorf("precedence clientIP = %q; want 203.0.113.50 (CIDR allowlist, not trust-any)", got)
	}
}

// --- threat-model D: hop cap bounds the walk ---

func TestForwardedHops_CapsAtMax(t *testing.T) {
	var b []byte
	for i := 0; i < maxForwardedHops+25; i++ {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, "10.0.0.1"...)
	}
	hops := forwardedHops(string(b))
	if len(hops) != maxForwardedHops {
		t.Errorf("forwardedHops capped at %d; want %d", len(hops), maxForwardedHops)
	}
}

func TestForwardedHops_DropsEmptyEntries(t *testing.T) {
	hops := forwardedHops("198.51.100.42, , 10.0.0.9,")
	if len(hops) != 2 || hops[0] != "198.51.100.42" || hops[1] != "10.0.0.9" {
		t.Errorf("forwardedHops = %v; want [198.51.100.42 10.0.0.9]", hops)
	}
}

func TestUserAgent_SurfacesHeader(t *testing.T) {
	r := newReq(t, "203.0.113.4:443", map[string]string{
		"User-Agent": "Mozilla/5.0 example",
	})
	if got := userAgent(r); got != "Mozilla/5.0 example" {
		t.Errorf("userAgent = %q; want \"Mozilla/5.0 example\"", got)
	}
}

func TestUserAgent_EmptyHeaderReturnsEmpty(t *testing.T) {
	r := newReq(t, "203.0.113.4:443", nil)
	if got := userAgent(r); got != "" {
		t.Errorf("userAgent (no header) = %q; want \"\"", got)
	}
}

func TestUserAgent_NilRequestReturnsEmpty(t *testing.T) {
	if got := userAgent(nil); got != "" {
		t.Errorf("userAgent(nil) = %q; want \"\"", got)
	}
}
