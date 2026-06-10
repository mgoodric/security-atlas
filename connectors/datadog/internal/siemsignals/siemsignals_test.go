package siemsignals

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	signals  []RawSignal
	err      error
	gotSince time.Time
}

func (f *fakeAPI) ListSignals(_ context.Context, since time.Time) ([]RawSignal, error) {
	f.gotSince = since
	return f.signals, f.err
}

func fixedClock() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	triaged := time.Date(2026, 6, 7, 11, 5, 0, 0, time.UTC)
	first := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	api := &fakeAPI{signals: []RawSignal{{
		ID: "sig-1", RuleID: "rule-aaa", RuleName: "Brute force on login",
		Severity: "HIGH", Status: "archived",
		FirstSeen: first, Triaged: triaged, LastUpdated: triaged,
		TriagerHandle: "alice-sec",
	}}}
	got, err := Collect(context.Background(), api, 24*time.Hour, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	s := got[0]
	if s.SignalID != "sig-1" || s.RuleID != "rule-aaa" || s.RuleName != "Brute force on login" {
		t.Errorf("id/rule wrong: %+v", s)
	}
	if s.Severity != "high" {
		t.Errorf("severity = %q; want lower-cased 'high'", s.Severity)
	}
	if s.Status != "archived" {
		t.Errorf("status = %q; want 'archived'", s.Status)
	}
	if s.TriagerHandle != "alice-sec" {
		t.Errorf("triager = %q; want 'alice-sec'", s.TriagerHandle)
	}
	if !s.TriagedAt.Equal(triaged) || !s.FirstSeenAt.Equal(first) {
		t.Errorf("timestamps wrong: %+v", s)
	}
	if !s.ObservedAt.Equal(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at not truncated to hour: %v", s.ObservedAt)
	}
	// The look-back window is honored: since = now - 24h.
	if !api.gotSince.Equal(time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("since = %v; want now-24h", api.gotSince)
	}
}

func TestCollect_LookbackDefaultsTo24h(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, 0, fixedClock); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("default lookback not 24h: since=%v", api.gotSince)
	}
}

func TestCollect_CustomLookback(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, 6*time.Hour, fixedClock); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !api.gotSince.Equal(time.Date(2026, 6, 7, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("custom lookback not honored: since=%v", api.gotSince)
	}
}

func TestCollect_DropsTriagerEmailPII(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{signals: []RawSignal{{
		ID: "s1", RuleID: "r1", Status: "archived",
		TriagerHandle: "victim@corp.test", // email-shaped: PII, must be dropped
	}}}
	got, err := Collect(context.Background(), api, time.Hour, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got[0].TriagerHandle != "" {
		t.Errorf("email triager not dropped: %q", got[0].TriagerHandle)
	}
}

func TestCollect_SkipsInvalid(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{signals: []RawSignal{
		{ID: "", RuleID: "r"},    // no signal id
		{ID: "s", RuleID: ""},    // no rule id
		{ID: "s2", RuleID: "r2"}, // valid
	}}
	got, _ := Collect(context.Background(), api, time.Hour, fixedClock)
	if len(got) != 1 || got[0].SignalID != "s2" {
		t.Errorf("invalid signals not skipped: %+v", got)
	}
}

func TestCollect_DefaultsSeverityAndStatus(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{signals: []RawSignal{
		{ID: "s", RuleID: "r", Severity: "", Status: ""},
	}}
	got, _ := Collect(context.Background(), api, time.Hour, fixedClock)
	if got[0].Severity != "info" {
		t.Errorf("severity = %q; want defaulted 'info'", got[0].Severity)
	}
	if got[0].Status != "open" {
		t.Errorf("status = %q; want defaulted 'open'", got[0].Status)
	}
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":             "open",
		"open":         "open",
		"new":          "open",
		"under_review": "under_review",
		"in_review":    "under_review",
		"reviewing":    "under_review",
		"archived":     "archived",
		"closed":       "archived",
		"resolved":     "archived",
		"triaged":      "triaged",
		"some_future":  "open",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, time.Hour, fixedClock); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403 forbidden")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}, time.Hour, fixedClock); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_NilClockUsesNow(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if _, err := Collect(context.Background(), api, time.Hour, nil); err != nil {
		t.Fatalf("Collect with nil clock: %v", err)
	}
	if api.gotSince.IsZero() {
		t.Error("since not set with nil clock")
	}
}

// TestStructuralOverCollectionGuard is the load-bearing over-collection guard
// (P0-636): it pins, via reflection, that neither RawSignal nor Signal has ANY
// field capable of holding a signal MESSAGE body, a matched log/event SAMPLE,
// the detection QUERY, a signal-body TAG/facet, or a recipient/actor email. If
// a future edit adds such a field, this test fails — the struct's field set IS
// the structural guard, mirroring slice 533.
func TestStructuralOverCollectionGuard(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]bool{
		"RawSignal": {
			"ID": true, "RuleID": true, "RuleName": true, "Severity": true,
			"Status": true, "FirstSeen": true, "Triaged": true,
			"LastUpdated": true, "TriagerHandle": true,
		},
		"Signal": {
			"SignalID": true, "RuleID": true, "RuleName": true, "Severity": true,
			"Status": true, "FirstSeenAt": true, "TriagedAt": true,
			"LastUpdatedAt": true, "TriagerHandle": true, "ObservedAt": true,
		},
	}
	// Substrings that must NOT appear in any field name on these structs — the
	// excluded over-collection surfaces.
	banned := []string{
		"message", "sample", "event", "payload", "query", "log",
		"secret", "token", "url", "webhook", "password", "recipient",
		"email", "body", "tag", "facet", "condition", "filter", "custom",
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
	check(reflect.TypeOf(RawSignal{}))
	check(reflect.TypeOf(Signal{}))
}
