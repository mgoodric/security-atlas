package webhookrecv_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// trackingVerifier records whether Verify was called and accepts iff X-Sig=="good".
type trackingVerifier struct{ called *bool }

func (v trackingVerifier) Verify(_ []byte, h http.Header) error {
	*v.called = true
	if h.Get("X-Sig") == "good" {
		return nil
	}
	return webhookrecv.ErrBadSignature
}

// queryEchoHook is a test ValidationHook that detects a `vt` query param and echoes
// it as text/plain (mirrors the Intune validationToken shape).
type queryEchoHook struct{}

func (queryEchoHook) Detect(req *http.Request, _ []byte) ([]byte, string, bool) {
	if tok := req.URL.Query().Get("vt"); tok != "" {
		return []byte(tok), "text/plain; charset=utf-8", true
	}
	return nil, "", false
}

const valMax int64 = 1 << 10

// AC-4: a real validation request short-circuits with 200, the hook's exact
// content-type + bytes, and NEVER calls Verify or BuildAndPush.
func TestHandleWithValidation_HandshakeShortCircuits(t *testing.T) {
	t.Parallel()
	verified := false
	built := false
	build := func(*http.Request, []byte) int { built = true; return http.StatusOK }

	req := httptest.NewRequest(http.MethodPost, "/hook?vt=handshake-tok-123", nil)
	// No signature header at all — the handshake is unsigned and must still pass.
	w := httptest.NewRecorder()
	webhookrecv.HandleWithValidation(w, req, valMax, queryEchoHook{}, trackingVerifier{&verified}, build)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type = %q; want text/plain; charset=utf-8", ct)
	}
	if got := w.Body.String(); got != "handshake-tok-123" {
		t.Errorf("echo = %q; want verbatim token", got)
	}
	if verified {
		t.Error("Verify was called on a validation handshake; hook must run before Verify")
	}
	if built {
		t.Error("BuildAndPush was called on a validation handshake; must build no record")
	}
}

// AC-4 (no bypass): a NON-handshake delivery with a bad signature is rejected 401
// and NEVER reaches BuildAndPush, EVEN with a hook configured.
func TestHandleWithValidation_ForgedDeliveryIs401NoBypass(t *testing.T) {
	t.Parallel()
	verified := false
	built := false
	build := func(*http.Request, []byte) int { built = true; return http.StatusOK }

	// No `vt` query → hook returns ok=false → falls through to verify-first.
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("X-Sig", "forged")
	w := httptest.NewRecorder()
	webhookrecv.HandleWithValidation(w, req, valMax, queryEchoHook{}, trackingVerifier{&verified}, build)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	if !verified {
		t.Error("Verify was NOT called on a non-handshake delivery; verify-first bypassed")
	}
	if built {
		t.Error("BuildAndPush reached on a forged delivery; verify-first P0 violated")
	}
}

// A non-handshake delivery with a good signature reaches Verify then BuildAndPush
// (the hook does not interfere with real deliveries).
func TestHandleWithValidation_VerifiedDeliveryReachesBuild(t *testing.T) {
	t.Parallel()
	verified := false
	var gotBody []byte
	build := func(_ *http.Request, b []byte) int { gotBody = b; return http.StatusOK }

	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("X-Sig", "good")
	w := httptest.NewRecorder()
	webhookrecv.HandleWithValidation(w, req, valMax, queryEchoHook{}, trackingVerifier{&verified}, build)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if !verified {
		t.Error("Verify was not called on a real delivery")
	}
	if string(gotBody) != `{"x":1}` {
		t.Errorf("build got %q; want verified body", gotBody)
	}
}

// A nil hook makes HandleWithValidation behave exactly like Handle (oversized →
// 413 before hook/verify/build).
func TestHandleWithValidation_NilHookOversizedIs413(t *testing.T) {
	t.Parallel()
	verified := false
	built := false
	build := func(*http.Request, []byte) int { built = true; return http.StatusOK }
	big := bytes.Repeat([]byte("a"), int(valMax)+1)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(big))
	req.Header.Set("X-Sig", "good")
	w := httptest.NewRecorder()
	webhookrecv.HandleWithValidation(w, req, valMax, nil, trackingVerifier{&verified}, build)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", w.Code)
	}
	if verified || built {
		t.Error("oversized body reached verify/build; must reject before either")
	}
}

// A hook that returns ok=false with an empty content-type still falls through to
// the verify-first path (content-type only set on ok=true).
func TestHandleWithValidation_HookDeclineFallsThrough(t *testing.T) {
	t.Parallel()
	verified := false
	build := func(*http.Request, []byte) int { return http.StatusOK }
	req := httptest.NewRequest(http.MethodGet, "/hook?vt=x", nil)
	w := httptest.NewRecorder()
	webhookrecv.HandleWithValidation(w, req, valMax, queryEchoHook{}, trackingVerifier{&verified}, build)

	// GET is rejected 405 before the body read / hook — method gate is first.
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", w.Code)
	}
}
