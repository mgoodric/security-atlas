package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/hostcapacity"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/monitors"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
)

type recordingClient struct{ recs []*evidencev1.EvidenceRecord }

func (r *recordingClient) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	r.recs = append(r.recs, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}

func (r *recordingClient) Close() error { return nil }

func installCapacity(t *testing.T, fn func(context.Context, hostcapacity.API, string, func() time.Time) ([]hostcapacity.Host, error)) {
	t.Helper()
	prev := capacityCollect
	capacityCollect = fn
	t.Cleanup(func() { capacityCollect = prev })
}

func capacitySetup(t *testing.T) *recordingClient {
	t.Helper()
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	rc := &recordingClient{}
	installSeams(t, seamOverrides{
		collect:   func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return rc, nil },
	})
	return rc
}

// TestCapacityFlagsDefault pins the surface off by default (a key made for
// the other surfaces has no timeseries_query) and the control to empty, so
// monitoring.host_capacity.v1 is routed by type (#2924).
func TestCapacityFlagsDefault(t *testing.T) {
	t.Parallel()
	fs := newRunCmd().Flags()
	for _, name := range []string{"capacity-production-scope", "capacity-control"} {
		fl := fs.Lookup(name)
		if fl == nil {
			t.Fatalf("--%s not registered", name)
		}
		if fl.DefValue != "" {
			t.Errorf("--%s default = %q, want empty", name, fl.DefValue)
		}
	}
	for _, secret := range []string{"api-key", "app-key", "datadog-api-key", "datadog-app-key"} {
		if fs.Lookup(secret) != nil {
			t.Errorf("--%s exists; Datadog keys come from the environment only", secret)
		}
	}
}

func TestDoRun_CapacitySkippedWithoutScope(t *testing.T) {
	rc := capacitySetup(t)
	installCapacity(t, func(context.Context, hostcapacity.API, string, func() time.Time) ([]hostcapacity.Host, error) {
		t.Error("capacity collected with no --capacity-production-scope")
		return nil, nil
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if len(rc.recs) != 0 {
		t.Errorf("pushed %d records, want 0", len(rc.recs))
	}
}

func TestDoRun_CapacityPushesOnePerHost(t *testing.T) {
	rc := capacitySetup(t)
	var gotScope string
	end := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	mem := 60.0
	installCapacity(t, func(_ context.Context, api hostcapacity.API, scope string, _ func() time.Time) ([]hostcapacity.Host, error) {
		if api == nil {
			t.Error("nil capacity API")
		}
		gotScope = scope
		return []hostcapacity.Host{
			{HostName: "web-1", MemoryUsedPercent: &mem, WindowStart: end.Add(-5 * time.Minute), WindowEnd: end},
			{HostName: "web-2", WindowStart: end.Add(-5 * time.Minute), WindowEnd: end},
		}, nil
	})
	f := okFlags()
	f.capacityScope = "env:production"
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if gotScope != "env:production" {
		t.Errorf("scope = %q", gotScope)
	}
	if len(rc.recs) != 2 {
		t.Fatalf("pushed %d records, want 2", len(rc.recs))
	}
	for _, rec := range rc.recs {
		if rec.GetEvidenceKind() != hostcapacity.EvidenceKind || rec.GetControlId() != "" {
			t.Errorf("record kind/control = %s/%q", rec.GetEvidenceKind(), rec.GetControlId())
		}
		if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:datadog:hostcapacity@") {
			t.Errorf("actor = %s", rec.GetSourceAttribution().GetActorId())
		}
	}
}

func TestDoRun_CapacityErrors(t *testing.T) {
	capacitySetup(t)
	f := okFlags()
	f.capacityScope = "env:prod} by {host"
	installCapacity(t, func(context.Context, hostcapacity.API, string, func() time.Time) ([]hostcapacity.Host, error) {
		t.Error("collected with an invalid scope")
		return nil, nil
	})
	if err := doRun(context.Background(), f); err == nil || !strings.Contains(err.Error(), "--capacity-production-scope") {
		t.Errorf("bad scope: err = %v", err)
	}

	boom := errors.New("boom")
	installCapacity(t, func(context.Context, hostcapacity.API, string, func() time.Time) ([]hostcapacity.Host, error) {
		return nil, boom
	})
	f.capacityScope = "env:production"
	if err := doRun(context.Background(), f); !errors.Is(err, boom) {
		t.Errorf("collect error = %v, want boom", err)
	}
}

func TestNewCapacityAPI_IsLiveClient(t *testing.T) {
	t.Parallel()
	if _, ok := newCapacityAPI(&http.Client{}, "https://api.datadoghq.com", "k", "a").(*hostcapacity.Client); !ok {
		t.Error("newCapacityAPI does not build a *hostcapacity.Client")
	}
}
