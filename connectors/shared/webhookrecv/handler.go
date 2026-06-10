package webhookrecv

import (
	"io"
	"net/http"
)

// Verifier authenticates a raw webhook delivery. Verify is called by the
// skeleton BEFORE the body is parsed or any record is built; a non-nil error
// rejects the delivery with a bare 401 (no detail leak — the error is never
// echoed into the response). The body passed is the exact, already-size-bounded
// bytes received; header is the request header set carrying the vendor signature.
//
// A connector's vendor adapter satisfies this — typically by wrapping an
// HMACConfig.Verify with its held secret.
type Verifier interface {
	Verify(body []byte, header http.Header) error
}

// BuildAndPush is the vendor-supplied step the skeleton hands a verified raw body
// to. It owns everything connector-specific downstream of verification: parsing
// the payload, the (re-read / fan-out / single) record build, and the push. It
// returns the HTTP status the skeleton writes (e.g. 200 ack, 400 bad payload,
// 502 upstream error). It must NOT write to w itself — the skeleton owns the
// response so the verify-first ordering and status discipline stay in one place.
//
// A single-record connector (github / pagerduty) returns 200 on success / 502 on
// push failure. A fan-out connector (hris) implements its multi-worker loop here
// and returns 200 / 502 per its partial-failure policy. The skeleton stays
// agnostic to which.
type BuildAndPush func(req *http.Request, body []byte) (status int)

// Handle is the verify-FIRST handler skeleton: enforce POST, size-cap the body
// (MaxBytesReader → 413), read it, call the vendor Verifier BEFORE anything else
// (forged delivery → 401 before any record is built), then hand the verified raw
// body to the vendor BuildAndPush step and write the status it returns.
//
// This is the single place the P0 verify-first-before-record invariant lives for
// every connector that adopts it: there is no code path that reaches BuildAndPush
// without a passing verification.
func Handle(w http.ResponseWriter, req *http.Request, maxBodyBytes int64, v Verifier, build BuildAndPush) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Size-bound the body BEFORE reading it (a hostile POST cannot exhaust
	// memory). MaxBytesReader caps the read and errors past the limit.
	req.Body = http.MaxBytesReader(w, req.Body, maxBodyBytes)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
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

// statusMessage maps the non-2xx statuses the skeleton emits to the exact
// response text the pre-refactor receivers used, so the response body stays
// byte-identical.
func statusMessage(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad request"
	case http.StatusBadGateway:
		return "upstream error"
	default:
		return http.StatusText(status)
	}
}
