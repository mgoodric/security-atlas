# Slice 657 — webhookrecv shared validation-handshake seam — decisions log

**Type:** JUDGMENT (refactor — shared-package seam extraction)
**Branch:** `feat/657-shared-validation-handshake`
**Parents:** #656 (webhookrecv), #557 (Intune validationToken handshake), #522 (Event Grid SubscriptionValidation handshake)

## Context

Two connectors independently hand-rolled a **validation-handshake-in-adapter**
wrapping the shared `webhookrecv` verify-first skeleton:

- **Intune (557):** Microsoft Graph `validationToken` **query-param** echo →
  `200 text/plain`, no record (`validationHandler` in `cmd_webhook.go`).
- **Event Grid (522):** `Microsoft.EventGrid.SubscriptionValidationEvent` **body
  event** → `200 application/json {"validationResponse":"<code>"}`, no record
  (`ValidationHandler` in `eventgrid/validation.go`).

The 557 decisions log filed the follow-on: file a first-class shared
validation-handshake hook when a 2nd handshake connector arrives. Event Grid is
that connector. This slice extracts the shape into a reusable seam and adopts it in
both connectors; externally-observable behaviour is unchanged.

---

## D1 — `Handle` backward-compat strategy: NEW entrypoint (`HandleWithValidation`)

**Decision:** add a NEW exported `HandleWithValidation(w, req, max, hook, verifier,
build)` and make the existing `Handle(w, req, max, verifier, build)` a one-line
wrapper that delegates with a `nil` hook. NOT a functional-option variant.

**Why:**

- It is the lowest-churn option that keeps every existing caller **byte-identical**.
  The six existing adapters (mdm Jamf+Intune-delivery, github, hris, pagerduty,
  eventgrid delivery) call `Handle(...)` with the exact same five args and recompile
  unchanged; `Handle`'s body is now `HandleWithValidation(w, req, max, nil, v, build)`.
- The existing `handler_test.go` is **unmodified and green** — it drives `Handle`
  and asserts the 405 / 413 / 401-before-build / verified-reaches-build / status
  ordering, all preserved because `Handle` is `HandleWithValidation` with nil hook.
- A functional-option variant (`Handle(..., opts ...Option)`) would have been
  source-compatible but is more surface and indirection than one extra named
  entrypoint warrants for a single optional pre-step. A nil-hook short-circuit reads
  more obviously than an options slice for the reviewer checking the verify-first
  ordering.
- Single read site: I factored the body read into an unexported `readAll` so the
  verify-first and handshake paths cannot diverge on the read/size-bound semantics.

**Where:** `connectors/shared/webhookrecv/validation.go` (new) +
`connectors/shared/webhookrecv/handler.go` (Handle → wrapper).

---

## D2 — `ValidationHook` interface shape

```go
type ValidationHook interface {
    Detect(req *http.Request, body []byte) (response []byte, contentType string, ok bool)
}
```

**Decision:** exactly the shape the slice proposed. `Detect` receives the request
AND the already-read, already-MaxBytesReader-bounded body, so both a query-param
handshake (Intune reads `req.URL.Query()`) and a body-event handshake (Event Grid
parses `body`) are expressible behind one interface.

- `Detect` returns the **exact response bytes** + the **content-type** + `ok`. The
  skeleton (not the hook) writes the response — `Detect` MUST NOT touch `w`, so the
  `200`/ordering/status discipline stays in one place (mirrors how `BuildAndPush`
  must not write `w`).
- `ok=false` returns `(nil, "", false)` and the delivery falls through to the
  UNCHANGED verify-first path. A malformed body on the handshake path is treated as
  `ok=false` (Event Grid's `ParseBatch` error → decline → the real-delivery path's
  parser produces the honest `400`), so a malformed body is never silently eaten by
  the handshake.
- The skeleton sets `Content-Type` only when the hook returns a non-empty
  content-type, then writes `200` and the bytes.

---

## D3 — Per-adapter detection + preserved response bytes/content-type

| Adapter    | Detection                                       | Response bytes                           | Content-Type       | Bound                          |
| ---------- | ----------------------------------------------- | ---------------------------------------- | ------------------ | ------------------------------ |
| Intune     | `req.URL.Query().Get("validationToken") != ""`  | the token verbatim                       | `text/plain`       | `maxValidationTokenLen` (2048) |
| Event Grid | `ParseBatch(body)` → any event `IsValidation()` | `{"validationResponse":"<code>"}` + `\n` | `application/json` | `maxValidationCodeLen` (2048)  |

**Intune (`validationTokenHook`, `cmd_webhook.go`):** the pre-657 hand-rolled
handler set `Content-Type: text/plain` (NOT `; charset=utf-8`) and wrote the raw
token bytes. The hook reproduces both exactly. The `validationHandler` struct is
retained (the slice-557 tests construct `validationHandler{rec}` positionally) but
its field is now `*mdmwebhook.Receiver` and its `ServeHTTP` delegates to the new
`Receiver.ServeHTTPWithValidation(w, req, validationTokenHook{})`.

**Event Grid (`validationCodeHook`, `eventgrid/validation.go`):** the pre-657
handler wrote the JSON via `json.NewEncoder(w).Encode(...)`, which appends a
trailing `\n`. To stay byte-identical the hook does `json.Marshal(...)` then
`append(out, '\n')`. The handshake now lives in `Receiver.ServeHTTP` (which calls
`HandleWithValidation(..., validationCodeHook{}, ...)`); the `ValidationHandler`
struct is retained as a **pass-through compat shim** (the cmd wiring
`cmd_eventgrid.go` and the slice-522 tests construct `ValidationHandler{Inner: rec}`
and `{Inner: rec, MaxBodyBytes: 8}`). Its `MaxBodyBytes` field is retained for
source-compat but is no longer load-bearing — the `Receiver`'s own configured
`MaxBodyBytes` governs the bound (the `OversizeBody_413` test sets it on both the
Receiver and the wrapper, so the byte-identical 413 fires from the Receiver).

**A new `mdmwebhook.Receiver.ServeHTTPWithValidation` method was added** (alongside
the unchanged `ServeHTTP`) so the Intune adapter can route through the shared seam
without exposing the receiver's unexported verifier/buildAndPush. Adding a new
method changes no existing behaviour (Jamf delivery + Intune delivery still call the
unchanged `ServeHTTP`).

---

## D4 — Verify-first ordering proof (hook before Verify; record-path intact)

The seam runs the hook at step 3 of 5 in `HandleWithValidation`:

1. method gate → 405
2. size-bounded body read (MaxBytesReader) → 413
3. **`hook.Detect` — ok=true ⇒ write 200 + bytes and RETURN** (no Verify, no build)
4. `Verifier.Verify` → 401 on failure
5. `BuildAndPush` → write returned status

- **Hook MUST run before Verify** because validation requests are **unsigned** (they
  establish the endpoint before the operator wires the delivery credential); routing
  them through Verify would reject them 401. Both connectors' pre-657 handlers also
  intercepted before verification — this preserves that.
- **Hook runs after the body read** so a body-event handshake (Event Grid) is
  available; reading the body first for an unsigned request is safe because it is
  already MaxBytesReader-bounded (no memory-exhaustion bypass — asserted by the
  nil-hook-oversized-413 test and the eventgrid OversizeBody_413 test).
- **Record-path verify-first is intact:** the hook builds **no** record and on
  `ok=false` the code falls through to step 4 UNCHANGED. There is no path that
  reaches step 5 (BuildAndPush) without passing step 4 (Verify) for a real delivery.
  `TestHandleWithValidation_ForgedDeliveryIs401NoBypass` proves a non-handshake
  delivery with a bad signature, **with a hook configured**, hits Verify, gets 401,
  and never reaches build. `TestHandleWithValidation_HandshakeShortCircuits` proves
  a real handshake never calls Verify or build.

This satisfies anti-criterion **P0-657-2** (handshake is not a verify-bypass or
record-forgery surface) and **P0-657-1 / invariant #3** (receiver stays source-side;
zero platform inbound API).

---

## D5 — Coverage outcomes (all floors held / exceeded)

| Package                               | Floor | Achieved  | Notes                                                                                             |
| ------------------------------------- | ----- | --------- | ------------------------------------------------------------------------------------------------- |
| `connectors/shared/webhookrecv`       | 96    | **98.9%** | new `validation.go` covered by new `validation_test.go` (5 cases)                                 |
| `connectors/azure/internal/eventgrid` | 95    | **98.4%** | new `validation_hook_test.go` covers decline / malformed / echo / truncation                      |
| `connectors/intune/cmd/atlas-intune`  | 86    | **86.7%** | new `cmd_webhook_hook_test.go` covers decline / echo / truncation (was 86.2% before the new test) |
| `connectors/mdm/mdmwebhook`           | 94    | **94.9%** | new `mdmwebhook_validation_test.go` for the new method — see floor-catch note below               |

No floor was raised (the ratchet is monotonic; I only added tests). The new shared
code has dedicated tests; all touched packages gained NEW test files (no existing
test file was modified).

**mdmwebhook floor catch (local-CI-parity win):** the new
`Receiver.ServeHTTPWithValidation` method is called by the _Intune_ package, not by
`mdmwebhook`'s own tests, so it first showed 0%-covered and dropped `mdmwebhook`
from its 94.8% baseline to **93.2% < its 94 floor** — `coverage-gate` flagged it
locally before push. Fixed by adding a NEW `mdmwebhook_validation_test.go`
exercising the method directly (handshake-no-record / forged-401-no-bypass /
verified-emits / nil-hook-is-ServeHTTP), restoring 94.9%. The floor was NOT lowered;
`go run ./cmd/scripts/coverage-gate` then reports ALL CHECKS PASS for the touched
packages.

---

## Verification (run locally, green)

- `go build ./...` — clean.
- `go test ./connectors/...` — all green.
- `golangci-lint run` on the four touched packages — **0 issues**.
- `gofmt`/`goimports` — clean.
- Invariant #3: `git diff origin/main...HEAD -- internal/api/ migrations/ proto/ schemaregistry/` — **empty**.
- Existing test files unmodified: `handler_test.go`, eventgrid `*_test.go`,
  `cmd_webhook_test.go`, `mdmwebhook_test.go` — **no diff**; only NEW test files added.

## Detection-tier classification

- `detection_tier_actual`: unit — the only behaviours that needed pinning during the
  build (the Event Grid trailing-newline byte-identical JSON shape; the Intune
  `text/plain` (no charset) content-type; the truncation bounds; the
  hook-before-Verify no-bypass ordering) were all caught by the new pure-Go unit
  tests in the three packages.
- `detection_tier_target`: unit — these are pure-Go HTTP-handler ordering and
  response-encoding branches, expressible without Postgres or a live vendor; unit is
  the correct tier and is where they were caught. No `production`/`fix-forward`
  defects surfaced.
