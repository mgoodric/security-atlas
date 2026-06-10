// Seam tests for the `webhook` (subscribe) subcommand. The sdk client constructor
// and the blocking Serve loop are swapped for fakes so doWebhook is exercised
// end-to-end without binding a real socket or hitting a real platform. Seams
// restored via t.Cleanup.
//
// No real Jamf secrets in fixtures — neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/mdm/mdmwebhook"
)

func installWebhookSeams(t *testing.T, newClient func(string, string, ...sdk.Option) (sdkPushClient, error), serve func(context.Context, *http.Server) error) {
	t.Helper()
	if newClient != nil {
		prev := newWebhookSDKClient
		newWebhookSDKClient = newClient
		t.Cleanup(func() { newWebhookSDKClient = prev })
	}
	if serve != nil {
		prev := webhookServe
		webhookServe = serve
		t.Cleanup(func() { webhookServe = prev })
	}
}

func okWebhookFlags() webhookFlags {
	return webhookFlags{environment: "prod", deviceControl: "scf:END-04", listen: "127.0.0.1:0", path: "/webhooks/jamf"}
}

func TestDoWebhook_Success(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JAMF_WEBHOOK_SECRET", "test-jamf-webhook-secret")

	fake := &fakeSDKClient{}
	var served bool
	installWebhookSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		func(_ context.Context, _ *http.Server) error { served = true; return nil },
	)
	if err := doWebhook(context.Background(), okWebhookFlags()); err != nil {
		t.Fatalf("doWebhook: %v", err)
	}
	if !served {
		t.Error("Serve seam not invoked")
	}
	if !fake.closeCalled {
		t.Error("sdk client Close not called")
	}
}

func TestDoWebhook_AuthMissing(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JAMF_WEBHOOK_SECRET", "")
	err := doWebhook(context.Background(), okWebhookFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}

func TestDoWebhook_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JAMF_WEBHOOK_SECRET", "test-jamf-webhook-secret")
	sentinel := errors.New("bad endpoint")
	installWebhookSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
		nil,
	)
	err := doWebhook(context.Background(), okWebhookFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoWebhook_ServeError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("JAMF_WEBHOOK_SECRET", "test-jamf-webhook-secret")
	sentinel := errors.New("bind failed")
	installWebhookSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
		func(_ context.Context, _ *http.Server) error { return sentinel },
	)
	err := doWebhook(context.Background(), okWebhookFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "serve: ") {
		t.Fatalf("want wrapped serve error; got %v", err)
	}
}

func TestNewWebhookCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newWebhookCmd()
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

// The webhook subcommand exists and advertises subscribe via ProfilesSupported.
func TestProfilesSupported_PullAndSubscribe(t *testing.T) {
	want := map[string]bool{"pull": true, "subscribe": true}
	if len(ProfilesSupported) != len(want) {
		t.Fatalf("ProfilesSupported = %v; want pull+subscribe", ProfilesSupported)
	}
	for _, p := range ProfilesSupported {
		if !want[p] {
			t.Errorf("unexpected profile %q", p)
		}
	}
	for _, p := range ProfilesSupported {
		if strings.Contains(strings.ToLower(p), "continuous") {
			t.Errorf("profile %q must not claim continuous monitoring", p)
		}
	}
}

// pushAdapter forwards to the underlying client (compile + behavior smoke).
func TestPushAdapter_Forwards(t *testing.T) {
	fake := &fakeSDKClient{}
	var _ mdmwebhook.Pusher = pushAdapter{fake}
	if _, err := (pushAdapter{fake}).Push(context.Background(), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if fake.pushed != 1 {
		t.Errorf("pushed = %d, want 1", fake.pushed)
	}
}
