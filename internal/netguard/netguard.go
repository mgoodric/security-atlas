// Package netguard is the one SSRF deny list for outbound requests to a
// destination someone other than the platform chose: the notify webhook
// and Slack channels (#2304) and a tenant's OIDC issuer (#2534).
//
// A config-time check of a hostname is not enough on its own: DNS can
// change between validation and the dial (rebinding), a redirect can send
// the request elsewhere, and an environment proxy dials the target on the
// client's behalf. NewHTTPClient closes all three: it judges every address
// actually dialled, never follows a redirect, and ignores proxies.
package netguard

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Policy parameterizes the guard. The zero value is the strict production
// policy: every internal or non-routable address is denied.
type Policy struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1.
	AllowLoopback bool
	// AllowPrivate permits RFC1918 (10/8, 172.16/12, 192.168/16), ULA
	// fc00::/7 and carrier-grade NAT 100.64/10. It is the operator opt-in
	// for a self-hosted IdP on a private network. Link-local (including
	// the 169.254.169.254 cloud metadata address) stays denied.
	AllowPrivate bool
}

var (
	// nat64 is the RFC 6052 well-known NAT64 prefix; the low 32 bits are
	// an IPv4 address.
	nat64 = mustCIDR("64:ff9b::/96")
	// nat64Local is the RFC 8215 local-use NAT64 prefix. The operator
	// picks the RFC 6052 prefix length inside it, so where the IPv4
	// address sits is not knowable from the address alone.
	nat64Local = mustCIDR("64:ff9b:1::/48")
	// sixToFour is RFC 3056 6to4; bits 16..47 are an IPv4 address.
	sixToFour = mustCIDR("2002::/16")
	// teredo is RFC 4380 Teredo: bits 32..63 are the server IPv4, and the
	// low 32 bits the client IPv4 with every bit inverted.
	teredo = mustCIDR("2001::/32")
	// v4Compat is the deprecated RFC 4291 IPv4-compatible ::a.b.c.d form;
	// :: and ::1 inside it are judged as themselves.
	v4Compat = mustCIDR("::/96")
	// v4Reserved are IPv4 ranges the stdlib helpers do not flag and that
	// are never a legitimate outbound target.
	v4Reserved = []struct {
		net    *net.IPNet
		reason string
	}{
		{mustCIDR("0.0.0.0/8"), "this-network"},
		{mustCIDR("198.18.0.0/15"), "benchmarking"},
		{mustCIDR("240.0.0.0/4"), "reserved"}, // includes 255.255.255.255
	}
	cgnat = mustCIDR("100.64.0.0/10")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// DenyReason returns a non-empty reason when ip is in a denied (internal
// or non-routable) range under the policy, and "" when it may be dialled.
//
// An IPv6 address that embeds an IPv4 address is judged by the IPv4 it
// embeds: NAT64 (RFC 6052 64:ff9b::/96 and RFC 8215 local-use
// 64:ff9b:1::/48), 6to4, Teredo (server and client) and IPv4-compatible
// ::a.b.c.d. A DNS64 network legitimately maps public IPv4 into
// 64:ff9b::/96, so the prefix itself is not denied, but 64:ff9b::a00:1
// (10.0.0.1) is.
func (p Policy) DenyReason(ip net.IP) string {
	if ip == nil {
		return "unparseable"
	}
	if ip.To4() == nil && len(ip) == net.IPv6len {
		if label, addrs, ok := embeddedIPv4(ip); ok {
			if len(addrs) == 0 {
				return label + " without an embedded address"
			}
			for _, e := range addrs {
				if r := p.DenyReason(e.ip); r != "" {
					return label + e.role + "-embedded " + r
				}
			}
			return ""
		}
	}
	switch {
	case ip.IsLoopback():
		if p.AllowLoopback {
			return ""
		}
		return "loopback"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// 169.254.0.0/16 incl. the 169.254.169.254 cloud metadata IP, and
		// fe80::/10.
		return "link-local"
	case ip.IsPrivate():
		if p.AllowPrivate {
			return ""
		}
		return "private"
	case ip.IsMulticast():
		return "multicast"
	case ip.IsInterfaceLocalMulticast():
		return "interface-local-multicast"
	}
	if v4 := ip.To4(); v4 != nil {
		if cgnat.Contains(v4) {
			if p.AllowPrivate {
				return ""
			}
			return "cgnat"
		}
		for _, r := range v4Reserved {
			if r.net.Contains(v4) {
				return r.reason
			}
		}
	}
	return ""
}

// embedded is one IPv4 address read out of an IPv6 address. role is ""
// or, for Teredo, "-server" / "-client".
type embedded struct {
	role string
	ip   net.IP
}

// embeddedIPv4 reports whether ip is an IPv4-embedding IPv6 form, with its
// label and every IPv4 address it may carry. ok with no addresses means
// the form is recognised but no address could be read: the caller denies.
func embeddedIPv4(ip net.IP) (label string, addrs []embedded, ok bool) {
	switch {
	case nat64.Contains(ip):
		return "nat64", []embedded{{"", ip[12:16]}}, true
	case nat64Local.Contains(ip):
		return "nat64-local", nat64LocalCandidates(ip), true
	case sixToFour.Contains(ip):
		return "6to4", []embedded{{"", ip[2:6]}}, true
	case teredo.Contains(ip):
		client := make(net.IP, net.IPv4len)
		for i := range client {
			client[i] = ^ip[12+i]
		}
		return "teredo", []embedded{{"-server", ip[4:8]}, {"-client", client}}, true
	case v4Compat.Contains(ip) && !ip.IsUnspecified() && !ip.IsLoopback():
		return "ipv4-compatible", []embedded{{"", ip[12:16]}}, true
	}
	return "", nil, false
}

// nat64LocalCandidates reads the IPv4 address out of a 64:ff9b:1::/48
// address at every RFC 6052 prefix length that fits inside the /48 (48,
// 56, 64 and 96) and whose layout the address is consistent with: the u
// octet (bits 64..71) and the suffix after the IPv4 are zero. A 0.0.0.0
// reading is dropped, since it is what a longer layout reads from the
// zero suffix of a shorter one. The caller denies when any remaining
// reading is denied, so an ambiguous address fails closed.
func nat64LocalCandidates(ip net.IP) []embedded {
	if ip[8] != 0 {
		return nil
	}
	layouts := []struct {
		octets [4]int
		suffix int // ip[suffix:] must be zero
	}{
		{[4]int{6, 7, 9, 10}, 11},    // /48
		{[4]int{7, 9, 10, 11}, 12},   // /56
		{[4]int{9, 10, 11, 12}, 13},  // /64
		{[4]int{12, 13, 14, 15}, 16}, // /96
	}
	var out []embedded
	for _, l := range layouts {
		if !allZero(ip[l.suffix:]) {
			continue
		}
		v4 := net.IP{ip[l.octets[0]], ip[l.octets[1]], ip[l.octets[2]], ip[l.octets[3]]}
		if allZero(v4) {
			continue
		}
		out = append(out, embedded{"", v4})
	}
	return out
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// DialControl is a net.Dialer.Control hook that applies the policy to the
// resolved address being dialled. Control runs after resolution and before
// connect, once per address tried (every Happy-Eyeballs attempt included),
// so it sees exactly what is connected to. An address that does not parse
// as ip:port is refused: fail closed.
func (p Policy) DialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: dial address %q unparseable: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: dial address %q is not an IP", address)
	}
	if reason := p.DenyReason(ip); reason != "" {
		return fmt.Errorf("ssrf guard: dial to %s refused (%s); internal targets are denied", ip, reason)
	}
	return nil
}

// NewHTTPClient returns an *http.Client hardened against SSRF:
//
//   - its own *http.Transport, not http.DefaultTransport (#2202);
//   - Proxy nil: an environment proxy (HTTPS_PROXY) would make the client
//     dial the proxy, and the proxy dial the target, past DialControl;
//   - net.Dialer.Control = DialControl, so every dialled IP is re-checked
//     (DNS rebinding);
//   - redirects are never followed: CheckRedirect returns
//     http.ErrUseLastResponse, so a 3xx comes back as the response and the
//     body and its headers go nowhere else;
//   - timeout as a whole-request backstop for a caller that sets no
//     deadline.
func (p Policy) NewHTTPClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   p.DialControl,
	}
	tr.DialContext = dialer.DialContext
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
