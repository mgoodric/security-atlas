package firing

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixedClock() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	fired := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	resolved := time.Date(2026, 6, 7, 10, 30, 0, 0, time.UTC)
	got := Collect(VendorDatadog, []RawFiring{{
		RuleID: "mon-1", State: "Alert", FiredAt: fired, ResolvedAt: resolved,
		TargetHandle: "slack-sec-oncall", TargetKind: "slack",
	}}, fixedClock)
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	f := got[0]
	if f.SourceVendor != VendorDatadog || f.RuleID != "mon-1" {
		t.Errorf("vendor/rule wrong: %+v", f)
	}
	if f.State != StateAlerting {
		t.Errorf("state = %q; want alerting", f.State)
	}
	if !f.FiredAt.Equal(fired) || !f.ResolvedAt.Equal(resolved) {
		t.Errorf("timestamps wrong: %+v", f)
	}
	if f.Target == nil || f.Target.Name != "slack-sec-oncall" || f.Target.Kind != "slack" {
		t.Errorf("target wrong: %+v", f.Target)
	}
	if !f.ObservedAt.Equal(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at not truncated to hour: %v", f.ObservedAt)
	}
}

func TestCollect_DropsRulelessAndUnfired(t *testing.T) {
	t.Parallel()
	fired := fixedClock().Add(-time.Hour)
	got := Collect(VendorGrafana, []RawFiring{
		{RuleID: "", State: "alerting", FiredAt: fired}, // no rule id
		{RuleID: "r", State: "alerting"},                // no fired_at (zero)
		{RuleID: "ok", State: "Normal", FiredAt: fired}, // valid
	}, fixedClock)
	if len(got) != 1 || got[0].RuleID != "ok" {
		t.Fatalf("invalid firings not dropped: %+v", got)
	}
	if got[0].State != StateResolved {
		t.Errorf("Normal not folded to resolved: %q", got[0].State)
	}
}

func TestCollect_DropsEmailTargetPII(t *testing.T) {
	t.Parallel()
	fired := fixedClock().Add(-time.Hour)
	got := Collect(VendorDatadog, []RawFiring{{
		RuleID: "r", State: "alerting", FiredAt: fired,
		TargetHandle: "victim@corp.test", TargetKind: "email", // email-shaped: PII, dropped
	}}, fixedClock)
	if got[0].Target != nil {
		t.Errorf("email target not dropped: %+v", got[0].Target)
	}
}

func TestCollect_EmptyTargetIsNil(t *testing.T) {
	t.Parallel()
	fired := fixedClock().Add(-time.Hour)
	got := Collect(VendorDatadog, []RawFiring{{RuleID: "r", State: "alerting", FiredAt: fired}}, fixedClock)
	if got[0].Target != nil {
		t.Errorf("empty target should be nil: %+v", got[0].Target)
	}
}

func TestCollect_DefaultTargetKind(t *testing.T) {
	t.Parallel()
	fired := fixedClock().Add(-time.Hour)
	got := Collect(VendorDatadog, []RawFiring{{
		RuleID: "r", State: "alerting", FiredAt: fired, TargetHandle: "pd-primary", TargetKind: "",
	}}, fixedClock)
	if got[0].Target == nil || got[0].Target.Kind != "handle" {
		t.Errorf("empty kind not defaulted to handle: %+v", got[0].Target)
	}
}

func TestCollect_SortedByRuleThenFired(t *testing.T) {
	t.Parallel()
	base := fixedClock().Add(-2 * time.Hour)
	got := Collect(VendorDatadog, []RawFiring{
		{RuleID: "b", State: "alerting", FiredAt: base.Add(time.Minute)},
		{RuleID: "a", State: "alerting", FiredAt: base.Add(2 * time.Minute)},
		{RuleID: "a", State: "alerting", FiredAt: base.Add(time.Minute)},
	}, fixedClock)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].RuleID != "a" || got[1].RuleID != "a" || got[2].RuleID != "b" {
		t.Errorf("not rule-sorted: %v %v %v", got[0].RuleID, got[1].RuleID, got[2].RuleID)
	}
	if !got[0].FiredAt.Before(got[1].FiredAt) {
		t.Error("same-rule firings not fired-at-sorted")
	}
}

func TestCollect_NilClockUsesNow(t *testing.T) {
	t.Parallel()
	got := Collect(VendorDatadog, []RawFiring{{RuleID: "r", State: "alerting", FiredAt: time.Now()}}, nil)
	if len(got) != 1 || got[0].ObservedAt.IsZero() {
		t.Errorf("nil clock not handled: %+v", got)
	}
}

func TestNormalizeState(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":            StateAlerting,
		"Alert":       StateAlerting,
		"alerting":    StateAlerting,
		"firing":      StateAlerting,
		"triggered":   StateAlerting,
		"Error":       StateAlerting,
		"OK":          StateResolved,
		"resolved":    StateResolved,
		"Normal":      StateResolved,
		"recovered":   StateResolved,
		"No Data":     StateNoData,
		"no_data":     StateNoData,
		"NoData":      StateNoData,
		"Pending":     StatePending,
		"warn":        StatePending,
		"some_future": StateAlerting,
	}
	for in, want := range cases {
		if got := NormalizeState(in); got != want {
			t.Errorf("NormalizeState(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestStructuralOverCollectionGuard is the load-bearing over-collection guard
// (P0-535, Information Disclosure DOMINANT): it pins, via reflection, that
// neither RawFiring nor Firing has ANY field capable of holding an alert
// MESSAGE body, a triggering METRIC VALUE, a secret WEBHOOK URL, or recipient
// PII. If a future edit adds such a field, this test fails — the struct's field
// set IS the structural guard, mirroring slice 636's siemsignals guard.
func TestStructuralOverCollectionGuard(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]bool{
		"RawFiring": {
			"RuleID": true, "State": true, "FiredAt": true, "ResolvedAt": true,
			"TargetHandle": true, "TargetKind": true,
		},
		"Firing": {
			"SourceVendor": true, "RuleID": true, "State": true, "FiredAt": true,
			"ResolvedAt": true, "Target": true, "ObservedAt": true,
		},
		"Target": {"Kind": true, "Name": true},
	}
	// Substrings that must NOT appear in any field name on these structs — the
	// excluded over-collection surfaces.
	banned := []string{
		"message", "body", "text", "annotation", "metric", "value", "sample",
		"secret", "token", "url", "webhook", "password", "recipient", "email",
		"query", "payload", "label", "tag",
	}
	check := func(typ reflect.Type) {
		perm, ok := allowed[typ.Name()]
		if !ok {
			t.Fatalf("unexpected struct %q under over-collection guard", typ.Name())
		}
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if !perm[name] {
				t.Errorf("%s has un-allow-listed field %q — possible over-collection surface", typ.Name(), name)
			}
			low := strings.ToLower(name)
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%s field %q contains banned substring %q (over-collection)", typ.Name(), name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(RawFiring{}))
	check(reflect.TypeOf(Firing{}))
	check(reflect.TypeOf(Target{}))
}
