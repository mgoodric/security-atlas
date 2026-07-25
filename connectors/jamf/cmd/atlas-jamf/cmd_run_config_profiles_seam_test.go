// Seam tests for doRunConfigProfiles. The Jamf config-profile read + the sdk
// client constructor are swapped for fakes so doRunConfigProfiles is exercised
// end-to-end without touching live Jamf or a real platform. Seams are restored
// via t.Cleanup. No real Jamf credentials — neutral "test-*" / "fake-*" only.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/jamf/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
)

func installConfigProfileSeams(t *testing.T,
	collect func(ctx context.Context, api devices.ConfigProfileAPI) ([]cfgprofile.RawDeviceProfiles, error),
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error),
) {
	t.Helper()
	if collect != nil {
		prev := configProfileCollect
		configProfileCollect = collect
		t.Cleanup(func() { configProfileCollect = prev })
	}
	if newClient != nil {
		prev := newSDKClient
		newSDKClient = newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func twoDevicesConfigProfiles() []cfgprofile.RawDeviceProfiles {
	return []cfgprofile.RawDeviceProfiles{
		{DeviceID: "1", Profiles: []cfgprofile.RawProfile{{Name: "Passcode", Settings: []cfgprofile.RawSetting{{Key: "passcode_required", Value: "true"}}}}},
		{DeviceID: "2", Profiles: []cfgprofile.RawProfile{{Name: "FileVault"}}},
	}
}

func okConfigProfileFlags() runConfigProfilesFlags {
	return runConfigProfilesFlags{environment: "prod", configControl: "scf:CFG-02"}
}

func TestDoRunConfigProfiles_PushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installConfigProfileSeams(t,
		func(_ context.Context, _ devices.ConfigProfileAPI) ([]cfgprofile.RawDeviceProfiles, error) {
			return twoDevicesConfigProfiles(), nil
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	)
	if err := doRunConfigProfiles(context.Background(), okConfigProfileFlags()); err != nil {
		t.Fatalf("doRunConfigProfiles: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRunConfigProfiles_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JAMF_BASE_URL", "")
	t.Setenv("JAMF_CLIENT_ID", "")
	t.Setenv("JAMF_CLIENT_SECRET", "")
	err := doRunConfigProfiles(context.Background(), okConfigProfileFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}

func TestDoRunConfigProfiles_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("bad endpoint")
	installConfigProfileSeams(t, nil,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
	)
	err := doRunConfigProfiles(context.Background(), okConfigProfileFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoRunConfigProfiles_CollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("403")
	installConfigProfileSeams(t,
		func(_ context.Context, _ devices.ConfigProfileAPI) ([]cfgprofile.RawDeviceProfiles, error) {
			return nil, sentinel
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	)
	err := doRunConfigProfiles(context.Background(), okConfigProfileFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "jamf config-profile collect: ") {
		t.Fatalf("want wrapped collect error; got %v", err)
	}
}

func TestDoRunConfigProfiles_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installConfigProfileSeams(t,
		func(_ context.Context, _ devices.ConfigProfileAPI) ([]cfgprofile.RawDeviceProfiles, error) {
			return twoDevicesConfigProfiles(), nil
		},
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	)
	err := doRunConfigProfiles(context.Background(), okConfigProfileFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push config-profile ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewConfigProfileAPI_Constructor(t *testing.T) {
	if newConfigProfileAPI(http.DefaultClient, "https://org.jamfcloud.com", "test-jamf-client", "fake-jamf-secret") == nil {
		t.Error("newConfigProfileAPI returned nil")
	}
}
