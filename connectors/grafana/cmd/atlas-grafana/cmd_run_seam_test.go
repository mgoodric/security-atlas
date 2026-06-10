// Seam tests for doRun. The Grafana read + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching live
// Grafana or a real platform. Seams are restored via t.Cleanup.
//
// No real Grafana tokens in fixtures — neutral "test-*" strings only.
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

	"github.com/mgoodric/security-atlas/connectors/grafana/internal/alerthistory"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/alertrules"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/ssoconfig"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

type fakeSDKClient struct {
	pushErr error
	// failAfter, when > 0, lets the first failAfter pushes succeed and fails the
	// next one with pushErr — used to isolate a downstream-pass push error from
	// an earlier always-pushing pass (e.g. the access-config record).
	failAfter   int
	pushed      int
	closeCalled bool
}

func (f *fakeSDKClient) Push(_ context.Context, _ *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.pushErr != nil {
		if f.failAfter <= 0 || f.pushed >= f.failAfter {
			return nil, f.pushErr
		}
	}
	f.pushed++
	return &evidencev1.EvidenceReceipt{}, nil
}

func (f *fakeSDKClient) Close() error { f.closeCalled = true; return nil }

type seamOverrides struct {
	collect       func(ctx context.Context, api alertrules.API) ([]alertcfg.RawRule, error)
	ssoCollect    func(ctx context.Context, api ssoconfig.API, now func() time.Time) (ssoconfig.AccessConfig, error)
	firingCollect func(ctx context.Context, api alerthistory.API, lookback time.Duration, now func() time.Time) ([]firing.Firing, error)
	newClient     func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// defaultSSOCollect returns a fixed access-config so tests that only exercise
// the alert-rule path do not hit live Grafana for the access-config pass.
func defaultSSOCollect(_ context.Context, _ ssoconfig.API, _ func() time.Time) (ssoconfig.AccessConfig, error) {
	return ssoconfig.AccessConfig{SSOEnabled: true, TeamCount: 1}, nil
}

// noFiring is the default firing-history seam: returns no firings so doRun's
// firing pass is a no-op (never touches a live state-history endpoint).
func noFiring(_ context.Context, _ alerthistory.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
	return nil, nil
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.collect != nil {
		prev := alertRulesCollect
		alertRulesCollect = o.collect
		t.Cleanup(func() { alertRulesCollect = prev })
	}
	// Always seam the SSO collector so doRun's access-config pass never touches
	// live Grafana; tests may override it explicitly.
	prevSSO := ssoConfigCollect
	if o.ssoCollect != nil {
		ssoConfigCollect = o.ssoCollect
	} else {
		ssoConfigCollect = defaultSSOCollect
	}
	t.Cleanup(func() { ssoConfigCollect = prevSSO })
	// Always seam the firing collector so doRun's firing pass never touches live
	// Grafana; tests may override it explicitly.
	prevFiring := alertHistoryCollect
	if o.firingCollect != nil {
		alertHistoryCollect = o.firingCollect
	} else {
		alertHistoryCollect = noFiring
	}
	t.Cleanup(func() { alertHistoryCollect = prevFiring })
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GRAFANA_URL", "https://grafana.example.com")
	t.Setenv("GRAFANA_TOKEN", "test-grafana-token")
}

func okFlags() runFlags {
	return runFlags{
		environment:    "prod",
		ruleControl:    "scf:MON-01",
		accessControl:  "scf:IAC-06",
		firingControl:  "scf:IRO-09",
		firingLookback: 24 * time.Hour,
	}
}

func twoFirings() []firing.Firing {
	fired := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	return []firing.Firing{
		{SourceVendor: firing.VendorGrafana, RuleID: "u1", State: firing.StateAlerting, FiredAt: fired, ObservedAt: fired},
		{SourceVendor: firing.VendorGrafana, RuleID: "u2", State: firing.StateResolved, FiredAt: fired, ResolvedAt: fired.Add(time.Hour), ObservedAt: fired},
	}
}

func twoRules() []alertcfg.RawRule {
	return []alertcfg.RawRule{
		{ID: "r1", Name: "High latency", Type: "grafana", Enabled: true, Targets: []alertcfg.Target{{Kind: "slack", Name: "sec-oncall"}}},
		{ID: "r2", Name: "Disk full", Type: "grafana", Enabled: false},
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
		collect:   func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return twoRules(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	// 2 alert-rule records + 1 access-config record.
	if fake.pushed != 3 {
		t.Errorf("pushed = %d; want 3 (2 alert rules + 1 access config)", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRun_AccessConfigCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403 sso-settings")
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return nil, nil },
		ssoCollect: func(_ context.Context, _ ssoconfig.API, _ func() time.Time) (ssoconfig.AccessConfig, error) {
			return ssoconfig.AccessConfig{}, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "grafana access-config collect: ") {
		t.Fatalf("want wrapped access-config collect error; got %v", err)
	}
}

func TestNewSSOConfigAPI_Constructor(t *testing.T) {
	if newSSOConfigAPI(http.DefaultClient, "https://grafana.example.com", "test-grafana-token") == nil {
		t.Error("newSSOConfigAPI returned nil")
	}
}

func TestDoRun_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("GRAFANA_URL", "")
	t.Setenv("GRAFANA_TOKEN", "")
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

func TestDoRun_CollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSeams(t, seamOverrides{
		collect:   func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return nil, sentinel },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "grafana collect: ") {
		t.Fatalf("want wrapped collect error; got %v", err)
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
		collect:   func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return twoRules(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push rule ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewAlertRulesAPI_Constructor(t *testing.T) {
	if newAlertRulesAPI(http.DefaultClient, "https://grafana.example.com", "test-grafana-token") == nil {
		t.Error("newAlertRulesAPI returned nil")
	}
}

func TestNewAlertHistoryAPI_Constructor(t *testing.T) {
	if newAlertHistoryAPI(http.DefaultClient, "https://grafana.example.com", "test-grafana-token") == nil {
		t.Error("newAlertHistoryAPI returned nil")
	}
}

// TestDoRun_PushesAllThreeKinds verifies one run pushes alert-rule +
// access-config + alert-firing records through the single Push RPC (invariant #3).
func TestDoRun_PushesAllThreeKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return twoRules(), nil },
		firingCollect: func(_ context.Context, _ alerthistory.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return twoFirings(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	// 2 alert-rule + 1 access-config + 2 firing.
	if fake.pushed != 5 {
		t.Errorf("pushed = %d; want 5 (2 alert rules + 1 access config + 2 firings)", fake.pushed)
	}
}

func TestDoRun_FiringLookbackThreaded(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	var gotLookback time.Duration
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ alerthistory.API, lookback time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			gotLookback = lookback
			return nil, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	f := okFlags()
	f.firingLookback = 8 * time.Hour
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if gotLookback != 8*time.Hour {
		t.Errorf("lookback = %v; want 8h threaded through", gotLookback)
	}
}

func TestDoRun_FiringCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("401 state-history")
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ alerthistory.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "grafana firing collect: ") {
		t.Fatalf("want wrapped firing collect error; got %v", err)
	}
}

func TestDoRun_FiringPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	// rules=0 pushes; access-config=1 push (succeeds, failAfter=1); firing push
	// is the 2nd push and fails — isolating the firing push error path.
	fake := &fakeSDKClient{pushErr: sentinel, failAfter: 1}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ alertrules.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ alerthistory.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return twoFirings(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "push firing ") {
		t.Fatalf("want wrapped firing push error; got %v", err)
	}
}
