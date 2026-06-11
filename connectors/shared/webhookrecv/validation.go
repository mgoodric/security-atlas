package webhookrecv

import "net/http"

// ValidationHook is the reusable validation-handshake seam (slice 657). Some
// vendors front a webhook subscription with an UNSIGNED validation request that
// the receiver must answer with a vendor-specific echo and for which it must build
// NO record:
//
//   - Microsoft Graph (Intune, slice 557) sends a `validationToken` QUERY PARAM
//     and expects it echoed verbatim as text/plain.
//   - Azure Event Grid (slice 522) POSTs a SubscriptionValidationEvent BODY event
//     carrying a validationCode and expects {"validationResponse":"<code>"} JSON.
//
// Both are UNSIGNED by design (the handshake establishes the endpoint before the
// operator has wired the delivery credential), so the hook MUST run BEFORE the
// Verifier.Verify call — a validation request carries no delivery HMAC/secret and
// would otherwise be rejected 401. The hook runs AFTER the body is read so a
// body-event handshake (Event Grid) and a query-param handshake (Intune) are both
// available; reading the body first for an unsigned validation request is safe
// because the body is already MaxBytesReader-bounded (no memory-exhaustion bypass).
//
// A non-validation delivery returns ok=false and falls through to the UNCHANGED
// verify-first path: there is no code path that reaches BuildAndPush — or even
// Verify — for a validation request, and no code path that skips Verify for a real
// delivery (P0 verify-first invariant intact).
type ValidationHook interface {
	// Detect inspects the request and its already-read, already-size-bounded body.
	// When the request is a validation handshake it returns the exact response
	// bytes, the response content-type, and ok=true; the skeleton then writes
	// 200 + content-type + response and RETURNS (no Verify, no record). Otherwise
	// it returns ok=false and the delivery falls through to the verify-first path.
	//
	// Detect MUST NOT write to the response; the skeleton owns the response so the
	// ordering and status discipline stay in one place. It MUST NOT mutate req.Body
	// (the skeleton hands the same body to the verify-first path on ok=false).
	Detect(req *http.Request, body []byte) (response []byte, contentType string, ok bool)
}

// HandleWithValidation is Handle plus a pre-verify validation-handshake seam. It
// is byte-identical to Handle for every non-handshake delivery; when hook is nil
// it IS Handle. The ordering is:
//
//  1. method gate (POST only → 405),
//  2. size-bounded body read (MaxBytesReader → 413),
//  3. hook.Detect — on ok=true write 200 + the hook's content-type + response and
//     RETURN (no Verify, no BuildAndPush, no record),
//  4. Verifier.Verify (forged/unsigned real delivery → 401 before any record),
//  5. BuildAndPush and write the returned status.
//
// The hook runs at step 3 — after the body read, before Verify — so a validation
// request (unsigned) is answered without a credential, while a real delivery still
// reaches Verify before any record (P0). Handle is a thin wrapper that passes a nil
// hook, so the six existing adapters keep their exact behaviour.
func HandleWithValidation(w http.ResponseWriter, req *http.Request, maxBodyBytes int64, hook ValidationHook, v Verifier, build BuildAndPush) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Size-bound the body BEFORE reading it (a hostile POST cannot exhaust
	// memory) — this bound applies to the unsigned validation path too, so reading
	// the body before Verify is not a memory-exhaustion bypass.
	req.Body = http.MaxBytesReader(w, req.Body, maxBodyBytes)
	body, err := readAll(req.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Validation handshake BEFORE Verify: an unsigned validation request is
	// answered with a vendor echo and builds no record. A non-handshake delivery
	// (ok=false) falls through UNCHANGED to the verify-first path below.
	if hook != nil {
		if response, contentType, ok := hook.Detect(req, body); ok {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(response)
			return
		}
	}

	// Verify BEFORE any work. Reject unsigned / forged / wrong-signature with a
	// bare 401 (no detail leak).
	if err := v.Verify(body, req.Header); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	status := build(req, body)
	if status >= 400 {
		http.Error(w, statusMessage(status), status)
		return
	}
	w.WriteHeader(status)
}
