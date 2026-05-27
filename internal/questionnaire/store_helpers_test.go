// Slice 319 — pure-Go unit tests for the store-helper conversion layer.
//
// Load-bearing functions covered:
//   - uuidToString — both branches: Valid==false returns "", Valid==true
//     formats the 16-byte payload as the canonical 8-4-4-4-12 hex form.
//   - pgUUID         — happy-path scan of a valid uuid string.
//   - pgUUIDFromBytes — identity pass-through (single statement).
//   - rowToAnswer    — empty Citations bytes branch (citations slice
//     stays nil) plus the non-empty branch via JSON.
//
// Kept in a non-integration test file so the conversion helpers report
// coverage even in the unit-only profile (the integration profile picks
// them up too).
package questionnaire

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestUUIDToString_InvalidReturnsEmpty(t *testing.T) {
	var zero pgtype.UUID // Valid defaults to false
	if got := uuidToString(zero); got != "" {
		t.Fatalf("expected empty string for invalid UUID, got %q", got)
	}
}

func TestUUIDToString_ValidFormatsCanonical(t *testing.T) {
	src := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	pu := pgtype.UUID{Bytes: src, Valid: true}
	got := uuidToString(pu)
	if got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuidToString = %q; want 11111111-2222-3333-4444-555555555555", got)
	}
}

func TestPgUUID_ScanRoundTrip(t *testing.T) {
	in := "11111111-2222-3333-4444-555555555555"
	pu := pgUUID(in)
	if !pu.Valid {
		t.Fatal("expected pgUUID(valid string) to be Valid")
	}
	if got := uuidToString(pu); got != in {
		t.Fatalf("round trip mismatch: %q vs %q", got, in)
	}
}

func TestPgUUIDFromBytes_Identity(t *testing.T) {
	src := pgUUID("11111111-2222-3333-4444-555555555555")
	out := pgUUIDFromBytes(src)
	if out != src {
		t.Fatal("pgUUIDFromBytes must be identity")
	}
}

func TestRowToAnswer_EmptyCitations(t *testing.T) {
	row := dbx.QuestionnaireAnswer{
		ID:          pgUUID("11111111-2222-3333-4444-555555555555"),
		AnswerValue: "yes",
		Narrative:   "n",
		AuthoredBy:  "alice",
		// Citations bytes intentionally empty.
	}
	a := rowToAnswer(row)
	if a.AnswerValue != "yes" || a.Narrative != "n" || a.AuthoredBy != "alice" {
		t.Fatalf("rowToAnswer wire fields: %+v", a)
	}
	if a.Citations != nil {
		t.Fatalf("Citations must remain nil for empty bytes, got %v", a.Citations)
	}
}

func TestRowToAnswer_PopulatedCitations(t *testing.T) {
	cites := []any{
		map[string]any{"evidence_id": "ev-01"},
		map[string]any{"policy_id": "pol-01"},
	}
	b, err := json.Marshal(cites)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := dbx.QuestionnaireAnswer{
		ID:          pgUUID("11111111-2222-3333-4444-555555555555"),
		AnswerValue: "no",
		Narrative:   "see exception",
		AuthoredBy:  "bob",
		Citations:   b,
	}
	a := rowToAnswer(row)
	if len(a.Citations) != 2 {
		t.Fatalf("expected 2 citations after unmarshal, got %d (%v)", len(a.Citations), a.Citations)
	}
}

func TestRowToQuestion_ScfAnchorPointerNilSetsNeedsMapping(t *testing.T) {
	row := dbx.QuestionnaireQuestion{
		ID:          pgUUID("22222222-3333-4444-5555-666666666666"),
		Code:        "Q-01",
		Text:        "?",
		ScfAnchorID: nil, // explicit
	}
	got := rowToQuestion(row)
	if !got.NeedsMapping {
		t.Fatal("nil ScfAnchorID must set NeedsMapping=true")
	}
	if got.ScfAnchorID != "" {
		t.Fatalf("expected empty ScfAnchorID, got %q", got.ScfAnchorID)
	}
}

func TestRowToQuestion_ScfAnchorPointerEmptyStringSetsNeedsMapping(t *testing.T) {
	empty := ""
	row := dbx.QuestionnaireQuestion{
		ID:          pgUUID("22222222-3333-4444-5555-666666666666"),
		Code:        "Q-02",
		Text:        "?",
		ScfAnchorID: &empty,
	}
	got := rowToQuestion(row)
	if !got.NeedsMapping {
		t.Fatal("empty-string ScfAnchorID must set NeedsMapping=true")
	}
}

func TestRowToQuestion_ScfAnchorPointerPopulatedClearsNeedsMapping(t *testing.T) {
	a := "IAC-06"
	row := dbx.QuestionnaireQuestion{
		ID:          pgUUID("22222222-3333-4444-5555-666666666666"),
		Code:        "Q-03",
		Text:        "?",
		ScfAnchorID: &a,
	}
	got := rowToQuestion(row)
	if got.NeedsMapping {
		t.Fatal("populated ScfAnchorID must NOT set NeedsMapping")
	}
	if got.ScfAnchorID != "IAC-06" {
		t.Fatalf("expected IAC-06, got %q", got.ScfAnchorID)
	}
}
