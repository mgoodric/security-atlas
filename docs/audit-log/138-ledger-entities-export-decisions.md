# Slice 138 — ledger entities export decisions log

**Slice:** 138 — Ledger entities export (evidence + policies + exceptions + samples)
**Type:** AFK (Backend / Frontend)
**Date:** 2026-05-19
**Author:** Engineer subagent (Claude)
**Status:** in-progress → PR open

This file records the build-time JUDGMENT decisions. Per
`CLAUDE.md` JUDGMENT process discipline, the maintainer reviews these
post-deployment rather than blocking the merge on a per-decision human
sign-off.

---

## Architectural posture

Slice 138 closes the **per-entity export cluster** following the slice 135
library + slice 145 hardening + slice 137 controls precedent. Four
endpoints, four BFFs, three Export-button surfaces (evidence + policies

- samples — see D6 below for exceptions), one migration covering four
  new meta-audit action values.

The slice 135 P0 anti-criteria + the slice 145 concurrency-cap pattern
are **inherited verbatim** (handler shape, role gate, meta-audit row,
streaming write, Retry-After=30, sync.Once-guarded release fn).
Per-entity nuances are captured below.

---

## D1 — Evidence: payload column EXCLUDED at v1 (LOAD-BEARING)

**Decision:** The evidence export column set does NOT include `payload`
or `payload_json` at v1.

**Rationale:**

- The evidence ledger payload may contain vendor-specific operational
  metadata: an AWS S3 evidence record's bucket-policy JSON, a 1Password
  evidence record's KDF parameter blob, etc.
- Operators who need payload introspection use the evidence-detail page
  (slice 106 — RLS-protected read of one record), NOT bulk export.
- The slice doc P0-A-Ledger-1 codifies this as a merge-blocking gate.

**Implementation:** `evidenceExportHeader()` in
`internal/api/evidence/export.go` enumerates the canonical columns;
`payload` is absent. The unit test
`TestSlice138_EvidenceExportHeader_ExcludesPayload` lights up if a
future contributor adds the column. The BFF route test
(`web/app/api/admin/evidence/export/route.test.ts`) additionally
asserts the column is absent from the streamed body — a regression
guard against drift between handler and BFF expectations.

**Surfaced columns:**

- `id`, `control_id`, `scope_id`, `evidence_query_id` (identity + topology)
- `observed_at`, `ingested_at`, `result`, `freshness_class` (observation)
- `content_hash` (mapped from the `evidence_records.hash` column)
- `payload_uri` (opaque artifact-store pointer — does NOT include payload bytes)
- `valid_until`, `created_at` (lifecycle)

**Rejected alternatives:**

- Include payload but redact at the encoder. Rejected because (a) the
  redaction surface is per-evidence_kind and the v1 catalog is open-
  ended (rejected for the same reason slice 145 rejected per-tenant
  payload redaction); (b) the encoder is in the slice 135 library which
  P0-A9 forbids modifying.
- Two-format mode (payload-on / payload-off). Rejected because v1 users
  cannot be trusted with the on switch absent the redaction surface.

---

## D2 — Policies: full row set INCLUDED (including body_md)

**Decision:** The policies export emits the full row set including
`body_md`. RLS is the only mitigation.

**Rationale:**

- Operators preparing for an audit dump policies as part of the
  evidence package. Stripping `body_md` would defeat the use case.
- The slice doc P0-A-Ledger threat-model addendum identifies this
  trade-off explicitly: large body text is in-scope; RLS guards
  cross-tenant leakage.
- The `acknowledgment_required_role` TEXT[] is joined with `,`
  (single-character separator — matches the slice 137 convention).

**Columns:** id, title, version, status, effective_date, owner,
approver, acknowledgment_required_role, next_review_at, body_md,
created_at, updated_at. The slice 094 `next_review_at` column is
included so calendar-cadence operators see policy review dates in
the dump.

---

## D3 — Exceptions: duration_days computed at the handler

**Decision:** The exceptions export emits a `duration_days` column
computed as `expires_at - requested_at` (truncated to whole days).

**Rationale:**

- Operators ask "what is the average exception duration?" — surfacing
  this as a column means downstream pivots in Excel don't need to
  compute it themselves.
- The DB CHECK constraint guarantees `expires_at <= requested_at +
365 days` AND `expires_at NOT NULL`, so the computed value is
  always >= 0 and <= 365.
- Computing at the handler (not in SQL) keeps the SQL portable across
  Postgres versions (the `INTERVAL` arithmetic varies) and keeps the
  unit test surface narrow.

**Columns:** id, control_id, status, justification, compensating_controls
(joined `|`), scope_cell_predicate (canonical JSON text), requested_by,
requested_at, approved_by, approved_at, denied_by, denied_at, activated_by,
activated_at, effective_from, expires_at, expired_at, duration_days,
created_at, updated_at.

---

## D4 — Samples: row cap raised to 250,000

**Decision:** `defaultSamplesExportRowCap = 250_000`, lifted from the
slice 135 default of 50K.

**Rationale (per slice doc):**

- Sample populations at multi-product orgs span many audit periods x
  many populations x N records each. 50K is too tight; 500K is overkill
  (samples rows themselves are narrow — population_id, n, seed,
  created_by, created_at, joined window/frozen/row_count from populations).
- Lifted to 250K — between the slice 135 default and slice 137's
  500K UCF graph cap.

**JOIN through populations:** the samples table itself does NOT carry
`audit_period_id`; the slice 028 freezing primitive lives on
`populations.audit_period_id`. The export query JOINs
`samples → populations ON (tenant_id, population_id)` to surface the
audit-period link + control_id + frozen_at + row_count for each sample
row.

---

## D5 — Down migration includes defensive DELETE of all 4 new action values

**Decision:** `20260520000010_ledger_entities_export_meta_audit.down.sql`
DELETEs rows with action IN (4 new values) BEFORE the constraint swap.

**Rationale:**

- Inherits the slice 137 lesson: down-migration constraint-swaps fail
  if existing rows hold the soon-to-be-deleted value. Slice 136
  experienced this 3 times.
- The 4 target packages (`internal/api/evidence/`,
  `internal/api/policies/`, `internal/api/exceptions/`,
  `internal/api/audit/`) are NOT in the CI integration-test list per
  `.github/workflows/ci.yml` lines 289–310, so the constraint never
  actually carries these rows under the current CI. But the defensive
  DELETE is cheap insurance against a future test refactor.
- The DELETE is documented in CHANGELOG as destructive in a
  prod-rollback context.

**Migration round-trip verified locally:** up → down → up produced
a clean constraint containing all 13 actions (9 prior + 4 new).
Captured in `migrations/sql/20260520000010_*.{sql,down.sql}`.

---

## D6 — Exceptions: no dedicated list-page UI surface at v1; Export button deferred to follow-on

**Decision:** The exceptions export ships with the backend handler +
BFF + tests, but NO list-page "Export" button is added at v1.

**Rationale:**

- `web/app/(authed)/` has no dedicated `/exceptions` list-page route.
  The exception register is accessed inline from the controls detail
  page (slice 022 + slice 067 patterns).
- Adding an exceptions list UI surface is outside the slice 138 budget.
- The BFF + handler ARE shipped and discoverable to API consumers
  (and to admins via direct URL); only the toolbar button is deferred.

**Spillover filed:** slice 177 — `/exceptions` list-page UI surface.

The other three entities ship Export buttons:

- evidence: `/v1/evidence` page actions toolbar — replaces the
  disabled "Export JSONL" placeholder with 3-format direct-download links.
- policies: `/policies` page actions toolbar — three links before the
  existing "Acknowledgment report" + "New policy" buttons.
- samples: `/audits` page actions toolbar — three links beside the
  existing audit-periods export buttons. (Samples don't have their
  own list page; they're drawn inside an audit period, so the audits
  toolbar is the canonical surface.)

---

## D7 — Plural-of-entity action-value spelling

**Decision:** Action values are `evidence_export`, `policies_export`,
`exceptions_export`, `samples_export` (all plural-of-entity-name).

**Rationale:**

- Matches slice 137 (`controls_export`) and slice 139
  (`audit_periods_export`, `vendors_export`).
- Slice 136's singular `risk_export` is the outlier — only one risk
  register per tenant — and that singular form is preserved.
- The unit tests
  (`TestSlice138_*MetaAuditActionConstant`) lock the literal spelling
  so a typo surfaces at unit-test time, not at migration round-trip
  time (which is how slice 136 lost three CI cycles).

---

## D8 — Inline SQL, not new sqlc queries

**Decision:** Each export handler runs its own RLS-scoped SELECT
inline (via `tx.Query(ctx, "SELECT ...")`), not through a new
sqlc-generated query.

**Rationale:**

- The column projection for each entity is slice-138-specific (e.g.
  evidence excludes payload; samples joins through populations); adding
  a one-off sqlc query for a single handler exceeds the slice budget.
- The inline SELECT is colocated with the canonical column projection
  helper (`*ExportHeader()`) so a future contributor sees both shapes
  at once.
- RLS is enforced by `tenancy.ApplyTenant(ctx, tx)` + defensive
  `WHERE tenant_id = $1` clause (belt-and-suspenders, matches the
  slice 137 pattern).

---

## Coverage gate posture

All four packages (`internal/api/evidence/`, `internal/api/policies/`,
`internal/api/exceptions/`, `internal/api/audit/`) are in the **excludes
list** of `cmd/scripts/coverage-thresholds.json` (lines 121–129 of the
JSON). The coverage gate does NOT enforce a floor for these packages,
so the slice 137 lesson (12 unit tests required to recover from a
floor drop) does NOT apply here.

The slice still ships ~14 unit tests per package targeting:

- canonical header invariants (esp. evidence's payload exclusion)
- row-iter projection in column order
- format validation (3 valid + N invalid)
- role-gate predicate
- meta-audit action-value spelling
- builder chain (NewExportHandler / WithSource / WithLimiter)
- source-dispatch with stub source (happy + error paths)
- early-exit handler branches (401 no-credential, 500 bad-tenant)
- counting writer byte tracking

---

## P0 anti-criteria audit

- **P0-A1** Evidence export EXCLUDES `payload_json`. ✓
  Locked at `internal/api/evidence/export.go` `evidenceExportHeader()`
  - unit test + BFF body-content assertion.
- **P0-A2** Migration filename exactly
  `20260520000010_ledger_entities_export_meta_audit.{sql,down.sql}`. ✓
- **P0-A3** Down migration DELETEs all 4 new action values BEFORE the
  constraint swap. ✓
- **P0-A4** Slice 145 `DefaultLimiter` acquired for each of the 4
  endpoints; `defer release()` on every path. ✓
- **P0-A5** Cross-tenant isolation: unit tests assert the role-gate +
  invalid-tenant 500 path; full integration test surface for cross-
  tenant deferred (no CI integration-test slot for these packages). ✓
- **P0-A6** Samples row cap 250K (default = max). ✓
- **P0-A7** Streaming write only — encoder pipes per-row into the
  response writer via the counting writer. ✓
- **P0-A8** Neutral test tokens — `test-bearer-token`, `*@example.com`,
  no vendor token prefixes. ✓
- **P0-A9** No `internal/export/` library modifications. ✓
- **P0-A10** Coverage gates: all 4 packages on the excludes list, so
  no floor lift needed. ✓ (per-package unit tests still added for
  correctness)

---

## Spillovers filed

- **Slice 177** — `/exceptions` list-page UI surface (D6 above). The
  backend handler + BFF + tests ship; only the Export-button UI is
  deferred.

(Spillover doc to be created post-merge if needed.)

---

## Verification

- `go build ./...` — clean.
- `go test -race -count=1 ./internal/api/...` — all packages green.
- `npx tsc --noEmit` — zero errors from slice 138 changes (pre-existing
  11 errors in `scripts/capture-readme-screenshots.test.ts` are
  unrelated and present on main).
- `npm run test -- --run app/api/admin/evidence app/api/admin/policies
app/api/admin/exceptions app/api/admin/samples` — 18 vitest tests
  pass across the 4 BFF routes.
- `bash scripts/check-openapi-drift.sh` — clean (185 routes documented,
  spec matches generator output).
- Migration round-trip — up + down + re-up locally verified against
  `postgres:16-alpine`; final constraint contains all 13 action values
  in expected order.
