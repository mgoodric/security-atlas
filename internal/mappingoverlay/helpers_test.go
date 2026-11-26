package mappingoverlay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Pure-Go branches: shape validation and conversions. The store's database
// paths are in integration_test.go.

func validEdge() NewEdge {
	return NewEdge{
		Framework: "soc2", FrameworkVersion: "2017", RequirementCode: "CC1.1",
		SCFRelease: "2025.2", AnchorCode: "HRS-07.1",
		RelationshipType: "intersects_with", Strength: 0.8,
		Rationale: "auditor tested it under CC1.1", SourceRef: "FY25 SOC 2 report, s4",
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*NewEdge)
		reason string
	}{
		{"ok", func(*NewEdge) {}, ""},
		{"empty framework version is allowed", func(e *NewEdge) { e.FrameworkVersion = "" }, ""},
		{"no scf_release (#1774)", func(e *NewEdge) { e.SCFRelease = " " }, "scf_release is required"},
		{"strength 0", func(e *NewEdge) { e.Strength = 0 }, ""},
		{"strength 1", func(e *NewEdge) { e.Strength = 1 }, ""},
		{"no framework", func(e *NewEdge) { e.Framework = " " }, "framework is required"},
		{"no requirement", func(e *NewEdge) { e.RequirementCode = "" }, "requirement_code is required"},
		{"no anchor", func(e *NewEdge) { e.AnchorCode = "" }, "anchor_code is required"},
		{"unknown relationship", func(e *NewEdge) { e.RelationshipType = "related" }, "relationship_type"},
		{"no_relationship refused", func(e *NewEdge) { e.RelationshipType = "no_relationship" }, "relationship_type"},
		{"strength below 0", func(e *NewEdge) { e.Strength = -0.1 }, "strength"},
		{"strength above 1", func(e *NewEdge) { e.Strength = 1.1 }, "strength"},
		{"blank rationale", func(e *NewEdge) { e.Rationale = "  \t" }, "rationale is required"},
		{"long rationale", func(e *NewEdge) { e.Rationale = strings.Repeat("r", maxRationale+1) }, "rationale exceeds"},
		{"blank source", func(e *NewEdge) { e.SourceRef = "" }, "source_ref is required"},
		{"long source", func(e *NewEdge) { e.SourceRef = strings.Repeat("s", maxSourceRef+1) }, "source_ref exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := validEdge()
			tc.mutate(&e)
			err := e.Validate()
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("Validate() = %v; want nil", err)
				}
				return
			}
			var invalid *InvalidEdgeError
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, tc.reason) {
				t.Fatalf("Validate() = %v; want InvalidEdgeError containing %q", err, tc.reason)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", StatusActive, StatusRetired} {
		if err := ValidateStatus(ok); err != nil {
			t.Errorf("ValidateStatus(%q) = %v", ok, err)
		}
	}
	var invalid *InvalidEdgeError
	if err := ValidateStatus("deleted"); !errors.As(err, &invalid) {
		t.Errorf("ValidateStatus(deleted) = %v; want InvalidEdgeError", err)
	}
}

// The store refuses bad input before it opens a transaction: a nil pool
// proves no database call is reached.
func TestStoreRefusesBeforeTheDatabase(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	ctx := context.Background()
	var invalid *InvalidEdgeError

	bad := validEdge()
	bad.Rationale = ""
	if _, err := s.Create(ctx, bad, "actor"); !errors.As(err, &invalid) {
		t.Errorf("Create without rationale = %v", err)
	}
	if _, err := s.Create(ctx, validEdge(), ""); err == nil || !strings.Contains(err.Error(), "no actor") {
		t.Errorf("Create without actor = %v", err)
	}
	if _, err := s.List(ctx, Filter{Status: "gone"}); !errors.As(err, &invalid) {
		t.Errorf("List bad status = %v", err)
	}
	if _, err := s.Retire(ctx, uuid.New(), " ", "actor"); !errors.As(err, &invalid) {
		t.Errorf("Retire without reason = %v", err)
	}
	if _, err := s.Retire(ctx, uuid.New(), strings.Repeat("x", maxReason+1), "actor"); !errors.As(err, &invalid) {
		t.Errorf("Retire long reason = %v", err)
	}
	if _, err := s.Retire(ctx, uuid.New(), "why", ""); err == nil || !strings.Contains(err.Error(), "no actor") {
		t.Errorf("Retire without actor = %v", err)
	}
	// No tenant on the context: refused before Begin.
	if _, err := s.List(ctx, Filter{}); err == nil {
		t.Error("List without a tenant succeeded")
	}
}

func TestCatalogEdgeExistsError_NamesTheEdge(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	err := &CatalogEdgeExistsError{EdgeID: id, RelationshipType: "equal", MappingTier: "verified"}
	for _, want := range []string{id.String(), "equal", "verified"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing %q", err.Error(), want)
		}
	}
}

func TestEdgeFromViewAndAuditJSON(t *testing.T) {
	t.Parallel()
	id, req, anchor := uuid.New(), uuid.New(), uuid.New()
	created := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	retired := created.Add(time.Hour)
	reason, by := "wrong criterion", "user-2"
	row := dbx.GetOverlayEdgeViewRow{
		ID: pgUUID(id), FrameworkRequirementID: pgUUID(req), ScfAnchorID: pgUUID(anchor),
		RelationshipType: dbx.StrmRelationshipTypeEqual, Strength: 0.5,
		Rationale: "r", SourceRef: "s", Status: StatusRetired,
		RetirementReason: &reason, CreatedBy: "user-1",
		CreatedAt: pgtype.Timestamptz{Time: created, Valid: true},
		RetiredBy: &by, RetiredAt: pgtype.Timestamptz{Time: retired, Valid: true},
		FrameworkSlug: "soc2", FrameworkVersion: "2017", RequirementCode: "CC1.1",
		AnchorCode: "HRS-07.1", ScfRelease: "2025.2",
	}
	e := edgeFromView(row)
	if e.ID != id || e.FrameworkRequirementID != req || e.SCFAnchorID != anchor ||
		e.RetirementReason != reason || e.RetiredBy != by || e.RetiredAt == nil || !e.RetiredAt.Equal(retired) ||
		e.Framework != "soc2" || e.SCFRelease != "2025.2" || e.RelationshipType != "equal" {
		t.Fatalf("edgeFromView = %+v", e)
	}

	active := edgeFromView(dbx.GetOverlayEdgeViewRow{Status: StatusActive})
	if active.RetiredAt != nil || active.RetirementReason != "" || active.RetiredBy != "" {
		t.Errorf("active edge has retirement fields: %+v", active)
	}

	raw, err := auditJSON(&e)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["requirement_code"] != "CC1.1" || got["anchor_code"] != "HRS-07.1" ||
		got["status"] != StatusRetired || got["retirement_reason"] != reason {
		t.Errorf("audit state = %v", got)
	}
	if b, err := auditJSON(nil); b != nil || err != nil {
		t.Errorf("auditJSON(nil) = %s, %v", b, err)
	}
}

func TestOptional(t *testing.T) {
	t.Parallel()
	if optional("") != nil {
		t.Error("optional(\"\") is not nil")
	}
	if p := optional("soc2"); p == nil || *p != "soc2" {
		t.Errorf("optional(soc2) = %v", p)
	}
}
