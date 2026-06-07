package workload

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAPI struct {
	workloads []RawWorkload
	err       error
}

func (f *fakeAPI) ListWorkloads(_ context.Context) ([]RawWorkload, error) {
	return f.workloads, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }
}

func hardened() RawWorkload {
	return RawWorkload{
		Kind: KindDeployment, Name: "api", Namespace: "prod",
		RunAsNonRoot: true, Privileged: false, ReadOnlyRootFilesystem: true,
		AllowPrivilegeEscalation: false, HostNetwork: false, HostPID: false,
		HostIPC: false, ContainerCount: 1,
	}
}

func TestInspect_Verdicts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*RawWorkload)
		want   ConfigResult
	}{
		{"hardened-pass", func(*RawWorkload) {}, ResultPass},
		{"privileged-fail", func(r *RawWorkload) { r.Privileged = true }, ResultFail},
		{"root-fail", func(r *RawWorkload) { r.RunAsNonRoot = false }, ResultFail},
		{"escalation-fail", func(r *RawWorkload) { r.AllowPrivilegeEscalation = true }, ResultFail},
		{"writable-fs-fail", func(r *RawWorkload) { r.ReadOnlyRootFilesystem = false }, ResultFail},
		{"hostnet-fail", func(r *RawWorkload) { r.HostNetwork = true }, ResultFail},
		{"hostpid-fail", func(r *RawWorkload) { r.HostPID = true }, ResultFail},
		{"hostipc-fail", func(r *RawWorkload) { r.HostIPC = true }, ResultFail},
		{"readerr-inconclusive", func(r *RawWorkload) { r.ReadError = "timeout" }, ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := hardened()
			tc.mutate(&raw)
			got, err := Inspect(context.Background(), &fakeAPI{workloads: []RawWorkload{raw}}, fixedNow())
			if err != nil {
				t.Fatalf("Inspect: %v", err)
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

func TestInspect_SkipsInvalidAndNormalizesKind(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{workloads: []RawWorkload{
		{Kind: KindDeployment, Name: "", Namespace: "p"}, // no name
		{Kind: KindDeployment, Name: "n", Namespace: ""}, // no namespace
		{Kind: "bogus", Name: "w", Namespace: "prod", RunAsNonRoot: true, ReadOnlyRootFilesystem: true, ContainerCount: 1},
	}}
	got, err := Inspect(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	if got[0].WorkloadKind != KindDeployment {
		t.Errorf("kind = %q; want normalized Deployment", got[0].WorkloadKind)
	}
}

func TestInspect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Inspect(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestInspect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Inspect(context.Background(), &fakeAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestInspect_DefaultNow(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{workloads: []RawWorkload{hardened()}}
	got, _ := Inspect(context.Background(), api, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}
