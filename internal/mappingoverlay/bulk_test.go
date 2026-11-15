package mappingoverlay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// Pure-Go branches of the import parsers and the export row. The import's
// database path is in the API integration tests.

const csvHeader = "framework,framework_version,requirement_code,scf_release,anchor_code,relationship_type,strength,rationale,source_ref\n"

func TestParseCSV_RowsAndRowErrors(t *testing.T) {
	t.Parallel()
	in := "\ufeff" + csvHeader +
		"soc2,2017,CC1.1,2025.2,HRS-07.1,intersects_with,0.8,auditor tested it,FY25 report s4\n" +
		"soc2,2017,CC1.2,2025.2,HRS-07.1,equal,abc,r,s\n" +
		"soc2,2017,CC1.3\n" +
		"soc2,,CC1.4,,IAC-06,equal,NaN,r,s\n" +
		"soc2,,CC1.5,,IAC-06,equal,,r,s\n"
	rows, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d; want 5", len(rows))
	}
	want := NewEdge{
		Framework: "soc2", FrameworkVersion: "2017", RequirementCode: "CC1.1", SCFRelease: "2025.2",
		AnchorCode: "HRS-07.1", RelationshipType: "intersects_with", Strength: 0.8,
		Rationale: "auditor tested it", SourceRef: "FY25 report s4",
	}
	if rows[0].Err != nil || rows[0].Edge != want || rows[0].Row != 1 || rows[0].Line != 2 {
		t.Errorf("row 1 = %+v", rows[0])
	}
	for i, reason := range map[int]string{
		1: "strength must be a number between 0 and 1",
		2: "the row has 3 fields; the header has 9",
		3: "strength must be a number between 0 and 1",
		4: "strength is required",
	} {
		r := rows[i]
		if r.Err == nil || r.Err.Reason != reason || r.Err.Row != i+1 || r.Err.Line != i+2 || r.Err.Code != "invalid_overlay_edge" {
			t.Errorf("row %d err = %+v; want reason %q", i+1, r.Err, reason)
		}
	}
}

func TestParseCSV_FileRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, reason string
	}{
		{"empty", "", "the file is empty"},
		{"tenant column", "tenant_id," + csvHeader, "the file names a tenant; the tenant comes from the credential"},
		{"tenant column any case", "Tenant," + csvHeader, "the file names a tenant"},
		{"unknown column", "=cmd|' /C calc'!A0," + csvHeader, "the file has a column that is not one of"},
		{"repeated column", "strength," + csvHeader, "the column strength appears twice"},
		{"missing column", "framework,requirement_code\n", "the CSV header has no anchor_code column"},
		{"bad quoting", csvHeader + "soc2,\"unterminated\n", "row 1 (line 2) is not valid CSV"},
		{"too many rows", csvHeader + strings.Repeat("soc2,,C,,A,equal,1,r,s\n", MaxImportRows+1),
			fmt.Sprintf("more than %d rows", MaxImportRows)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCSV(strings.NewReader(tc.in))
			var fe *FileError
			if !errors.As(err, &fe) || !strings.Contains(fe.Reason, tc.reason) {
				t.Fatalf("err = %v; want FileError containing %q", err, tc.reason)
			}
			if strings.Contains(fe.Reason, "calc") {
				t.Errorf("reason echoes the file's content: %q", fe.Reason)
			}
		})
	}
}

func TestParseYAML_RowsAndRefusals(t *testing.T) {
	t.Parallel()
	in := `
- framework: soc2
  framework_version: "2017"
  requirement_code: CC1.1
  scf_release: "2025.2"
  anchor_code: HRS-07.1
  relationship_type: intersects_with
  strength: 0.8
  rationale: auditor tested it
  source_ref: FY25 report s4
- framework: soc2
  requirement_code: CC1.2
  anchor_code: IAC-06
  relationship_type: equal
  strength: [1]
  rationale: r
  source_ref: s
- just a string
- framework: soc2
  framework_version: ~
  requirement_code: CC1.3
  anchor_code: IAC-06
  relationship_type: equal
  strength: 1
  rationale: r
  source_ref: s
`
	rows, err := ParseYAML(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d; want 4", len(rows))
	}
	if rows[0].Err != nil || rows[0].Edge.FrameworkVersion != "2017" || rows[0].Edge.Strength != 0.8 || rows[0].Line != 2 {
		t.Errorf("row 1 = %+v", rows[0])
	}
	if rows[1].Err == nil || rows[1].Err.Reason != "strength must be a single value" {
		t.Errorf("row 2 err = %+v", rows[1].Err)
	}
	if rows[2].Err == nil || rows[2].Err.Reason != "the row is not a mapping" || rows[2].Err.Row != 3 {
		t.Errorf("row 3 err = %+v", rows[2].Err)
	}
	if rows[3].Err != nil || rows[3].Edge.FrameworkVersion != "" || rows[3].Edge.Strength != 1 {
		t.Errorf("row 4 = %+v", rows[3])
	}

	for _, tc := range []struct{ name, in, reason string }{
		{"empty", "", "the file is empty"},
		{"not yaml", "- [", "the file is not valid YAML"},
		{"not a list", "framework: soc2\n", "must be a list"},
		{"two documents", "- framework: soc2\n---\n- framework: soc2\n", "exactly one document"},
		{"bad second document", "- framework: soc2\n---\n- [\n", "exactly one document"},
		{"merge key", "- &a {framework: soc2}\n- <<: *a\n", "row 2 (line 2): the file has a column that is not one of"},
		{"tenant key", "- tenant_id: x\n  framework: soc2\n", "row 1 (line 1): the file names a tenant"},
		{"unknown key", "- framework: soc2\n  evil: x\n", "row 1 (line 1): the file has a column that is not one of"},
	} {
		_, err := ParseYAML(strings.NewReader(tc.in))
		var fe *FileError
		if !errors.As(err, &fe) || !strings.Contains(fe.Reason, tc.reason) {
			t.Errorf("%s: err = %v; want FileError containing %q", tc.name, err, tc.reason)
		}
	}
}

// Aliases are never expanded into rows or values: an aliased row is not a
// mapping and an aliased value is not a single value, so a file cannot
// reuse (or multiply) content through anchors.
func TestParseYAML_AliasesAreRowErrors(t *testing.T) {
	t.Parallel()
	in := `
- &edge
  framework: soc2
  requirement_code: CC1.1
  anchor_code: &code HRS-07.1
  relationship_type: equal
  strength: 1
  rationale: r
  source_ref: s
- *edge
- framework: soc2
  requirement_code: CC1.2
  anchor_code: *code
  relationship_type: equal
  strength: 1
  rationale: r
  source_ref: s
`
	rows, err := ParseYAML(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3", len(rows))
	}
	if rows[0].Err != nil || rows[0].Edge.AnchorCode != "HRS-07.1" {
		t.Errorf("row 1 (the anchored original) = %+v", rows[0])
	}
	if rows[1].Err == nil || rows[1].Err.Reason != "the row is not a mapping" {
		t.Errorf("row 2 (an aliased row) err = %+v", rows[1].Err)
	}
	if rows[2].Err == nil || rows[2].Err.Reason != "anchor_code must be a single value" {
		t.Errorf("row 3 (an aliased value) err = %+v", rows[2].Err)
	}
}

func TestImport_RefusesNoRowsAndNoActor(t *testing.T) {
	t.Parallel()
	s := &Store{}
	var fe *FileError
	if _, err := s.Import(context.Background(), nil, false, "a"); !errors.As(err, &fe) {
		t.Errorf("no rows: err = %v", err)
	}
	if _, err := s.Import(context.Background(), []ImportRow{{Row: 1}}, false, ""); err == nil {
		t.Error("no actor: want error")
	}
}

func TestClassify_ReasonsCarryNoCallerText(t *testing.T) {
	t.Parallel()
	const planted = "=HYPERLINK(evil)"
	id := uuid.New()
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&InvalidEdgeError{Reason: "rationale is required"}, "invalid_overlay_edge"},
		{fmt.Errorf("%w: soc2 %s", ErrUnknownFrameworkVersion, planted), "unknown_framework_version"},
		{fmt.Errorf("%w: %q", ErrUnknownRequirement, planted), "unknown_requirement"},
		{fmt.Errorf("%w: %s", scfrelease.ErrUnknownRelease, planted), "unknown_scf_release"},
		{scfrelease.ErrNoCurrentRelease, "unknown_scf_release"},
		{fmt.Errorf("%w: %s", scfrelease.ErrCodeNotInRelease, planted), "unknown_anchor"},
		{&CatalogEdgeExistsError{EdgeID: id, RelationshipType: "equal", MappingTier: "verified"}, "catalog_edge_exists"},
		{ErrDuplicateOverlay, "overlay_edge_exists"},
		{ErrAlreadyRetired, "already_retired"},
		{ErrNotFound, "not_found"},
	} {
		code, reason, ok := Classify(tc.err)
		if !ok || code != tc.code || reason == "" || strings.Contains(reason, planted) {
			t.Errorf("Classify(%v) = %q, %q, %v; want code %q and a reason without the planted text", tc.err, code, reason, ok, tc.code)
		}
	}
	if _, _, ok := Classify(errors.New("connection reset")); ok {
		t.Error("a database failure classified as a refusal")
	}
}

func TestExportRow_MatchesColumns(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	retired := created.Add(time.Hour)
	e := Edge{
		ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Framework: "soc2", FrameworkVersion: "2017",
		RequirementCode: "CC1.1", SCFRelease: "2025.2", AnchorCode: "HRS-07.1", RelationshipType: "equal",
		Strength: 0.75, Rationale: "r", SourceRef: "s", Status: StatusRetired, RetirementReason: "withdrawn",
		CreatedBy: "cred-1", CreatedAt: created, RetiredBy: "cred-2", RetiredAt: &retired,
	}
	row := ExportRow(e)
	if len(row) != len(ExportColumns) {
		t.Fatalf("row has %d cells; header has %d", len(row), len(ExportColumns))
	}
	got := map[string]string{}
	for i, c := range ExportColumns {
		got[c] = row[i]
	}
	for k, v := range map[string]string{
		"id": "11111111-1111-1111-1111-111111111111", "strength": "0.75", "status": "retired",
		"retirement_reason": "withdrawn", "created_by": "cred-1", "created_at": "2026-11-01T09:00:00Z",
		"retired_by": "cred-2", "retired_at": "2026-11-01T10:00:00Z",
	} {
		if got[k] != v {
			t.Errorf("%s = %q; want %q", k, got[k], v)
		}
	}
	e.RetiredAt = nil
	if r := ExportRow(e); r[len(r)-1] != "" {
		t.Errorf("retired_at of an active edge = %q", r[len(r)-1])
	}
}
