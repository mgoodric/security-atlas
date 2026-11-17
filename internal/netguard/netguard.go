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
	// sixToFour is RFC 3056 6to4; bits 16..47 are an IPv4 address.
	sixToFour = mustCIDR("2002::/16")
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
// NAT64 and 6to4 addresses are judged by the IPv4 address they embed: a
// DNS64 network legitimately maps public IPv4 into 64:ff9b::/96, so the
// prefix itself is not denied, but 64:ff9b::a00:1 (10.0.0.1) is.
func (p Policy) DenyReason(ip net.IP) string {
	if ip == nil {
		return "unparseable"
	}
	if ip.To4() == nil && len(ip) == net.IPv6len {
		switch {
		case nat64.Contains(ip):
			if r := p.DenyReason(net.IP(ip[12:16])); r != "" {
				return "nat64-embedded " + r
			}
			return ""
		case sixToFour.Contains(ip):
			if r := p.DenyReason(net.IP(ip[2:6])); r != "" {
				return "6to4-embedded " + r
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
