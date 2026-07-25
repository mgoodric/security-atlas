package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
)

// Provider is the thin email-delivery abstraction (D1): one method, one
// concrete SMTP implementation. The interface is the seam that lets the
// integration test substitute an in-memory sink (AC-11); it is NOT a
// plugin registry (no SES/SendGrid adapters in v0 -- YAGNI for the
// single-VM self-host target).
type Provider interface {
	// Send transmits one built Message. Implementations MUST honor the
	// context deadline (AC-8: SMTP sends have a timeout). The Message is
	// already header-safe (CRLF-stripped) and minimum-disclosure.
	Send(ctx context.Context, msg Message) error
}

// ErrNotConfigured is returned when delivery is attempted with no SMTP
// host configured. The channel treats this as "channel inert", not a
// hard error.
var ErrNotConfigured = errors.New("email: SMTP not configured")

// SMTPProvider is the stdlib-net/smtp implementation. Credentials come
// from Config (env-only, never logged -- D9).
type SMTPProvider struct {
	cfg Config
	// dial is injectable for tests; production uses net.Dialer.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewSMTPProvider constructs an SMTPProvider from config.
func NewSMTPProvider(cfg Config) *SMTPProvider {
	return &SMTPProvider{
		cfg: cfg,
		dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// Send delivers via SMTP submission. The context deadline bounds the dial
// (AC-8); a timeout/failure returns an error WITHOUT a hot retry -- the
// channel records the failure and leaves the digest unclaimed for the
// next tick (D8).
func (p *SMTPProvider) Send(ctx context.Context, msg Message) error {
	if !p.cfg.Enabled() {
		return ErrNotConfigured
	}
	// Apply the configured timeout as the dial+session deadline.
	if p.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.Timeout)
		defer cancel()
	}

	addr := net.JoinHostPort(p.cfg.Host, fmt.Sprintf("%d", p.cfg.Port))
	conn, err := p.dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	// Ensure the dial deadline is honored at the conn level too.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, p.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer func() { _ = c.Close() }()

	// Opportunistic STARTTLS when the server advertises it.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: p.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	// Authenticate when credentials are configured.
	if p.cfg.Username != "" {
		auth := smtp.PlainAuth("", p.cfg.Username, p.cfg.Password, p.cfg.Host)
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				// Never echo the credential; the server response is safe.
				return fmt.Errorf("email: smtp auth failed: %w", err)
			}
		}
	}

	sender := stripHeaderValue(p.cfg.Sender)
	recipient := stripHeaderValue(msg.Recipient)
	if err := c.Mail(sender); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(recipient); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	msg.Sender = sender
	msg.Recipient = recipient
	if _, err := w.Write(msg.Wire()); err != nil {
		_ = w.Close()
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close DATA: %w", err)
	}
	return c.Quit()
}
