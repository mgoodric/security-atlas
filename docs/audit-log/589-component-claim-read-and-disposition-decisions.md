# Slice 589 — vendor-claim read API + operator disposition — decisions log

**Type:** JUDGMENT (the disposition states + how a vendor claim surfaces in
the UI + the read-API shape are subjective build-time calls). Parent: slice
512 (OSCAL component-definition import). None of these calls touch the runtime
AI-assist boundary — this is purely human disposition, no LLM.

**Detection-tier classification (slice 353 / Q-13):**

- detection_tier_actual: none (no bug surfaced during the slice; the colon-verb
  route shape was verified against the slice-025 walkthroughs `{id}:finalize`
  precedent before wiring, not discovered by a failure)
- detection_tier_target: integration (the RLS isolation + the disposition →
  audit-row + the claim-is-assertion boundary are the load-bearing behaviours;
  they are pinned by `internal/api/oscalcomponents/integration_test.go`)

## Context

Slice 512 lands a vendor's OSCAL component-definition as vendor-attributed
CLAIMS (`imported_component_claims`, `is_vendor_claim=TRUE`,
`claim_status='asserted'`). The import deliberately stops at `'asserted'`: it
never auto-accepts a claim (P0-512-1 — no fabricated coverage). Slice 512
shipped NO read surface and NO operator action on the claims. This slice
closes that gap with a read API + the accept/reject/needs-info disposition.

## Decisions

### D1 — Disposition states: accept / reject / needs_info

The spec named accept + reject; I added a third, `needs_info`, as the third
disposition target. Rationale: a solo security leader reviewing a vendor's
claims will routinely hit a claim that is neither clearly creditable nor
clearly false — it needs a follow-up question to the vendor. Without a "park
it" state the operator is forced into a premature accept/reject. `needs_info`
is the honest middle state. Slice 512's `claim_status` CHECK already permitted
`asserted | accepted | rejected`; migration `20260608050000` widens it to add
`needs_info`. The import still only ever writes `'asserted'`; all three
non-asserted values are operator-only.

### D2 — Disposition is METADATA on the claim, never a control satisfaction

The load-bearing boundary (canvas invariant #2 / P0-512-1 / the slice-589
anti-criteria): a vendor claim is an ASSERTION, not platform-verified
evidence. The disposition writes ONLY (a) the claim's `claim_status` +
`dispositioned_by` / `dispositioned_at` / `disposition_note` metadata columns,
and (b) an append-only audit row. It NEVER writes to `control_evaluations` or
the evidence ledger, and the `is_vendor_claim = TRUE` CHECK from slice 512 is
left untouched. Accepting a claim records that the operator CREDITS the
vendor's assertion — it does not manufacture a passing evaluation. This is
asserted in the integration test `TestAccept_DispositionsClaimAndAudits` (it
checks `control_evaluations` count stays 0 and `is_vendor_claim` stays true)
and surfaced in the UI via the explicit `vendor-claim-disclaimer` copy. I did
NOT make an accepted claim a first-class evidence ledger record (the spec's
open design call): keeping it a credited-but-distinct claim is the
conservative choice that cannot, by construction, fabricate coverage. Whether
an accepted claim becomes SSP control-implementation evidence is deferred to a
v2 slice (spillover #619).

### D3 — Append-only disposition audit in a NEW dedicated table

I added `imported_component_claim_dispositions` (one append-only row per
disposition event: `from_status -> to_status`, actor, note, time) rather than
reusing `imported_catalog_audit_log`. Rationale: the catalog audit log is
keyed on `catalog_id` and models IMPORT events (immutable provenance of an
import run); a per-claim disposition is a different grain (claim_id) and a
different lifecycle (repeatable — an operator can re-disposition). Mixing them
would muddy both queries. The new table mirrors the slice-021
`exception_audit_log` append-only precedent. The `from_status` is read inside
the same RLS transaction as the update so the trail is accurate even on a
re-disposition (e.g. `accepted -> rejected`).

### D4 — Read-API shape mirrors slice 599 (HTTP, RLS, no bridge)

`GET /v1/oscal/component-definitions` (list) +
`GET /v1/oscal/component-definitions/{id}` (one import's components + claims,
flattened with the SCF-anchor mapping + the `unmapped` flag). This mirrors the
slice-599 `oscalprovenance` read precedent: a pure SQL read over persisted
rows, tenant-scoped via RLS + a defense-in-depth `tenant_id` predicate +ay a
handler-level read gate (admin | approver | owner), and NO compliance-trestle
bridge (the read path is over Postgres rows, so the integration suite runs
bridge-free, exercising the persisted-row read path only). The `{id}` read is
kind-pinned to `'component_definition'` so a
profile/catalog id 404s.

### D5 — Disposition write gate: grc_engineer (IsApprover) | IsAdmin

The read is gated on the broad operator set (admin | approver | owner,
mirroring `oscalprovenance`); the disposition WRITE is gated tighter on
`IsApprover` | `IsAdmin` (the slice-021 exceptions precedent). Rationale:
crediting or declining a vendor's control-implementation assertion is an
adjudication, not a view — a read-only control owner should not be able to
move a claim. The integration test `TestAuthz_NonApproverForbiddenDisposition`
pins that an owner can read but cannot disposition.

### D6 — Colon-verb routes `{id}:accept` / `:reject` / `:needs-info`

The disposition routes use the OSCAL/AIP-style colon-verb shape, matching the
slice-025 walkthroughs `/{id}:finalize` precedent (verified chi extracts the
`id` param correctly with `{id}:verb`). The web BFF maps these to a single
`POST /api/oscal/component-claims/{id}/disposition` route carrying
`{disposition, note}` in the body — a cleaner Next.js route segment than three
colon-verb folders, while the upstream stays canonical-verb.

### D7 — UI: per-claim accept/reject/needs-info + honest assertion framing

The detail page lists every claim with a "Vendor claim" badge, the
claim-status badge, the `unmapped` flag (the slice-512 NULL-anchor case), and
a `vendor-claim-disclaimer` that states plainly the claims are "not
platform-verified evidence" and that "accepting a claim credits the vendor's
statement; it does not satisfy a control on its own." This is the subjective
UX call the JUDGMENT type names: the framing is deliberately defensive so an
operator never reads acceptance as control coverage. The e2e is hermetic
(route-mocks the BFF GET/POST, no shared-DB seed — the slice-594 b219 lesson).

### D8 — Map-an-unmapped-claim UX deferred

The spec floated an inline "map an unmapped claim to an SCF anchor" affordance.
The view surfaces the `unmapped` flag (an "Unmapped to SCF" badge) so the
operator SEES which claims need mapping, but the write-side anchor-mapping flow
is deferred to a focused slice (spillover #620) — it needs an SCF-anchor
picker + its own validation, and folding it in would bloat this slice past M.

## Mechanical checklist (b221/b219 lessons)

- `routes.go` edited + `just openapi-generate` run + `docs/openapi.yaml`
  committed; `scripts/check-openapi-drift.sh` → no drift (228 routes).
- sqlc queries added + `just sqlc-generate` (v1.31.1) run + `internal/db/dbx`
  regenerated and committed; no sqlc drift.
- New integration-tagged package `internal/api/oscalcomponents` enrolled in
  `scripts/integration-shards.txt` (shard A); `audit-integration-enrolment.sh`
  → OK.
- New floored package added to `cmd/scripts/coverage-thresholds.json` at 78
  (measured merged 80.6%).
- Migration `20260608050000_oscal_component_claim_disposition.sql` (+ paired
  `.down.sql`) applied clean, verified reversible + re-appliable.
- e2e `web/e2e/oscal-component-claims.spec.ts` is hermetic route-mock.
