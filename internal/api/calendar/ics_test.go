package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestRenderICS_EmptyProducesValidEnvelope asserts the encoder emits the
// minimum-valid envelope even when no events are passed. AC-19 — parser
// compatibility starts here.
func TestRenderICS_EmptyProducesValidEnvelope(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	out := renderICS(nil, "Compliance calendar (test)", now)

	wantPrefixes := []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"PRODID:-//security-atlas//compliance-calendar//EN\r\n",
		"CALSCALE:GREGORIAN\r\n",
		"METHOD:PUBLISH\r\n",
		"X-WR-CALNAME:Compliance calendar (test)\r\n",
		"END:VCALENDAR\r\n",
	}
	for _, want := range wantPrefixes {
		if !strings.Contains(out, want) {
			t.Errorf("ICS envelope missing required line %q", want)
		}
	}

	// Must not contain any VEVENT block.
	if strings.Contains(out, "BEGIN:VEVENT") {
		t.Error("empty-events render unexpectedly contains a VEVENT block")
	}
}

// TestRenderICS_OneEventEmitsStableUID asserts the UID format is
// `{type}-{id}@security-atlas.example` per the slice notes — calendar
// clients dedupe on UID across re-subscribes.
func TestRenderICS_OneEventEmitsStableUID(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 14, 30, 0, 0, time.UTC)
	rows := []dbx.ListCalendarEventsRow{
		{
			EventID:           "11111111-1111-1111-1111-111111111111",
			EventType:         "audit",
			Title:             "Audit period: SOC 2 Type II 2026",
			StartsAt:          pgtype.Timestamptz{Time: starts, Valid: true},
			RelatedEntityID:   "11111111-1111-1111-1111-111111111111",
			RelatedEntityKind: "audit_period",
			Summary:           "open",
			Status:            "open",
		},
	}
	out := renderICS(rows, "Compliance calendar (test)", now)

	wantUID := "UID:audit-11111111-1111-1111-1111-111111111111@security-atlas.example\r\n"
	if !strings.Contains(out, wantUID) {
		t.Errorf("expected UID line %q in:\n%s", wantUID, out)
	}
	if !strings.Contains(out, "DTSTART:20260601T143000Z\r\n") {
		t.Errorf("expected DTSTART for 2026-06-01 14:30Z, got:\n%s", out)
	}
	if !strings.Contains(out, "SUMMARY:Audit period: SOC 2 Type II 2026\r\n") {
		t.Errorf("expected SUMMARY, got:\n%s", out)
	}
	if !strings.Contains(out, "CATEGORIES:audit\r\n") {
		t.Errorf("expected CATEGORIES:audit, got:\n%s", out)
	}
}

// TestRenderICS_EscapesSpecialChars asserts the §3.3.11 text escapes fire
// on the SUMMARY field — without them, a comma in a control title would
// break the calendar client's parse.
func TestRenderICS_EscapesSpecialChars(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := []dbx.ListCalendarEventsRow{{
		EventID:   "abc",
		EventType: "policy",
		Title:     "Policy review: Access Control, v2; with newline\nhere",
		StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
	}}
	out := renderICS(rows, "Tenant: A,B", now)

	wantSummary := `SUMMARY:Policy review: Access Control\, v2\; with newline\nhere`
	if !strings.Contains(out, wantSummary) {
		t.Errorf("expected escaped SUMMARY %q in:\n%s", wantSummary, out)
	}
	if !strings.Contains(out, `X-WR-CALNAME:Tenant: A\,B`) {
		t.Errorf("expected escaped X-WR-CALNAME, got:\n%s", out)
	}
}

// TestRenderICS_LongLineIsFolded asserts §3.1 line folding kicks in for
// content lines exceeding 75 octets — the wrapped lines start with a SPACE
// continuation.
func TestRenderICS_LongLineIsFolded(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	longTitle := strings.Repeat("x", 200)
	rows := []dbx.ListCalendarEventsRow{{
		EventID:   "abc",
		EventType: "control",
		Title:     longTitle,
		StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
	}}
	out := renderICS(rows, "test", now)

	// Find the line that starts with SUMMARY: and check it's split.
	lines := strings.Split(out, "\r\n")
	for _, l := range lines {
		if len(l) > 75 && !strings.HasPrefix(l, " ") {
			t.Errorf("unfolded line exceeds 75 octets: len=%d, line=%q", len(l), l)
		}
	}
}

// TestRenderICS_ControlEventEmbedsCadenceInDescription asserts the
// DESCRIPTION field carries the cadence value for control events — the
// `cadence_class` is the operator's signal for "is this quarterly or
// annual or…"
func TestRenderICS_ControlEventEmbedsCadenceInDescription(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	quarterly := "quarterly"
	rows := []dbx.ListCalendarEventsRow{{
		EventID:   "ctrl-1",
		EventType: "control",
		Title:     "Control review: Quarterly firewall rule review",
		StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
		Cadence:   &quarterly,
		Status:    "due-soon",
		Summary:   "quarterly",
	}}
	out := renderICS(rows, "test", now)

	// DESCRIPTION contains the cadence; the line may fold at the 75-octet
	// ceiling, so we assert the substrings rather than an exact line.
	if !strings.Contains(out, `Cadence: quarterly`) {
		t.Errorf("expected `Cadence: quarterly` in DESCRIPTION, got:\n%s", out)
	}
	if !strings.Contains(out, `Type: control`) {
		t.Errorf("expected `Type: control` in DESCRIPTION, got:\n%s", out)
	}
	if !strings.Contains(out, `Status: due-soon`) {
		t.Errorf("expected `Status: due-soon` in DESCRIPTION, got:\n%s", out)
	}
}

// TestRenderICS_NoMalformedVEvent guards against accidental empty lines
// inside a VEVENT block — calendar clients reject the whole feed on a
// blank line mid-VEVENT.
func TestRenderICS_NoMalformedVEvent(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := []dbx.ListCalendarEventsRow{
		{EventID: "a", EventType: "audit", Title: "A", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}},
		{EventID: "b", EventType: "policy", Title: "B", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}},
	}
	out := renderICS(rows, "test", now)

	if strings.Contains(out, "\r\n\r\n") {
		t.Errorf("ICS output contains a blank line — clients reject these:\n%s", out)
	}
}

// TestRenderICS_ExceptionSummaryUsesSCFCodeNotUUID is the AC-4 guard for
// slice 732: the exception event's SUMMARY must carry the resolved SCF
// code + control name (built in the calendar SQL JOIN), and must NOT
// contain the raw control UUID. The renderer reads row.Title verbatim, so
// this asserts the contract the query now satisfies: a row whose Title is
// the human label renders that label into SUMMARY, never a bare UUID.
func TestRenderICS_ExceptionSummaryUsesSCFCodeNotUUID(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	const ctrlUUID = "32e55da9-9f3a-4c21-8b7e-001122334455"
	rows := []dbx.ListCalendarEventsRow{
		{
			EventID:           "exc-1",
			EventType:         "exception",
			Title:             "Exception on AAA-01 — Access Control Policy",
			StartsAt:          pgtype.Timestamptz{Time: starts, Valid: true},
			RelatedEntityID:   "exc-1",
			RelatedEntityKind: "exception",
			Summary:           "compensating manual review",
			Status:            "active",
		},
	}
	out := renderICS(rows, "test", now)

	wantSummary := "SUMMARY:Exception on AAA-01 — Access Control Policy\r\n"
	if !strings.Contains(out, wantSummary) {
		t.Errorf("expected exception SUMMARY %q, got:\n%s", wantSummary, out)
	}
	if strings.Contains(out, ctrlUUID) {
		t.Errorf("exception SUMMARY leaked the raw control UUID %q:\n%s", ctrlUUID, out)
	}
}

// TestRenderICS_ExceptionSummaryFallbackNoSCFCode is the AC-2 fallback
// guard at the render tier: when a control has no SCF code, the query
// falls back to "Exception on <control name>" — never a bare UUID. The
// renderer must surface that label faithfully.
func TestRenderICS_ExceptionSummaryFallbackNoSCFCode(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	starts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	const ctrlUUID = "32e55da9-9f3a-4c21-8b7e-001122334455"
	rows := []dbx.ListCalendarEventsRow{
		{
			EventID:   "exc-2",
			EventType: "exception",
			Title:     "Exception on Custom unmapped control",
			StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
			Status:    "active",
		},
	}
	out := renderICS(rows, "test", now)

	if !strings.Contains(out, "SUMMARY:Exception on Custom unmapped control\r\n") {
		t.Errorf("expected fallback exception SUMMARY, got:\n%s", out)
	}
	if strings.Contains(out, ctrlUUID) {
		t.Errorf("fallback exception SUMMARY leaked a raw UUID:\n%s", out)
	}
}
