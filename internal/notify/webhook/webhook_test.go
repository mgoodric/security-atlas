package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/notify"
)

func TestConfig_Redacted_NeverLeaksSecrets(t *testing.T) {
	t.Parallel()
	cfg := Config{
		URL:        "https://hooks.public.test/x",
		Bearer:     notify.Secret("test-bearer-secret"),
		HMACSecret: notify.Secret("test-hmac-secret"),
	}
	out := cfg.Redacted()
	for _, leak := range []string{"test-bearer-secret", "test-hmac-secret"} {
		if strings.Contains(out, leak) {
			t.Fatalf("Redacted leaked secret %q: %q", leak, out)
		}
	}
	// The URL (operator config, no credential) is fine to show.
	if !strings.Contains(out, "https://hooks.public.test/x") {
		t.Fatalf("Redacted should show the non-secret URL: %q", out)
	}
}

func TestBuildPayload_MinimumDisclosure(t *testing.T) {
	t.Parallel()
	body, err := BuildPayload(notify.Summary{
		TypeCounts:  map[string]int{"control.drift": 2, "policy_ack_due": 1},
		TotalUnread: 3,
		DeepLink:    "https://atlas.example.test/notifications",
	})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if p.TotalUnread != 3 {
		t.Errorf("total = %d", p.TotalUnread)
	}
	if p.Counts["Control-drift alerts"] != 2 || p.Counts["Policy acknowledgments due"] != 1 {
		t.Errorf("counts not mapped to closed labels: %+v", p.Counts)
	}
	if p.DeepLink != "https://atlas.example.test/notifications" {
		t.Errorf("deep link = %q", p.DeepLink)
	}
	// NO raw type strings on the wire (minimum disclosure / closed labels).
	s := string(body)
	for _, leak := range []string{"control.drift", "policy_ack_due"} {
		if strings.Contains(s, leak) {
			t.Errorf("raw type %q leaked into payload:\n%s", leak, s)
		}
	}
}

func TestBuildPayload_EmptyRejected(t *testing.T) {
	t.Parallel()
	if _, err := BuildPayload(notify.Summary{TotalUnread: 0}); err == nil {
		t.Fatalf("empty summary must be rejected")
	}
}

// SSRF guard at construction: an internal target is rejected before any
// send (P0-543-2). Uses a hermetic resolver.
func TestNewHTTPTransport_SSRFDeny(t *testing.T) {
	t.Parallel()
	resolve := func(host string) ([]net.IP, error) {
		if host == "metadata.evil.test" {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	policy := notify.SSRFPolicy{}
	// Inject the resolver via the exported policy by routing through a
	// literal-IP target (no DNS) AND through the hermetic resolver test in
	// the notify package; here we assert the literal internal IP is denied.
	_ = resolve

	deny := []string{
		"https://169.254.169.254/hook",
		"https://127.0.0.1/hook",
		"https://10.0.0.1/hook",
		"http://hooks.public.test/hook", // http scheme denied by strict policy
	}
	for _, raw := range deny {
		if _, err := NewHTTPTransport(Config{URL: raw}, policy); err == nil {
			t.Errorf("expected SSRF/scheme deny for %q at construction", raw)
		}
	}
}

func TestNewHTTPTransport_PublicAllowed(t *testing.T) {
	t.Parallel()
	// A literal public IP passes the strict policy with no DNS.
	tr, err := NewHTTPTransport(Config{URL: "https://93.184.216.34/hook"}, notify.SSRFPolicy{})
	if err != nil {
		t.Fatalf("public target should pass: %v", err)
	}
	if tr.url == "" {
		t.Fatalf("transport url not set")
	}
}

func TestNewHTTPTransport_InertNoURL(t *testing.T) {
	t.Parallel()
	tr, err := NewHTTPTransport(Config{}, notify.SSRFPolicy{})
	if err != nil {
		t.Fatalf("inert config should not error: %v", err)
	}
	if err := tr.Post(context.Background(), []byte(`{}`)); err != ErrNotConfigured {
		t.Fatalf("inert transport should return ErrNotConfigured, got %v", err)
	}
}

// HTTPTransport.Post sends bearer + HMAC headers and never leaks secrets.
func TestHTTPTransport_Post_HeadersAndScrub(t *testing.T) {
	t.Parallel()
	var gotAuth, gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSig = r.Header.Get(signatureHeader)
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use test allowances so the loopback httptest server is reachable.
	cfg := Config{
		URL:        srv.URL + "/hook",
		Bearer:     notify.Secret("test-bearer-secret"),
		HMACSecret: notify.Secret("test-hmac-secret"),
		Timeout:    DefaultTimeout,
	}
	tr, err := NewHTTPTransport(cfg, notify.SSRFPolicy{AllowHTTP: true, AllowLoopback: true})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	body := []byte(`{"event":"notification.digest"}`)
	if err := tr.Post(context.Background(), body); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if gotAuth != "Bearer test-bearer-secret" {
		t.Errorf("bearer header = %q", gotAuth)
	}
	// HMAC header is the hex SHA256 of the body keyed by the secret.
	mac := hmac.New(sha256.New, []byte("test-hmac-secret"))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Errorf("signature = %q want %q", gotSig, want)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body = %q", gotBody)
	}
}

func TestHTTPTransport_Post_ErrorScrubsSecret(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Authorization: Bearer test-bearer-secret was rejected"))
	}))
	defer srv.Close()

	cfg := Config{
		URL:     srv.URL + "/hook",
		Bearer:  notify.Secret("test-bearer-secret"),
		Timeout: DefaultTimeout,
	}
	tr, err := NewHTTPTransport(cfg, notify.SSRFPolicy{AllowHTTP: true, AllowLoopback: true})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	err = tr.Post(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatalf("expected non-2xx error")
	}
	if strings.Contains(err.Error(), "test-bearer-secret") {
		t.Fatalf("error leaked bearer secret: %v", err)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 256)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
