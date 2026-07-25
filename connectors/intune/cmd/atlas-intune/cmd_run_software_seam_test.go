// Seam tests for doRunSoftware. The Graph software read + the sdk client
// constructor are swapped for fakes so doRunSoftware is exercised end-to-end
// without touching live Graph or a real platform. Seams are restored via
// t.Cleanup. No real Graph credentials — neutral "test-*" / "fake-*" only.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
)

func installSoftwareSeams(t *testing.T,
	collect func(ctx context.Context, api devices.SoftwareAPI) ([]swinventory.RawDeviceSoftware, error),
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error),
) {
	t.Helper()
	if collect != nil {
		prev := softwareCollect
		softwareCollect = collect
		t.Cleanup(func() { softwareCollect = prev })
	}
	if newClient != nil {
		prev := newSDKClient
		newSDKClient = newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func twoDevicesSoftware() []swinventory.RawDeviceSoftware {
	return []swinventory.RawDeviceSoftware{
		{DeviceID: "1", Software: []swinventory.RawSoftwareItem{{Name: "Chrome", Version: "125"}}},
		{DeviceID: "2", Software: []swinventory.RawSoftwareItem{{Name: "Edge"}}},
	}
}

func okSoftwareFlags() runSoftwareFlags {
	return runSoftwareFlags{environment: "prod", softwareControl: "scf:VPM-04"}
}

func TestDoRunSoftware_PushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSoftwareSeams(t,
		func(_ context.Context, _ devices.SoftwareAPI) ([]swinventory.RawDeviceSoftware, error) {
			return twoDevicesSoftware(), nil
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	)
	if err := doRunSoftware(context.Background(), okSoftwareFlags()); err != nil {
		t.Fatalf("doRunSoftware: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRunSoftware_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("INTUNE_TENANT_ID", "")
	t.Setenv("INTUNE_CLIENT_ID", "")
	t.Setenv("INTUNE_CLIENT_SECRET", "")
	err := doRunSoftware(context.Background(), okSoftwareFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}

func TestDoRunSoftware_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("bad endpoint")
	installSoftwareSeams(t, nil,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
	)
	err := doRunSoftware(context.Background(), okSoftwareFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoRunSoftware_CollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installSoftwareSeams(t,
		func(_ context.Context, _ devices.SoftwareAPI) ([]swinventory.RawDeviceSoftware, error) {
			return nil, sentinel
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	)
	err := doRunSoftware(context.Background(), okSoftwareFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "intune software collect: ") {
		t.Fatalf("want wrapped collect error; got %v", err)
	}
}

func TestDoRunSoftware_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSoftwareSeams(t,
		func(_ context.Context, _ devices.SoftwareAPI) ([]swinventory.RawDeviceSoftware, error) {
			return twoDevicesSoftware(), nil
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	)
	err := doRunSoftware(context.Background(), okSoftwareFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push software ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewSoftwareAPI_Constructor(t *testing.T) {
	if newSoftwareAPI(devices.ClientConfig{GraphBaseURL: "https://graph.microsoft.com/v1.0"}) == nil {
		t.Error("newSoftwareAPI returned nil")
	}
}
