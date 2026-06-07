// Seam tests for doRun. The Intune read + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching live
// Graph or a real platform. Seams are restored via t.Cleanup.
//
// No real Intune credentials in fixtures — neutral "test-*" / "fake-*" strings only.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
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
	collect   func(ctx context.Context, api devices.API) ([]devposture.RawDevice, error)
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.collect != nil {
		prev := devicesCollect
		devicesCollect = o.collect
		t.Cleanup(func() { devicesCollect = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("INTUNE_TENANT_ID", "test-tenant")
	t.Setenv("INTUNE_CLIENT_ID", "test-intune-client")
	t.Setenv("INTUNE_CLIENT_SECRET", "fake-graph-secret")
}

func okFlags() runFlags {
	return runFlags{environment: "prod", deviceControl: "scf:END-04"}
}

func twoDevices() []devposture.RawDevice {
	return []devposture.RawDevice{
		{DeviceID: "1", DiskEncryptionEnabled: true, ScreenLockEnabled: true, Managed: true, Enrolled: true},
		{DeviceID: "2", DiskEncryptionEnabled: false},
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
		collect:   func(_ context.Context, _ devices.API) ([]devposture.RawDevice, error) { return twoDevices(), nil },
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
	t.Setenv("INTUNE_TENANT_ID", "")
	t.Setenv("INTUNE_CLIENT_ID", "")
	t.Setenv("INTUNE_CLIENT_SECRET", "")
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
		collect:   func(_ context.Context, _ devices.API) ([]devposture.RawDevice, error) { return nil, sentinel },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "intune collect: ") {
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
		collect:   func(_ context.Context, _ devices.API) ([]devposture.RawDevice, error) { return twoDevices(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push device ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewDevicesAPI_Constructor(t *testing.T) {
	api := newDevicesAPI(devices.ClientConfig{GraphBaseURL: "https://graph.microsoft.com/v1.0", ClientID: "test-intune-client", ClientSecret: "fake-graph-secret"})
	if api == nil {
		t.Error("newDevicesAPI returned nil")
	}
}
