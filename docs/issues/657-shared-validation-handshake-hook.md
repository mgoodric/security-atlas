# 657 — webhookrecv: first-class shared validation-handshake hook

**Cluster:** Connectors
**Estimate:** S-M (1-2d)
**Type:** refactor (shared-package seam extraction)
**Status:** `ready`

## Narrative

Surfaced during slice 522 (Azure Event Grid subscribe profile). TWO connectors now
own a vendor-specific **validation-handshake-in-adapter** wrapping the shared
`connectors/shared/webhookrecv` verify-first skeleton:

- **Intune** (slice 557): a Microsoft Graph `validationToken` **query-param** echo →
  `200 text/plain`, no record (`connectors/intune/cmd/atlas-intune/cmd_webhook.go`
  `validationHandler`).
- **Event Grid** (slice 522): a `Microsoft.EventGrid.SubscriptionValidationEvent`
  **body event** carrying a `validationCode` → `200 application/json
{"validationResponse":"<code>"}`, no record
  (`connectors/azure/internal/eventgrid/validation.go` `ValidationHandler`).

The slice-557 decisions log explicitly flagged: "file a first-class shared
validation-handshake hook in `webhookrecv` when a 2nd validation-handshake connector
arrives." Event Grid **is** that 2nd connector. Both adapters share the same shape:
intercept a non-record validation request **FIRST** (before the verify-first
delivery path), echo a vendor-specific response, build no record. They differ only in
(a) how the validation request is **recognised** (query param vs body event), and
(b) the **response encoding** (text/plain token vs JSON `validationResponse`).

This slice extracts that shape into a reusable `webhookrecv` seam — e.g. a
`ValidationHook` interface (`Detect(req, body) (response []byte, contentType string,
ok bool)`) the shared `Handle`/server wiring consults BEFORE the verify-first path —
so a third validation-handshake connector configures it declaratively rather than
re-authoring the wrapper. Both existing adapters then adopt the shared hook; their
current tests stay green (the externally-observable behaviour is unchanged).

## Acceptance criteria

- [ ] **AC-1.** `webhookrecv` exposes a reusable validation-handshake seam that runs
      BEFORE the verify-first delivery path and builds no record.
- [ ] **AC-2.** The Intune `validationToken` handshake adopts the shared seam; its
      slice-557 tests stay green (200 text/plain echo, no record).
- [ ] **AC-3.** The Event Grid `SubscriptionValidationEvent` handshake adopts the
      shared seam; its slice-522 tests stay green (200 JSON `validationResponse`, no
      record).
- [ ] **AC-4.** The shared seam keeps the verify-first invariant: a NON-handshake
      delivery still reaches the credential check before any record (no bypass).
- [ ] **AC-5.** `webhookrecv` coverage floor maintained; the two adapters' floors
      maintained.

## Anti-criteria

- **P0-657-1.** Does NOT widen the platform-side wire (push only, invariant #3);
  receiver stays source-side.
- **P0-657-2.** Does NOT let a validation handshake become a record-forgery or
  verify-bypass surface for real deliveries.

## Dependencies

- **#656** (`connectors/shared/webhookrecv`) — merged.
- **#557** (Intune validation handshake) — merged.
- **#522** (Event Grid validation handshake) — the 2nd consumer that triggers this.
