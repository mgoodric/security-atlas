// Seam + handler tests for the `webhook` (subscribe) subcommand. The sdk client
// constructor and the blocking Serve loop are swapped for fakes; the
// validationToken handshake + clientState verify paths are tested directly via
// httptest. No live Graph — synthetic deliveries; neutral "test-*" strings only.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/mdmwebhook"
)

// recordingPusher counts pushes so a test can assert the handshake builds NO
// record and a verified delivery DOES.
type recordingPusher struct{ n int }

func (p *recordingPusher) Push(_ context.Context, _ *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	p.n++
	return &evidencev1.EvidenceReceipt{}, nil
}

const testClientState = "test-client-state-not-a-real-value"

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
	return webhookFlags{environment: "prod", deviceControl: "scf:END-04", listen: "127.0.0.1:0", path: "/webhooks/intune"}
}

func TestDoWebhook_Success(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("INTUNE_WEBHOOK_CLIENT_STATE", testClientState)

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
	t.Setenv("INTUNE_WEBHOOK_CLIENT_STATE", "")
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
	t.Setenv("INTUNE_WEBHOOK_CLIENT_STATE", testClientState)
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
	t.Setenv("INTUNE_WEBHOOK_CLIENT_STATE", testClientState)
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

func TestProfilesSupported_PullAndSubscribe(t *testing.T) {
	want := map[string]bool{"pull": true, "subscribe": true}
	if len(ProfilesSupported) != len(want) {
		t.Fatalf("ProfilesSupported = %v; want pull+subscribe", ProfilesSupported)
	}
	for _, p := range ProfilesSupported {
		if !want[p] {
			t.Errorf("unexpected profile %q", p)
		}
		if strings.Contains(strings.ToLower(p), "continuous") {
			t.Errorf("profile %q must not claim continuous monitoring", p)
		}
	}
}

// --- validationHandler tests (the Graph subscription-validation handshake) ---

// newTestHandler builds the validationHandler wrapping a real mdmwebhook.Receiver
// with the Intune parser/verifier and a counting pusher.
func newTestHandler(t *testing.T, p mdmwebhook.Pusher) validationHandler {
	t.Helper()
	rec, err := mdmwebhook.NewReceiver(mdmwebhook.Config{
		SourceMDM:   devposture.MDMIntune,
		Verifier:    mdmwebhook.NewClientStateVerifier(testClientState, devices.ExtractClientState),
		Parser:      devices.ParseChangeNotification,
		Pusher:      p,
		ActorID:     "connector:intune:devices@test",
		Service:     "intune",
		Environment: "prod",
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return validationHandler{rec}
}

// AC (P0): the Graph validationToken handshake responds 200 with the token echoed
// as text/plain and builds NO record.
func TestValidationHandler_Handshake_EchoesNoRecord(t *testing.T) {
	p := &recordingPusher{}
	h := newTestHandler(t, p)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune?validationToken=test-handshake-token-123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != "test-handshake-token-123" {
		t.Errorf("echoed body = %q, want the verbatim token", string(body))
	}
	if p.n != 0 {
		t.Errorf("handshake built %d records, want 0", p.n)
	}
}

// AC (P0): a delivery with the correct clientState is accepted and emits a record.
func TestValidationHandler_VerifiedDelivery_Emits(t *testing.T) {
	p := &recordingPusher{}
	h := newTestHandler(t, p)

	body := []byte(`{"value":[{"clientState":"test-client-state-not-a-real-value","changeType":"updated","resourceData":{"id":"device-guid-1","isEncrypted":true,"complianceState":"compliant"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if p.n != 1 {
		t.Fatalf("emitted %d records, want 1", p.n)
	}
}

// AC (DOMINANT P0): a delivery with a forged clientState is rejected 401 BEFORE
// any record is built.
func TestValidationHandler_ForgedClientState_401NoRecord(t *testing.T) {
	p := &recordingPusher{}
	h := newTestHandler(t, p)

	body := []byte(`{"value":[{"clientState":"test-forged-state","changeType":"updated","resourceData":{"id":"device-guid-1"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if p.n != 0 {
		t.Fatalf("forged delivery built %d records, want 0", p.n)
	}
}

// A delivery with no clientState at all is rejected 401.
func TestValidationHandler_MissingClientState_401(t *testing.T) {
	p := &recordingPusher{}
	h := newTestHandler(t, p)

	body := []byte(`{"value":[{"changeType":"updated","resourceData":{"id":"device-guid-1"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if p.n != 0 {
		t.Fatalf("unauthenticated delivery built %d records, want 0", p.n)
	}
}
