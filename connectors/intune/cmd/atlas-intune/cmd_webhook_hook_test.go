package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Slice 657: the validationToken handshake adopts the shared webhookrecv seam via
// validationTokenHook. These tests pin the hook's behaviour directly (the
// existing cmd_webhook_test.go drives it through the validationHandler / Receiver).

// A request with no validationToken query param declines (ok=false) and falls
// through to the verify-first delivery path.
func TestValidationTokenHook_NoTokenDeclines(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune", nil)
	resp, ct, ok := validationTokenHook{}.Detect(req, nil)
	if ok {
		t.Fatalf("Detect returned ok=true for a non-handshake request (resp=%q ct=%q)", resp, ct)
	}
}

// The handshake echoes the token verbatim as text/plain — the EXACT response shape
// (content-type + bytes) the pre-657 hand-rolled handler produced.
func TestValidationTokenHook_EchoesVerbatim(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune?validationToken=tok-abc-123", nil)
	resp, ct, ok := validationTokenHook{}.Detect(req, nil)
	if !ok {
		t.Fatal("Detect returned ok=false for a validationToken handshake")
	}
	if ct != "text/plain" {
		t.Errorf("content-type = %q; want text/plain (byte-identical to pre-657)", ct)
	}
	if string(resp) != "tok-abc-123" {
		t.Errorf("echo = %q; want verbatim token", resp)
	}
}

// An over-long token is truncated to maxValidationTokenLen (defensive: cannot
// reflect an unbounded body).
func TestValidationTokenHook_TruncatesOverLongToken(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxValidationTokenLen+500)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/intune?validationToken="+long, nil)
	resp, _, ok := validationTokenHook{}.Detect(req, nil)
	if !ok {
		t.Fatal("Detect returned ok=false for a long validationToken")
	}
	if len(resp) != maxValidationTokenLen {
		t.Fatalf("echoed %d bytes; want bounded to %d", len(resp), maxValidationTokenLen)
	}
}
