// Pure-Go unit tests for the slice-515 CSF assessment domain — no Postgres,
// no build tag (the slice 353 Q-2 pre-DB unit convention). Covers the Tier /
// kind / outcome validators and the pure Gap + TierGap computation. The DB
// round-trips (RLS isolation, audit writes, CRUD) live in the
// integration-tagged suite under internal/api/csfassessment.
package csfassessment

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidTiers_EnumeratesFour(t *testing.T) {
	t.Parallel()
	want := []string{"tier1_partial", "tier2_risk_informed", "tier3_repeatable", "tier4_adaptive"}
	if len(ValidTiers) != len(want) {
		t.Fatalf("ValidTiers has %d entries, want %d", len(ValidTiers), len(want))
	}
	for _, tok := range want {
		if !ValidTiers[tok] {
			t.Errorf("ValidTiers missing %q", tok)
		}
	}
	for _, bad := range []string{"", "tier0", "tier5_godmode", "adaptive", "TIER1_PARTIAL"} {
		if ValidTiers[bad] {
			t.Errorf("ValidTiers accepts invalid token %q", bad)
		}
	}
}

func TestValidKinds_CurrentTarget(t *testing.T) {
	t.Parallel()
	if !ValidKinds["current"] || !ValidKinds["target"] {
		t.Fatal("ValidKinds must accept current + target")
	}
	for _, bad := range []string{"", "baseline", "Current", "future"} {
		if ValidKinds[bad] {
			t.Errorf("ValidKinds accepts invalid kind %q", bad)
		}
	}
}

func TestValidOutcomes_OrdinalScale(t *testing.T) {
	t.Parallel()
	cases := map[string]int{"not_targeted": 0, "partial": 1, "largely": 2, "fully": 3}
	for tok, ord := range cases {
		got, ok := ValidOutcomes[tok]
		if !ok {
			t.Errorf("ValidOutcomes missing %q", tok)
			continue
		}
		if got != ord {
			t.Errorf("ValidOutcomes[%q] = %d, want %d", tok, got, ord)
		}
	}
	if _, ok := ValidOutcomes["unknown"]; ok {
		t.Error("ValidOutcomes accepts unknown outcome")
	}
}

func sel(code, outcome string) Selection {
	return Selection{
		SubcategoryCode:  code,
		SubcategoryTitle: code + " title",
		RequirementID:    uuid.New(),
		TargetOutcome:    outcome,
	}
}

func TestGap_DeltaAndMet(t *testing.T) {
	t.Parallel()
	current := []Selection{
		sel("GV.OC-01", "partial"), // ord 1
		sel("GV.OC-02", "fully"),   // ord 3
	}
	target := []Selection{
		sel("GV.OC-01", "fully"),   // ord 3 → gap +2, not met
		sel("GV.OC-02", "largely"), // ord 2 → gap -1, met
		sel("ID.AM-01", "partial"), // current absent → current not_targeted (0) → gap +1
	}
	rows := Gap(current, target)
	if len(rows) != 3 {
		t.Fatalf("Gap rows = %d, want 3 (union of subcategories)", len(rows))
	}
	byCode := map[string]GapRow{}
	for _, r := range rows {
		byCode[r.SubcategoryCode] = r
	}
	if r := byCode["GV.OC-01"]; r.GapDelta != 2 || r.Met {
		t.Errorf("GV.OC-01: delta=%d met=%v, want delta=2 met=false", r.GapDelta, r.Met)
	}
	if r := byCode["GV.OC-02"]; r.GapDelta != -1 || !r.Met {
		t.Errorf("GV.OC-02: delta=%d met=%v, want delta=-1 met=true", r.GapDelta, r.Met)
	}
	if r := byCode["ID.AM-01"]; r.GapDelta != 1 || r.CurrentOutcome != "not_targeted" || r.Met {
		t.Errorf("ID.AM-01: delta=%d cur=%q met=%v, want delta=1 cur=not_targeted met=false", r.GapDelta, r.CurrentOutcome, r.Met)
	}
}

func TestGap_PreservesFirstSeenOrder(t *testing.T) {
	t.Parallel()
	current := []Selection{sel("PR.AA-05", "partial"), sel("GV.OC-01", "partial")}
	target := []Selection{sel("ID.AM-01", "fully")}
	rows := Gap(current, target)
	// Order is first-seen across current then target: PR.AA-05, GV.OC-01, ID.AM-01.
	wantOrder := []string{"PR.AA-05", "GV.OC-01", "ID.AM-01"}
	if len(rows) != len(wantOrder) {
		t.Fatalf("rows = %d, want %d", len(rows), len(wantOrder))
	}
	for i, w := range wantOrder {
		if rows[i].SubcategoryCode != w {
			t.Errorf("row[%d] = %q, want %q", i, rows[i].SubcategoryCode, w)
		}
	}
}

func TestGap_EmptyProfiles(t *testing.T) {
	t.Parallel()
	if rows := Gap(nil, nil); len(rows) != 0 {
		t.Fatalf("Gap(nil,nil) = %d rows, want 0", len(rows))
	}
}

func TestTierGap(t *testing.T) {
	t.Parallel()
	if d, ok := TierGap("tier1_partial", "tier3_repeatable"); !ok || d != 2 {
		t.Errorf("TierGap(1,3) = %d,%v want 2,true", d, ok)
	}
	if d, ok := TierGap("tier4_adaptive", "tier2_risk_informed"); !ok || d != -2 {
		t.Errorf("TierGap(4,2) = %d,%v want -2,true", d, ok)
	}
	if _, ok := TierGap("tier1_partial", "bogus"); ok {
		t.Error("TierGap accepted a bogus target tier")
	}
	if _, ok := TierGap("", "tier1_partial"); ok {
		t.Error("TierGap accepted an empty current tier")
	}
}
