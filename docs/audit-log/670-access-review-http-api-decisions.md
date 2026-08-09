# OE-670 — access-review campaign HTTP API: decisions log

**Type:** JUDGMENT
**Status at build:** OPENENGINE-670 filed by the OE-628 close-out as the
HTTP-surface child of the store-first decomposition (OE-628 decision D5,
mirroring OE-630 → OE-663). This fire lands `internal/api/accessreviews`
(handlers + authz + package-local ReadStore), the registrar, the OPA
enrolment, and the unit + integration test suites.

- detection_tier_actual: none
- detection_tier_target: none

The calls below were made by the implementing engineer and recorded here
rather than blocking the merge on a human sign-off; the maintainer iterates
post-deployment.

## What this slice does (one paragraph)

An RLS-scoped HTTP API over `internal/accessreview`: create a campaign
(SCIM-sourced JSON or manual-CSV multipart), list campaigns, read one
campaign with its completion rollup and review items, attest keep/revoke
per item as the assigned reviewer, download the revoke list as CSV (the
operator enforcement handoff — no route revokes access), and complete a
campaign (which emits the `access_review.completion.v1` CC6.3 evidence).
Six routes registered via `register_accessreview.go`; reviewers can drive
a certification end-to-end over HTTP.

## Decisions made

### D1 — `Complete` keeps the store's direct evidence insert (OE-628 D3 follow-through)

OE-628 D3 deferred the choice between the store's single-transaction
direct `evidence_records` INSERT and the policyacks-style
`ingest.Service.Process` orchestration at the handler, noting the
schema-conformant payload makes the swap mechanical. **Decision: the
tx-atomicity argument wins — the direct insert stays.** The store's
`Complete` performs the pending-items check, the evidence insert, the
campaign status flip, and the `evidence_record_id` backfill in ONE
transaction with a deterministic idempotency key
(`access-review:<campaign>:completion`). Routing emission through
`ingest.Service` at the handler would split that into
domain-write-then-emit and reintroduce the policyacks best-effort window
(ack row authoritative, evidence backfilled later). That posture is
acceptable for policyacks because the ack row is itself the authoritative
artifact; here the evidence record IS the completion artifact for CC6.3 —
a "completed" campaign with no evidence record would be exactly the state
the control exists to preclude. The handler is a thin
`store.Complete` → 200/404/409 mapping; re-completing is an idempotent
200 returning the prior evidence id. If a future slice moves emission
into ingest (e.g. for receipt/audit uniformity), the OE-628 D1
schema-conformant payload still makes it a mechanical swap.

### D2 — Reviewer identity is the verified credential, mismatch is 403 via a pre-check read

The store's `Attest` folds "item missing" and "assigned to someone else"
into one `ErrNotFound` (its `UPDATE ... WHERE reviewer_id = $3` predicate).
The issue's acceptance criteria require distinguishing them: non-assigned
reviewer → 403, missing/cross-tenant → 404. **Decision:** the handler
resolves the attesting reviewer from the verified credential
(`jwtmw.SubjectUserID(cred.UserID)`, never a body field — the slice-384
repudiation posture), then pre-checks the assignment via the
package-local `ReadStore.ItemReviewer(campaignID, itemID)` (tenant- and
campaign-scoped, so a cross-tenant or wrong-campaign item stays an
indistinguishable 404, never a 403 that would confirm existence). Only an
existing, same-tenant item assigned to a different reviewer earns the 403. The store's own reviewer predicate remains the authoritative
enforcement (the pre-check is advisory; a race falls back to the store's
404).

### D3 — Campaign-specific multipart contract, reusing the artifacts-upload shape

The manual-CSV create rides `multipart/form-data`: fields `name`,
`due_at` (RFC3339), `reviewers` / `scope_systems` / `scope_entitlements`
/ `scope_user_ids` (repeatable and/or comma-separated), plus the CSV in
the `file` part (columns `system,entitlement,user_id[,email,source_ref]`
— the OE-628 `parseManualCSV` contract, unchanged). This reuses the
in-repo upload conventions from `internal/api/artifacts` (single `file`
part, `http.MaxBytesReader` cap BEFORE the multipart parse, 413 on
oversize) rather than inventing a new envelope; the manual-connector CSV
contract was considered and not adopted — it is a connector-process
contract (source-side credential holder emitting via `Push`), not an
HTTP-API request shape, so there was no conflict requiring escalation.
JSON-body creates are SCIM-sourced only; `source=manual_csv` over JSON is
a 400 pointing at the multipart shape.

### D4 — CSV parse failures map to 422 by message fragment

`parseManualCSV` returns fmt-wrapped errors ("csv missing %s column",
"read csv header", "read csv"), not sentinels — OE-628 had no HTTP
consumer to shape them for. **Decision:** the create error mapper
recognizes them by their "csv" message fragment and returns 422 with the
store's message; every `parseManualCSV` error carries the fragment and no
other error reachable from `CreateCampaign` does. Sentinel-izing the CSV
errors in the store was deliberately NOT done in this fire (it would
touch the merged OE-628 surface for a cosmetic gain); if a future slice
adds one more consumer, promote them to sentinels then.

### D5 — OPA enrolment mirrors OE-663: `access-reviews` in grc_engineer's writable set only

The slice-035 middleware derives `resource.type = "access-reviews"` from
the path; the production write gate needs the resource in a role's
writable set. **Decision:** enroll it in
`policies/authz/grc_engineer.rego` exactly as OE-663 enrolled
`personnel-security` — the campaign operator persona is the GRC engineer.
`control_owner` reviewers are NOT enrolled in this fire: the v1 reviewer
persona is the program operator, and widening the write surface to
control_owner is a deliberate one-line follow-up for the slice that lands
the reviewer-facing UI (the handler's assignment binding already bounds
what an enrolled reviewer could do). The handler-level
`hasProgramRead`/`hasProgramWrite` guards (admin / approver / owner-role
derivation, the actionplans shape) remain the defense-in-depth twin and
the testable enforcement point (test servers leave OPA nil).

### D6 — Reads the store lacks live in a package-local ReadStore

The OE-628 store has no campaign index, no single-campaign load, and no
item listing. **Decision:** they live in
`internal/api/accessreviews/store.go` as a read-only `ReadStore` (plain
SELECTs, per-call tx + tenant GUC), the OE-663 / internal/api/calendar
precedent for API-owned reads over another slice's tables — the merged
store's surface stays untouched.

### D7 — Coverage posture

`internal/api/accessreviews` ships a pure-Go unit tier
(`helpers_test.go`: guards, error mapping, wire mappers — the slice-290
pattern) plus the Postgres-backed integration suite, and is enrolled on
integration Leg B2 next to `internal/accessreview` and the OE-663
sibling. No per-package floor is added in
`cmd/scripts/coverage-thresholds.json` — the same posture as
`internal/api/personnelsecurity` (unlisted; the handler package's
correctness gate is its integration suite). Existing floors are
unchanged.
