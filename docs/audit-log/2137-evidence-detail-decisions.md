# Issue #2137 — Evidence detail

`GET /v1/evidence/{id}` (`internal/api/controldetail/evidence_detail.go`)
returns one evidence record with its payload. The `/evidence/[id]` page
renders it, and evidence rows on the evidence list and the control page open
it.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Access is the list's, and a denial is a 404

**Decision.** The route is `resource=evidence, action=read` to the OPA
middleware, the same as `GET /v1/evidence`, and the handler repeats the
list's `requireControlRead` role guard. Where the list answers a failed guard
with 403, the detail answers 404, the same as an unknown id or another
tenant's id (RLS hides it). A malformed id is also a 404.

**Why.** The dispatcher asked that evidence ids not be probeable: 403 versus
404 would tell a caller without access which ids exist. A role the OPA
middleware itself denies (a viewer) still gets the middleware's 403 before
the handler runs. That reveals the role, not the id, and the ticket accepts
either 403 or 404.

## D2 — Redaction rules are re-applied on read

**Decision.** The payload is stored redacted (slice 015 redacts at ingest).
The handler also looks up the kind's current `x-redaction-rules` from the
schema registry and runs `redact.Apply` before serialising. The stored row is
never rewritten. When the registry is the in-memory one, which has no rules,
the stored payload is served as is.

**Why.** A rule added after a record was ingested would otherwise not hide
that record's field. Running the existing redactor late is the shape
ADR-0020 §3 describes for erasure, so no second redactor is written. The
payload is never logged.

## D3 — Erasure tombstones: nothing to apply yet

**Decision.** No tombstone handling was written. ADR-0020 designs the
erasure tombstone (`privacy.erasure_audit_log` plus in-place `<<ERASED>>`
redaction), but neither the table nor the erasure verb exists on `main`. A
record redacted in place by a future erasure would still be served correctly,
because the endpoint returns the stored columns as they are.

**Why.** Writing code against a table that does not exist would be guessing
its shape. When erasure lands, its PR owns the read-side flag (for example,
marking a record as erased).

## D4 — The list's row drawer is replaced by the detail page

**Decision.** A click on an `/evidence` row navigates to `/evidence/{id}`
instead of opening the slice-099 inline dialog. The dialog showed only the
list's wire fields; the detail page shows those plus the payload. Control
page evidence-stream rows become links to the same page.

**Why.** The ticket says rows open an evidence detail view. Two views of one
record would drift. The drawer's testids sat in the UI-honesty spec's
`allowedExtraTestIds` only, and its e2e assertions were already commented out.

## D5 — The Playwright spec mocks the list and not the detail

**Decision.** `evidence-detail.spec.ts` route-mocks `/api/evidence` (the
list) to return the seeded row, then lets the detail read go through the real
BFF and platform against `fixtures/e2e/evidence-detail.sql`.

**Why.** Other fixtures add rows to the same ledger, so the first row on an
unmocked list is not deterministic. The detail read is the thing under test,
so it stays real.
