package netpol

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAPI struct {
	namespaces []RawNamespace
	err        error
}

func (f *fakeAPI) ListNamespaceCoverage(_ context.Context) ([]RawNamespace, error) {
	return f.namespaces, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }
}

// defaultDenyIngress is the canonical default-deny ingress policy: empty
// podSelector, Ingress type, zero ingress rules.
func defaultDenyIngress() RawPolicy {
	return RawPolicy{
		Name: "default-deny-ingress", PolicyTypes: []string{PolicyTypeIngress},
		SelectsAllPods: true, IngressRuleCount: 0,
	}
}

func TestAssess_Verdicts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ns   RawNamespace
		want CoverageResult
	}{
		{
			"default-deny-ingress-pass",
			RawNamespace{Name: "prod", Policies: []RawPolicy{defaultDenyIngress()}},
			ResultPass,
		},
		{
			"default-deny-both-pass",
			RawNamespace{Name: "prod", Policies: []RawPolicy{{
				Name: "deny-all", PolicyTypes: []string{PolicyTypeIngress, PolicyTypeEgress},
				SelectsAllPods: true,
			}}},
			ResultPass,
		},
		{
			"no-policies-fail",
			RawNamespace{Name: "dev", Policies: nil},
			ResultFail,
		},
		{
			"per-pod-allow-only-fail",
			RawNamespace{Name: "stage", Policies: []RawPolicy{{
				Name: "allow-api", PolicyTypes: []string{PolicyTypeIngress},
				SelectsAllPods: false, IngressRuleCount: 1,
			}}},
			ResultFail,
		},
		{
			"all-pods-but-has-allow-rule-fail",
			RawNamespace{Name: "stage", Policies: []RawPolicy{{
				Name: "allow-some", PolicyTypes: []string{PolicyTypeIngress},
				SelectsAllPods: true, IngressRuleCount: 1,
			}}},
			ResultFail,
		},
		{
			"read-error-inconclusive",
			RawNamespace{Name: "kube-system", ReadError: "timeout"},
			ResultInconclusive,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Assess(context.Background(), &fakeAPI{namespaces: []RawNamespace{tc.ns}}, fixedNow())
			if err != nil {
				t.Fatalf("Assess: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d; want 1", len(got))
			}
			if got[0].Result != tc.want {
				t.Errorf("result = %q; want %q (reason: %q)", got[0].Result, tc.want, got[0].Reason)
			}
		})
	}
}

func TestAssess_DefaultDenyFlagsAndPolicyCount(t *testing.T) {
	t.Parallel()
	ns := RawNamespace{Name: "prod", Policies: []RawPolicy{
		defaultDenyIngress(),
		{Name: "deny-egress", PolicyTypes: []string{PolicyTypeEgress}, SelectsAllPods: true},
	}}
	got, err := Assess(context.Background(), &fakeAPI{namespaces: []RawNamespace{ns}}, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	c := got[0]
	if !c.DefaultDenyIngress || !c.DefaultDenyEgress {
		t.Errorf("want both default-deny flags; got ingress=%v egress=%v", c.DefaultDenyIngress, c.DefaultDenyEgress)
	}
	if c.PolicyCount != 2 {
		t.Errorf("policy_count = %d; want 2", c.PolicyCount)
	}
	if len(c.Policies) != 2 {
		t.Errorf("policy summaries = %d; want 2", len(c.Policies))
	}
}

func TestAssess_SkipsUnnamedNamespace(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{namespaces: []RawNamespace{
		{Name: "", Policies: []RawPolicy{defaultDenyIngress()}},
		{Name: "prod", Policies: []RawPolicy{defaultDenyIngress()}},
	}}
	got, err := Assess(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(got) != 1 || got[0].Namespace != "prod" {
		t.Fatalf("want only the named namespace; got %+v", got)
	}
}

func TestAssess_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Assess(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestAssess_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Assess(context.Background(), &fakeAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestAssess_DefaultNow(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{namespaces: []RawNamespace{{Name: "prod", Policies: []RawPolicy{defaultDenyIngress()}}}}
	got, _ := Assess(context.Background(), api, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

func TestNormalizeTypes_DedupAndDropsUnknown(t *testing.T) {
	t.Parallel()
	got := normalizeTypes([]string{"Ingress", "Ingress", "bogus", "Egress"})
	if len(got) != 2 || got[0] != PolicyTypeEgress || got[1] != PolicyTypeIngress {
		t.Errorf("normalizeTypes = %v; want sorted [Egress Ingress]", got)
	}
}
