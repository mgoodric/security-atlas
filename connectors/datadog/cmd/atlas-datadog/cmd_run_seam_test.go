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

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/monitors"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemrules"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
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
	collect     func(ctx context.Context, api monitors.API) ([]alertcfg.RawRule, error)
	siemCollect func(ctx context.Context, api siemrules.API, now func() time.Time) ([]siemrules.Rule, error)
	newClient   func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// noSIEM is the default SIEM seam used by tests that only exercise the monitor
// path: it returns no rules so doRun's SIEM pass is a no-op (never touches a
// live Datadog security-monitoring endpoint).
func noSIEM(_ context.Context, _ siemrules.API, _ func() time.Time) ([]siemrules.Rule, error) {
	return nil, nil
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.collect != nil {
		prev := monitorsCollect
		monitorsCollect = o.collect
		t.Cleanup(func() { monitorsCollect = prev })
	}
	// Always stub the SIEM collector unless a test overrides it, so the monitor
	// path tests never reach a live security-monitoring API.
	prevSIEM := siemrulesCollect
	if o.siemCollect != nil {
		siemrulesCollect = o.siemCollect
	} else {
		siemrulesCollect = noSIEM
	}
	t.Cleanup(func() { siemrulesCollect = prevSIEM })
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
	return runFlags{environment: "prod", monitorControl: "scf:MON-01", siemControl: "scf:THR-01"}
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
