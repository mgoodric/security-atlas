package eventgrid

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// ValidationHandler is the Event-Grid adapter that owns the SubscriptionValidation
// handshake (D2). When Event Grid validates a webhook subscription it POSTs an event
// of type Microsoft.EventGrid.SubscriptionValidationEvent carrying
// data.validationCode; the receiver MUST respond 200 with
// {"validationResponse":"<code>"} and MUST NOT process it as a delivery (no
// credential verification, no re-read, no record).
//
// This handler intercepts that case FIRST, then delegates every real delivery to
// the wrapped Receiver (the shared verify-FIRST skeleton), which authenticates the
// delivery key BEFORE enqueuing any change event. Keeping the handshake in the
// Event-Grid adapter (not the shared package) honors the slice-557 directive to
// avoid bending the shared seam for one vendor's special path — Event Grid is the
// 2nd such vendor (Intune was the 1st); a first-class shared hook is filed as a
// spillover.
//
// The validation event is UNAUTHENTICATED by design: it establishes the endpoint
// before the operator has wired the delivery key into deliveries, so it MUST be
// intercepted before the verify-first skeleton (exactly as Intune's validationToken
// was). It builds no record, so an unauthenticated handshake is not a record-forgery
// surface — a hostile caller can at most elicit an echo of the code it supplied.
type ValidationHandler struct {
	// Inner is the wrapped Receiver (a real delivery is delegated to it). It must be
	// the shared verify-FIRST handler so every NON-handshake delivery is
	// credential-checked before any record work.
	Inner http.Handler
	// MaxBodyBytes bounds the peeked validation body. 0 falls back to
	// DefaultMaxBodyBytes.
	MaxBodyBytes int64
}

// validationResponse is the JSON the receiver echoes for a SubscriptionValidation
// handshake.
type validationResponse struct {
	ValidationResponse string `json:"validationResponse"`
}

// ServeHTTP intercepts the SubscriptionValidation handshake (echo the code, 200,
// no record) and delegates every other request to the wrapped Receiver.
func (h ValidationHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Only a POST can carry a validation event; let the inner handler reject other
	// methods (405) so the method discipline stays in one place.
	if req.Method != http.MethodPost {
		h.Inner.ServeHTTP(w, req)
		return
	}

	maxBody := h.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	// Size-bound the peek so a hostile POST cannot exhaust memory even on the
	// handshake path.
	limited := http.MaxBytesReader(w, req.Body, maxBody)
	body, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Restore the body for the inner handler (it re-reads + verifies a real
	// delivery). The peek consumed it.
	req.Body = io.NopCloser(bytes.NewReader(body))

	if code, ok := validationCode(body); ok {
		// Bound the echoed code (defensive: cannot reflect an unbounded body).
		if len(code) > maxValidationCodeLen {
			code = code[:maxValidationCodeLen]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(validationResponse{ValidationResponse: code})
		return
	}
	h.Inner.ServeHTTP(w, req)
}

// validationCode parses the (already-read) body and returns the validationCode iff
// the delivery is a SubscriptionValidation handshake. A non-validation or malformed
// body returns ("", false) so it falls through to the inner verify-first delivery
// path.
func validationCode(body []byte) (string, bool) {
	events, err := ParseBatch(body)
	if err != nil {
		return "", false
	}
	for _, e := range events {
		if e.IsValidation() {
			return e.ValidationCode, true
		}
	}
	return "", false
}
