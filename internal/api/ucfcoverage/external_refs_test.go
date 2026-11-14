package ucfcoverage

// #1737 — controlWireFromControlRow carries controls.external_refs onto
// the ControlCoverage wire. Pure-Go, no database.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestControlWireFromControlRow_ExternalRefs(t *testing.T) {
	t.Parallel()
	row := dbx.GetControlByIDRow{
		BundleID:     "risk_assessment_annual",
		Title:        "Annual risk assessment",
		ExternalRefs: []byte(`[{"system":"acme-audit","kind":"reference","id":"RA-03"},{"system":"soc2","kind":"tested_under","id":"CC3.1","note":"FY25"}]`),
	}
	w, err := controlWireFromControlRow(row)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	want := []externalRefWire{
		{System: "acme-audit", Kind: "reference", ID: "RA-03"},
		{System: "soc2", Kind: "tested_under", ID: "CC3.1", Note: "FY25"},
	}
	if len(w.ExternalRefs) != len(want) {
		t.Fatalf("got %+v, want %+v", w.ExternalRefs, want)
	}
	for i := range want {
		if w.ExternalRefs[i] != want[i] {
			t.Errorf("ref[%d] = %+v, want %+v", i, w.ExternalRefs[i], want[i])
		}
	}
}

func TestControlWireFromControlRow_NoRefsOmitsField(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "[]"} {
		w, err := controlWireFromControlRow(dbx.GetControlByIDRow{ExternalRefs: []byte(raw)})
		if err != nil {
			t.Fatalf("map %q: %v", raw, err)
		}
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(b), "external_refs") {
			t.Errorf("raw %q: wire %s should omit external_refs", raw, b)
		}
	}
}

func TestControlWireFromControlRow_MalformedRefsErrors(t *testing.T) {
	t.Parallel()
	_, err := controlWireFromControlRow(dbx.GetControlByIDRow{ExternalRefs: []byte(`{"not":"an array"}`)})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}
