// ics.go — minimal iCalendar 2.0 (RFC 5545) renderer for the slice-094
// compliance calendar.
//
// We hand-roll the encoder rather than pulling a library because the
// surface is tiny (a VCALENDAR containing N VEVENTs, no recurrence /
// alarms / timezones beyond UTC). The encoder produces VEVENTs whose
// UID:`{type}-{id}@security-atlas.example` is stable across re-renders,
// so Google / Apple / Outlook dedupe correctly when the same client
// re-fetches the URL.
//
// Line discipline per RFC 5545 §3.1:
//   - CRLF line endings.
//   - Long lines folded at 75 octets with a CRLF + leading space on the
//     continuation.
//   - Text fields are escaped per §3.3.11 (backslash, comma, semicolon,
//     newline).
//
// The encoder is pure: it takes a slice of sqlc rows and returns a string.
// No I/O, no time-source side effects beyond the `now` argument.
package calendar

import (
	"fmt"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// icsLineMaxOctets is the RFC 5545 §3.1 ceiling. The continuation line
// starts with a SPACE; the unfolded payload is the original.
const icsLineMaxOctets = 75

// icsProductID identifies the publishing application. Per RFC 5545 §3.7.3
// this is a free-form string; convention is `-//org//product//lang`.
const icsProductID = "-//security-atlas//compliance-calendar//EN"

// icsUIDDomain is the trailing "@<domain>" on UID values. It is a
// deliberate placeholder for self-host installs that want to disambiguate
// multiple calendars in their own client; the slice's narrative explicitly
// allows it (per UID:{type}-{id}@security-atlas.example).
const icsUIDDomain = "security-atlas.example"

// renderICS encodes the event rows as a valid iCalendar 2.0 document.
// calName is the X-WR-CALNAME label calendar clients display. now is the
// DTSTAMP timestamp put on every VEVENT (RFC 5545 requires it; "the time
// at which the calendar information was created").
func renderICS(rows []dbx.ListCalendarEventsRow, calName string, now time.Time) string {
	var b strings.Builder
	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:"+icsProductID)
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:PUBLISH")
	writeLine(&b, "X-WR-CALNAME:"+escapeText(calName))

	for _, row := range rows {
		uid := fmt.Sprintf("%s-%s@%s", row.EventType, row.EventID, icsUIDDomain)
		writeLine(&b, "BEGIN:VEVENT")
		writeLine(&b, "UID:"+uid)
		writeLine(&b, "DTSTAMP:"+formatUTC(now))
		writeLine(&b, "DTSTART:"+formatUTC(row.StartsAt.Time))
		// All-day-style fallback: VEVENTs without DTEND default to the same
		// instant as DTSTART, which most calendar clients render as a single
		// point. Acceptable for a deadline-style reminder. Only emit DTEND
		// when an explicit end is present.
		if row.EndsAt.Valid {
			writeLine(&b, "DTEND:"+formatUTC(row.EndsAt.Time))
		}
		writeLine(&b, "SUMMARY:"+escapeText(row.Title))
		desc := buildDescription(row)
		if desc != "" {
			writeLine(&b, "DESCRIPTION:"+escapeText(desc))
		}
		writeLine(&b, "STATUS:CONFIRMED")
		writeLine(&b, "CATEGORIES:"+escapeText(row.EventType))
		writeLine(&b, "END:VEVENT")
	}

	writeLine(&b, "END:VCALENDAR")
	return b.String()
}

// buildDescription gives calendar clients a one-line context blurb. We
// keep it terse so the calendar event view in Google/Apple shows the
// useful bits without scrolling. Returns the raw payload with real
// newlines between parts; escapeText turns those into the RFC 5545
// `\n` literal at line-write time.
func buildDescription(row dbx.ListCalendarEventsRow) string {
	parts := []string{
		"Type: " + row.EventType,
		"Status: " + row.Status,
	}
	if row.Cadence != nil && *row.Cadence != "" {
		parts = append(parts, "Cadence: "+*row.Cadence)
	}
	if row.Summary != "" {
		parts = append(parts, "Details: "+row.Summary)
	}
	return strings.Join(parts, "\n")
}

// formatUTC returns an RFC 5545 §3.3.5 UTC timestamp ("20260516T120000Z").
func formatUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// escapeText escapes the RFC 5545 §3.3.11 special characters in a text
// field: backslash, comma, semicolon, and CR/LF (rendered as the literal
// two-character sequence `\n`).
func escapeText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`,`, `\,`,
		`;`, `\;`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
	)
	return r.Replace(s)
}

// writeLine writes one logical iCalendar content line, folding at 75
// octets per RFC 5545 §3.1. Lines are terminated with CRLF.
func writeLine(b *strings.Builder, line string) {
	if len(line) <= icsLineMaxOctets {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	// First chunk goes out unprefixed; subsequent chunks start with a
	// SPACE per the folding rule.
	first := true
	for len(line) > 0 {
		chunk := icsLineMaxOctets
		// Subsequent lines reserve one octet for the leading SPACE so
		// the total line length still respects the 75-octet ceiling.
		if !first {
			chunk = icsLineMaxOctets - 1
		}
		if chunk > len(line) {
			chunk = len(line)
		}
		if !first {
			b.WriteString(" ")
		}
		b.WriteString(line[:chunk])
		b.WriteString("\r\n")
		line = line[chunk:]
		first = false
	}
}
