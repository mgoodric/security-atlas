package oncall

import (
	"context"
	"errors"
	"testing"
)

type fakeAPI struct {
	policies []RawPolicy
	err      error
}

func (f *fakeAPI) ListEscalationPolicies(_ context.Context) ([]RawPolicy, error) {
	return f.policies, f.err
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_NormalizesAndDerivesCoverage(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{policies: []RawPolicy{
		{
			ID:   "PABC",
			Name: "Primary",
			Tiers: []RawTier{
				{Level: 1, Targets: []RawTarget{{Kind: "user_reference", ID: "U1", Name: "Alice"}}},
				{Level: 2, Targets: []RawTarget{{Kind: "schedule_reference", ID: "S1", Name: "Backup Rotation"}}},
			},
		},
		// Uncovered policy: a tier with no usable targets.
		{ID: "PEMPTY", Name: "Empty", Tiers: []RawTier{{Level: 1, Targets: []RawTarget{{ID: "", Name: ""}}}}},
		// Dropped: blank id.
		{ID: "", Name: "blank"},
	}}
	got, err := Collect(context.Background(), api)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d policies; want 2 (blank-id dropped)", len(got))
	}
	p := got[0]
	if p.ID != "PABC" || !p.Covered || p.NumTier != 2 {
		t.Errorf("policy0 = %+v", p)
	}
	if p.Tiers[0].OnCall[0].Kind != "user" {
		t.Errorf("tier1 kind = %q; want user", p.Tiers[0].OnCall[0].Kind)
	}
	if p.Tiers[1].OnCall[0].Kind != "schedule" {
		t.Errorf("tier2 kind = %q; want schedule", p.Tiers[1].OnCall[0].Kind)
	}
	if got[1].Covered {
		t.Error("PEMPTY should be uncovered")
	}
}

func TestNormalizeKind(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"user_reference":     "user",
		"schedule_reference": "schedule",
		"":                   "user",
		"SCHEDULE":           "schedule",
	}
	for in, want := range cases {
		if got := normalizeKind(in); got != want {
			t.Errorf("normalizeKind(%q) = %q; want %q", in, got, want)
		}
	}
}
