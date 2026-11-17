package notify

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeepLink(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", "/notifications"},
		{"https://atlas.example.test", "https://atlas.example.test/notifications"},
		{"https://atlas.example.test/", "https://atlas.example.test/notifications"},
		{"  https://atlas.example.test//  ", "https://atlas.example.test/notifications"},
	}
	for _, c := range cases {
		if got := DeepLink(c.in); got != c.want {
			t.Errorf("DeepLink(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestTypeLabel_ClosedMap(t *testing.T) {
	t.Parallel()
	if got := TypeLabel("control.drift"); got != "Control-drift alerts" {
		t.Errorf("known label = %q", got)
	}
	// An unknown / attacker-controlled type never echoes raw — falls back.
	if got := TypeLabel("evil<script>"); got != "Other notifications" {
		t.Errorf("unknown type must fall back, got %q", got)
	}
}

func TestSummary_SortedTypes(t *testing.T) {
	t.Parallel()
	s := Summary{TypeCounts: map[string]int{"b": 1, "a": 2, "c": 3}}
	got := s.SortedTypes()
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("SortedTypes=%v not sorted", got)
	}
}

func TestDigestKeyForDay_ChannelNamespaced(t *testing.T) {
	t.Parallel()
	slack := DigestKeyForDay("slack", "2026-06-07")
	webhook := DigestKeyForDay("webhook", "2026-06-07")
	if slack == webhook {
		t.Fatalf("per-channel keys must not collide: %q == %q", slack, webhook)
	}
	if slack != "slack:digest:2026-06-07" {
		t.Errorf("unexpected key %q", slack)
	}
}

// Secret must NEVER render its plaintext through any formatting or
// serialization path (P0-543-5).
func TestSecret_RedactsEverywhere(t *testing.T) {
	t.Parallel()
	const plain = "test-slack-token-value"
	s := Secret(plain)

	checks := map[string]string{
		"String()": s.String(),
		"%v":       fmt.Sprintf("%v", s),
		"%q":       fmt.Sprintf("%q", s),
		"%#v":      fmt.Sprintf("%#v", s),
		"%+v":      fmt.Sprintf("%+v", s),
	}
	for verb, out := range checks {
		if strings.Contains(out, plain) {
			t.Errorf("Secret leaked plaintext via %s: %q", verb, out)
		}
		if !strings.Contains(out, "redacted") {
			t.Errorf("Secret %s did not render redaction: %q", verb, out)
		}
	}

	// json.Marshal of a struct embedding the secret must not leak it.
	type cfg struct {
		Token Secret `json:"token"`
	}
	b, err := json.Marshal(cfg{Token: s})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), plain) {
		t.Fatalf("Secret leaked through json.Marshal: %s", b)
	}

	// Reveal is the only path to plaintext (transport boundary).
	if s.Reveal() != plain {
		t.Fatalf("Reveal must return plaintext for transport")
	}
}

func TestScrubSecret(t *testing.T) {
	t.Parallel()
	tok := Secret("test-bearer-abc123")
	msg := "POST failed: header Authorization: Bearer test-bearer-abc123 rejected"
	got := ScrubSecret(msg, tok)
	if strings.Contains(got, "test-bearer-abc123") {
		t.Fatalf("ScrubSecret left plaintext: %q", got)
	}
	if !strings.Contains(got, "redacted") {
		t.Fatalf("ScrubSecret did not insert placeholder: %q", got)
	}
	// Empty secret is a no-op (does not blank the whole string).
	if ScrubSecret("hello", Secret("")) != "hello" {
		t.Fatalf("empty secret should be a no-op")
	}
}

// SSRF guard: internal targets are denied; public targets pass (P0-543-2).
func TestSSRFPolicy_DeniesInternal(t *testing.T) {
	t.Parallel()
	// Strict production policy with a fake resolver so the test is
	// hermetic (no real DNS).
	resolve := func(host string) ([]net.IP, error) {
		switch host {
		case "metadata.evil.test":
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		case "internal.corp.test":
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		case "hooks.public.test":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "rebind.test":
			// One public, one private — must be denied (every addr checked).
			return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("192.168.1.5")}, nil
		}
		return nil, fmt.Errorf("no such host %q", host)
	}
	p := SSRFPolicy{resolveHost: resolve}

	denied := []string{
		"https://metadata.evil.test/hook",          // cloud metadata via DNS
		"https://internal.corp.test/hook",          // RFC1918 via DNS
		"https://169.254.169.254/latest/meta-data", // literal metadata IP
		"https://127.0.0.1/hook",                   // loopback literal
		"https://10.0.0.5/hook",                    // RFC1918 literal
		"https://192.168.1.1/hook",                 // RFC1918 literal
		"https://[::1]/hook",                       // ipv6 loopback
		"https://[fd00::1]/hook",                   // ULA
		"https://100.64.0.1/hook",                  // CGNAT
		"http://hooks.public.test/hook",            // http scheme denied
		"ftp://hooks.public.test/hook",             // non-http scheme
		"https://rebind.test/hook",                 // mixed public+private
		"",                                         // empty
		// Shared netguard ranges (#2534): this-network, broadcast,
		// benchmarking, reserved, and NAT64 / 6to4 wrapping internal v4.
		"https://0.1.2.3/hook",
		"https://255.255.255.255/hook",
		"https://198.18.0.1/hook",
		"https://240.0.0.1/hook",
		"https://[64:ff9b::a9fe:a9fe]/hook",
		"https://[2002:a00:1::1]/hook",
	}
	for _, raw := range denied {
		if _, err := p.ValidateWebhookURL(raw); err == nil {
			t.Errorf("expected deny for %q, got allow", raw)
		}
	}

	if got, err := p.ValidateWebhookURL("https://hooks.public.test/services/X"); err != nil {
		t.Errorf("public https target should pass: %v", err)
	} else if !strings.HasPrefix(got, "https://hooks.public.test/") {
		t.Errorf("unexpected cleaned url %q", got)
	}
}

// Test-only allowances let a local httptest server be targeted without
// loosening the production default.
func TestSSRFPolicy_TestAllowances(t *testing.T) {
	t.Parallel()
	p := SSRFPolicy{AllowHTTP: true, AllowLoopback: true}
	if _, err := p.ValidateWebhookURL("http://127.0.0.1:12345/hook"); err != nil {
		t.Fatalf("loopback+http should pass with test allowances: %v", err)
	}
	// Even with allowances, link-local metadata is still denied.
	if _, err := p.ValidateWebhookURL("http://169.254.169.254/"); err == nil {
		t.Fatalf("metadata IP must be denied even with test allowances")
	}
}

// The dial-time hook judges the resolved ip:port actually being dialled
// (#2304). Anything that is not a parseable public IP is refused.
func TestSSRFPolicy_DialControl(t *testing.T) {
	t.Parallel()
	strict := SSRFPolicy{}
	deny := []string{
		"127.0.0.1:443",
		"[::1]:443",
		"0.0.0.0:443",
		"[::]:443",
		"169.254.169.254:80",
		"[fe80::1]:443",
		"10.1.2.3:443",
		"192.168.1.1:443",
		"[fd00::1]:443",
		"100.64.0.1:443",
		"[::ffff:127.0.0.1]:443", // IPv4-mapped IPv6 loopback
		"[::ffff:169.254.169.254]:443",
		"not-an-address",
		"hooks.public.test:443", // a hostname, not a resolved IP: fail closed
	}
	for _, a := range deny {
		if err := strict.DialControl("tcp", a, nil); err == nil {
			t.Errorf("DialControl(%q) = nil, want refusal", a)
		}
	}
	for _, a := range []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443"} {
		if err := strict.DialControl("tcp", a, nil); err != nil {
			t.Errorf("DialControl(%q) = %v, want allowed", a, err)
		}
	}
	if err := (SSRFPolicy{AllowLoopback: true}).DialControl("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("AllowLoopback should permit loopback at dial time: %v", err)
	}
}

// The hardened client must not route through an environment proxy (a
// proxy dial would bypass DialControl), must refuse redirects, and must
// carry a backstop timeout (#2304).
func TestSSRFPolicy_NewHTTPClient_Hardened(t *testing.T) {
	t.Parallel()
	c := SSRFPolicy{}.NewHTTPClient(7 * time.Second)
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr == http.DefaultTransport {
		t.Fatalf("client must own its *http.Transport")
	}
	if tr.Proxy != nil {
		t.Fatalf("Proxy must be nil: an environment proxy bypasses the dial-time check")
	}
	if c.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %s, want 7s backstop", c.Timeout)
	}
	if c.CheckRedirect == nil || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatalf("CheckRedirect must return http.ErrUseLastResponse")
	}
}
