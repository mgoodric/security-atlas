// Seam tests for the provision / deprovision subcommands. The ARM management
// client + token acquisition are swapped for fakes so the flow is exercised
// end-to-end without touching live Azure. No real Azure secrets / tenant ids —
// neutral test strings + the all-zero GUID only.
package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/eventgrid"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/provision"
)

// fakeProvisionAPI records the calls so tests can assert what was provisioned /
// torn down without an ARM round-trip.
type fakeProvisionAPI struct {
	calls []string
}

func (f *fakeProvisionAPI) PutSystemTopic(context.Context, provision.SystemTopic) error {
	f.calls = append(f.calls, "put-topic")
	return nil
}
func (f *fakeProvisionAPI) PutEventSubscription(context.Context, provision.EventSubscription) error {
	f.calls = append(f.calls, "put-sub")
	return nil
}
func (f *fakeProvisionAPI) PutDiagnosticSetting(context.Context, provision.DiagnosticSetting) error {
	f.calls = append(f.calls, "put-diag")
	return nil
}
func (f *fakeProvisionAPI) DeleteEventSubscription(context.Context, provision.EventSubscription) error {
	f.calls = append(f.calls, "del-sub")
	return nil
}
func (f *fakeProvisionAPI) DeleteSystemTopic(context.Context, provision.SystemTopic) error {
	f.calls = append(f.calls, "del-topic")
	return nil
}
func (f *fakeProvisionAPI) DeleteDiagnosticSetting(context.Context, provision.DiagnosticSetting) error {
	f.calls = append(f.calls, "del-diag")
	return nil
}

// installProvisionSeams swaps the token acquisition + ARM API constructor and
// records the credential the token acquisition was called with (so a test can
// assert credential SEPARATION — the elevated cred, not the receiver's).
func installProvisionSeams(t *testing.T, api provision.API) *azureauth.Credential {
	t.Helper()
	var seen azureauth.Credential
	prevTok := provisionAcquireToken
	provisionAcquireToken = func(_ context.Context, cred azureauth.Credential, _ *http.Client, scope string) (string, error) {
		seen = cred
		if scope != provision.ARMWriteScope {
			t.Errorf("provision token scope = %q; want ARM write scope", scope)
		}
		return "test-elevated-token", nil
	}
	t.Cleanup(func() { provisionAcquireToken = prevTok })

	prevAPI := newProvisionAPI
	newProvisionAPI = func(_ *http.Client, _, token string) provision.API {
		if token != "test-elevated-token" {
			t.Errorf("provision API built with token %q; want the elevated token", token)
		}
		return api
	}
	t.Cleanup(func() { newProvisionAPI = prevAPI })
	return &seen
}

// elevatedEnv sets ONLY the elevated provisioning credential env vars +
// delivery key. It deliberately CLEARS the receiver's read-only AZURE_* vars so
// a test proves provision does NOT fall back to the receiver credential.
func elevatedEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv(provisionTenantEnv, "elevated-tenant")
	t.Setenv(provisionClientEnv, "elevated-client")
	t.Setenv(provisionSecretEnv, "test-elevated-secret")
	t.Setenv(deliveryKeyEnv, "test-delivery-key")
}

func okProvisionFlags() provisionFlags {
	return provisionFlags{
		subscriptionID:   "00000000-0000-0000-0000-000000000000",
		resourceGroup:    "rg-atlas",
		location:         "eastus",
		systemTopic:      "atlas-azure-activitylog",
		subscriptionName: "atlas-azure-receiver",
		webhookHost:      "https://atlas.example.com",
		path:             "/webhooks/azure/eventgrid",
		credentialIn:     "header",
		credentialName:   "Authorization",
	}
}

func TestDoProvision_CreatesTopicAndSubscription(t *testing.T) {
	elevatedEnv(t)
	api := &fakeProvisionAPI{}
	seen := installProvisionSeams(t, api)

	if err := doProvision(context.Background(), okProvisionFlags()); err != nil {
		t.Fatalf("doProvision: %v", err)
	}
	if got := strings.Join(api.calls, ","); got != "put-topic,put-sub" {
		t.Errorf("calls = %q; want put-topic,put-sub", got)
	}
	// Credential separation (P0-658-1): the cred used MUST be the elevated one,
	// resolved from AZURE_PROVISION_*, NOT the receiver's AZURE_* (which is empty).
	if seen.TenantID() != "elevated-tenant" || seen.ClientID() != "elevated-client" {
		t.Errorf("provision used cred %+v; want the elevated AZURE_PROVISION_* credential", seen)
	}
}

func TestDoProvision_WithDiagnostic(t *testing.T) {
	elevatedEnv(t)
	api := &fakeProvisionAPI{}
	installProvisionSeams(t, api)
	f := okProvisionFlags()
	f.withDiagnostic = true
	f.diagnosticName = "atlas-azure-activitylog"
	f.activityLogCats = []string{"Administrative"}
	if err := doProvision(context.Background(), f); err != nil {
		t.Fatalf("doProvision: %v", err)
	}
	if got := strings.Join(api.calls, ","); got != "put-topic,put-sub,put-diag" {
		t.Errorf("calls = %q; want topic,sub,diag", got)
	}
}

func TestDoProvision_Teardown(t *testing.T) {
	elevatedEnv(t)
	api := &fakeProvisionAPI{}
	installProvisionSeams(t, api)
	f := okProvisionFlags()
	f.teardown = true
	if err := doProvision(context.Background(), f); err != nil {
		t.Fatalf("doProvision teardown: %v", err)
	}
	if got := strings.Join(api.calls, ","); got != "del-sub,del-topic" {
		t.Errorf("calls = %q; want del-sub,del-topic", got)
	}
}

func TestDoProvision_RequiresElevatedCredential(t *testing.T) {
	// Only the receiver's read-only AZURE_* set; the elevated AZURE_PROVISION_*
	// are unset → provision must refuse (it does NOT borrow the receiver cred).
	t.Setenv(provisionTenantEnv, "")
	t.Setenv(provisionClientEnv, "")
	t.Setenv(provisionSecretEnv, "")
	t.Setenv("AZURE_TENANT_ID", "receiver-tenant")
	t.Setenv("AZURE_CLIENT_ID", "receiver-client")
	t.Setenv("AZURE_CLIENT_SECRET", "test-receiver-secret")
	t.Setenv(deliveryKeyEnv, "test-delivery-key")

	api := &fakeProvisionAPI{}
	installProvisionSeams(t, api)
	err := doProvision(context.Background(), okProvisionFlags())
	if err == nil || !strings.Contains(err.Error(), provisionTenantEnv) {
		t.Fatalf("want elevated-credential-required error; got %v", err)
	}
	if len(api.calls) != 0 {
		t.Errorf("no ARM call should be issued without elevated cred; got %v", api.calls)
	}
}

func TestDoProvision_RequiresDeliveryKey(t *testing.T) {
	elevatedEnv(t)
	t.Setenv(deliveryKeyEnv, "")
	api := &fakeProvisionAPI{}
	installProvisionSeams(t, api)
	err := doProvision(context.Background(), okProvisionFlags())
	if err == nil || !strings.Contains(err.Error(), deliveryKeyEnv) {
		t.Fatalf("want delivery-key-required error; got %v", err)
	}
}

func TestDoProvision_RequiresCoreFlags(t *testing.T) {
	elevatedEnv(t)
	installProvisionSeams(t, &fakeProvisionAPI{})
	cases := map[string]func(*provisionFlags){
		"subscription-id": func(f *provisionFlags) { f.subscriptionID = "" },
		"resource-group":  func(f *provisionFlags) { f.resourceGroup = "" },
		"location":        func(f *provisionFlags) { f.location = "" },
	}
	for want, mutate := range cases {
		f := okProvisionFlags()
		mutate(&f)
		err := doProvision(context.Background(), f)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s: want error mentioning it; got %v", want, err)
		}
	}
}

func TestDoProvision_BadCredentialIn(t *testing.T) {
	elevatedEnv(t)
	installProvisionSeams(t, &fakeProvisionAPI{})
	f := okProvisionFlags()
	f.credentialIn = "bogus"
	if err := doProvision(context.Background(), f); err == nil {
		t.Fatal("want error for bad --credential-in")
	}
}

func TestDoProvision_TokenError(t *testing.T) {
	elevatedEnv(t)
	sentinel := errors.New("token 401")
	prevTok := provisionAcquireToken
	provisionAcquireToken = func(context.Context, azureauth.Credential, *http.Client, string) (string, error) {
		return "", sentinel
	}
	t.Cleanup(func() { provisionAcquireToken = prevTok })
	prevAPI := newProvisionAPI
	newProvisionAPI = func(*http.Client, string, string) provision.API { return &fakeProvisionAPI{} }
	t.Cleanup(func() { newProvisionAPI = prevAPI })

	err := doProvision(context.Background(), okProvisionFlags())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "elevated token") {
		t.Fatalf("want wrapped elevated-token error; got %v", err)
	}
}

func TestBuildPlan_WebhookURLFromHostAndPath(t *testing.T) {
	f := okProvisionFlags()
	f.webhookHost = "https://atlas.example.com/"
	f.path = "webhooks/azure/eventgrid"
	plan := buildPlan(f, parseLoc(t, f.credentialIn), "test-delivery-key")
	if plan.Subscription.WebhookURL != "https://atlas.example.com/webhooks/azure/eventgrid" {
		t.Errorf("webhook url = %q", plan.Subscription.WebhookURL)
	}
	if plan.Subscription.DeliveryKeyHeader != "Authorization" {
		t.Errorf("header credential mapping wrong: %+v", plan.Subscription)
	}
}

func TestBuildPlan_QueryCredential(t *testing.T) {
	f := okProvisionFlags()
	f.credentialIn = "query"
	f.credentialName = "code"
	plan := buildPlan(f, parseLoc(t, "query"), "k")
	if plan.Subscription.DeliveryKeyQueryParam != "code" || plan.Subscription.DeliveryKeyHeader != "" {
		t.Errorf("query credential mapping wrong: %+v", plan.Subscription)
	}
}

func TestBuildPlan_DiagnosticSystemTopicID(t *testing.T) {
	f := okProvisionFlags()
	f.withDiagnostic = true
	f.diagnosticName = "d"
	plan := buildPlan(f, parseLoc(t, f.credentialIn), "k")
	want := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-atlas/providers/Microsoft.EventGrid/systemTopics/atlas-azure-activitylog"
	if plan.Diagnostic.SystemTopicID != want {
		t.Errorf("diagnostic system topic id = %q; want %q", plan.Diagnostic.SystemTopicID, want)
	}
}

func parseLoc(t *testing.T, in string) eventgrid.CredentialLocation {
	t.Helper()
	loc, err := parseCredentialLocation(in)
	if err != nil {
		t.Fatalf("parseCredentialLocation(%q): %v", in, err)
	}
	return loc
}

func TestPrintProvisionRBAC(t *testing.T) {
	// printProvisionRBAC writes to a *os.File; route output through a pipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	printProvisionRBAC(w)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "Microsoft.EventGrid/systemTopics/write") {
		t.Errorf("RBAC output missing EventGrid write action; got %q", out)
	}
	if !strings.Contains(out, "Microsoft.Insights/diagnosticSettings/write") {
		t.Errorf("RBAC output missing diagnosticSettings write action; got %q", out)
	}
}

func TestNewProvisionCmd_PrintRBACFlag(t *testing.T) {
	cmd := newProvisionCmd()
	if cmd.Flags().Lookup("print-rbac") == nil {
		t.Error("provision missing --print-rbac flag")
	}
	if cmd.Flags().Lookup("with-diagnostic") == nil {
		t.Error("provision missing --with-diagnostic flag")
	}
	if cmd.Flags().Lookup("teardown") == nil {
		t.Error("provision missing --teardown flag")
	}
}

func TestNewDeprovisionCmd_Wiring(t *testing.T) {
	cmd := newDeprovisionCmd()
	if cmd.Name() != "deprovision" {
		t.Errorf("name = %q", cmd.Name())
	}
	if cmd.Flags().Lookup("subscription-id") == nil {
		t.Error("deprovision missing --subscription-id")
	}
}

// TestReceiverNeverConstructsWriteAPI is the P0-658-1 guard: the long-lived
// receiver path (doEventGrid) must NEVER construct the write-capable ARM
// provision client. We wire the provision API constructor to fail the test if it
// is ever called, then drive the receiver far enough to exercise its auth +
// re-read setup. The receiver errors out on the unreachable platform endpoint,
// but it must do so WITHOUT ever reaching for the write API.
func TestReceiverNeverConstructsWriteAPI(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	// Receiver read-only credential present; elevated provisioning creds present
	// in the environment too — the receiver must STILL never touch them.
	t.Setenv("AZURE_TENANT_ID", "tenant-1")
	t.Setenv("AZURE_CLIENT_ID", "client-1")
	t.Setenv("AZURE_CLIENT_SECRET", "test-azure-client-secret")
	t.Setenv(provisionTenantEnv, "elevated-tenant")
	t.Setenv(provisionClientEnv, "elevated-client")
	t.Setenv(provisionSecretEnv, "test-elevated-secret")
	t.Setenv(deliveryKeyEnv, "test-delivery-key")

	prevAPI := newProvisionAPI
	newProvisionAPI = func(*http.Client, string, string) provision.API {
		t.Fatal("P0-658-1 VIOLATED: the receiver path constructed the write-capable ARM provision API")
		return nil
	}
	t.Cleanup(func() { newProvisionAPI = prevAPI })
	prevTok := provisionAcquireToken
	provisionAcquireToken = func(context.Context, azureauth.Credential, *http.Client, string) (string, error) {
		t.Fatal("P0-658-1 VIOLATED: the receiver path acquired an elevated provisioning token")
		return "", nil
	}
	t.Cleanup(func() { provisionAcquireToken = prevTok })

	// Short-circuit the receiver before it binds a socket: fail the sdk-client
	// constructor so doEventGrid returns after building auth + the re-reader but
	// before serving. That window is exactly where a write path, if any existed,
	// would be wired — and it is not.
	prevSDK := newEventGridSDKClient
	newEventGridSDKClient = func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) {
		return nil, errors.New("test: stop before serve")
	}
	t.Cleanup(func() { newEventGridSDKClient = prevSDK })

	f := eventGridFlags{
		environment:    "prod",
		authMode:       "client-credentials",
		subscriptionID: "sub-1",
		listen:         "127.0.0.1:0",
		path:           "/webhooks/azure/eventgrid",
		credentialIn:   "header",
		credentialName: "Authorization",
	}
	_ = doEventGrid(context.Background(), f)
	// No t.Fatal fired ⇒ the receiver never reached for the write API. Pass.
}
