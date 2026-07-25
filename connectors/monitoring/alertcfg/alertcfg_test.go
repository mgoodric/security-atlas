package alertcfg

import (
	"testing"
	"time"
)

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }
}

func TestNormalize_HappyPath(t *testing.T) {
	t.Parallel()
	raw := []RawRule{{
		ID: "mon-1", Name: "High error rate", Type: "metric alert", Enabled: true,
		Folder: "Prod",
		Targets: []Target{
			{Kind: "slack", Name: "#sec-oncall"},
			{Kind: "pagerduty", Name: "pd-primary"},
		},
	}}
	got := Normalize(VendorDatadog, raw, fixedNow())
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	r := got[0]
	if r.SourceVendor != VendorDatadog {
		t.Errorf("vendor = %q", r.SourceVendor)
	}
	if r.RuleID != "mon-1" || r.RuleName != "High error rate" || r.RuleType != "metric alert" {
		t.Errorf("fields = %+v", r)
	}
	if !r.Enabled {
		t.Error("enabled should be true")
	}
	if r.Folder != "Prod" {
		t.Errorf("folder = %q", r.Folder)
	}
	if r.ObservedAt != time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) {
		t.Errorf("observedAt not truncated to hour: %v", r.ObservedAt)
	}
	if len(r.Targets) != 2 {
		t.Fatalf("targets = %d; want 2", len(r.Targets))
	}
	// Sorted by kind: pagerduty < slack.
	if r.Targets[0].Kind != "pagerduty" || r.Targets[1].Kind != "slack" {
		t.Errorf("targets not sorted: %+v", r.Targets)
	}
}

func TestNormalize_DropsInvalidRules(t *testing.T) {
	t.Parallel()
	raw := []RawRule{
		{ID: "", Name: "n", Type: "t"},        // no id
		{ID: "i", Name: "", Type: "t"},        // no name
		{ID: "i", Name: "n", Type: ""},        // no type
		{ID: " i ", Name: " n ", Type: " t "}, // trimmed -> valid
	}
	got := Normalize(VendorGrafana, raw, fixedNow())
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1 (only the trimmed-valid rule)", len(got))
	}
	if got[0].RuleID != "i" || got[0].RuleName != "n" || got[0].RuleType != "t" {
		t.Errorf("trimming failed: %+v", got[0])
	}
}

func TestSanitizeTargets_DropsNamelessAndDefaultsKind(t *testing.T) {
	t.Parallel()
	raw := []RawRule{{
		ID: "i", Name: "n", Type: "t",
		Targets: []Target{
			{Kind: "slack", Name: ""},      // nameless -> dropped
			{Kind: "", Name: "ops@x.test"}, // kind defaults to "unknown" (name kept verbatim — see note)
			{Kind: "webhook", Name: "hook-1"},
		},
	}}
	got := Normalize(VendorDatadog, raw, fixedNow())
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	tg := got[0].Targets
	if len(tg) != 2 {
		t.Fatalf("targets = %d; want 2 (nameless dropped)", len(tg))
	}
	foundUnknown := false
	for _, x := range tg {
		if x.Name == "" {
			t.Error("nameless target survived")
		}
		if x.Kind == "" {
			t.Error("empty kind survived")
		}
		if x.Kind == "unknown" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Error("empty kind should default to 'unknown'")
	}
}

func TestNormalize_NoTargetsYieldsNil(t *testing.T) {
	t.Parallel()
	got := Normalize(VendorGrafana, []RawRule{{ID: "i", Name: "n", Type: "t"}}, fixedNow())
	if got[0].Targets != nil {
		t.Errorf("targets should be nil when none present; got %v", got[0].Targets)
	}
}

func TestNormalize_DefaultNow(t *testing.T) {
	t.Parallel()
	got := Normalize(VendorDatadog, []RawRule{{ID: "i", Name: "n", Type: "t"}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

func TestNormalize_Empty(t *testing.T) {
	t.Parallel()
	if got := Normalize(VendorDatadog, nil, fixedNow()); len(got) != 0 {
		t.Errorf("len = %d; want 0", len(got))
	}
}
