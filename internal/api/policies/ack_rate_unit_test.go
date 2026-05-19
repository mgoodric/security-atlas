package policies

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// helper: build a baseline ListPoliciesWithAckRateRow with all the
// required scalar fields populated. Tests override the ack-rate columns
// + status to exercise the wire branches.
func baseAckRateRow(t *testing.T) dbx.ListPoliciesWithAckRateRow {
	t.Helper()
	id := uuid.New()
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	return dbx.ListPoliciesWithAckRateRow{
		ID:                          pgtype.UUID{Bytes: id, Valid: true},
		Title:                       "Information Security Policy",
		Version:                     "v3.2",
		BodyMd:                      "",
		OwnerRole:                   "security_lead",
		ApproverRole:                "cto",
		LinkedControlIds:            []pgtype.UUID{},
		AcknowledgmentRequiredRoles: []string{"all_staff"},
		Status:                      "published",
		SourceAttribution:           "tenant_authored",
		CreatedBy:                   "user-1",
		CreatedAt:                   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:                   pgtype.Timestamptz{Time: now, Valid: true},
	}
}

// ISC-14 — published policy with acks: rate passes through. The
// numerator + denominator come from the SQL CASE branch; the wire layer
// computes percent.
func TestWireFromAckRateRow_PublishedWithAcks_PassesThrough(t *testing.T) {
	row := baseAckRateRow(t)
	denom, num := int64(10), int64(4)
	row.AckDenominator = &denom
	row.AckNumerator = &num

	w := wireFromAckRateRow(row)

	if w.AckRate == nil {
		t.Fatalf("expected non-nil AckRate; got nil")
	}
	if w.AckRate.Numerator != 4 {
		t.Errorf("numerator = %d; want 4", w.AckRate.Numerator)
	}
	if w.AckRate.Denominator != 10 {
		t.Errorf("denominator = %d; want 10", w.AckRate.Denominator)
	}
	if w.AckRate.Percent == nil {
		t.Fatalf("expected non-nil Percent")
	}
	if *w.AckRate.Percent != 40.0 {
		t.Errorf("percent = %f; want 40.0", *w.AckRate.Percent)
	}
	if w.Status != "published" {
		t.Errorf("status = %q; want published", w.Status)
	}
}

// ISC-15 — non-published policy (draft / under_review / approved /
// retired / superseded) returns ack_rate: null. The SQL CASE returns
// NULL for both columns; the wire layer must NOT fabricate a zero cell.
func TestWireFromAckRateRow_NonPublished_AckRateIsNil(t *testing.T) {
	for _, status := range []string{"draft", "under_review", "approved", "superseded"} {
		t.Run(status, func(t *testing.T) {
			row := baseAckRateRow(t)
			row.Status = status
			// Both columns NULL — slice 159 CTE filters published-only,
			// so the LEFT JOIN produces nil pointers for non-published rows.
			row.AckDenominator = nil
			row.AckNumerator = nil

			w := wireFromAckRateRow(row)

			if w.AckRate != nil {
				t.Errorf("expected nil AckRate for %s; got %+v", status, w.AckRate)
			}
			if w.Status != status {
				t.Errorf("status = %q; want %q", w.Status, status)
			}
		})
	}
}

// ISC-16 — published policy with zero denominator (no required-role
// users exist yet): percent is null but the cell is populated with
// 0/0. Mirrors the slice-023 rateResponse semantic (handler emits null
// percent so consumers can distinguish "0% acknowledged" from "no
// required-role members exist").
func TestWireFromAckRateRow_PublishedZeroDenominator_PercentNull(t *testing.T) {
	row := baseAckRateRow(t)
	denom, num := int64(0), int64(0)
	row.AckDenominator = &denom
	row.AckNumerator = &num

	w := wireFromAckRateRow(row)

	if w.AckRate == nil {
		t.Fatalf("expected non-nil AckRate (denominator column was valid)")
	}
	if w.AckRate.Numerator != 0 || w.AckRate.Denominator != 0 {
		t.Errorf("expected 0/0; got %d/%d", w.AckRate.Numerator, w.AckRate.Denominator)
	}
	if w.AckRate.Percent != nil {
		t.Errorf("expected nil Percent for zero denominator; got %v", *w.AckRate.Percent)
	}
}

// ISC-17 — includesAckRate parser handles plain, CSV, repeated,
// whitespace, and rejects omitted / unknown / empty. Mirrors slice
// 104's includesState test.
func TestIncludesAckRate(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"plain", "/v1/policies?include=ack_rate", true},
		{"csv-first", "/v1/policies?include=ack_rate,state", true},
		{"csv-second", "/v1/policies?include=state,ack_rate", true},
		{"repeated", "/v1/policies?include=other&include=ack_rate", true},
		{"trims whitespace", "/v1/policies?include=%20ack_rate%20", true},
		{"omitted", "/v1/policies", false},
		{"unknown only", "/v1/policies?include=state", false},
		{"empty value", "/v1/policies?include=", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", c.url, nil)
			if got := includesAckRate(req); got != c.want {
				t.Errorf("includesAckRate(%s) = %v; want %v", c.url, got, c.want)
			}
		})
	}
}

// Defensive: the joined wire shape MUST be additive — the embedded
// policyWire keeps every field name the omitted-include caller already
// depends on, and only adds `ack_rate`. This test pins the contract:
// the published row's title/version/status/owner_role pass through
// verbatim. (Anti-criterion ISC-A2.)
func TestPolicyWithAckRateWire_PreservesEmbeddedPolicyWireShape(t *testing.T) {
	row := baseAckRateRow(t)
	denom, num := int64(5), int64(5)
	row.AckDenominator = &denom
	row.AckNumerator = &num

	w := wireFromAckRateRow(row)

	if w.Title != "Information Security Policy" {
		t.Errorf("title = %q", w.Title)
	}
	if w.Version != "v3.2" {
		t.Errorf("version = %q", w.Version)
	}
	if w.OwnerRole != "security_lead" {
		t.Errorf("owner_role = %q", w.OwnerRole)
	}
	if w.Status != "published" {
		t.Errorf("status = %q", w.Status)
	}
	if w.AckRate == nil || w.AckRate.Percent == nil || *w.AckRate.Percent != 100.0 {
		t.Errorf("expected 100%% ack rate; got %+v", w.AckRate)
	}
}

// Pin the orphan_policy warning surfaces on the joined shape just as it
// does on the omitted-include shape — keeps the slice-022 AC-7 surface
// consistent across query variants.
func TestWireFromAckRateRow_EmptyLinkedControls_AddsOrphanWarning(t *testing.T) {
	row := baseAckRateRow(t)
	row.LinkedControlIds = []pgtype.UUID{}

	w := wireFromAckRateRow(row)

	found := false
	for _, warn := range w.Warnings {
		if warn == "orphan_policy" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected orphan_policy warning; got %v", w.Warnings)
	}
}
