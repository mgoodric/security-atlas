// Seam tests for doRun. The PagerDuty reads + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching live
// PagerDuty or a real platform. Seams are restored via t.Cleanup.
//
// No real PagerDuty tokens in fixtures — neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
)

type fakeSDKClient struct {
	pushErr     error
	pushed      int
	closeCalled bool
}

func (f *fakeSDKClient) Push(_ context.Context, _ *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	f.pushed++
	return &evidencev1.EvidenceReceipt{}, nil
}

func (f *fakeSDKClient) Close() error { f.closeCalled = true; return nil }

type seamOverrides struct {
	oncall    func(ctx context.Context, api oncall.API) ([]oncall.Policy, error)
	incidents func(ctx context.Context, api incidents.API, since, until time.Time) ([]incidents.Incident, error)
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.oncall != nil {
		prev := oncallCollect
		oncallCollect = o.oncall
		t.Cleanup(func() { oncallCollect = prev })
	}
	if o.incidents != nil {
		prev := incidentsCollect
		incidentsCollect = o.incidents
		t.Cleanup(func() { incidentsCollect = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PAGERDUTY_TOKEN", "test-pagerduty-token")
}

func okFlags() runFlags {
	return runFlags{environment: "prod", service: "pagerduty", onCallControl: "scf:IRO-04", incidentControl: "scf:IRO-02", lookbackDays: 90}
}

func twoPolicies() []oncall.Policy {
	return []oncall.Policy{
		{ID: "P1", Name: "Primary", NumTier: 1, Covered: true},
		{ID: "P2", Name: "Secondary", NumTier: 0, Covered: false},
	}
}

func oneIncident() []incidents.Incident {
	return []incidents.Incident{
		{ID: "INC1", Number: 1, Status: "resolved", Urgency: "high", CreatedAt: time.Now()},
	}
}

func TestDoRun_PushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		oncall: func(_ context.Context, _ oncall.API) ([]oncall.Policy, error) { return twoPolicies(), nil },
		incidents: func(_ context.Context, _ incidents.API, _, _ time.Time) ([]incidents.Incident, error) {
			return oneIncident(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 3 {
		t.Errorf("pushed = %d; want 3 (2 policies + 1 incident)", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRun_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("PAGERDUTY_TOKEN", "")
	err := doRun(context.Background(), okFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}

func TestDoRun_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("bad endpoint")
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoRun_OnCallCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSeams(t, seamOverrides{
		oncall:    func(_ context.Context, _ oncall.API) ([]oncall.Policy, error) { return nil, sentinel },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "pagerduty oncall collect: ") {
		t.Fatalf("want wrapped oncall collect error; got %v", err)
	}
}

func TestDoRun_IncidentsCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("429")
	installSeams(t, seamOverrides{
		oncall: func(_ context.Context, _ oncall.API) ([]oncall.Policy, error) { return twoPolicies(), nil },
		incidents: func(_ context.Context, _ incidents.API, _, _ time.Time) ([]incidents.Incident, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "pagerduty incidents collect: ") {
		t.Fatalf("want wrapped incidents collect error; got %v", err)
	}
}

func TestDoRun_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		oncall:    func(_ context.Context, _ oncall.API) ([]oncall.Policy, error) { return twoPolicies(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push oncall ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestDoRun_IncidentLookbackWindow(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	prev := nowFn
	fixed := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return fixed }
	t.Cleanup(func() { nowFn = prev })

	var gotSince, gotUntil time.Time
	installSeams(t, seamOverrides{
		oncall: func(_ context.Context, _ oncall.API) ([]oncall.Policy, error) { return nil, nil },
		incidents: func(_ context.Context, _ incidents.API, since, until time.Time) ([]incidents.Incident, error) {
			gotSince, gotUntil = since, until
			return nil, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	f := okFlags()
	f.lookbackDays = 30
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if !gotUntil.Equal(fixed) {
		t.Errorf("until = %v; want %v", gotUntil, fixed)
	}
	if !gotSince.Equal(fixed.AddDate(0, 0, -30)) {
		t.Errorf("since = %v; want fixed-30d", gotSince)
	}
}

func TestNewAPIConstructors(t *testing.T) {
	if newOnCallAPI(nil, "https://api.pagerduty.com", "test-pagerduty-token") == nil {
		t.Error("newOnCallAPI returned nil")
	}
	if newIncidentsAPI(nil, "https://api.pagerduty.com", "test-pagerduty-token") == nil {
		t.Error("newIncidentsAPI returned nil")
	}
}
