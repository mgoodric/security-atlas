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

	"github.com/mgoodric/security-atlas/connectors/grafana/internal/alertrules"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/ssoconfig"
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
	collect    func(ctx context.Context, api alertrules.API) ([]alertcfg.RawRule, error)
	ssoCollect func(ctx context.Context, api ssoconfig.API, now func() time.Time) (ssoconfig.AccessConfig, error)
	newClient  func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// defaultSSOCollect returns a fixed access-config so tests that only exercise
// the alert-rule path do not hit live Grafana for the access-config pass.
func defaultSSOCollect(_ context.Context, _ ssoconfig.API, _ func() time.Time) (ssoconfig.AccessConfig, error) {
	return ssoconfig.AccessConfig{SSOEnabled: true, TeamCount: 1}, nil
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
	return runFlags{environment: "prod", ruleControl: "scf:MON-01", accessControl: "scf:IAC-06"}
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
