package notify

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/mgoodric/security-atlas/internal/netguard"
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
// target fails fast and visibly, not silently at send time. A config-time
// check alone is not enough: DNS can change between validation and the
// dial. NewHTTPClient re-applies the same deny list to every dialled
// address and refuses redirects (#2304).

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
// The deny list itself is netguard's, shared with the OIDC client (#2534).
func (p SSRFPolicy) denyReason(ip net.IP) string {
	return p.guard().DenyReason(ip)
}

// guard is the netguard policy this notify policy maps to. Notify targets
// never get the private-network opt-in.
func (p SSRFPolicy) guard() netguard.Policy {
	return netguard.Policy{AllowLoopback: p.AllowLoopback}
}

// WithResolver returns a copy of the policy whose config-time check
// resolves hosts with f instead of net.LookupIP. It is a test seam: it
// changes ValidateWebhookURL only, never the dial-time check, which always
// judges the address actually being connected to.
func (p SSRFPolicy) WithResolver(f func(host string) ([]net.IP, error)) SSRFPolicy {
	p.resolveHost = f
	return p
}

// DialControl is a net.Dialer.Control hook that applies the policy to the
// resolved address being dialled (#2304). ValidateWebhookURL checks the
// host once, at config time; every later dial resolves the name again, so
// a hostname whose DNS an attacker controls could rebind to an internal
// address after validation. See netguard.Policy.DialControl.
func (p SSRFPolicy) DialControl(network, address string, c syscall.RawConn) error {
	return p.guard().DialControl(network, address, c)
}

// NewHTTPClient returns an *http.Client for an operator-configured
// outbound target, hardened against SSRF (#2304): dial-time IP checks, no
// proxy, no redirects, timeout backstop. See netguard.Policy.NewHTTPClient.
func (p SSRFPolicy) NewHTTPClient(timeout time.Duration) *http.Client {
	return p.guard().NewHTTPClient(timeout)
}
