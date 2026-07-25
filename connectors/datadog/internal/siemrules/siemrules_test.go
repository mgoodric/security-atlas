package siemrules

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	rules []RawRule
	err   error
}

func (f *fakeAPI) ListRules(_ context.Context) ([]RawRule, error) { return f.rules, f.err }

func fixedClock() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{{
		ID: "abc-123", Name: "Brute force on login", DetectionClass: "log_detection",
		Enabled: true, Severity: "HIGH",
		Handles: []string{"@slack-sec-oncall", "@pagerduty-primary"},
	}}}
	got, err := Collect(context.Background(), api, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	r := got[0]
	if r.RuleID != "abc-123" || r.RuleName != "Brute force on login" {
		t.Errorf("id/name wrong: %+v", r)
	}
	if r.DetectionClass != "log" {
		t.Errorf("class = %q; want normalized 'log'", r.DetectionClass)
	}
	if !r.Enabled {
		t.Error("enabled should be true")
	}
	if r.Severity != "high" {
		t.Errorf("severity = %q; want lower-cased 'high'", r.Severity)
	}
	if !r.ObservedAt.Equal(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at not truncated to hour: %v", r.ObservedAt)
	}
	if len(r.Targets) != 2 {
		t.Fatalf("targets = %d; want 2", len(r.Targets))
	}
	kinds := map[string]string{}
	for _, tg := range r.Targets {
		kinds[tg.Name] = tg.Kind
	}
	if kinds["slack-sec-oncall"] != "slack" || kinds["pagerduty-primary"] != "pagerduty" {
		t.Errorf("handles misclassified: %v", kinds)
	}
}

// TestCollect_DropsEmailRecipientPII pins P0-533 / threat-model I: an
// "@user@email" recipient mention is PII and must never become a target.
func TestCollect_DropsEmailRecipientPII(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{{
		ID: "1", Name: "n", DetectionClass: "log", Enabled: true,
		Handles: []string{"@oncall@example.com", "@slack-ops", "@victim@corp.test"},
	}}}
	got, err := Collect(context.Background(), api, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, tg := range got[0].Targets {
		if strings.Contains(tg.Name, "@") {
			t.Errorf("target name looks like an email (contains @): %q", tg.Name)
		}
	}
	if len(got[0].Targets) != 1 || got[0].Targets[0].Name != "slack-ops" {
		t.Errorf("expected only the slack handle to survive; got %+v", got[0].Targets)
	}
}

func TestCollect_BareHandleForm(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{{
		ID: "1", Name: "n", DetectionClass: "log", Enabled: true,
		Handles: []string{"slack-ops", "slack-ops"}, // bare + duplicated
	}}}
	got, _ := Collect(context.Background(), api, fixedClock)
	if len(got[0].Targets) != 1 || got[0].Targets[0].Name != "slack-ops" {
		t.Errorf("bare handle not parsed/deduped: %+v", got[0].Targets)
	}
}

func TestCollect_DefaultsClassAndSeverity(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{
		{ID: "2", Name: "ok", DetectionClass: "", Enabled: true, Severity: ""},
	}}
	got, _ := Collect(context.Background(), api, fixedClock)
	if got[0].DetectionClass != "log" {
		t.Errorf("class = %q; want defaulted 'log'", got[0].DetectionClass)
	}
	if got[0].Severity != "info" {
		t.Errorf("severity = %q; want defaulted 'info'", got[0].Severity)
	}
}

func TestNormalizeClass(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                   "log",
		"log_detection":      "log",
		"signal_correlation": "signal_correlation",
		"correlation":        "signal_correlation",
		"threshold":          "threshold",
		"impossible_travel":  "threshold",
		"new_value":          "threshold",
		"some_future_kind":   "some_future_kind",
	}
	for in, want := range cases {
		if got := normalizeClass(in); got != want {
			t.Errorf("normalizeClass(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCollect_SkipsInvalid(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{
		{ID: "", Name: "n"},   // no id
		{ID: "1", Name: ""},   // no name
		{ID: "2", Name: "ok"}, // valid
	}}
	got, _ := Collect(context.Background(), api, fixedClock)
	if len(got) != 1 || got[0].RuleID != "2" {
		t.Errorf("invalid rules not skipped: %+v", got)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, fixedClock); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403 forbidden")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}, fixedClock); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestClassifyHandle(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"slack-x": "slack", "pagerduty-x": "pagerduty", "webhook-x": "webhook",
		"teams-x": "teams", "opsgenie-x": "opsgenie", "jira-x": "jira", "team": "handle",
	}
	for in, want := range cases {
		if got := classifyHandle(in); got != want {
			t.Errorf("classifyHandle(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestStructuralOverCollectionGuard is the load-bearing over-collection guard
// (P0-533): it pins, via reflection, that neither RawRule nor Rule has ANY
// field capable of holding a firing signal, a raw log sample, a matched-event
// payload, a secret notification target, or the raw detection query. If a
// future edit adds such a field, this test fails — the struct's field set IS
// the structural guard.
func TestStructuralOverCollectionGuard(t *testing.T) {
	t.Parallel()
	// The ONLY field names permitted on the secret-free structs.
	allowed := map[string]map[string]bool{
		"RawRule": {
			"ID": true, "Name": true, "DetectionClass": true,
			"Enabled": true, "Severity": true, "Handles": true,
		},
		"Rule": {
			"RuleID": true, "RuleName": true, "DetectionClass": true,
			"Enabled": true, "Severity": true, "Targets": true, "ObservedAt": true,
		},
		"Target": {"Kind": true, "Name": true},
	}
	// Substrings that must NOT appear in any field name on these structs — the
	// excluded over-collection surfaces.
	banned := []string{
		"signal", "sample", "event", "payload", "query", "log",
		"secret", "token", "url", "webhook", "password", "recipient", "email",
		"condition", "filter",
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
	check(reflect.TypeOf(RawRule{}))
	check(reflect.TypeOf(Rule{}))
	check(reflect.TypeOf(Target{}))
}
