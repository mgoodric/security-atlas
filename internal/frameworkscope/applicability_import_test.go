package frameworkscope

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseDecisionCSV(t *testing.T) {
	t.Parallel()
	in := "\ufeffRequirement Code,Decision,Justification,Implementation Reference\n" +
		"A.5.1, Applicable ,,https://wiki/A.5.1\n" +
		",,,\n" +
		"A.8.25,excluded,\"No in-house development, all SaaS\",\n" +
		"A.8.26,excluded,bad \"quote,\n" +
		"A.8.27,applicable\n"
	rows, rejected, err := ParseDecisionCSV(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []ImportRow{
		{Line: 2, RequirementCode: "A.5.1", Decision: "applicable", ImplementationRef: "https://wiki/A.5.1"},
		{Line: 4, RequirementCode: "A.8.25", Decision: "excluded", Justification: "No in-house development, all SaaS"},
		{Line: 6, RequirementCode: "A.8.27", Decision: "applicable"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if len(rejected) != 1 || rejected[0].Line != 5 || rejected[0].Reason != ReasonMalformedRow {
		t.Fatalf("rejected = %+v, want line 5 malformed", rejected)
	}
}

func TestParseDecisionCSV_HeaderAndEmpty(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"empty file":       "",
		"no code column":   "decision,justification\napplicable,\n",
		"no decision":      "requirement_code,justification\nA.5.1,x\n",
		"data row as head": "A.5.1,applicable\n",
	} {
		if _, _, err := ParseDecisionCSV(strings.NewReader(in)); !errors.Is(err, ErrImportHeader) {
			t.Errorf("%s: err = %v, want ErrImportHeader", name, err)
		}
	}
	if _, _, err := ParseDecisionCSV(strings.NewReader("code,decision\n,\n")); !errors.Is(err, ErrImportEmpty) {
		t.Errorf("header only: err = %v, want ErrImportEmpty", err)
	}
}

func TestCheckImportRows(t *testing.T) {
	t.Parallel()
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	codes := map[string]uuid.UUID{"A.1": a, "A.2": b, "A.3": c, "A.4": d}
	apply, rejected := checkImportRows([]ImportRow{
		{Line: 2, RequirementCode: "A.1", Decision: "excluded", Justification: "n/a here"},
		{Line: 3, RequirementCode: "Z.9", Decision: "applicable"},
		{Line: 4, RequirementCode: "A.2", Decision: "excluded"},
		{Line: 5, RequirementCode: "A.2", Decision: "maybe"},
		{Line: 6, RequirementCode: "A.1", Decision: "applicable"},
		{Line: 7, RequirementCode: "A.3", Decision: "excluded", Justification: "same"},
		{Line: 8, RequirementCode: "A.3", Decision: "excluded", Justification: "same"},
		{Line: 9, RequirementCode: "A.4", Decision: "applicable"},
		{Line: 10, RequirementCode: "A.4", Decision: "applicable", ImplementationRef: "https://wiki/A.4"},
	}, codes)
	// A.1 and A.2 repeat with different decisions, and A.4 with a different
	// implementation reference: every row for each is rejected. A.3's two
	// rows are identical and apply once.
	if len(apply) != 1 || apply[0].RequirementID != c || apply[0].Justification != "same" {
		t.Fatalf("apply = %+v", apply)
	}
	want := []ImportRejection{
		{Line: 2, RequirementCode: "A.1", Reason: ReasonConflictingDuplicate},
		{Line: 3, RequirementCode: "Z.9", Reason: ReasonUnknownCode},
		{Line: 4, RequirementCode: "A.2", Reason: ReasonConflictingDuplicate},
		{Line: 5, RequirementCode: "A.2", Reason: ReasonConflictingDuplicate},
		{Line: 6, RequirementCode: "A.1", Reason: ReasonConflictingDuplicate},
		{Line: 9, RequirementCode: "A.4", Reason: ReasonConflictingDuplicate},
		{Line: 10, RequirementCode: "A.4", Reason: ReasonConflictingDuplicate},
	}
	if len(rejected) != len(want) {
		t.Fatalf("rejected = %+v, want %+v", rejected, want)
	}
	for i := range want {
		if rejected[i] != want[i] {
			t.Errorf("rejection %d = %+v, want %+v", i, rejected[i], want[i])
		}
	}
}
