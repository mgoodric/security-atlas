package pss

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	namespaces []RawNamespace
	err        error
}

func (f *fakeAPI) ListNamespacePSS(_ context.Context) ([]RawNamespace, error) {
	return f.namespaces, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }
}

func TestAssess_Verdicts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ns   RawNamespace
		want AssessResult
	}{
		{
			"enforce-restricted-pass",
			RawNamespace{Name: "prod", EnforceLevel: LevelRestricted},
			ResultPass,
		},
		{
			"enforce-baseline-pass",
			RawNamespace{Name: "prod", EnforceLevel: LevelBaseline},
			ResultPass,
		},
		{
			"enforce-privileged-fail",
			RawNamespace{Name: "legacy", EnforceLevel: LevelPrivileged},
			ResultFail,
		},
		{
			"unenforced-no-labels-fail",
			RawNamespace{Name: "dev"},
			ResultFail,
		},
		{
			"audit-warn-only-no-enforce-fail",
			RawNamespace{Name: "stage", AuditLevel: LevelRestricted, WarnLevel: LevelRestricted},
			ResultFail,
		},
		{
			"unknown-enforce-value-fail",
			RawNamespace{Name: "weird", EnforceLevel: Level("bogus")},
			ResultFail,
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

// TestAssess_UnenforcedRecordedHonestly pins that a namespace with zero PSS
// labels is RECORDED (not dropped) with every level unset and Configured=false
// — the "unenforced, told honestly" path.
func TestAssess_UnenforcedRecordedHonestly(t *testing.T) {
	t.Parallel()
	got, err := Assess(context.Background(), &fakeAPI{namespaces: []RawNamespace{{Name: "dev"}}}, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	a := got[0]
	if a.Configured {
		t.Error("a namespace with no PSS labels must be Configured=false")
	}
	if a.EnforceLevel != LevelUnset || a.AuditLevel != LevelUnset || a.WarnLevel != LevelUnset {
		t.Errorf("unenforced namespace should have all levels unset; got %+v", a)
	}
	if a.Result != ResultFail || !strings.Contains(a.Reason, "no Pod-Security-Standards admission labels") {
		t.Errorf("unenforced verdict wrong: result=%q reason=%q", a.Result, a.Reason)
	}
}

// TestAssess_PartialLabels pins the partial-config case: only enforce is set, no
// audit/warn. enforce drives the verdict; audit/warn stay unset.
func TestAssess_PartialLabels(t *testing.T) {
	t.Parallel()
	ns := RawNamespace{Name: "prod", EnforceLevel: LevelRestricted, EnforceVersion: "v1.29"}
	got, err := Assess(context.Background(), &fakeAPI{namespaces: []RawNamespace{ns}}, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	a := got[0]
	if a.EnforceLevel != LevelRestricted || a.EnforceVersion != "v1.29" {
		t.Errorf("enforce not preserved: %+v", a)
	}
	if a.AuditLevel != LevelUnset || a.WarnLevel != LevelUnset {
		t.Errorf("audit/warn should stay unset; got %+v", a)
	}
	if !a.Configured || a.Result != ResultPass {
		t.Errorf("partial enforce-restricted should be Configured+PASS; got %+v", a)
	}
}

// TestAssess_AllThreeModesPreserved pins that all three modes + their pinned
// versions survive the transform.
func TestAssess_AllThreeModesPreserved(t *testing.T) {
	t.Parallel()
	ns := RawNamespace{
		Name:         "prod",
		EnforceLevel: LevelRestricted, EnforceVersion: "v1.29",
		AuditLevel: LevelBaseline, AuditVersion: "latest",
		WarnLevel: LevelPrivileged, WarnVersion: "v1.28",
	}
	got, _ := Assess(context.Background(), &fakeAPI{namespaces: []RawNamespace{ns}}, fixedNow())
	a := got[0]
	if a.EnforceLevel != LevelRestricted || a.AuditLevel != LevelBaseline || a.WarnLevel != LevelPrivileged {
		t.Errorf("levels not preserved: %+v", a)
	}
	if a.EnforceVersion != "v1.29" || a.AuditVersion != "latest" || a.WarnVersion != "v1.28" {
		t.Errorf("versions not preserved: %+v", a)
	}
}

// TestStruct_PSSLabelsOnly_NoOverCollectionFields is the structural
// over-collection guard (mirrors slice-520 / 523's reflection guard). It reflects
// over every field name of RawNamespace + Admission and FAILS if any field name
// hints at a pod-spec / secret / annotation / arbitrary-label surface, so a
// future field that opens an over-collection door trips the build. The PSS struct
// must carry ONLY: namespace name, the three modes + levels, the optional
// versions, and derived verdict fields.
func TestStruct_PSSLabelsOnly_NoOverCollectionFields(t *testing.T) {
	t.Parallel()
	banned := []string{
		"podspec", "pod_spec", "pod", "container", "secret", "credential",
		"password", "token", "env", "configmap", "annotation", "label",
		"payload", "content", "spec", "status", "owner", "managedfield",
		"pii",
	}
	// allow lists the substrings that legitimately appear in a banned token but
	// are part of a permitted field name (none today; kept for clarity).
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned over-collection token %q — PSS struct must carry only namespace name + the three PSS modes/levels/versions + verdict",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(RawNamespace{}))
	check(reflect.TypeOf(Admission{}))
}

func TestAssess_SkipsUnnamedNamespace(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{namespaces: []RawNamespace{
		{Name: "", EnforceLevel: LevelRestricted},
		{Name: "prod", EnforceLevel: LevelRestricted},
	}}
	got, err := Assess(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(got) != 1 || got[0].Namespace != "prod" {
		t.Fatalf("want only the named namespace; got %+v", got)
	}
}

// TestAssess_BoundedByCap proves the per-run namespace cap holds: feeding more
// than maxNamespaces namespaces truncates the assessment.
func TestAssess_BoundedByCap(t *testing.T) {
	t.Parallel()
	over := maxNamespaces + 25
	raw := make([]RawNamespace, 0, over)
	for i := 0; i < over; i++ {
		raw = append(raw, RawNamespace{Name: "ns-" + string(rune('a'+i%26)) + itoa(i), EnforceLevel: LevelRestricted})
	}
	got, err := Assess(context.Background(), &fakeAPI{namespaces: raw}, fixedNow())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(got) != maxNamespaces {
		t.Errorf("assessed = %d; want capped at %d", len(got), maxNamespaces)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
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
	api := &fakeAPI{namespaces: []RawNamespace{{Name: "prod", EnforceLevel: LevelRestricted}}}
	got, _ := Assess(context.Background(), api, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

func TestNormalizeLevel_KeepsValidDropsUnknown(t *testing.T) {
	t.Parallel()
	for _, valid := range []Level{LevelPrivileged, LevelBaseline, LevelRestricted} {
		if normalizeLevel(valid) != valid {
			t.Errorf("normalizeLevel(%q) dropped a valid level", valid)
		}
	}
	for _, bad := range []Level{Level("bogus"), Level("RESTRICTED"), Level(" baseline")} {
		if normalizeLevel(bad) != LevelUnset {
			t.Errorf("normalizeLevel(%q) should collapse to unset", bad)
		}
	}
}
