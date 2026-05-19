# Slice 136 — Risk register data-export decisions

Slice 136 (`docs/issues/136-risk-register-export.md`) wires the slice 135
data-export library + slice 145 concurrency cap into the risk register surface.

The slice is typed **AFK** (not JUDGMENT) — the slice 135 + 145 patterns are
load-bearing precedent, and slice 136 is mostly a mechanical "one more entity"
wire-up. The decisions below are the build-time calls that fell outside the
boilerplate.

## D1 — Canonical column set + ordering

**Decision:** Eighteen columns in the following order:

```
id, title, description, category, methodology,
treatment, treatment_owner, accepter, instrument_reference,
inherent_score, residual_score, severity,
org_unit_id, themes,
review_due_at, accepted_until, created_at, updated_at
```

**Why:**

- **Identity → narrative → classification → posture → ownership → audit.**
  The ordering follows the way an auditor reads a risk register row:
  identify the row (id, title, description), classify it (category,
  methodology), see what is being done about it (treatment,
  treatment_owner, accepter, instrument_reference), see how bad it is
  (inherent_score, residual_score, severity), see where it lives in the
  org (org_unit_id, themes), see when it needs review (review_due_at,
  accepted_until), and finally the audit columns (created_at,
  updated_at). A Fortune 50 SOC 2 auditor's Excel filter pass usually
  goes left-to-right through these in roughly that order.
- **Stable contract.** The slice doc explicitly mentions a canonical
  column set, and the column list is the load-bearing wire shape for
  downstream consumers (board pack template macros, quarterly-report
  scripts, the auditor handoff zip's index sheet). Changing it later is
  a breaking change; locking it now means future contributors must
  surface the conflict via the slice doc + decisions log rather than
  silently rearranging.
- **JSON fields stay as raw JSON blobs.** `inherent_score` and
  `residual_score` are JSONB on disk; the export emits them as a single
  stringified blob per cell rather than fanning each subkey
  (likelihood, impact, severity) into its own column. The fan-out
  would inflate the column count by ~2x and lock the schema to the
  current 5x5 shape — methodologies that score on different axes (FAIR
  uses LEF/PLM/SLEF; ISO 31000 uses qualitative bands) would have to
  emit empty cells. Keeping the JSON as JSON is methodology-agnostic.
- **`severity` is derived, not stored.** The handler computes severity
  from the inherent_score JSONB via the existing `severityOf` helper
  in `internal/api/risks/handlers.go` (slice 067). Surfacing it as its
  own column avoids forcing downstream consumers to re-implement the
  derivation in jq / Excel / pandas / SQL — which would diverge over
  time.

**Rejected:**

- **Excluding the JSON score columns entirely.** Would lose information
  the auditor needs (the methodology-specific score components a
  quarterly report deep-dive would cite). The JSON blob is opaque to a
  CSV consumer but fine for a JSON consumer; the row stays usable in
  both.
- **Fanning JSON score subkeys into individual columns.** Methodology-
  coupled (FAIR vs NIST vs ISO 31000 produce different keys). Would
  force the export to bias toward one shape.
- **Adding `linked_control_ids` as a JSON-array column.** Would mix two
  granularities of data in one row (the risk + its O(N) control
  refs). Slice 137 (controls UCF graph export) is the natural home
  for a risk↔control linkage export; a separate
  `/v1/risks/{id}/controls/export` endpoint can ship as a future
  spillover when there's demand. **Provisional `linked_control_count`
  considered and rejected**: a count without the ids forces a second
  query for any downstream consumer that wants to actually USE the
  linkage, so it adds friction without buying anything. The export
  stays narrow.

## D2 — Audit-period freezing for risk-register exports

**Decision:** Risk-register exports do NOT clamp to `frozen_at` at v1.
The export captures the live state of the register.

**Why:**

- **Risks don't carry an `observed_at` field the way evidence does.**
  The slice 135 audit-log export clamps `to ≤ frozen_at` because audit
  events have an `occurred_at` timestamp; rows past the horizon are
  meaningfully excludable. Risks have `created_at` and `updated_at`,
  but the audit-period freezing constraint is about the **state**
  attested in the period, not the row's mutation timestamp. A risk
  whose `updated_at` is after `frozen_at` could still be the same risk
  that existed at `frozen_at` — its updated_at moved because someone
  touched the description.
- **The constitutional contract for risks is the slice 028 audit-
  period attestation workflow, not a wire-level clamp.** When an
  audit period is frozen, the slice 028 workflow snapshots the
  attested risk state at `frozen_at` into the period's attestation
  ledger. A separate "export the attested risk state for audit period
  N" workflow is a future slice (call it 136-A or fold into a sample
  export); it queries the attestation ledger, not the live `risks`
  table.
- **The slice doc inherits slice 135 AC-12 in spirit, but the
  semantics differ per entity.** The slice 135 P0-A-Risk-1 and
  P0-A-Risk-2 are explicit; the freezing constraint is not. Recording
  this decision here so the next person who reads the slice doc
  doesn't conclude the export is missing a constraint.
- **Meta-audit still records the export.** Operators who want a
  point-in-time snapshot can timestamp the export attempt — the
  `me_audit_log` row with `action='risk_export'` carries the exact
  moment the bulk read happened, so forensic reconstruction is still
  available even though the wire body is "live state at export time".

**Rejected:**

- **Clamping `updated_at ≤ frozen_at`.** Would surface a stale row
  state for any risk modified after `frozen_at`, which is not what
  the constitutional invariant actually says — invariant #10 is about
  "evidence collected before the freeze", not "rows mutated before
  the freeze". Applying it to risks would produce semantically
  meaningless output (you'd see the title at frozen_at but no way to
  know which other columns moved since).
- **Refusing exports when any frozen period overlaps the tenant.**
  Operational cost vs benefit is unfavorable. The risk register is a
  live operational surface; quarterly board packs need exports every
  90 days, almost always during periods that overlap a frozen audit.
  Refusing the export would force operators to ALWAYS use the slice
  028 attestation workflow, which is heavier and rarely the right
  shape for "Excel-ize the current register for the board pack
  deadline tomorrow morning."
- **Filing as a follow-on slice.** Considered, decided against —
  the slice 028 attestation workflow IS the audit-period freezing
  story for risks. A separate "export per-period attestation" slice
  would re-implement what slice 028 already does. If quarterly
  board-pack workflows need the per-period view, it's a UI surface
  on the existing slice 028 attestation, not a new export endpoint.

## D3 — Row cap default

**Decision:** Default row cap = **50,000 risks**.

**Why:**

- **Three orders of magnitude of headroom.** A large enterprise
  security program runs O(10²) to O(10³) risks; Fortune 50s have
  reported counts in the low thousands. 50K leaves ~50x headroom for
  an organisation that subdivides risks across business units +
  subsidiaries to the absolute extreme.
- **Smaller than slice 135's 100K (audit log) and slice 137's
  proposed 500K (UCF graph) because the entity volume is smaller.**
  The risk register is a curated artifact (someone wrote each row by
  hand or via aggregation); the audit log is a fire-hose (every
  user action produces rows). Different volume profiles → different
  caps.
- **The cap is a defense, not a target.** Hitting 50K risks in a
  single tenant is a strong signal something is wrong (the
  aggregation rules engine is misconfigured, or the tenant has been
  loaded with test data). The 413 body tells the operator to contact
  the maintainer; that's the right friction.
- **Streaming write keeps the implementation honest.** The cap is
  enforced by asking the store for `(rowCap + 1)` rows and detecting
  the overflow BEFORE writing headers; no body bytes ever leave the
  process on the 413 path.

**Rejected:**

- **Same 100K cap as slice 135's audit log.** Bigger than necessary
  for the risk register's volume profile; the cap should be a
  defense calibrated to realistic risk counts, not a uniform value
  for visual symmetry.
- **Smaller cap (e.g. 5K).** Would put real friction on legitimate
  large tenants. 5K is "Fortune 100 program manager exporting the
  full register for the next board cycle" territory — clipping the
  cap there would force the operator to filter the export every
  time, which the slice doc explicitly rules out (no `from`/`to`
  filter at v1; the register is small enough to dump whole).

## D4 — Storage of `linked_control_ids` (not included)

**Decision:** `linked_control_ids` is NOT a column on the v1 risk
export. Slice 137 (controls UCF graph export) is the natural home for
the risk↔control linkage; a separate export is filed only when there's
demand.

**Why:**

- **The risk row has O(N) linked controls; serializing them inline
  inflates the CSV unpredictably.** A risk with 200 linked controls
  produces a 200-id JSON-array cell that breaks Excel column widths
  and is hard to filter in pandas / jq.
- **The linkage already lives in `risk_control_links`** and is
  reachable via `GET /v1/risks/{id}` — single-risk callers have the
  data. A bulk-linkage export is a separate concern from a bulk-
  risk-register export.
- **Slice 137 (UCF graph) exports the linkage as part of the
  graph projection.** A risk↔control export on top of slice 137
  is a small spillover if it turns out to be needed; until then, the
  v1 surface is the per-risk GET.

**Rejected:**

- **Pipe-separated `linked_control_id` cell** (e.g.
  `id1|id2|id3`). Solves the JSON-array width problem but introduces a
  format that no downstream consumer expects. CSV `,`-injection
  threats are also non-trivial — the OWASP CSV-injection mitigation
  in slice 135's encoder doesn't cover separator confusion.
- **`linked_control_count` integer column.** Considered initially in
  the slice doc as a "compact form". Rejected at pickup because a
  count without the ids forces a second query for any consumer that
  wants the linkage — adds noise to the export without adding signal.

## D5 — Meta-audit action distinct from slice 135

**Decision:** `risk_export` (not reusing `audit_log_export`).

**Why:**

- **Forensic separability.** A query like
  `WHERE action = 'risk_export'` cleanly enumerates risk-register
  extractions; `WHERE action = 'audit_log_export'` cleanly enumerates
  audit-log extractions. Different entities, different downstream
  consumers (risk registers → board packs; audit logs → auditors), so
  they deserve distinct labels.
- **Migration cost is one ALTER TABLE.** The `me_audit_log.action`
  CHECK constraint already enumerates each value; adding `risk_export`
  is a 4-line migration. The future per-entity exports (slices 137,
  138, 139) will each add their own action value; this slice
  establishes the per-entity naming pattern.
- **Slice 135 D8 explicitly anticipated this.** "Different action
  enables downstream consumers... to distinguish 'operator browsed
  the log' from 'operator dumped the log'." The same logic applies
  here: a risk register dump is materially different from an audit
  log dump.

**Rejected:**

- **Reusing `audit_log_export`.** Would conflate the two extraction
  surfaces in the forensic record. A spike in risk exports (one
  operator preparing for the board) would look like a spike in
  audit-log exports (potential incident response), which is
  meaningfully bad signal.

## D6 — Role gate via `requireProgramRead` (not `callerAllowedUnified`)

**Decision:** The export endpoint uses the same
`requireProgramRead` helper as slice 067's read endpoints, NOT slice
135's `callerAllowedUnified`.

**Why:**

- **Slice 067 already established the program-read role set for risk
  reads.** Admin + approver + owner-roles are the existing read gates
  for `/v1/risks`, `/v1/risks/theme-heatmap`. Using the same gate for
  the export ensures the admit set is bit-for-bit identical to the
  read endpoints — an operator who CAN read the register CAN export
  it. Different gate would invite inconsistency.
- **`callerAllowedUnified` is audit-log-specific.** It probes for
  `auditor` or `grc_engineer` roles in `user_roles`, which is the
  audit-log read set. The risk register is the program manager's
  surface, not the auditor's.
- **Defense-in-depth, same posture as slice 067.** The slice 035 OPA
  middleware is the canonical authz gate in production; this guard is
  the testable second leg.

**Rejected:**

- **Pure `IsAdmin`-only gate.** Too narrow — would lock approvers and
  owner-role holders (the v1 risk-program operators) out of their
  own register export.
- **Open access (any authenticated bearer).** A bare push credential
  has no business reading the risk register; the program-read gate
  is the same friction slice 067 enforced.

## D7 — Concurrency-cap test posture

**Decision:** The slice-136 concurrency-cap test asserts a LOOSER
invariant than slice 145's: "at least 1 denial out of N=5 concurrent
workers with cap=2". The slice 145 test asserts exactly "2 OK, 3
denied".

**Why:**

- **Worker scheduling is non-deterministic.** With cap=2 and 5
  goroutines that each hold a slot for milliseconds (the empty-
  tenant streaming write is fast), the schedule "2 acquire, 3
  refused" is the WORST case. In practice the early workers
  often release before the later workers acquire, and you see
  3 or 4 succeed.
- **The load-bearing invariant is "at least one was denied".** That
  proves the cap mechanism fired. Tighter assertions become flaky
  under CI load.
- **Slice 145's tighter test holds because its export DOES heavier
  work** (the audit-log aggregator queries the unified view; the
  streaming write is materially slower). The risk register has no
  rows in the test, so the streaming write is microseconds.

**Rejected:**

- **Identical test shape to slice 145.** Would be flaky in CI.
- **Skipping the cap test entirely.** The cap IS the slice 145
  inheritance; not testing it would leave the inheritance
  unverified.

## P0 anti-criteria audit

All P0 anti-criteria from the slice doc + the orchestrator's
amplified set honored:

- **P0-A1** (no `treatment_narrative` column): Verified by unit test
  `TestRiskExportHeader_ExcludesTreatmentNarrative` AND integration
  test `TestSlice136_RiskExportReturnsExpectedColumns` (asserts the
  CSV header does not contain `treatment_narrative`).
- **P0-A-Risk-2** (include `org_unit_id`): Verified by unit test
  `TestRiskExportHeader_IncludesOrgUnitID`.
- **P0-A2** (no widening beyond risks): Verified — slice 136 ships
  only `/v1/risks/export` + its BFF. Controls / ledger / audit-
  periods exports are explicitly out of scope (slices 137/138/139).
- **P0-A3** (concurrency cap acquired): Verified by integration test
  `TestSlice136_ConcurrencyCapInherited` (5 concurrent workers under
  cap=2; at least one 429).
- **P0-A4** (no `?include_payload` flag): Verified — `parseRiskExportFormat`
  parses only `?format=`; no payload flag wired. Risks have no
  payload_json column.
- **P0-A5** (no modification to `internal/export/`): Verified — only
  the consumer-side calls into the library; no library changes.
- **P0-A6** (no vendor-prefixed test tokens): Verified — test
  credentials use locally-scoped string literals (`test-risk-export-id`,
  `user-risk-export-test`).
- **P0-A7** (50K row cap): `defaultRiskExportRowCap = 50_000`.
- **P0-A8** (streaming write): Encoder consumes `iter.Seq[[]string]`
  pull-style; no full-result-set buffering. The `exportCountingWriter`
  forwards bytes per-row.
- **P0-A9** (goroutine-leak hygiene): `defer release()` after every
  successful `limiter.Acquire`; release fn is `sync.Once`-guarded
  (slice 145 D5 inheritance).
- **P0-A10** (cross-tenant test): `TestSlice136_CrossTenantIsolationAllThreeFormats`
  seeds risks in tenant B and exports from tenant A; asserts tenant
  B's risk id + a unique probe title are absent from the body in CSV,
  JSON, AND XLSX formats.

## Test footprint

- `internal/api/risks/export.go` — new file, 460 lines incl doc
  comments.
- `internal/api/risks/export_test.go` — new file, 4 unit tests
  (header invariants + row projection).
- `internal/api/risks/export_integration_test.go` — new file, 7
  integration tests (success column set, all three formats, cross-
  tenant isolation × 3, meta-audit on every outcome, role gate,
  concurrency cap, empty tenant).
- `migrations/sql/20260519000010_risk_export_meta_audit.sql` — new
  migration, extends `me_audit_log.action` CHECK to permit
  `risk_export`.
- `web/app/api/risks/export/route.ts` — new BFF (pure passthrough).
- `web/app/api/risks/export/route.test.ts` — 6 vitest tests for the
  BFF (401 / 200 / 400 / 403 / 429 / cookie isolation).
- `web/lib/api/risks-export.ts` — new client URL builder.
- `web/lib/api/risks-export.test.ts` — 5 vitest tests for the URL
  builder.
- `web/app/(authed)/risks/page.tsx` — wires CSV / JSON / XLSX
  download buttons in place of the previous disabled `Export CSV`
  placeholder.
- `internal/api/httpserver.go` — one mount line for the new route.

## Spillovers filed

None. Two near-misses considered and decided against:

- **Risk↔control linkage export** (D4). Filed only when the maintainer
  surfaces demand; slice 137 may absorb it via the UCF graph
  projection.
- **Audit-period-frozen risk-state export** (D2). Belongs in slice
  028's attestation workflow, not a separate export endpoint.

If a downstream use case surfaces a need beyond these (e.g. column
selection / `?columns=...` query param), file as the next-available
slice number per Amendment 2.
