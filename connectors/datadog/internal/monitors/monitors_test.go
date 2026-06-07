package monitors

import (
	"context"
	"errors"
	"testing"
)

type fakeAPI struct {
	monitors []RawMonitor
	err      error
}

func (f *fakeAPI) ListMonitors(_ context.Context) ([]RawMonitor, error) {
	return f.monitors, f.err
}

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{monitors: []RawMonitor{{
		ID: "12345", Name: "API 5xx high", Type: "metric alert", Enabled: true,
		Message: "5xx rate too high @slack-sec-oncall @pagerduty-primary",
	}}}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	r := got[0]
	if r.ID != "12345" || r.Name != "API 5xx high" || r.Type != "metric alert" || !r.Enabled {
		t.Errorf("fields = %+v", r)
	}
	if len(r.Targets) != 2 {
		t.Fatalf("targets = %d; want 2", len(r.Targets))
	}
	kinds := map[string]string{}
	for _, tg := range r.Targets {
		kinds[tg.Name] = tg.Kind
	}
	if kinds["slack-sec-oncall"] != "slack" {
		t.Errorf("slack handle misclassified: %v", kinds)
	}
	if kinds["pagerduty-primary"] != "pagerduty" {
		t.Errorf("pagerduty handle misclassified: %v", kinds)
	}
}

// TestCollect_DropsEmailRecipientPII pins P0-488-3 / AC-10: an "@user@email"
// recipient mention is PII and must never become a target.
func TestCollect_DropsEmailRecipientPII(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{monitors: []RawMonitor{{
		ID: "1", Name: "n", Type: "log alert", Enabled: true,
		Message: "alert @oncall@example.com @slack-ops @victim@corp.test",
	}}}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, tg := range got[0].Targets {
		if tg.Name == "oncall@example.com" || tg.Name == "victim@corp.test" {
			t.Fatalf("email recipient PII leaked into target: %q", tg.Name)
		}
		if containsAt(tg.Name) {
			t.Errorf("target name looks like an email (contains @): %q", tg.Name)
		}
	}
	// The legitimate slack handle survives.
	if len(got[0].Targets) != 1 || got[0].Targets[0].Name != "slack-ops" {
		t.Errorf("expected only the slack handle to survive; got %+v", got[0].Targets)
	}
}

func TestCollect_DedupesHandles(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{monitors: []RawMonitor{{
		ID: "1", Name: "n", Type: "t", Enabled: true,
		Message: "@slack-ops @slack-ops @slack-ops",
	}}}
	got, _ := Collect(context.Background(), api)
	if len(got[0].Targets) != 1 {
		t.Errorf("handles not deduped: %+v", got[0].Targets)
	}
}

func TestCollect_NoMessageNoTargets(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{monitors: []RawMonitor{{ID: "1", Name: "n", Type: "t", Enabled: false}}}
	got, _ := Collect(context.Background(), api)
	if got[0].Targets != nil {
		t.Errorf("targets should be nil; got %+v", got[0].Targets)
	}
	if got[0].Enabled {
		t.Error("enabled should be false")
	}
}

func TestCollect_SkipsInvalidAndDefaultsType(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{monitors: []RawMonitor{
		{ID: "", Name: "n", Type: "t"},                 // no id
		{ID: "1", Name: "", Type: "t"},                 // no name
		{ID: "2", Name: "ok", Type: "", Enabled: true}, // empty type -> "monitor"
	}}
	got, _ := Collect(context.Background(), api)
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	if got[0].Type != "monitor" {
		t.Errorf("type = %q; want defaulted 'monitor'", got[0].Type)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403 forbidden")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestClassifyHandle(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"slack-x":     "slack",
		"pagerduty-x": "pagerduty",
		"webhook-x":   "webhook",
		"teams-x":     "teams",
		"opsgenie-x":  "opsgenie",
		"jira-x":      "jira",
		"some-team":   "handle",
	}
	for in, want := range cases {
		if got := classifyHandle(in); got != want {
			t.Errorf("classifyHandle(%q) = %q; want %q", in, got, want)
		}
	}
}

func containsAt(s string) bool {
	for _, r := range s {
		if r == '@' {
			return true
		}
	}
	return false
}
