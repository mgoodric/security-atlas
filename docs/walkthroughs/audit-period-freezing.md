# Audit Periods — How open Becomes frozen, and What Changes

_2026-05-16T06:16:41Z by Showboat 0.6.1_

<!-- showboat-id: 7a4b4284-4e30-423b-a699-aff99341b447 -->

> **Walkthrough kind:** this is a PAI Walkthrough skill document (slice 070 — showboat-generated). It is distinct from slice 027’s audit walkthrough (`internal/audit/walkthrough`), which records auditor evidence capture against controls. The two concepts share a word and nothing else.

## Overview

Constitutional invariant 10 (`CLAUDE.md`): "Audit-period freezing. When an AuditPeriod is frozen, sample populations draw only from evidence with `observed_at ≤ frozen_at`. Live state continues independently."

This walkthrough traces an AuditPeriod from `open` to `frozen`, demonstrating what flips and what does not. The freeze stamps `frozen_at`, `frozen_by`, and a `frozen_hash` content commitment (per ADR-0003), and sample populations drawn after the freeze respect the horizon. Live evaluation queries continue unaffected.

Captured against the slice-037 docker-compose bundle, seeded by `fixtures/walkthroughs/00-seed.sql` + `audit-period.sql`. The fixture installs one open period (SOC2 Q1 2026) and three evidence records straddling the freeze cutoff: two before, one after.

## 1. The Open Period

The fixture creates one period in `open` status. The frozen columns are all NULL by construction (check constraint `audit_periods_frozen_coherent` enforces the coherence — `open` requires NULL on all freeze fields; `frozen` requires non-NULL):

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT name, status, frozen_at, frozen_hash IS NULL AS hash_is_null, frozen_by FROM audit_periods WHERE id = '55555555-5555-5555-5555-555555550001'; ROLLBACK;"
```

```output
BEGIN
SET
     name     | status | frozen_at | hash_is_null | frozen_by
--------------+--------+-----------+--------------+-----------
 SOC2 Q1 2026 | open   |           | t            |
(1 row)

ROLLBACK
```

## 2. The Evidence Population — Before and After the Freeze Cutoff

The fixture seeds three evidence records for CRY-05 in this tenant. The walkthrough freezes the period at `2026-04-15T12:00:00Z`. Two observations fall before the cutoff; one falls after.

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT substring(id::text, 1, 13) AS short_id, observed_at, CASE WHEN observed_at <= '2026-04-15T12:00:00Z' THEN 'before cutoff' ELSE 'after cutoff' END AS relative_to_freeze FROM evidence_records WHERE control_id = '33333333-3333-3333-3333-333333330001' ORDER BY observed_at; ROLLBACK;"
```

```output
BEGIN
SET
   short_id    |      observed_at       | relative_to_freeze
---------------+------------------------+--------------------
 66666666-6666 | 2026-02-01 00:00:00+00 | before cutoff
 66666666-6666 | 2026-03-15 00:00:00+00 | before cutoff
 66666666-6666 | 2026-05-01 00:00:00+00 | after cutoff
(3 rows)

ROLLBACK
```

## 3. The Freeze Hash Inputs

Per ADR-0003, the freeze stamps a `frozen_hash` content commitment over: the period’s identity (id, period_start, period_end, framework_version_id), the sorted list of evidence_record_ids visible at the freeze cutoff, and the sorted list of control_ids in the tenant. Critically, `frozen_at` itself is NOT in the hash inputs (it would self-reference) — but it gates which evidence ids the hash sees.

Looking at the helper `internal/audit/period/period.go::Freeze`:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "226,246p" internal/audit/period/period.go
```

```output
		// Compute the content commitment hash BEFORE the UPDATE so a
		// hash-computation failure aborts the freeze without partial
		// state. Ingredients: sorted evidence_record_ids visible at
		// `at` + sorted control_ids in tenant. Per ADR 0003.
		evIDs, err := q.ListEvidenceIDsForPeriodHash(ctx, dbx.ListEvidenceIDsForPeriodHashParams{
			TenantID:   pgUUID(tenantID),
			ObservedAt: pgTimestamptz(at),
		})
		if err != nil {
			return fmt.Errorf("list evidence ids for hash: %w", err)
		}
		ctrlIDs, err := q.ListControlIDsForPeriodHash(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list control ids for hash: %w", err)
		}
		hash, err := computeFreezeHash(freezeHashInputs{
			AuditPeriodID:      uuid.UUID(existing.ID.Bytes),
			PeriodStart:        existing.PeriodStart.Time,
			PeriodEnd:          existing.PeriodEnd.Time,
			FrameworkVersionID: uuid.UUID(existing.FrameworkVersionID.Bytes),
			EvidenceRecordIDs:  pgUUIDsToUUIDs(evIDs),
```

`ListEvidenceIDsForPeriodHash` filters on `observed_at <= $at` — that is the freeze horizon. The post-freeze record is excluded from the hash, and also excluded from any sample population drawn against the frozen period.

## 4. Perform the Freeze (SQL)

The Go `Freeze(...)` method is a transaction that runs the hash query then UPDATEs. For the walkthrough we replay the operative SQL directly so the state transition is observable. Note: the production code computes the hash and writes it; here we use a deterministic placeholder to keep the walkthrough byte-stable across runs.

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; UPDATE audit_periods SET status = 'frozen', frozen_at = '2026-04-15T12:00:00Z', frozen_by = 'demo-operator@example.invalid', frozen_hash = decode('a17ed3a0','hex') WHERE id = '55555555-5555-5555-5555-555555550001' AND status = 'open' RETURNING name, status, frozen_at, encode(frozen_hash, 'hex') AS frozen_hash_hex, frozen_by; ROLLBACK;"
```

```output
BEGIN
SET
     name     | status |       frozen_at        | frozen_hash_hex |           frozen_by
--------------+--------+------------------------+-----------------+-------------------------------
 SOC2 Q1 2026 | frozen | 2026-04-15 12:00:00+00 | a17ed3a0        | demo-operator@example.invalid
(1 row)

UPDATE 1
ROLLBACK
```

(The walkthrough rolls back so the period stays `open` for re-runs — same fixture, same hash, deterministic.)

The `WHERE ... AND status = 'open'` guard is load-bearing: a concurrent second freeze attempt loses the race and gets zero rows updated, which the Go layer maps to `ErrAlreadyFrozen` (`internal/audit/period/period.go::Freeze`).

## 5. Sample Population Honors the Frozen Horizon

With the period frozen at 2026-04-15T12:00Z, a sample query drawing from the population MUST exclude the 2026-05-01 record. The slice-026 query template uses `observed_at <= COALESCE(frozen_at, 'infinity')`:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT substring(id::text, 1, 13) AS short_id, observed_at FROM evidence_records WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0' AND control_id = '33333333-3333-3333-3333-333333330001' AND observed_at <= '2026-04-15T12:00:00Z'::timestamptz ORDER BY observed_at; ROLLBACK;"
```

```output
BEGIN
SET
   short_id    |      observed_at
---------------+------------------------
 66666666-6666 | 2026-02-01 00:00:00+00
 66666666-6666 | 2026-03-15 00:00:00+00
(2 rows)

ROLLBACK
```

(Only the two before-cutoff records appear.) The post-freeze 2026-05-01 record exists in the ledger, but the frozen view does not see it. That is the whole point of the freeze: the audit population is a snapshot, immutable for sampling, regardless of what the platform observes afterward.

## 6. Live State Is Unaffected

Meanwhile, the live evaluation surface continues to use the full evidence population — the frozen period does not gate live queries. This is the canvas §8.4 "live state continues independently" clause:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT count(*) AS live_evidence_count FROM evidence_records WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0' AND control_id = '33333333-3333-3333-3333-333333330001'; ROLLBACK;"
```

```output
BEGIN
SET
 live_evidence_count
---------------------
                   3
(1 row)

ROLLBACK
```

Three records visible to live queries; only two visible to the frozen audit-period queries. Two coexisting views of the same ledger, one snapshotted at the freeze, one continuously growing.

## 7. The audit_periods Audit Log

Every freeze (and every freeze-rejection from `ErrAlreadyFrozen`) writes a row to `audit_period_audit_log`. AC-6 of slice 028 demands this: who froze, when, with what hash:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "\\d audit_period_audit_log" 2>&1 | head -20
```

```output
                      Table "public.audit_period_audit_log"
     Column      |           Type           | Collation | Nullable |   Default
-----------------+--------------------------+-----------+----------+-------------
 id              | uuid                     |           | not null |
 tenant_id       | uuid                     |           | not null |
 audit_period_id | uuid                     |           | not null |
 action          | text                     |           | not null |
 actor           | text                     |           | not null |
 detail          | jsonb                    |           | not null | '{}'::jsonb
 occurred_at     | timestamp with time zone |           | not null | now()
Indexes:
    "audit_period_audit_log_pkey" PRIMARY KEY, btree (id)
    "idx_audit_period_audit_log_tenant_occurred" btree (tenant_id, occurred_at DESC)
    "idx_audit_period_audit_log_tenant_period" btree (tenant_id, audit_period_id, occurred_at DESC)
Check constraints:
    "audit_period_audit_log_action_chk" CHECK (action = ANY (ARRAY['period_created'::text, 'period_frozen'::text, 'freeze_rejected_already_frozen'::text, 'population_attached'::text]))
    "audit_period_audit_log_actor_nonempty" CHECK (length(actor) > 0)
Policies (forced row security enabled):
    POLICY "tenant_read" FOR SELECT
      USING (current_tenant_matches(tenant_id))
```

## 8. Re-Freeze Is Idempotent (and Logged)

A second `Freeze(...)` call against an already-frozen period returns `ErrAlreadyFrozen` and writes a `freeze_rejected_already_frozen` row to the audit log. The UPDATE itself never runs because of the `AND status = 'open'` guard in section 4. We can prove this with a temporary CTE that simulates a freeze-then-second-freeze in a single transaction:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; WITH first_freeze AS (UPDATE audit_periods SET status = 'frozen', frozen_at = '2026-04-15T12:00:00Z', frozen_by = 'demo-operator@example.invalid', frozen_hash = decode('a17ed3a0','hex') WHERE id = '55555555-5555-5555-5555-555555550001' AND status = 'open' RETURNING id), second_freeze AS (UPDATE audit_periods SET status = 'frozen', frozen_at = now(), frozen_by = 'second-attempt', frozen_hash = decode('beef','hex') WHERE id = '55555555-5555-5555-5555-555555550001' AND status = 'open' RETURNING id) SELECT (SELECT count(*) FROM first_freeze) AS first_rows_updated, (SELECT count(*) FROM second_freeze) AS second_rows_updated; ROLLBACK;"
```

```output
BEGIN
SET
 first_rows_updated | second_rows_updated
--------------------+---------------------
                  1 |                   0
(1 row)

ROLLBACK
```

`first_rows_updated = 1`, `second_rows_updated = 0`. The Go layer sees the second UPDATE return zero affected rows and emits `ErrAlreadyFrozen` + writes a `freeze_rejected_already_frozen` row to the audit log (`internal/audit/period/period.go::Freeze`, slice 028 AC-6).

## 9. Putting It All Together

The freeze contract:

| Surface                  | Behavior pre-freeze                   | Behavior post-freeze                                                                      |
| ------------------------ | ------------------------------------- | ----------------------------------------------------------------------------------------- |
| `audit_periods` row      | `status=open`, all freeze fields NULL | `status=frozen`, `frozen_at`, `frozen_by`, `frozen_hash` set                              |
| Sample populations       | Drawn against the full ledger         | Drawn only from `observed_at <= frozen_at` (section 5)                                    |
| Live evaluation          | Drawn against the full ledger         | Drawn against the full ledger — unchanged (section 6)                                     |
| Re-freeze                | Allowed (and runs the UPDATE)         | Rejected via `status=open` guard; writes `freeze_rejected_already_frozen` row (section 8) |
| `audit_period_audit_log` | Empty for this period                 | One `period_frozen` row recording who/when/hash (section 7)                               |
| OSCAL export             | Refused                               | Generated against the frozen view (next walkthrough)                                      |

The hash (section 3) is the durable content commitment — given the period id, period bounds, framework version, and the _exact_ set of evidence + control ids visible at the freeze, a verifier can recompute the hash and confirm the frozen view has not drifted. ADR-0003 codifies the inputs.

### Where to read more

- **Canvas:** [`Plans/canvas/08-audit-workflow.md`](../../Plans/canvas/08-audit-workflow.md) §8.4 — audit-period freezing
- **ADR:** [`docs/adr/0003-audit-period-freeze-hash.md`](../adr/0003-audit-period-freeze-hash.md)
- **Slice docs:** [`docs/issues/028-audit-period-freezing.md`](../issues/028-audit-period-freezing.md)
- **Go package:** [`internal/audit/period/`](../../internal/audit/period/) — `Store.Freeze`, `computeFreezeHash`, freeze-hash-input plumbing
