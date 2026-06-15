// Pure-Go unit tests for the slice 445 email message builder.
//
// These cover the LOAD-BEARING security branches with no Postgres / no
// SMTP server:
//
//   - stripHeaderValue: CRLF-strip on header fields (AC-6 / AC-12 /
//     P0-445-2) — the open-relay / header-injection guard.
//   - BuildDigest: HTML-escape of interpolated values (AC-14), the
//     minimum-disclosure body shape (AC-4 / AC-7 / P0-445-4 — counts +
//     deep-link only, no payload), and the deterministic digest key
//     (AC-5).
//
// Run via `go test ./internal/notify/email/...` — no build tag.
package email

import (
	"strings"
	"testing"
)

func TestStripHeaderValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Atlas digest", "Atlas digest"},
		{"crlf injection", "Subject\r\nBcc: attacker@evil.test", "SubjectBcc: attacker@evil.test"},
		{"lone lf", "a\nb", "ab"},
		{"lone cr", "a\rb", "ab"},
		{"multiple", "a\r\n\r\nb\nc", "abc"},
		{"leading/trailing", "\r\nhi\r\n", "hi"},
		{"tab and null stripped too", "a\tb\x00c", "abc"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripHeaderValue(tc.in)
			if got != tc.want {
				t.Fatalf("stripHeaderValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("stripHeaderValue(%q) leaked CR/LF: %q", tc.in, got)
			}
		})
	}
}

// AC-12 / P0-445-2: a notification-derived value with CRLF must not
// inject an extra header or recipient into the assembled wire message.
func TestBuildDigest_NoHeaderInjection(t *testing.T) {
	t.Parallel()
	// An attacker-influenced account email (defense-in-depth: even if a
	// malicious value reached the recipient field, the header strip must
	// neutralize it).
	msg, err := BuildDigest(DigestInput{
		Recipient:   "victim@example.test\r\nBcc: attacker@evil.test",
		BaseURL:     "https://atlas.example.test",
		TypeCounts:  map[string]int{"audit_note.reply": 2},
		TotalUnread: 2,
	})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	wire := string(msg.Wire())
	headerBlock := wire
	if idx := strings.Index(wire, "\r\n\r\n"); idx >= 0 {
		headerBlock = wire[:idx]
	}
	// The guard's job: the CRLF strip must collapse the injected value so
	// no NEW header line is introduced. A standalone "Bcc:" header line
	// (a line that begins with "Bcc:") would be the injection; a merged
	// single-token "To:" line is the neutralized value.
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("header injection: standalone Bcc header line:\n%s", headerBlock)
		}
	}
	// Exactly one To: header line.
	toLines := 0
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(line, "To:") {
			toLines++
		}
	}
	if toLines != 1 {
		t.Fatalf("expected exactly one To: header, got %d:\n%s", toLines, headerBlock)
	}
	if got := msg.Recipient; strings.ContainsAny(got, "\r\n") {
		t.Fatalf("recipient leaked CR/LF: %q", got)
	}
	if got := msg.Subject; strings.ContainsAny(got, "\r\n") {
		t.Fatalf("subject leaked CR/LF: %q", got)
	}
}

// AC-14: HTML in any interpolated value is escaped in the HTML body.
func TestBuildDigest_HTMLEscaped(t *testing.T) {
	t.Parallel()
	msg, err := BuildDigest(DigestInput{
		Recipient: "user@example.test",
		// A malicious base URL standing in for any interpolated value.
		BaseURL:     `https://atlas.example.test/"><script>alert(1)</script>`,
		TypeCounts:  map[string]int{"audit_note.reply": 1},
		TotalUnread: 1,
	})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	body := msg.HTMLBody
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unescaped script tag in body:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected escaped script entity in body:\n%s", body)
	}
}

// AC-4 / AC-7 / P0-445-4: the body carries counts + a deep-link only.
// It must NOT echo notification payload values (evidence IDs, S3 URLs,
// control text, operator-entered note bodies).
func TestBuildDigest_MinimumDisclosure(t *testing.T) {
	t.Parallel()
	msg, err := BuildDigest(DigestInput{
		Recipient:   "user@example.test",
		BaseURL:     "https://atlas.example.test",
		TypeCounts:  map[string]int{"audit_note.reply": 3, "control.drift": 1},
		TotalUnread: 4,
	})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	body := msg.HTMLBody
	// Counts present.
	if !strings.Contains(body, "4") {
		t.Fatalf("total count missing from body:\n%s", body)
	}
	// Deep-link present and points at the notifications page.
	if !strings.Contains(body, "https://atlas.example.test/notifications") {
		t.Fatalf("deep-link missing from body:\n%s", body)
	}
	// Human-readable type labels (from the closed map), not raw type
	// constants leaking the internal taxonomy verbatim is acceptable, but
	// no payload markers should ever appear.
	for _, forbidden := range []string{"s3://", "evidence_id", "payload", "control_text"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("over-disclosure: %q present in body:\n%s", forbidden, body)
		}
	}
}

// An unknown notification type renders under a safe generic label rather
// than echoing the raw type string (defense against type-taxonomy
// injection + keeps the label set closed).
func TestTypeLabel_UnknownIsGeneric(t *testing.T) {
	t.Parallel()
	got := typeLabel("totally.unknown.type")
	if got == "totally.unknown.type" {
		t.Fatalf("unknown type echoed raw: %q", got)
	}
	if got == "" {
		t.Fatalf("unknown type produced empty label")
	}
}

// TestTypeLabel_StalenessHasSpecificLabel is the slice-541 unit-tier guard on
// the 439 -> 445 wiring: the slice-439 'evidence.staleness' notification type
// must resolve to its OWN human label in the digest (not the generic "Other
// notifications" fallback), so a staleness reminder reads as such in the
// inbox. Dropping the type from the label map would regress AC-2 silently;
// this pins it at the fast tier (the integration test pins the full sweep).
func TestTypeLabel_StalenessHasSpecificLabel(t *testing.T) {
	t.Parallel()
	got := typeLabel("evidence.staleness")
	if got == typeLabel("totally.unknown.type") {
		t.Fatalf("evidence.staleness fell through to the generic label: %q", got)
	}
	if got != "Stale-evidence digests" {
		t.Fatalf("evidence.staleness label = %q, want %q", got, "Stale-evidence digests")
	}
}

// TestBuildDigest_StalenessSurfacesInBody proves a digest built from an
// 'evidence.staleness' type count renders the staleness label in the body —
// the unit-tier complement to the scheduler integration test's full-sweep
// proof (slice-541 AC-2; slice-353 Q-2 fast pure-Go branch).
func TestBuildDigest_StalenessSurfacesInBody(t *testing.T) {
	t.Parallel()
	msg, err := BuildDigest(DigestInput{
		Recipient:   "op@example.test",
		BaseURL:     "https://atlas.example.test",
		TypeCounts:  map[string]int{"evidence.staleness": 3},
		TotalUnread: 3,
	})
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	if !strings.Contains(msg.HTMLBody, "Stale-evidence digests") {
		t.Fatalf("staleness label missing from digest body: %q", msg.HTMLBody)
	}
}

// AC-5: the digest key is deterministic per UTC day so a same-day
// re-run collides (idempotency / 24h rate-limit, D5/D6).
func TestDigestKeyForDay(t *testing.T) {
	t.Parallel()
	a := DigestKeyForDay(mustTime(t, "2026-06-07T01:00:00Z"))
	b := DigestKeyForDay(mustTime(t, "2026-06-07T23:59:00Z"))
	c := DigestKeyForDay(mustTime(t, "2026-06-08T00:00:00Z"))
	if a != b {
		t.Fatalf("same-day keys differ: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different-day keys collide: %q", a)
	}
	if !strings.HasPrefix(a, "digest:") {
		t.Fatalf("digest key missing prefix: %q", a)
	}
}
