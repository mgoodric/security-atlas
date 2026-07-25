package eventgrid

import (
	"encoding/json"
	"net/http"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// validationResponse is the JSON the receiver echoes for a SubscriptionValidation
// handshake.
type validationResponse struct {
	ValidationResponse string `json:"validationResponse"`
}

// validationCodeHook is the Event-Grid webhookrecv.ValidationHook (slice 657). It
// parses the (already-read, already-size-bounded) body and, on a
// Microsoft.EventGrid.SubscriptionValidationEvent, returns
// {"validationResponse":"<code>"} as application/json with the validationCode
// bounded by maxValidationCodeLen — the EXACT response shape the pre-657
// hand-rolled ValidationHandler produced. A non-validation or malformed body
// returns ok=false and falls through to the shared verify-first delivery path.
//
// The validation event is UNAUTHENTICATED by design: it establishes the endpoint
// before the operator has wired the delivery key into deliveries, so the shared
// skeleton runs this hook BEFORE the delivery-key Verifier (exactly as Intune's
// validationToken). It builds no record, so an unauthenticated handshake is not a
// record-forgery surface — a hostile caller can at most elicit an echo of the code
// it supplied.
type validationCodeHook struct{}

func (validationCodeHook) Detect(_ *http.Request, body []byte) ([]byte, string, bool) {
	code, ok := validationCode(body)
	if !ok {
		return nil, "", false
	}
	// Bound the echoed code (defensive: cannot reflect an unbounded body).
	if len(code) > maxValidationCodeLen {
		code = code[:maxValidationCodeLen]
	}
	// Encode with the SAME trailing-newline shape json.Encoder.Encode produced in
	// the pre-657 handler (byte-identical response). json.Marshal cannot error on
	// this fixed struct of strings.
	out, _ := json.Marshal(validationResponse{ValidationResponse: code})
	out = append(out, '\n')
	return out, "application/json", true
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

// ValidationHandler is the back-compat wrapper for the Event-Grid validation
// handshake. Since slice 657 the handshake lives in the shared webhookrecv seam,
// consulted inside Receiver.ServeHTTP via validationCodeHook; this wrapper simply
// delegates to Inner so existing call sites (the cmd wiring + tests that construct
// ValidationHandler{Inner: rec}) keep working with byte-identical behaviour. Inner
// is the Receiver, which now owns the handshake; MaxBodyBytes is retained for
// source compatibility but is no longer load-bearing (the Receiver enforces its own
// configured body bound).
type ValidationHandler struct {
	// Inner is the wrapped Receiver; it owns both the validation handshake (via the
	// shared hook) and the verify-first delivery path.
	Inner http.Handler
	// MaxBodyBytes is retained for source/back-compat; the Receiver's configured
	// MaxBodyBytes governs the actual bound.
	MaxBodyBytes int64
}

// ServeHTTP delegates to the wrapped Inner handler. The handshake interception now
// lives in the shared seam inside the Receiver, so this is a pass-through.
func (h ValidationHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.Inner.ServeHTTP(w, req)
}

// ensure validationCodeHook satisfies the shared seam at compile time.
var _ webhookrecv.ValidationHook = validationCodeHook{}
