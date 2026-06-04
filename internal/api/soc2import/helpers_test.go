package soc2import

// Pure-Go unit coverage for the helpers in import.go. The integration
// suite (import_test.go, build tag `integration`) exercises Import /
// importIntoTx end-to-end against a real Postgres; this file covers the
// shapes that don't need a DB:
//
//   - uuidFromString    — deterministic UUIDv5 derivation
//   - uuidString        — pgtype.UUID → canonical string (valid + invalid)
//   - parseDate         — RFC3339 date parsing (empty + valid + malformed)
//   - edgeContentEqual  — equality predicate over the four edge attributes
//   - Import            — BeginTx-error branch via a stub pgxBeginner
//
// Together with the existing loader_test.go these push internal/api/soc2import
// from 25.2% unit-only toward the merged-coverage 70%+ slice 310 target;
// the merged lift comes from enrolling the integration suite in CI's
// -coverpkg list (see .github/workflows/ci.yml + slice 310 PR notes).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// uuidFromString is deterministic — the same seed must always map to
// the same UUID. Different seeds must map to different UUIDs. Both are
// load-bearing for idempotent re-imports (slice 007 ISC-15).
func TestUUIDFromString_Deterministic(t *testing.T) {
	t.Parallel()
	a := uuidFromString("framework-soc2")
	b := uuidFromString("framework-soc2")
	if a != b {
		t.Fatalf("uuidFromString not deterministic: %v vs %v", a, b)
	}
	if !a.Valid {
		t.Fatal("uuidFromString returned an invalid pgtype.UUID")
	}
}

func TestUUIDFromString_DistinctSeedsDiffer(t *testing.T) {
	t.Parallel()
	a := uuidFromString("framework-soc2")
	b := uuidFromString("framework-iso27001")
	if a == b {
		t.Fatalf("distinct seeds collided: %v", a)
	}
}

func TestUUIDFromString_EmptySeed(t *testing.T) {
	t.Parallel()
	got := uuidFromString("")
	if !got.Valid {
		t.Fatal("empty seed should still produce a valid pgtype.UUID")
	}
	// Stable second call with the same empty seed.
	again := uuidFromString("")
	if again != got {
		t.Fatalf("empty seed not deterministic: %v vs %v", got, again)
	}
}

// uuidString returns "" for the zero/invalid pgtype.UUID and the canonical
// hyphenated 36-character form for a valid one. importIntoTx feeds the
// result of uuidFromString into the Report — testing the round-trip locks
// the contract.
func TestUUIDString_ValidRoundTrip(t *testing.T) {
	t.Parallel()
	id := uuidFromString("round-trip-seed")
	s := uuidString(id)
	if len(s) != 36 {
		t.Fatalf("canonical UUID string should be 36 chars; got %q (len=%d)", s, len(s))
	}
	if strings.Count(s, "-") != 4 {
		t.Fatalf("canonical UUID string should have 4 hyphens; got %q", s)
	}
}

func TestUUIDString_InvalidReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := uuidString(pgtype.UUID{}); got != "" {
		t.Fatalf("invalid pgtype.UUID should stringify to %q; got %q", "", got)
	}
	if got := uuidString(pgtype.UUID{Valid: false}); got != "" {
		t.Fatalf("Valid=false should stringify to %q; got %q", "", got)
	}
}

// parseDate accepts "" and the YYYY-MM-DD form; anything else returns an
// invalid pgtype.Date so the importer's UpsertFrameworkVersion call still
// succeeds when the YAML omits release_date.
func TestParseDate_Empty(t *testing.T) {
	t.Parallel()
	got := parseDate("")
	if got.Valid {
		t.Fatalf("empty string should produce invalid pgtype.Date; got %+v", got)
	}
}

func TestParseDate_Valid(t *testing.T) {
	t.Parallel()
	got := parseDate("2017-04-01")
	if !got.Valid {
		t.Fatal("valid ISO date should produce a valid pgtype.Date")
	}
	if got.Time.Year() != 2017 || got.Time.Month() != 4 || got.Time.Day() != 1 {
		t.Fatalf("parsed date wrong: %+v", got.Time)
	}
}

func TestParseDate_Malformed(t *testing.T) {
	t.Parallel()
	// Each of these is malformed and should fall through to the zero
	// pgtype.Date. The importer treats Valid=false as "no release date"
	// so a bad string never blows up a transaction.
	for _, bad := range []string{
		"not-a-date",
		"2017/04/01",
		"April 1 2017",
		"2017-13-01", // month 13
		"2017-04",    // missing day
	} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			got := parseDate(bad)
			if got.Valid {
				t.Fatalf("parseDate(%q) should be invalid; got %+v", bad, got)
			}
		})
	}
}

// edgeContentEqual returns true only when every one of the four content
// attributes match. Used by importIntoTx to decide Unchanged vs Updated
// on a re-import. Each branch must reject if its field differs.
func TestEdgeContentEqual_AllEqual(t *testing.T) {
	t.Parallel()
	row := dbx.FwToScfEdge{
		RelationshipType:  dbx.StrmRelationshipType("equal"),
		Strength:          0.9,
		SourceAttribution: dbx.CrosswalkSourceAttribution("community_draft"),
		Rationale:         "same content",
	}
	if !edgeContentEqual(row,
		dbx.StrmRelationshipType("equal"),
		0.9,
		dbx.CrosswalkSourceAttribution("community_draft"),
		"same content",
	) {
		t.Fatal("edgeContentEqual should return true when all four attributes match")
	}
}

func TestEdgeContentEqual_RelationshipTypeDiffers(t *testing.T) {
	t.Parallel()
	row := dbx.FwToScfEdge{
		RelationshipType:  dbx.StrmRelationshipType("equal"),
		Strength:          0.9,
		SourceAttribution: dbx.CrosswalkSourceAttribution("community_draft"),
		Rationale:         "x",
	}
	if edgeContentEqual(row,
		dbx.StrmRelationshipType("subset_of"),
		0.9,
		dbx.CrosswalkSourceAttribution("community_draft"),
		"x",
	) {
		t.Fatal("edgeContentEqual must reject when relationship_type differs")
	}
}

func TestEdgeContentEqual_StrengthDiffers(t *testing.T) {
	t.Parallel()
	row := dbx.FwToScfEdge{
		RelationshipType:  dbx.StrmRelationshipType("equal"),
		Strength:          0.9,
		SourceAttribution: dbx.CrosswalkSourceAttribution("community_draft"),
		Rationale:         "x",
	}
	if edgeContentEqual(row,
		dbx.StrmRelationshipType("equal"),
		0.7,
		dbx.CrosswalkSourceAttribution("community_draft"),
		"x",
	) {
		t.Fatal("edgeContentEqual must reject when strength differs")
	}
}

func TestEdgeContentEqual_SourceAttributionDiffers(t *testing.T) {
	t.Parallel()
	row := dbx.FwToScfEdge{
		RelationshipType:  dbx.StrmRelationshipType("equal"),
		Strength:          0.9,
		SourceAttribution: dbx.CrosswalkSourceAttribution("community_draft"),
		Rationale:         "x",
	}
	if edgeContentEqual(row,
		dbx.StrmRelationshipType("equal"),
		0.9,
		dbx.CrosswalkSourceAttribution("scf_official"),
		"x",
	) {
		t.Fatal("edgeContentEqual must reject when source_attribution differs")
	}
}

func TestEdgeContentEqual_RationaleDiffers(t *testing.T) {
	t.Parallel()
	row := dbx.FwToScfEdge{
		RelationshipType:  dbx.StrmRelationshipType("equal"),
		Strength:          0.9,
		SourceAttribution: dbx.CrosswalkSourceAttribution("community_draft"),
		Rationale:         "old rationale",
	}
	if edgeContentEqual(row,
		dbx.StrmRelationshipType("equal"),
		0.9,
		dbx.CrosswalkSourceAttribution("community_draft"),
		"new rationale",
	) {
		t.Fatal("edgeContentEqual must reject when rationale differs")
	}
}

// stubBeginner satisfies pgxBeginner without touching a real DB. It's the
// cheapest way to assert Import's BeginTx-error branch wraps the cause
// with the canonical "crosswalk: begin tx" prefix.
type stubBeginner struct {
	err error
}

func (s stubBeginner) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return nil, s.err
}

func TestImport_BeginTxErrorIsWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("test-sentinel-begin-failure")
	report, err := Import(context.Background(), stubBeginner{err: sentinel}, &Crosswalk{})
	if err == nil {
		t.Fatal("Import should fail when BeginTx fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap the sentinel; got %v", err)
	}
	if !strings.Contains(err.Error(), "crosswalk: begin tx") {
		t.Fatalf("error message should carry the 'crosswalk: begin tx' prefix; got %q", err.Error())
	}
	// Report carries a map field so it isn't directly comparable; assert
	// the load-bearing zero fields instead. On a BeginTx failure none of
	// the importIntoTx counters should be touched.
	if report.FrameworkSlug != "" || report.RequirementsCreated != 0 || report.EdgesCreated != 0 {
		t.Fatalf("Import should return a zero Report on failure; got %+v", report)
	}
}
