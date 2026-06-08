package notify

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SSRF guard for the generic-webhook channel (slice 543 / threat-model S /
// P0-543-2).
//
// The webhook URL is OPERATOR-configured (env), never derived from
// notification content and never user-controlled free-text. That alone
// removes the per-notification SSRF vector. This guard is the SECOND leg:
// even an operator misconfiguration (or a future config surface) cannot
// point the channel at an internal-only address. The check is:
//
//   - scheme MUST be https (http allowed ONLY for explicit loopback in
//     tests is NOT permitted in production config — see AllowHTTP below);
//   - host MUST resolve/parse to a NON-internal address: loopback,
//     link-local (incl. the cloud metadata 169.254.169.254), private
//     RFC1918 / ULA, unspecified, and multicast ranges are denied;
//   - a literal-IP host is checked directly; a DNS name is resolved and
//     EVERY returned address is checked (DNS-rebinding-resistant at config
//     time — the resolved set must be entirely public).
//
// The guard runs at config-validation time (channel construction) so a bad
// target fails fast and visibly, not silently at send time.

// SSRFPolicy parameterizes the guard. The zero value is the strict
// production policy (https-only, deny all internal).
type SSRFPolicy struct {
	// AllowHTTP permits the http scheme. Production leaves this false
	// (https-only). It exists only so a test harness can target a local
	// httptest server; production config must never set it.
	AllowHTTP bool
	// AllowLoopback permits 127.0.0.0/8 and ::1. Production leaves this
	// false. Test-only, same rationale as AllowHTTP.
	AllowLoopback bool
	// resolveHost is injectable for tests; nil uses net.LookupIP.
	resolveHost func(host string) ([]net.IP, error)
}

// ValidateWebhookURL parses + validates a webhook target against the
// policy. It returns the cleaned absolute URL string on success, or a
// descriptive error naming the deny reason (never echoing a secret —
// the URL carries no secret; the bearer/HMAC are separate).
func (p SSRFPolicy) ValidateWebhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("webhook url empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("webhook url parse: %w", err)
	}
	switch u.Scheme {
	case "https":
		// ok
	case "http":
		if !p.AllowHTTP {
			return "", fmt.Errorf("webhook url scheme %q denied (https required)", u.Scheme)
		}
	default:
		return "", fmt.Errorf("webhook url scheme %q denied (https required)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("webhook url has no host")
	}

	ips, err := p.resolve(host)
	if err != nil {
		return "", fmt.Errorf("webhook url host %q resolve: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("webhook url host %q resolves to no addresses", host)
	}
	for _, ip := range ips {
		if reason := p.denyReason(ip); reason != "" {
			return "", fmt.Errorf("webhook url host %q resolves to %s (%s); internal targets are denied (SSRF guard)", host, ip, reason)
		}
	}
	return u.String(), nil
}

// resolve returns the IPs for a host. A literal IP host short-circuits DNS.
func (p SSRFPolicy) resolve(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if p.resolveHost != nil {
		return p.resolveHost(host)
	}
	return net.LookupIP(host)
}

// denyReason returns a non-empty reason string when ip is in a denied
// (internal / non-routable) range, honoring the policy's test allowances.
func (p SSRFPolicy) denyReason(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		if p.AllowLoopback {
			return ""
		}
		return "loopback"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// Covers 169.254.0.0/16 incl. the 169.254.169.254 cloud metadata IP
		// and fe80::/10.
		return "link-local"
	case ip.IsPrivate():
		// RFC1918 (10/8, 172.16/12, 192.168/16) + ULA fc00::/7.
		return "private"
	case ip.IsMulticast():
		return "multicast"
	case ip.IsInterfaceLocalMulticast():
		return "interface-local-multicast"
	}
	// Carrier-grade NAT 100.64.0.0/10 is not flagged by the stdlib helpers;
	// deny it explicitly (it is not a legitimate public webhook target).
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return "cgnat"
	}
	return ""
}
