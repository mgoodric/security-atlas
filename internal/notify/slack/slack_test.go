package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/notify"
)

func TestConfig_Redacted_NeverLeaksURL(t *testing.T) {
	t.Parallel()
	cfg := Config{WebhookURL: notify.Secret("https://hooks.slack.com/services/T000/B000/test-slack-token")}
	out := cfg.Redacted()
	if strings.Contains(out, "test-slack-token") {
		t.Fatalf("Redacted leaked webhook URL secret: %q", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Fatalf("Redacted did not render placeholder: %q", out)
	}
}

func TestConfigFromEnv_ParsesAndDefaults(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		envSlackWebhookURL: "https://hooks.slack.com/services/X",
		envBaseURL:         "https://atlas.example.test",
	}
	cfg := configFromLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if !cfg.Enabled() {
		t.Fatalf("expected enabled with a webhook url")
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("expected default timeout, got %s", cfg.Timeout)
	}
	if cfg.BaseURL != "https://atlas.example.test" {
		t.Fatalf("base url not parsed: %q", cfg.BaseURL)
	}
	// Empty env => inert.
	empty := configFromLookup(func(string) (string, bool) { return "", false })
	if empty.Enabled() {
		t.Fatalf("empty config must be inert")
	}
}

// BuildMessage: minimum disclosure (counts + deep-link only, closed labels)
// and Slack-context escaping (P0-543-1 / threat-model I, T).
func TestBuildMessage_MinimumDisclosure(t *testing.T) {
	t.Parallel()
	body, err := BuildMessage(notify.Summary{
		TypeCounts:  map[string]int{"control.drift": 2, "policy_ack_due": 1},
		TotalUnread: 3,
		DeepLink:    "https://atlas.example.test/notifications",
	})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	s := string(body)
	// Carries counts + closed labels + deep-link.
	for _, want := range []string{"Control-drift alerts", "Policy acknowledgments due",
		"https://atlas.example.test/notifications", "3 unread"} {
		if !strings.Contains(s, want) {
			t.Errorf("payload missing %q:\n%s", want, s)
		}
	}
	// Carries NO raw type strings (minimum disclosure / no raw-type echo).
	for _, leak := range []string{"control.drift", "policy_ack_due"} {
		if strings.Contains(s, leak) {
			t.Errorf("raw type string %q leaked into payload:\n%s", leak, s)
		}
	}
	// Must be valid JSON.
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
}

func TestBuildMessage_Escapes(t *testing.T) {
	t.Parallel()
	// A deep-link with Slack control chars must be entity-escaped, not
	// passed through into the <url|text> link.
	body, err := BuildMessage(notify.Summary{
		TypeCounts:  map[string]int{"control.drift": 1},
		TotalUnread: 1,
		DeepLink:    "https://atlas.example.test/notifications?a=1&b=<2>",
	})
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	// Decode the JSON so we assert on the actual Slack-escaped text (JSON
	// itself further escapes & and < as & / < on the wire).
	var m struct {
		Blocks []struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	var linkBlock string
	for _, b := range m.Blocks {
		if strings.Contains(b.Text.Text, "notifications") {
			linkBlock = b.Text.Text
		}
	}
	// The raw "<2>" must not appear unescaped (it would corrupt the link).
	if strings.Contains(linkBlock, "b=<2>") {
		t.Errorf("unescaped Slack control chars in link block:\n%s", linkBlock)
	}
	if !strings.Contains(linkBlock, "&amp;") || !strings.Contains(linkBlock, "&lt;2&gt;") {
		t.Errorf("expected Slack-escaped entities in link block:\n%s", linkBlock)
	}
}

func TestBuildMessage_EmptyRejected(t *testing.T) {
	t.Parallel()
	if _, err := BuildMessage(notify.Summary{TotalUnread: 0}); err == nil {
		t.Fatalf("empty summary must be rejected")
	}
}

// HTTPTransport posts to the configured URL; the URL secret never appears in
// a returned error.
func TestHTTPTransport_Post_OK(t *testing.T) {
	t.Parallel()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(Config{WebhookURL: notify.Secret(srv.URL), Timeout: DefaultTimeout})
	if err := tr.Post(context.Background(), []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if string(got) != `{"text":"hi"}` {
		t.Fatalf("server got %q", got)
	}
}

func TestHTTPTransport_Post_ErrorScrubsSecret(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	secretURL := srv.URL + "/services/test-slack-token"
	tr := NewHTTPTransport(Config{WebhookURL: notify.Secret(secretURL), Timeout: DefaultTimeout})
	err := tr.Post(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatalf("expected non-2xx error")
	}
	if strings.Contains(err.Error(), "test-slack-token") {
		t.Fatalf("error leaked URL secret: %v", err)
	}
}

func TestHTTPTransport_Inert(t *testing.T) {
	t.Parallel()
	tr := NewHTTPTransport(Config{})
	if err := tr.Post(context.Background(), []byte(`{}`)); err != ErrNotConfigured {
		t.Fatalf("inert transport should return ErrNotConfigured, got %v", err)
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
