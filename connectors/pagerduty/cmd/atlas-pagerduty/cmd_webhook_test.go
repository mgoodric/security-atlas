// Seam tests for the `webhook` (subscribe) subcommand. The sdk client
// constructor and the blocking Serve loop are swapped for fakes so doWebhook is
// exercised end-to-end without binding a real socket or hitting a real platform.
// Seams restored via t.Cleanup.
//
// No real PagerDuty secrets in fixtures — neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/webhook"
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
	return webhookFlags{environment: "prod", service: "pagerduty", incidentControl: "scf:IRO-02", listen: "127.0.0.1:0", path: "/webhooks/pagerduty"}
}

func TestDoWebhook_Success(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("PAGERDUTY_WEBHOOK_SECRET", "test-webhook-secret")

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

func TestDoWebhook_MissingSecret(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("PAGERDUTY_WEBHOOK_SECRET", "")
	err := doWebhook(context.Background(), okWebhookFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error on missing secret; got %v", err)
	}
}

func TestDoWebhook_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("PAGERDUTY_WEBHOOK_SECRET", "test-webhook-secret")
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
	t.Setenv("PAGERDUTY_WEBHOOK_SECRET", "test-webhook-secret")
	sentinel := errors.New("listen failed")
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

func TestNewWebhookCmd_PreRunResolveCommonFails(t *testing.T) {
	resetCommon(t)
	t.Setenv("SECURITY_ATLAS_ENDPOINT", "")
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	cmd := newWebhookCmd()
	if err := cmd.ParseFlags([]string{"--environment", "prod"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err == nil {
		t.Fatal("expected resolveCommon error to bubble up")
	}
}

func TestNewWebhookCmd_HasFlagsAndHonestProfile(t *testing.T) {
	cmd := newWebhookCmd()
	for _, want := range []string{"environment", "service", "incident-control", "listen", "path"} {
		if cmd.Flags().Lookup(want) == nil {
			t.Errorf("webhook flag %q missing", want)
		}
	}
	// Loopback default bind.
	if got, _ := cmd.Flags().GetString("listen"); got != "127.0.0.1:8474" {
		t.Errorf("default listen = %q, want loopback 127.0.0.1:8474", got)
	}
	// Honest profile naming: the long help must say subscribe + NOT continuous.
	if !strings.Contains(cmd.Long, "subscribe") {
		t.Error("webhook help must name the subscribe profile")
	}
	if strings.Contains(cmd.Long, "continuous") && !strings.Contains(cmd.Long, "NOT continuous") {
		t.Error("webhook help must not claim continuous monitoring")
	}
}

func TestRoot_HasWebhookSubcommand(t *testing.T) {
	resetCommon(t)
	root := newRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "webhook" {
			found = true
		}
	}
	if !found {
		t.Error("root missing webhook subcommand")
	}
}

func TestProfilesSupported_HonestPullAndSubscribe(t *testing.T) {
	if len(ProfilesSupported) != 2 {
		t.Fatalf("ProfilesSupported = %v, want [pull subscribe]", ProfilesSupported)
	}
	got := map[string]bool{}
	for _, p := range ProfilesSupported {
		got[p] = true
	}
	if !got["pull"] || !got["subscribe"] {
		t.Errorf("ProfilesSupported = %v, want pull + subscribe", ProfilesSupported)
	}
}

func TestPushAdapter_DelegatesToClient(t *testing.T) {
	fake := &fakeSDKClient{}
	a := pushAdapter{c: fake}
	if _, err := a.Push(context.Background(), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if fake.pushed != 1 {
		t.Errorf("delegated push count = %d, want 1", fake.pushed)
	}
}

func TestSignalContext_NotNil(t *testing.T) {
	if signalContext() == nil {
		t.Fatal("signalContext returned nil")
	}
}

// compile-time check the receiver Pusher binding holds.
var _ webhook.Pusher = pushAdapter{}
