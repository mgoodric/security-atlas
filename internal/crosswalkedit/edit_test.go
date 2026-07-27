package crosswalkedit

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Pure-Go unit suite for the slice-536b content-edit validation surface (the
// slice-353 Q-2 convention). Every branch of Content.Validate, the STRM type
// parser, the no-op comparison and the demotion-note formatter is reachable
// without Postgres; the transactional store itself is covered by the
// integration suite.

func TestRelationshipType_IsValid(t *testing.T) {
	t.Parallel()
	for _, ok := range []RelationshipType{
		RelEqual, RelSubsetOf, RelSupersetOf, RelIntersectsWith, RelNoRelationship,
	} {
		if !ok.IsValid() {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []RelationshipType{"", "EQUAL", "equals", "related_to", "no relationship"} {
		if bad.IsValid() {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestParseRelationshipType(t *testing.T) {
	t.Parallel()
	got, err := ParseRelationshipType("subset_of")
	if err != nil {
		t.Fatalf("ParseRelationshipType(subset_of): %v", err)
	}
	if got != RelSubsetOf {
		t.Fatalf("got %q, want %q", got, RelSubsetOf)
	}

	// An unknown type must NOT be coerced to a default: silently rewriting a
	// mapping into a type the reviewer did not choose is the failure mode.
	if _, err := ParseRelationshipType("sorta_equal"); !errors.Is(err, ErrInvalidRelationshipType) {
		t.Fatalf("want ErrInvalidRelationshipType, got %v", err)
	}
}

func TestRelationshipType_DBRoundTrip(t *testing.T) {
	t.Parallel()
	for _, rt := range []RelationshipType{
		RelEqual, RelSubsetOf, RelSupersetOf, RelIntersectsWith, RelNoRelationship,
	} {
		if got := RelationshipTypeFromDB(rt.DBType()); got != rt {
			t.Fatalf("round trip %q -> %q", rt, got)
		}
	}
	if RelationshipTypeFromDB(dbx.StrmRelationshipTypeEqual) != RelEqual {
		t.Fatal("dbx equal did not map to RelEqual")
	}
}

func TestContentValidate_Accepts(t *testing.T) {
	t.Parallel()
	cases := []Content{
		{RelationshipType: RelEqual, Strength: 1.0, Rationale: "logically equivalent"},
		{RelationshipType: RelNoRelationship, Strength: 0, Rationale: ""},
		{RelationshipType: RelIntersectsWith, Strength: 0.4, Rationale: strings.Repeat("x", maxRationaleLen)},
	}
	for _, c := range cases {
		if err := c.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v; want nil", c, err)
		}
	}
}

func TestContentValidate_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Content
		want error
	}{
		{"unknown type", Content{RelationshipType: "maybe", Strength: 0.5}, ErrInvalidRelationshipType},
		{"empty type", Content{RelationshipType: "", Strength: 0.5}, ErrInvalidRelationshipType},
		{"strength above 1", Content{RelationshipType: RelEqual, Strength: 1.01}, ErrStrengthOutOfRange},
		{"strength below 0", Content{RelationshipType: RelEqual, Strength: -0.0001}, ErrStrengthOutOfRange},
		{"strength NaN", Content{RelationshipType: RelEqual, Strength: math.NaN()}, ErrStrengthOutOfRange},
		{"strength +Inf", Content{RelationshipType: RelEqual, Strength: math.Inf(1)}, ErrStrengthOutOfRange},
		{"strength -Inf", Content{RelationshipType: RelEqual, Strength: math.Inf(-1)}, ErrStrengthOutOfRange},
		{
			"rationale over cap",
			Content{RelationshipType: RelEqual, Strength: 1, Rationale: strings.Repeat("x", maxRationaleLen+1)},
			ErrRationaleTooLong,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.in.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestContentEqual(t *testing.T) {
	t.Parallel()
	base := Content{RelationshipType: RelSubsetOf, Strength: 0.8, Rationale: "covered by IAC-22"}
	if !base.Equal(base) {
		t.Fatal("a content value must equal itself")
	}
	// A deliberate nudge is a real edit, not a rounding artefact — it must be
	// audited, so Equal compares strength exactly.
	nudged := base
	nudged.Strength = 0.81
	if base.Equal(nudged) {
		t.Fatal("0.80 vs 0.81 must not compare equal")
	}
	retyped := base
	retyped.RelationshipType = RelIntersectsWith
	if base.Equal(retyped) {
		t.Fatal("a type change must not compare equal")
	}
	reworded := base
	reworded.Rationale = "covered by IAC-22 (reworded)"
	if base.Equal(reworded) {
		t.Fatal("a rationale change must not compare equal")
	}
}

func TestValidateNote(t *testing.T) {
	t.Parallel()
	if err := validateNote(strings.Repeat("n", maxRationaleLen)); err != nil {
		t.Fatalf("note at the cap: %v", err)
	}
	if err := validateNote(strings.Repeat("n", maxRationaleLen+1)); !errors.Is(err, ErrRationaleTooLong) {
		t.Fatalf("want ErrRationaleTooLong, got %v", err)
	}
}

func TestNormalizeText(t *testing.T) {
	t.Parallel()
	// A trailing newline from a textarea must not read as a change, which is
	// what makes the ErrNoChange guard trustworthy.
	if got := normalizeText("  covered by IAC-22\n"); got != "covered by IAC-22" {
		t.Fatalf("normalizeText = %q", got)
	}
	if got := normalizeText("   "); got != "" {
		t.Fatalf("whitespace-only should normalize to empty, got %q", got)
	}
}

func TestDemotionNote(t *testing.T) {
	t.Parallel()
	// A reader of the tier trail alone must be able to tell an edit-driven
	// demotion from an operator-driven one.
	bare := demotionNote("")
	if !strings.Contains(bare, "content edited") {
		t.Fatalf("demotionNote(\"\") = %q; want the edit marker", bare)
	}
	withNote := demotionNote("strength was overstated")
	if !strings.HasPrefix(withNote, bare) || !strings.Contains(withNote, "strength was overstated") {
		t.Fatalf("demotionNote carried neither marker nor note: %q", withNote)
	}
}

// TestEditNeverTouchesEdgeEndpoints is the invariant-#7 pin. A content edit
// changes an existing requirement -> SCF anchor edge's semantics; it must never
// be able to change WHICH requirement or WHICH anchor the edge connects,
// because re-pointing an edge is how a requirement -> requirement shape would
// be reachable from this surface.
//
// Two independent guards are asserted:
//
//  1. The Content type — the only thing the store writes — has no endpoint
//     field at all, so there is no value a caller could supply.
//  2. The SetFwToScfEdgeContent SQL names neither column. (The atlas_app column
//     grant in migration 20260612110000 is the third guard, enforced by
//     Postgres and covered by the integration suite.)
func TestEditNeverTouchesEdgeEndpoints(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "edit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse edit.go: %v", err)
	}
	var checked bool
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Content" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		checked = true
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				switch name.Name {
				case "RequirementID", "FrameworkRequirementID", "AnchorID", "ScfAnchorID", "SourceAttribution":
					t.Errorf("Content must not carry %s — an edit may never re-point an edge (invariant #7) "+
						"nor rewrite provenance", name.Name)
				}
			}
		}
		return false
	})
	if !checked {
		t.Fatal("Content type not found in edit.go")
	}

	// Guard 2 — the UPDATE statement itself. Read from the sqlc source of
	// truth so a future hand-edit to the query is caught here rather than in
	// production.
	src, err := os.ReadFile(filepath.Join("..", "db", "queries", "framework_crosswalk.sql"))
	if err != nil {
		t.Fatalf("read framework_crosswalk.sql: %v", err)
	}
	stmt := extractQuery(t, string(src), "SetFwToScfEdgeContent")
	for _, forbidden := range []string{
		"framework_requirement_id",
		"scf_anchor_id",
		"source_attribution",
	} {
		if strings.Contains(stmt, forbidden) {
			t.Errorf("SetFwToScfEdgeContent must not write %s: %s", forbidden, stmt)
		}
	}
	for _, required := range []string{"relationship_type", "strength", "rationale"} {
		if !strings.Contains(stmt, required) {
			t.Errorf("SetFwToScfEdgeContent should write %s: %s", required, stmt)
		}
	}
}

// extractQuery pulls one named sqlc query's EXECUTABLE body out of the .sql
// file: everything from its `-- name: <name> ` marker to the next marker or
// EOF, with `--` comment lines stripped. The comments are stripped on purpose —
// the doc comment above SetFwToScfEdgeContent names the very columns this test
// asserts are absent, and matching against prose rather than SQL would make the
// assertion meaningless.
func extractQuery(t *testing.T, src, name string) string {
	t.Helper()
	marker := "-- name: " + name + " "
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("query %q not found", name)
	}
	rest := src[i+len(marker):]
	if j := strings.Index(rest, "-- name: "); j >= 0 {
		rest = rest[:j]
	}
	var sql []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		sql = append(sql, line)
	}
	return strings.Join(sql, "\n")
}
