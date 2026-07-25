package eventgrid

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Slice 657: the SubscriptionValidation handshake adopts the shared webhookrecv
// seam via validationCodeHook. These tests pin the hook directly (the existing
// receiver_test.go drives it through the Receiver). They lock the byte-identical
// response shape the pre-657 hand-rolled ValidationHandler produced.

// A non-validation body declines (ok=false) and falls through to verify-first.
func TestValidationCodeHook_NonValidationDeclines(t *testing.T) {
	t.Parallel()
	body := storageEventBody(testStorageID)
	resp, ct, ok := validationCodeHook{}.Detect(nil, body)
	if ok {
		t.Fatalf("Detect ok=true for a resource event (resp=%q ct=%q)", resp, ct)
	}
}

// A malformed body declines (ok=false) so the shared seam treats it as a real
// delivery and verify-first rejects/parses it.
func TestValidationCodeHook_MalformedDeclines(t *testing.T) {
	t.Parallel()
	hook := validationCodeHook{}
	if _, _, ok := hook.Detect(nil, []byte(`{bad`)); ok {
		t.Fatal("Detect ok=true for a malformed body")
	}
}

// A SubscriptionValidation event echoes {"validationResponse":"<code>"} as
// application/json — byte-identical (incl. trailing newline) to the pre-657
// json.Encoder.Encode response.
func TestValidationCodeHook_EchoesJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`[{"eventType":"Microsoft.EventGrid.SubscriptionValidationEvent","data":{"validationCode":"code-xyz"}}]`)
	resp, ct, ok := validationCodeHook{}.Detect(new(http.Request), body)
	if !ok {
		t.Fatal("Detect ok=false for a validation event")
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q; want application/json", ct)
	}
	if want := `{"validationResponse":"code-xyz"}` + "\n"; string(resp) != want {
		t.Errorf("body = %q; want %q (byte-identical to pre-657)", string(resp), want)
	}
}

// An over-long validationCode is truncated to maxValidationCodeLen.
func TestValidationCodeHook_TruncatesOverLongCode(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", maxValidationCodeLen+500)
	body := []byte(`[{"eventType":"Microsoft.EventGrid.SubscriptionValidationEvent","data":{"validationCode":"` + long + `"}}]`)
	resp, _, ok := validationCodeHook{}.Detect(new(http.Request), body)
	if !ok {
		t.Fatal("Detect ok=false for a long-code validation event")
	}
	var got validationResponse
	if err := json.Unmarshal(bytes.TrimSpace(resp), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ValidationResponse) != maxValidationCodeLen {
		t.Fatalf("echoed code len = %d; want bounded to %d", len(got.ValidationResponse), maxValidationCodeLen)
	}
}
