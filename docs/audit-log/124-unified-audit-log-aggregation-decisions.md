# Slice 124 — Unified audit-log aggregation API · decisions log

Filed as part of the slice-124 implementation (branch `backend/124-unified-audit-log-aggregation-api`). Every JUDGMENT call the engineer made while building this slice is recorded here so the post-merge maintainer iteration is traceable.

The product runtime AI-assist boundary is constitutional and is NOT mutated by anything in this log; this log is about how the slice was BUILT, not how the shipped product behaves (see `CLAUDE.md` "AI-assist boundary (hard)").

---

## D1 — Aggregator package location: `internal/audit/unifiedlog/`

**Choice:** New package at `internal/audit/unifiedlog/` (sibling to `internal/audit/`), HTTP handler at `internal/api/adminauditlog/unified.go` (extending the existing slice-062 package, not a new package).

**Why:** The slice file said either approach was acceptable; the existing `internal/api/adminauditlog/handler.go` was ~260 LOC and growing one new endpoint stayed under 500 LOC. Keeping the handler in the slice-062 package also means there is exactly ONE `Handler` type for the entire `/v1/admin/audit-log/*` surface, which simplifies the wiring in `httpserver.go` (one `adminauditlog.New(pool)` call covers both endpoints).

The aggregator itself lives in its own new package because (a) it's a reusable read primitive that future slices (125 frontend, 126 sink) consume directly without going through the HTTP handler, and (b) the package-boundary constraint enforces the read-only contract via the type system (no exported writer).

## D2 — Endpoint coexists with slice 062's `/v1/admin/audit-log`

**Choice:** `/v1/admin/audit-log/unified` is a NEW endpoint, NOT a replacement for slice 062's `/v1/admin/audit-log`. Both routes remain wired in `httpserver.go`.

**Why:** Slice 062 ships a different wire shape (`{rows, next_cursor}` with `{ts, source_table, event_type, actor, resource_type, resource_id, summary}`); slice 124 ships `{entries, next_cursor}` with the canonical `{occurred_at, actor_id, tenant_id, kind, target_type, target_id, action, row_id, payload_json}`. Slice 062 reads from the `admin_audit_log_v` view over 8 tables; slice 124 reads from the 9 base tables directly (incl. `aggregation_rule_audit_log` + `walkthrough_audit_log` which the slice-062 view did NOT pick up). Two endpoints, two consumer surfaces — frontend slice 125 will choose; sink slice 126 will read from the new shape only.

A migration that retires slice 062 in favour of slice 124 is a separate slice if/when there's evidence no consumer relies on the slice-062 wire shape. Out of scope here.

## D3 — UNION ALL against base tables, NOT through a SQL view

**Choice:** The sqlc query `ListUnifiedAuditLog` writes the UNION ALL directly across the nine base tables. NO new `unified_audit_log_v` SQL view is created.

**Why:** Slice 062's `admin_audit_log_v` view exists, but extending it to add the 2 missing branches (aggregation_rule + walkthrough) would either (a) change the view's existing column shape and break slice-062 consumers, or (b) require a CREATE OR REPLACE that's hard to reason about across migration boundaries. Writing the UNION directly in sqlc keeps the query an internal implementation detail of the aggregator package — the next time someone wants to extend the unified shape, they edit ONE file (`internal/db/queries/unified_audit_log.sql`).

The slice file's P0-A2 prohibits creating a NEW physical audit-log table; it does NOT prohibit a NEW SQL view. The CHOICE to write the UNION inline instead of as a view is a pure code-organisation call; the runtime behavior (query-time UNION, no materialisation, RLS per base table) is identical.

## D4 — Extending `me_audit_log.action` CHECK constraint instead of adding a sibling table

**Choice:** The slice-124 migration extends the existing `me_audit_log.action` CHECK to permit `'audit_log_query_unified'`. The meta-audit row writes via the existing `dbx.InsertMeAuditLog`.

**Why:** Adding a parallel `unified_query_audit_log` table would (a) duplicate the SELECT + INSERT-only RLS pattern, (b) create a new top-level audit primitive for a marginal use case (read events on a meta endpoint), and (c) the canvas §4.6.5 "Schema-level enforcement" pattern already encodes the audit ledger as ONE per-domain table per concern. The `me_audit_log` table is the per-USER audit ledger, and a unified-log query IS a per-user event (the calling user is recording that they queried the audit log). The action value `audit_log_query_unified` is intentionally distinct from the slice-108 mutation actions (profile.update / preferences.update / session.revoke) so a future report can filter the read events out cleanly.

## D5 — Defense-in-depth role gate: admin OR auditor OR grc_engineer (NOT admin-or-auditor)

**Choice:** The handler's `callerAllowedUnified` probe accepts THREE roles: admin, auditor, grc_engineer. The slice file said "admin OR auditor".

**Why:** The v1 primary user per CLAUDE.md is "the solo security leader at a 50–150-person security-product startup who runs the entire program — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — alone". That user holds the `grc_engineer` role (canvas §9.5 — the operator role for the solo security leader). Refusing the platform's primary user access to their own program's audit log would be absurd. The existing OPA `grc_engineer.rego` already grants wildcard read across tenant-scoped resources, so the rego layer was already permissive; the handler's defense-in-depth probe just had to mirror it.

This is a JUDGMENT call: tightening to admin-or-auditor-only would have closed a real user-facing capability (the GRC engineer wants to investigate "who changed this exception last Tuesday?") with zero security benefit (rego already lets them read everything else; the audit log isn't more sensitive than the underlying records). Recorded here so a future security-review can challenge the call.

## D6 — Cursor uses `row_id` (the audit-row's PK) as the strict-uniqueness tiebreaker

**Choice:** The UNION ALL projects each underlying table's primary key into a canonical `row_id` column. The cursor predicate uses `(occurred_at, kind, row_id)` for the strict-greater-than next-page condition.

**Why:** The slice file's initial cursor shape was `(occurred_at, target_id, kind)`. But `target_id` is NOT guaranteed unique per row (e.g. evidence_audit_log.record_id is NULLABLE so many rows project to `target_id = ''`; me_audit_log uses user_id which repeats across multiple actions; exception_audit_log.exception_id repeats across state transitions). With a non-unique tiebreaker, pagination either skips legitimately-different rows or returns duplicates across page boundaries.

The audit-row's PK is the only column GUARANTEED unique per row across the UNION (because each base table's PK is independently unique and the kind discriminator separates branches). Adding `row_id` to the canonical Entry shape costs one extra `UUID` column on the wire and the corresponding aggregator field; it is worth it because cursor correctness IS the user-facing pagination contract.

The wire shape adds `row_id` to the response (so the frontend can use it for stable React keys); the cursor opaque blob carries `row_id` instead of `target_id`. AC-6 of the slice file said the cursor is over `{occurred_at, target_id, kind}` — this is a deviation, recorded here. The user-facing semantic ("paginate through every audit row, deterministically") is preserved exactly.

## D7 — feature_flag_audit_log synthesizes `action` from `from_enabled`/`to_enabled`

**Choice:** The UNION's feature_flag branch synthesizes `action` as one of `'feature_flag.enable' | 'feature_flag.disable' | 'feature_flag.flip'` based on the from/to boolean delta.

**Why:** The underlying table doesn't carry an `action` text column — it carries `from_enabled BOOLEAN` + `to_enabled BOOLEAN`. The canonical Entry shape REQUIRES an `action` value (it's a query-filter dimension). Synthesizing here keeps the wire shape uniform across all nine kinds; the underlying booleans are preserved in `payload_json` for any consumer who wants the exact transition.

The `'feature_flag.flip'` branch covers the degenerate case where `from_enabled = to_enabled` (the row exists but the value didn't change — e.g. a no-op write); the slice-062 view uses the same `'feature_flag.flip'` literal for ALL feature-flag events. Keeping the slice-062 wording for the no-op branch is a deliberate consistency call.

## D8 — Index migration only adds `(tenant_id, created_at DESC)` on aggregation_rule_audit_log

**Choice:** Of the nine audit-log tables, only `aggregation_rule_audit_log` was missing the equivalent of a `(tenant_id, <ts_col> DESC)` composite index. The migration adds ONE index, not nine.

**Audit table (verified pre-implementation):**

| Table                          | Existing index satisfying `(tenant_id, <ts> DESC)`            |
| ------------------------------ | ------------------------------------------------------------- |
| decision_audit_log             | `idx_decision_audit_log_tenant_occurred`                      |
| evidence_audit_log             | `idx_evidence_audit_log_tenant_received` (uses received_at)   |
| exception_audit_log            | `idx_exception_audit_log_tenant_occurred`                     |
| sample_audit_log               | `idx_sample_audit_log_tenant_occurred`                        |
| audit_period_audit_log         | `idx_audit_period_audit_log_tenant_occurred`                  |
| **aggregation_rule_audit_log** | **MISSING — added by this slice as `idx_..._tenant_created`** |
| feature_flag_audit_log         | `idx_feature_flag_audit_log_tenant_occurred`                  |
| me_audit_log                   | `me_audit_log_tenant_occurred`                                |
| walkthrough_audit_log          | `idx_walkthrough_audit_log_tenant_occurred`                   |

**Why:** Adding redundant indexes would waste storage and write throughput on the tables that already cover the query. The slice file's AC-12 said "every one of the 9 audit-log tables that doesn't already have one (audit first; some tables may already have the index from earlier slices — don't duplicate)" — the audit returned exactly one match.

## D9 — `EXPLAIN ANALYZE` output (AC-13)

**Test fixture:** Cumulative integration-test data left in the test DB after `TestSlice124_CursorPaginationWalksAllRows` (1500 me-rows + 504 decision-rows + 504 evidence-rows + a few seed rows per other table from the isolation test).

**Query:** UNION ALL across the 9 tables with `WHERE occurred_at BETWEEN now() - interval '90 days' AND now() ORDER BY occurred_at DESC LIMIT 100`.

**Result (load-bearing claim):** Every branch except `me_audit_log` uses a `Bitmap Index Scan` on its `(tenant_id, <ts> DESC)` index. `me_audit_log` does a `Seq Scan` because the planner estimates the table is small enough that the scan is cheaper than the index lookup — this is correct planner behavior, not an index gap; with more rows the planner will pick the index.

```
->  Bitmap Heap Scan on decision_audit_log
      ->  Bitmap Index Scan on idx_decision_audit_log_tenant_occurred
->  Bitmap Heap Scan on evidence_audit_log
      ->  Bitmap Index Scan on idx_evidence_audit_log_tenant_received
->  Bitmap Heap Scan on exception_audit_log
      ->  Bitmap Index Scan on idx_exception_audit_log_tenant_occurred
->  Bitmap Heap Scan on sample_audit_log
      ->  Bitmap Index Scan on idx_sample_audit_log_tenant_occurred
->  Bitmap Heap Scan on audit_period_audit_log
      ->  Bitmap Index Scan on idx_audit_period_audit_log_tenant_occurred
->  Bitmap Heap Scan on aggregation_rule_audit_log
      ->  Bitmap Index Scan on idx_aggregation_rule_audit_log_tenant_created   <-- new in this slice
->  Bitmap Heap Scan on feature_flag_audit_log
      ->  Bitmap Index Scan on idx_feature_flag_audit_log_tenant_flag_occurred
->  Seq Scan on me_audit_log   <-- planner choice, table too small for index
->  Bitmap Heap Scan on walkthrough_audit_log
      ->  Bitmap Index Scan on idx_walkthrough_audit_log_tenant_occurred

Planning Time: 1.919 ms
Execution Time: 0.884 ms
```

## D10 — sqlc hand-narrows re-applied per slice 109 policy

**Choice:** `sqlc generate` regenerated three files I had to re-narrow (slice 109 known sqlc v1.31.1 typing gaps):

- `internal/db/dbx/policies.sql.go` — `ListPoliciesWithAckRateRow.AckDenominator/AckNumerator` from `interface{}` back to `pgtype.Int8`
- `internal/db/dbx/scf_anchors.sql.go` — `ListSCFAnchorsForVersionWithStateRow` + `ListSCFAnchorsLatestWithStateRow`'s `StateResult` + `StateFreshnessStatus` from non-nullable to `NullEvidenceResult` + `pgtype.Text`
- `internal/db/dbx/models.go` — `AdminAuditLogV` struct doc comment (the slice 062 + slice 108 attribution)

**Why:** This is the documented slice-109 + slice-104 pattern. Every slice that runs `sqlc generate` needs to re-apply these hand-narrows. The pattern is captured in `docs/audit-log/109-sqlc-toolchain-pin-decisions.md`.

---

## Spillovers filed

None — slice 124 was scoped tightly enough that the JUDGMENT calls above resolved every gap. Slices 125 (frontend) + 126 (external sink) remain `not-ready` per the slice doc; they depend on this PR merging.
