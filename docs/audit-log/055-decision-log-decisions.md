# Slice 055 — Decision Log CRUD + linkage — decisions log

**Slice type:** `AFK` (the ten ACs are mechanically verifiable). This log is
not a JUDGMENT-slice sign-off gate — it records the **build-time judgment
calls** the slice surfaced so post-deployment iteration is tractable. None
of these blocked merge.

## Decisions made

### D1 — `decisions_audit` is the audit-log table name, distinct from the pre-existing `decision_audit_log`

The issue (AC-3) names the Decision Log's mutation log `decisions_audit`.
While building, a name-collision risk surfaced: slice 035 (migration `_018`)
already shipped a table called **`decision_audit_log`** — but that is the
OPA *authorization* allow/deny log (it records every RBAC/ABAC `Decide`
call's `result IN ('allow','deny')`). It is a completely different concept
from the Decision Log's domain-mutation trail.

Resolved: keep the issue's chosen name `decisions_audit` (plural-noun +
`_audit`). It is unambiguous against `decision_audit_log` and the two never
need to be joined. The migration comment calls out the distinction
explicitly so a future reader does not conflate them.

**Confidence: high.** The two tables model orthogonal concerns; the issue's
name was deliberate.

### D2 — `decision_id` format `DL-YYYY-MM-DD-NNNN`: `NNNN` is a per-tenant, per-day sequence

AC-1 specifies the format `DL-YYYY-MM-DD-NNNN` but does not define what
`NNNN` counts. Resolved:

- The date portion is `decided_at`'s **UTC calendar date** (not the row's
  `created_at`) — `decided_at` is the human-set, audit-meaningful date.
- `NNNN` is a **zero-padded, per-tenant, per-day sequence**: count the
  decisions already filed in that tenant whose `decided_at` falls on the
  same UTC calendar date, add one. So the first decision dated 2026-05-14
  is `DL-2026-05-14-0001`, the second `-0002`.
- The count is computed **in Go** (a start-of-day .. start-of-next-day
  `decided_at` window passed as two bound `timestamptz` parameters), never
  as SQL date arithmetic on a placeholder — this sidesteps the documented
  pgx `$N`-in-two-type-contexts prepare error (SQLSTATE 42P08).
- Concurrent same-day creates are guarded by the slice-052
  `UNIQUE (tenant_id, decision_id)` constraint. v1 is a solo-operator tool
  (canvas §1) so a same-millisecond double-create is not a real workload;
  a retry loop is deliberately *not* added — if a collision ever fires it
  surfaces as a clean 500 and the operator retries. A retry loop is the
  obvious follow-up if the tool ever goes multi-writer.

_Options considered:_ a global per-tenant monotonic counter (rejected — the
date in the id would then not match `decided_at`, breaking the human-legible
contract); a UUID-suffixed id (rejected — the issue's format is explicit and
auditors quote these ids).

**Confidence: high** on the format; **medium** on the no-retry-loop call —
revisit if the deployment model ever becomes concurrent-writer.

### D3 — `audit_narrative_opt_out` is a per-decision boolean, not a tenant-config table

The P0 anti-criterion says: "Do NOT expose decision narratives in OSCAL
export for tenants that have opted decisions out of audit-narrative emission
(config flag)." No tenant-configuration table exists anywhere in the schema.

Resolved: add a per-decision boolean column `audit_narrative_opt_out` to
`decisions` (migration `_030` ALTER, default `false`). The OSCAL emission
function (`EmitRemark`) drops any decision with the flag set.

_Why per-decision, not per-tenant:_ opt-out is a per-record editorial
judgement ("this particular tradeoff is internal, keep it out of the
auditor's SSP") — an all-or-nothing per-tenant switch is both coarser and
would need a net-new config surface. The per-decision flag is the smallest
shape that satisfies the anti-criterion and is strictly more expressive.

**Confidence: medium-high.** Revisit if a tenant-config table later lands
for unrelated reasons — at that point a tenant-level default could layer
*above* the per-decision flag without a schema change to `decisions`.

### D4 — AC-7 ships an exported emission function, not a live OSCAL export

AC-7 requires decisions to "appear in the SSP narrative as `<remarks>`
blocks" when slice 030 runs the OSCAL export. Slice 030 (OSCAL SSP+POA&M
export) is **not on main yet**.

Resolved: `internal/decision/narrative.go` exports `EmitRemarkText`,
`EmitRemark`, and `EmitRemarks` — pure, deterministic functions that render
a decision (plus its resolved linked-control UUIDs and risk identifiers)
into the exact AC-7 format string. Slice 030 will call `EmitRemarks` from
its OSCAL pipeline. Slice 055 unit-tests the format and the opt-out / no-
linked-controls exclusions; the integration test exercises it against a
real decision with real links.

This mirrors the 060→062 placeholder precedent (a slice ships the seam its
downstream dependency needs, fully tested, rather than blocking on the
dependency). The format string is frozen here so slice 030 inherits a
tested contract.

**Confidence: high.** The function is pure and fully tested; the only
slice-030 work left is the call site.

### D5 — the `cross_tenant_link_denied` audit row is written in a fresh transaction

A failed link INSERT against a foreign-tenant target trips the composite-FK
violation (SQLSTATE 23503), which **aborts the surrounding transaction** —
any subsequent statement in that tx fails with SQLSTATE 25P02. So the
denied-attempt audit row (AC-9) *cannot* be written inline.

Resolved: `AddLink` detects the FK violation, lets the poisoned transaction
roll back, then writes the `cross_tenant_link_denied` audit row in a second,
fresh transaction (`writeAuditTx`). The happy path (link succeeds) still
writes its `link_added` audit row in the *same* tx as the INSERT, so a
successful link without an audit row remains impossible.

Fail-ordering: the 404 is the security boundary and is always returned; the
audit write is the courtesy and is best-effort *after* the boundary. If the
second tx also fails, the caller still gets `ErrCrossTenantLink` (→ 404) —
fail-closed on the boundary, fail-open only on the audit courtesy.

**Confidence: high.** Verified by `TestAddLink_CrossTenantDenied` (asserts
exactly four `cross_tenant_link_denied` rows across the four link kinds).

### D6 — the daily overdue job dedups on the `overdue_notified` audit row, not a notification-table key

AC-6 / the P0 anti-criterion: one notification per overdue decision, never
repeated. The `notifications` table has no natural dedup key.

Resolved: the `Notifier` writes an `overdue_notified` row to
`decisions_audit` in the **same transaction** as the notification INSERT,
and probes `CountDecisionOverdueNotifications` (served index-only by the
partial index `idx_decisions_audit_overdue_notified`) before emitting. The
audit row *is* the authoritative "already notified" marker — append-only,
per-decision, queryable. A second sweep on the same (or any later) day for
a still-active, still-overdue decision emits nothing.

_Why not a `NOT EXISTS` join inside `ListOverdueDecisions`:_ that query also
backs the plain `GET /v1/decisions/overdue` endpoint, which must show *all*
overdue decisions regardless of notification state. Keeping the dedup probe
in the job (not the shared query) keeps the read-surface query reusable.
The resulting per-decision probe is an N+1-shaped pattern, but it is bounded
by the (small) count of overdue decisions per tenant and runs once a day —
an acceptable, documented tradeoff.

**Confidence: high.** Verified by `TestOverdue_NotifierEmitsOncePerDecision`
(two sweeps → exactly one `overdue_notified` row).

### D7 — supersession is its own endpoint, not a `PATCH status=superseded`

`PATCH /v1/decisions/{id}` can move a decision to `revisited` or `expired`,
but it **rejects** `status=superseded` (returns `ErrWrongState`). The only
path to `superseded` is `POST /v1/decisions/{id}/supersede`, which requires
the `superseded_by` replacement id.

_Why:_ supersession is not a simple status flip — it must also populate the
`superseded_by` FK and the slice-052 DB CHECK
(`decisions_superseded_status_chk`) requires the two to move together. A
dedicated endpoint keeps the `superseded_by` linkage and the `superseded`
audit action explicit and atomic, and prevents a `PATCH` from landing a
decision in `superseded` with a NULL `superseded_by`.

**Confidence: high.** Matches the slice-052 schema's intent (the CHECK
constraint enforces the pairing).

## Revisit once in use

- **D2 — `decision_id` collision retry.** If the deployment model ever
  becomes concurrent-writer, add a retry loop around `CreateDecision` for
  the `UNIQUE (tenant_id, decision_id)` violation.
- **D3 — `audit_narrative_opt_out` granularity.** If a tenant-config table
  lands, consider a tenant-level default that layers above the per-decision
  flag.
- **D6 — overdue dedup after status change.** Current behaviour: once a
  decision has an `overdue_notified` row it is never re-notified, even if it
  is later edited to a new `revisit_by` and goes overdue again. For v1 this
  is the safe (no-spam) reading of the anti-criterion. If operators report
  they *want* a fresh notification after they push out a `revisit_by`, the
  fix is to scope the dedup probe to "since the last `updated` audit row" —
  deferred until that demand actually surfaces.
- **D4 — OSCAL narrative wiring.** Slice 030 must call
  `decision.EmitRemarks` from its SSP export pipeline; the contract is
  frozen and tested but the call site does not exist yet.

## Confidence summary

| Decision | Confidence  |
| -------- | ----------- |
| D1       | high        |
| D2       | high / medium (no-retry-loop) |
| D3       | medium-high |
| D4       | high        |
| D5       | high        |
| D6       | high        |
| D7       | high        |
