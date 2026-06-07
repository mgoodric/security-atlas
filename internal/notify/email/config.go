// Package email is the slice 445 email/SMTP notification delivery
// substrate. It is a delivery SINK for notifications already written to
// the slice-029 `/v1/me/notifications` store -- it does NOT produce
// notifications (P0-445-5).
//
// Shape:
//
//	Provider     -- a one-method abstraction (Send) with a stdlib-SMTP
//	                implementation (D1). Thin by design; no plugin registry.
//	Config       -- env-driven (ATLAS_SMTP_*) for the single-VM self-host
//	                target. Credentials are env-only and never logged (D9).
//	Message      -- a built, header-safe wire message (see message.go).
//	Channel      -- the delivery orchestrator (see channel.go): reads the
//	                opted-in user's notifications under their OWN tenant
//	                context (RLS), builds a minimum-disclosure digest, and
//	                delivers idempotently to the account email only.
//
// Constitutional invariants honored:
//
//	#6  Tenant isolation via RLS -- the channel reads notifications under
//	    the notification's own tenant GUC; cross-tenant email is proven
//	    absent (AC-13).
//
// Security guards (threat model T / I / S):
//
//   - header fields CRLF-stripped unconditionally (open-relay guard, AC-12)
//   - HTML body escaped (AC-14)
//   - body carries counts + deep-link only (minimum disclosure, AC-7)
//   - recipient is the account email only (no user-controlled field, AC-3)
//   - SMTP credentials env-only, never logged (D9)
package email

import (
	"fmt"
	"os"
	"time"
)

// Environment variable names. Prefixed ATLAS_SMTP_ per the repo
// ATLAS_* platform-config convention (mirrors internal/llm/config.go).
const (
	envSMTPHost    = "ATLAS_SMTP_HOST"
	envSMTPPort    = "ATLAS_SMTP_PORT"
	envSMTPSender  = "ATLAS_SMTP_SENDER"
	envSMTPUser    = "ATLAS_SMTP_USERNAME"
	envSMTPPass    = "ATLAS_SMTP_PASSWORD"
	envSMTPTimeout = "ATLAS_SMTP_TIMEOUT"
	// envBaseURL is the public base URL of the authenticated app, used to
	// build the digest deep-link. Shared convention with the rest of the
	// platform; falls back to a relative path when unset.
	envBaseURL = "ATLAS_PUBLIC_BASE_URL"
)

const (
	// DefaultSMTPPort is the SMTP submission port (STARTTLS).
	DefaultSMTPPort = 587
	// DefaultSendTimeout bounds a single SMTP dial+send (AC-8). A
	// slow/unreachable server fails fast rather than blocking the tick.
	DefaultSendTimeout = 10 * time.Second
)

// Config is the env-driven SMTP configuration for self-host (AC-1).
type Config struct {
	Host     string
	Port     int
	Sender   string
	Username string
	Password string
	Timeout  time.Duration
	// BaseURL is the public app base URL for the digest deep-link.
	BaseURL string
}

// ConfigFromEnv reads the SMTP config from the process environment.
func ConfigFromEnv() Config {
	return configFromLookup(os.LookupEnv)
}

// configFromLookup is the testable core of ConfigFromEnv. The lookup
// indirection lets tests supply an env without mutating os state in
// parallel.
func configFromLookup(lookup func(string) (string, bool)) Config {
	cfg := Config{
		Port:    DefaultSMTPPort,
		Timeout: DefaultSendTimeout,
	}
	if v, ok := lookup(envSMTPHost); ok {
		cfg.Host = v
	}
	if v, ok := lookup(envSMTPPort); ok {
		if p, err := parsePort(v); err == nil {
			cfg.Port = p
		}
	}
	if v, ok := lookup(envSMTPSender); ok {
		cfg.Sender = v
	}
	if v, ok := lookup(envSMTPUser); ok {
		cfg.Username = v
	}
	if v, ok := lookup(envSMTPPass); ok {
		cfg.Password = v
	}
	if v, ok := lookup(envSMTPTimeout); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Timeout = d
		}
	}
	if v, ok := lookup(envBaseURL); ok {
		cfg.BaseURL = v
	}
	return cfg
}

// Enabled reports whether enough config is present to attempt delivery.
// A deployment with no SMTP host configured has the channel inert -- the
// digest is computed/skipped, never sent to a dead host.
func (c Config) Enabled() bool {
	return c.Host != "" && c.Sender != ""
}

// Redacted returns a log-safe rendering that NEVER includes the password
// (D9 / threat-model S).
func (c Config) Redacted() string {
	return fmt.Sprintf(
		"smtp host=%s port=%d sender=%s username=%s password=<redacted> timeout=%s",
		c.Host, c.Port, c.Sender, c.Username, c.Timeout,
	)
}

func parsePort(s string) (int, error) {
	var p int
	if _, err := fmt.Sscanf(s, "%d", &p); err != nil {
		return 0, err
	}
	if p <= 0 || p > 65535 {
		return 0, fmt.Errorf("port out of range: %d", p)
	}
	return p, nil
}
