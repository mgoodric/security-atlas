package email

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

// Message is a built, header-safe email ready for a Provider to send.
// Recipient + Subject are already CRLF-stripped; HTMLBody is already
// HTML-escaped. Provider implementations transmit Wire() verbatim.
type Message struct {
	Sender    string
	Recipient string
	Subject   string
	HTMLBody  string
}

// DigestInput is the minimum-disclosure input to BuildDigest. It carries
// COUNTS, not notification contents (P0-445-4 / AC-7). TypeCounts maps a
// notification `type` constant to the number unread of that type.
type DigestInput struct {
	Recipient   string
	BaseURL     string
	TypeCounts  map[string]int
	TotalUnread int
}

// digestSubject is the fixed subject template -- a constant, never
// interpolated from notification content (defense against subject-line
// header injection, the classic vector; the CRLF strip is still applied
// unconditionally as defense-in-depth, D3).
const digestSubject = "Your security-atlas notification digest"

// BuildDigest assembles the minimum-disclosure digest message.
//
//   - Recipient + Subject are CRLF-stripped unconditionally
//     (header-injection / open-relay guard, AC-6 / AC-12 / P0-445-2).
//   - All interpolated body values are HTML-escaped (AC-14).
//   - The body carries summary counts + a single deep-link only; no
//     notification payload, no evidence/S3/control text (AC-4 / AC-7).
func BuildDigest(in DigestInput) (Message, error) {
	if in.Recipient == "" {
		return Message{}, fmt.Errorf("email: digest recipient empty")
	}

	deepLink := buildDeepLink(in.BaseURL)

	var b strings.Builder
	b.WriteString("<!doctype html><html><body>")
	b.WriteString("<p>Hello,</p>")
	fmt.Fprintf(&b,
		"<p>You have <strong>%d</strong> unread notification%s in security-atlas:</p>",
		in.TotalUnread, plural(in.TotalUnread),
	)

	b.WriteString("<ul>")
	for _, t := range sortedTypes(in.TypeCounts) {
		count := in.TypeCounts[t]
		if count <= 0 {
			continue
		}
		// typeLabel maps a type constant to a closed human-readable label;
		// html.EscapeString is belt-and-suspenders (the label set is
		// closed, but the integer count and label both pass through escape
		// so a future open label inherits the guard).
		fmt.Fprintf(&b,
			"<li>%s: %s</li>",
			html.EscapeString(typeLabel(t)),
			html.EscapeString(fmt.Sprintf("%d", count)),
		)
	}
	b.WriteString("</ul>")

	fmt.Fprintf(&b,
		`<p><a href="%s">Open your notifications in security-atlas</a> to see the details.</p>`,
		html.EscapeString(deepLink),
	)
	b.WriteString("<hr>")
	b.WriteString(
		"<p style=\"color:#666;font-size:12px\">" +
			"You received this because you opted in to email notifications. " +
			"Manage delivery in Settings &rarr; Notifications." +
			"</p>")
	b.WriteString("</body></html>")

	return Message{
		Recipient: stripHeaderValue(in.Recipient),
		Subject:   stripHeaderValue(digestSubject),
		HTMLBody:  b.String(),
	}, nil
}

// Wire renders the RFC-5322 message (headers + HTML body) as bytes for
// SMTP DATA. Sender is filled by the channel before send; here it is
// rendered from Message.Sender (also CRLF-stripped).
func (m Message) Wire() []byte {
	var b strings.Builder
	b.WriteString("From: " + stripHeaderValue(m.Sender) + "\r\n")
	b.WriteString("To: " + stripHeaderValue(m.Recipient) + "\r\n")
	b.WriteString("Subject: " + stripHeaderValue(m.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(m.HTMLBody)
	return []byte(b.String())
}

// stripHeaderValue removes ALL control characters (including CR, LF, NUL,
// tab) from a value destined for an email header. This is the
// header-injection / open-relay guard (AC-6 / AC-12 / P0-445-2): a value
// containing "\r\nBcc: attacker" collapses to a single header line, so no
// notification-derived value can introduce a new header or recipient.
func stripHeaderValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop C0 controls (incl. CR, LF, NUL, TAB) and DEL.
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// buildDeepLink composes the notifications deep-link from the public base
// URL. A missing base URL yields a relative path (still a working link in
// most clients via the operator's known host).
func buildDeepLink(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "/notifications"
	}
	return base + "/notifications"
}

// typeLabels is the closed map from notification `type` constant to a
// human-readable label. Keeping this closed means the digest never echoes
// a raw type string from the row (defense against type-taxonomy
// injection + minimum-disclosure). Unknown types fall back to a generic
// label.
var typeLabels = map[string]string{
	"audit_note.reply":        "Audit-note replies",
	"control.drift":           "Control-drift alerts",
	"policy_ack_due":          "Policy acknowledgments due",
	"risk_review_overdue":     "Overdue risk reviews",
	"audit_period_assignment": "Audit-period assignments",
	"evidence.staleness":      "Stale-evidence digests",
}

func typeLabel(t string) string {
	if l, ok := typeLabels[t]; ok {
		return l
	}
	return "Other notifications"
}

func sortedTypes(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// DigestKeyForDay returns the deterministic idempotency / rate-limit key
// for a given instant (D5/D6): one digest per user per UTC day. A
// same-day re-run collides on the UNIQUE delivery-log key; a new day
// produces a fresh key.
func DigestKeyForDay(at time.Time) string {
	return "digest:" + at.UTC().Format("2006-01-02")
}
