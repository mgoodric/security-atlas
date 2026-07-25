// Seam tests for doRun. The Datadog read + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching live
// Datadog or a real platform. Seams are restored via t.Cleanup.
//
// No real Datadog keys in fixtures — neutral "test-*" strings only.
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

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/firingevents"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/monitors"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemrules"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemsignals"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
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
	collect       func(ctx context.Context, api monitors.API) ([]alertcfg.RawRule, error)
	siemCollect   func(ctx context.Context, api siemrules.API, now func() time.Time) ([]siemrules.Rule, error)
	signalCollect func(ctx context.Context, api siemsignals.API, lookback time.Duration, now func() time.Time) ([]siemsignals.Signal, error)
	firingCollect func(ctx context.Context, api firingevents.API, lookback time.Duration, now func() time.Time) ([]firing.Firing, error)
	newClient     func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// noSIEM is the default SIEM-rule seam used by tests that only exercise the
// monitor path: it returns no rules so doRun's SIEM-rule pass is a no-op (never
// touches a live Datadog security-monitoring endpoint).
func noSIEM(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
	return nil, nil
}

// noSignals is the default signal-history seam: returns no signals so doRun's
// signal pass is a no-op (never touches a live security-signals endpoint).
func noSignals(_ context.Context, _ siemsignals.API, _ time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
	return nil, nil
}

// noFiring is the default firing-history seam: returns no firings so doRun's
// firing pass is a no-op (never touches a live Events endpoint).
func noFiring(_ context.Context, _ firingevents.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
	return nil, nil
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.collect != nil {
		prev := monitorsCollect
		monitorsCollect = o.collect
		t.Cleanup(func() { monitorsCollect = prev })
	}
	// Always stub the SIEM-rule collector unless a test overrides it, so the
	// monitor path tests never reach a live security-monitoring API.
	prevSIEM := siemrulesCollect
	if o.siemCollect != nil {
		siemrulesCollect = o.siemCollect
	} else {
		siemrulesCollect = noSIEM
	}
	t.Cleanup(func() { siemrulesCollect = prevSIEM })
	// Always stub the signal-history collector unless a test overrides it.
	prevSignals := siemSignalsCollect
	if o.signalCollect != nil {
		siemSignalsCollect = o.signalCollect
	} else {
		siemSignalsCollect = noSignals
	}
	t.Cleanup(func() { siemSignalsCollect = prevSignals })
	// Always stub the firing-history collector unless a test overrides it.
	prevFiring := firingCollect
	if o.firingCollect != nil {
		firingCollect = o.firingCollect
	} else {
		firingCollect = noFiring
	}
	t.Cleanup(func() { firingCollect = prevFiring })
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATADOG_API_KEY", "test-datadog-api-key")
	t.Setenv("DATADOG_APP_KEY", "test-datadog-app-key")
}

func okFlags() runFlags {
	return runFlags{
		environment:       "prod",
		monitorControl:    "scf:MON-01",
		siemControl:       "scf:THR-01",
		siemSignalControl: "scf:IRO-09",
		firingControl:     "scf:IRO-09",
		siemLookback:      24 * time.Hour,
		firingLookback:    24 * time.Hour,
	}
}

func twoFirings() []firing.Firing {
	fired := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	return []firing.Firing{
		{SourceVendor: firing.VendorDatadog, RuleID: "1", State: firing.StateAlerting, FiredAt: fired, ObservedAt: fired},
		{SourceVendor: firing.VendorDatadog, RuleID: "2", State: firing.StateResolved, FiredAt: fired, ResolvedAt: fired.Add(time.Hour), ObservedAt: fired},
	}
}

func twoSignals() []siemsignals.Signal {
	ts := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	return []siemsignals.Signal{
		{SignalID: "sig-1", RuleID: "rule-a", RuleName: "Brute force", Severity: "high", Status: "archived", TriagedAt: ts, ObservedAt: ts},
		{SignalID: "sig-2", RuleID: "rule-b", Severity: "medium", Status: "open", ObservedAt: ts},
	}
}

func twoSIEMRules() []siemrules.Rule {
	return []siemrules.Rule{
		{RuleID: "s1", RuleName: "Brute force", DetectionClass: "log", Enabled: true, Severity: "high",
			Targets: []siemrules.Target{{Kind: "slack", Name: "slack-sec"}}},
		{RuleID: "s2", RuleName: "Impossible travel", DetectionClass: "threshold", Enabled: false, Severity: "medium"},
	}
}

func twoMonitors() []alertcfg.RawRule {
	return []alertcfg.RawRule{
		{ID: "1", Name: "m1", Type: "metric alert", Enabled: true, Targets: []alertcfg.Target{{Kind: "slack", Name: "slack-x"}}},
		{ID: "2", Name: "m2", Type: "log alert", Enabled: false},
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
		collect:   func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return twoMonitors(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
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
	t.Setenv("DATADOG_API_KEY", "")
	t.Setenv("DATADOG_APP_KEY", "")
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
		collect:   func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, sentinel },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "datadog collect: ") {
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
		collect:   func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return twoMonitors(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push monitor ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewMonitorsAPI_Constructor(t *testing.T) {
	if newMonitorsAPI(http.DefaultClient, "https://api.datadoghq.com", "test-datadog-api-key", "test-datadog-app-key") == nil {
		t.Error("newMonitorsAPI returned nil")
	}
}

func TestNewSIEMRulesAPI_Constructor(t *testing.T) {
	if newSIEMRulesAPI(http.DefaultClient, "https://api.datadoghq.com", "test-datadog-api-key", "test-datadog-app-key") == nil {
		t.Error("newSIEMRulesAPI returned nil")
	}
}

// TestDoRun_PushesBothKinds verifies one run pushes both monitor and SIEM-rule
// records through the single Push RPC (invariant #3).
func TestDoRun_PushesBothKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return twoMonitors(), nil },
		siemCollect: func(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
			return twoSIEMRules(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 4 {
		t.Errorf("pushed = %d; want 4 (2 monitors + 2 siem rules)", fake.pushed)
	}
}

func TestDoRun_SIEMCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		siemCollect: func(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "datadog siem collect: ") {
		t.Fatalf("want wrapped siem collect error; got %v", err)
	}
}

func TestDoRun_SIEMPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		siemCollect: func(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
			return twoSIEMRules(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "push siem rule ") {
		t.Fatalf("want wrapped siem push error; got %v", err)
	}
}

func TestNewSIEMSignalsAPI_Constructor(t *testing.T) {
	if newSIEMSignalsAPI(http.DefaultClient, "https://api.datadoghq.com", "test-datadog-api-key", "test-datadog-app-key") == nil {
		t.Error("newSIEMSignalsAPI returned nil")
	}
}

// TestDoRun_PushesAllThreeKinds verifies one run pushes monitor + SIEM-rule +
// SIEM-signal records through the single Push RPC (invariant #3).
func TestDoRun_PushesAllThreeKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return twoMonitors(), nil },
		siemCollect: func(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
			return twoSIEMRules(), nil
		},
		signalCollect: func(_ context.Context, _ siemsignals.API, _ time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
			return twoSignals(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 6 {
		t.Errorf("pushed = %d; want 6 (2 monitors + 2 siem rules + 2 siem signals)", fake.pushed)
	}
}

// TestDoRun_SignalLookbackThreaded asserts the --siem-lookback flag value
// reaches the signal collector unchanged.
func TestDoRun_SignalLookbackThreaded(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	var gotLookback time.Duration
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		signalCollect: func(_ context.Context, _ siemsignals.API, lookback time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
			gotLookback = lookback
			return nil, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	f := okFlags()
	f.siemLookback = 6 * time.Hour
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if gotLookback != 6*time.Hour {
		t.Errorf("lookback = %v; want 6h threaded through", gotLookback)
	}
}

func TestDoRun_SignalCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		signalCollect: func(_ context.Context, _ siemsignals.API, _ time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "datadog siem signal collect: ") {
		t.Fatalf("want wrapped siem signal collect error; got %v", err)
	}
}

func TestDoRun_SignalPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		signalCollect: func(_ context.Context, _ siemsignals.API, _ time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
			return twoSignals(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "push siem signal ") {
		t.Fatalf("want wrapped siem signal push error; got %v", err)
	}
}

func TestNewFiringAPI_Constructor(t *testing.T) {
	if newFiringAPI(http.DefaultClient, "https://api.datadoghq.com", "test-datadog-api-key", "test-datadog-app-key") == nil {
		t.Error("newFiringAPI returned nil")
	}
}

// TestDoRun_PushesAllFourKinds verifies one run pushes monitor + SIEM-rule +
// SIEM-signal + alert-firing records through the single Push RPC (invariant #3).
func TestDoRun_PushesAllFourKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return twoMonitors(), nil },
		siemCollect: func(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
			return twoSIEMRules(), nil
		},
		signalCollect: func(_ context.Context, _ siemsignals.API, _ time.Duration, _ func() time.Time) ([]siemsignals.Signal, error) {
			return twoSignals(), nil
		},
		firingCollect: func(_ context.Context, _ firingevents.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return twoFirings(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 8 {
		t.Errorf("pushed = %d; want 8 (2 monitors + 2 siem rules + 2 siem signals + 2 firings)", fake.pushed)
	}
}

// TestDoRun_FiringLookbackThreaded asserts the --datadog-firing-lookback flag
// value reaches the firing collector unchanged.
func TestDoRun_FiringLookbackThreaded(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	var gotLookback time.Duration
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ firingevents.API, lookback time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			gotLookback = lookback
			return nil, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	f := okFlags()
	f.firingLookback = 12 * time.Hour
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if gotLookback != 12*time.Hour {
		t.Errorf("lookback = %v; want 12h threaded through", gotLookback)
	}
}

func TestDoRun_FiringCollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ firingevents.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "datadog firing collect: ") {
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
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		collect: func(_ context.Context, _ monitors.API) ([]alertcfg.RawRule, error) { return nil, nil },
		firingCollect: func(_ context.Context, _ firingevents.API, _ time.Duration, _ func() time.Time) ([]firing.Firing, error) {
			return twoFirings(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "push firing ") {
		t.Fatalf("want wrapped firing push error; got %v", err)
	}
}
