package alertrules

import (
	"context"
	"errors"
	"testing"
)

type fakeAPI struct {
	rules    []RawRule
	contacts []ContactPoint
	rulesErr error
	cpErr    error
}

func (f *fakeAPI) ListAlertRules(_ context.Context) ([]RawRule, error) {
	return f.rules, f.rulesErr
}

func (f *fakeAPI) ListContactPoints(_ context.Context) ([]ContactPoint, error) {
	return f.contacts, f.cpErr
}

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		rules: []RawRule{
			{UID: "r1", Title: "High latency", RuleType: "grafana", Paused: false, FolderUID: "f1", ReceiverName: "sec-oncall"},
			{UID: "r2", Title: "Disk full", Paused: true, ReceiverName: "ops-email"},
		},
		contacts: []ContactPoint{
			{Name: "sec-oncall", Kind: "slack"},
			{Name: "ops-email", Kind: "email"},
		},
	}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if !got[0].Enabled {
		t.Error("unpaused rule should be enabled")
	}
	if got[1].Enabled {
		t.Error("paused rule should be disabled")
	}
	if got[0].Folder != "f1" {
		t.Errorf("folder = %q", got[0].Folder)
	}
	if len(got[0].Targets) != 1 || got[0].Targets[0].Kind != "slack" || got[0].Targets[0].Name != "sec-oncall" {
		t.Errorf("target wrong: %+v", got[0].Targets)
	}
	if got[1].Targets[0].Kind != "email" {
		t.Errorf("email contact kind wrong: %+v", got[1].Targets)
	}
}

func TestCollect_UnknownReceiverDefaultsKind(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		rules:    []RawRule{{UID: "r1", Title: "t", ReceiverName: "mystery"}},
		contacts: nil, // no matching contact point
	}
	got, _ := Collect(context.Background(), api)
	if got[0].Targets[0].Kind != "contact_point" {
		t.Errorf("unknown receiver should default to contact_point; got %q", got[0].Targets[0].Kind)
	}
	if got[0].Targets[0].Name != "mystery" {
		t.Errorf("receiver name not preserved: %q", got[0].Targets[0].Name)
	}
}

func TestCollect_NoReceiverNoTarget(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{{UID: "r1", Title: "t"}}}
	got, _ := Collect(context.Background(), api)
	if got[0].Targets != nil {
		t.Errorf("no-receiver rule should have nil targets; got %+v", got[0].Targets)
	}
}

func TestCollect_SkipsInvalidAndDefaultsType(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{rules: []RawRule{
		{UID: "", Title: "t"},       // no uid
		{UID: "r", Title: ""},       // no title
		{UID: "ok", Title: "valid"}, // empty type -> "grafana"
	}}
	got, _ := Collect(context.Background(), api)
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	if got[0].Type != "grafana" {
		t.Errorf("type = %q; want defaulted 'grafana'", got[0].Type)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesRulesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Collect(context.Background(), &fakeAPI{rulesErr: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_PropagatesContactPointsError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Collect(context.Background(), &fakeAPI{cpErr: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_ContactPointWithEmptyKindDefaults(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		rules:    []RawRule{{UID: "r1", Title: "t", ReceiverName: "cp"}},
		contacts: []ContactPoint{{Name: "cp", Kind: ""}},
	}
	got, _ := Collect(context.Background(), api)
	if got[0].Targets[0].Kind != "contact_point" {
		t.Errorf("empty contact kind should default; got %q", got[0].Targets[0].Kind)
	}
}
