package mdmwebhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// Slice 657: ServeHTTPWithValidation routes the receiver through the shared
// validation-handshake seam. A vendor adapter (Intune's validationToken handshake)
// supplies a webhookrecv.ValidationHook; the handshake is answered BEFORE the
// verify-first delivery path and builds no record, while a real delivery still
// reaches the credential Verifier.

// echoHook is a test ValidationHook that detects a `vt` query param and echoes it
// as text/plain (stands in for the Intune validationToken hook).
type echoHook struct{}

func (echoHook) Detect(req *http.Request, _ []byte) ([]byte, string, bool) {
	if tok := req.URL.Query().Get("vt"); tok != "" {
		return []byte(tok), "text/plain", true
	}
	return nil, "", false
}

// A handshake request short-circuits 200 with the echoed bytes and builds NO record
// (no credential needed — it is unsigned).
func TestServeHTTPWithValidation_HandshakeNoRecord(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jamf?vt=tok-657", nil)
	w := httptest.NewRecorder()
	rec.ServeHTTPWithValidation(w, req, echoHook{})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if w.Body.String() != "tok-657" {
		t.Errorf("echo = %q; want verbatim token", w.Body.String())
	}
	if len(p.records) != 0 {
		t.Errorf("handshake built %d records; want 0", len(p.records))
	}
}

// A non-handshake delivery (hook declines) with a forged credential is rejected 401
// BEFORE any record, even with a hook configured (no verify bypass).
func TestServeHTTPWithValidation_ForgedDelivery401NoBypass(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jamf", bytes.NewReader([]byte(`{}`)))
	req.Header.Set(testHeader, "test-wrong-secret")
	w := httptest.NewRecorder()
	rec.ServeHTTPWithValidation(w, req, echoHook{})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	if len(p.records) != 0 {
		t.Errorf("forged delivery built %d records; want 0", len(p.records))
	}
}

// A non-handshake delivery with a valid credential reaches build and emits a record
// (the hook does not interfere with real deliveries).
func TestServeHTTPWithValidation_VerifiedDeliveryEmits(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jamf", bytes.NewReader([]byte(`{}`)))
	req.Header.Set(testHeader, testSecret)
	w := httptest.NewRecorder()
	rec.ServeHTTPWithValidation(w, req, echoHook{})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("emitted %d records; want 1", len(p.records))
	}
}

// A nil hook makes ServeHTTPWithValidation identical to ServeHTTP.
func TestServeHTTPWithValidation_NilHookIsServeHTTP(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jamf", bytes.NewReader([]byte(`{}`)))
	req.Header.Set(testHeader, testSecret)
	w := httptest.NewRecorder()
	var nilHook webhookrecv.ValidationHook
	rec.ServeHTTPWithValidation(w, req, nilHook)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("emitted %d records; want 1", len(p.records))
	}
}
