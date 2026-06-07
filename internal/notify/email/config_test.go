package email

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// AC-1: config reads from ATLAS_SMTP_* env with sensible defaults.
func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Parallel()
	// Use a name-scoped lookup so we don't disturb the real process env in
	// parallel tests: ConfigFromEnv reads os.Getenv, so set+unset around a
	// non-parallel sub-block.
	cfg := configFromLookup(func(string) (string, bool) { return "", false })
	if cfg.Port != DefaultSMTPPort {
		t.Fatalf("default port = %d, want %d", cfg.Port, DefaultSMTPPort)
	}
	if cfg.Timeout != DefaultSendTimeout {
		t.Fatalf("default timeout = %s, want %s", cfg.Timeout, DefaultSendTimeout)
	}
	if cfg.Enabled() {
		t.Fatalf("empty config must report Enabled()=false (no host)")
	}
}

func TestConfigFromEnv_Populated(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		envSMTPHost:    "smtp.example.test",
		envSMTPPort:    "2525",
		envSMTPSender:  "atlas@example.test",
		envSMTPUser:    "atlas-user",
		envSMTPPass:    "test-smtp-password",
		envSMTPTimeout: "5s",
		envBaseURL:     "https://atlas.example.test",
	}
	cfg := configFromLookup(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if cfg.Host != "smtp.example.test" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.Port != 2525 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if cfg.Sender != "atlas@example.test" {
		t.Fatalf("sender = %q", cfg.Sender)
	}
	if cfg.Timeout != 5*time.Second {
		t.Fatalf("timeout = %s", cfg.Timeout)
	}
	if cfg.BaseURL != "https://atlas.example.test" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if !cfg.Enabled() {
		t.Fatalf("populated config must report Enabled()=true")
	}
}

// D9: the SMTP password must never appear in a log/redacted rendering.
func TestConfigRedact_NoPassword(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Host:     "smtp.example.test",
		Port:     587,
		Sender:   "atlas@example.test",
		Username: "atlas-user",
		Password: "test-smtp-password",
	}
	s := cfg.Redacted()
	if contains(s, "test-smtp-password") {
		t.Fatalf("Redacted() leaked password: %q", s)
	}
	if !contains(s, "smtp.example.test") {
		t.Fatalf("Redacted() should still show host: %q", s)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
