# 124 — Unified audit-log aggregation API (read-only across 9 per-domain audit tables)

**Cluster:** Backend (Multi-tenancy)
**Estimate:** 2d
**Type:** AFK

## Narrative

security-atlas accumulates audit-log writes across nine per-domain tables today (`decision_audit_log`, `evidence_audit_log`, `exception_audit_log`, `sample_audit_log`, `audit_period_audit_log`, `aggregation_rule_audit_log`, `feature_flag_audit_log`, `me_audit_log`, `walkthrough_audit_log`). Each is owned by its domain slice and follows slice 036's append-only four-policy RLS pattern (`tenant_read` FOR SELECT + `tenant_write` FOR INSERT WITH CHECK under `FORCE ROW LEVEL SECURITY`, no UPDATE/DELETE policies).

There is no unified read surface today. The maintainer's user-facing feature ask — "every audit event visible in the app + written to an external sink for tamper-evident retention outside the app" — depends on three new surfaces. Per `/idea-to-slice` discipline (single primary slice + spillover stubs), this slice ships ONLY the foundation: a backend aggregation API that the other two surfaces read from. The frontend `/audit-log` page is filed as slice 125 (`not-ready`, deps on this slice merging). The external-file sink is filed as slice 126 (`not-ready`, deps on this slice merging + a JUDGMENT decision among 4 sink mechanisms).

This slice ships a read-only Go package + new admin endpoint that UNION-ALLs across the 9 per-domain audit tables, normalizes them to a canonical schema (`occurred_at`, `actor_id`, `tenant_id`, `kind`, `target_type`, `target_id`, `action`, `payload_json`), paginates safely, and returns the aggregated stream. The slice does NOT create any new audit-log table. The slice does NOT change how individual domain slices write their audit rows. The aggregator is purely a query-time UNION.

The load-bearing constraint is tenant isolation: each underlying audit-log table has its own RLS policy; the UNION query MUST respect those automatically. The integration test surface for slice 033 (RLS enforcement) is the reference — Tenant A's query of the unified view MUST NOT return Tenant B's rows, and the test MUST verify this against every one of the 9 underlying tables.

## Threat model

| STRIDE                       | Threat                                                                                                                                                | Mitigation                                                                                                                                                                                                                                                                 |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | New authenticated endpoint exposes sensitive audit data — forged identity (stolen cookie, replayed bearer) reaches it                                 | AC-7: endpoint enforces admin OR auditor role at the OPA policy boundary (`atlas.adminauditlog.unified_query.allow`); same auth middleware as `internal/api/adminauditlog/handler.go`                                                                                      |
| **T** Tampering              | None — aggregator is strictly READ-ONLY                                                                                                               | n/a — anti-criterion P0-A1 enforces no DB writes from this package                                                                                                                                                                                                         |
| **R** Repudiation            | A malicious admin queries audit logs to identify their own past activity, then attempts to cover tracks. The aggregator query itself must be recorded | AC-9: every unified-log query writes a row to `me_audit_log` (slice 108) with the query params (from/to/actor/kind filters). The meta-audit can itself be tampered (admin → admin), but the spillover slice 126's external sink closes that loop                           |
| **I** Information disclosure | UNION ALL across 9 tables could leak Tenant A's rows into Tenant B's response if the RLS policy isn't applied uniformly                               | AC-8: integration test (per-underlying-table) verifies Tenant A → Tenant B isolation across all 9. AC-3: query goes through `tenancy.ApplyTenant` context so Postgres RLS auto-enforces — the aggregator does NOT use `BYPASSRLS`                                          |
| **D** Denial of service      | Unbounded query window → query plan blowup, especially since audit-log tables grow unbounded over time                                                | AC-5: max 90-day window per query (handler-level validation, 400 on violation). AC-6: pagination is cursor-based with hard cap 1000 rows. AC-11: migration adds `(tenant_id, occurred_at DESC)` composite index on every one of the 9 tables that doesn't already have one |
| **E** Elevation of privilege | A non-admin caller reaches the aggregator endpoint via a code path that bypasses role check                                                           | AC-7: single OPA policy guards entry; handler exits with 403 before touching DB if `data.atlas.adminauditlog.unified_query.allow` returns false. AC-10: unit test verifies non-admin caller gets 403 from EVERY query-parameter combination (matrix test)                  |

**Anti-criteria additions from threat model:** P0-A4 (no `BYPASSRLS` anywhere in the aggregator package) and P0-A6 (no per-table fallback that omits the role check) are load-bearing. The aggregator's correctness IS the tenant-isolation guarantee for every other slice that subsequently reads from it (125 + 126).

## Acceptance criteria

### Aggregation package

- [ ] AC-1: New Go package `internal/audit/unifiedlog/` exposes `Query(ctx, params) ([]Entry, cursor)` where `Entry` is `{occurred_at, actor_id, tenant_id, kind, target_type, target_id, action, payload_json}`. The package has zero exported writers — read-only API surface.
- [ ] AC-2: The query is a single SQL statement: UNION ALL across all 9 audit-log tables, projected to the canonical schema, with `WHERE` filters applied at each table (occurred_at range + optional actor_id + optional kind + cursor opaque). Use sqlc; no hand-rolled string concatenation.
- [ ] AC-3: Query is executed under `tenancy.ApplyTenant(ctx, tenantID)` — the session role is `atlas_app` (NOT `atlas_service_account` or anything with BYPASSRLS). The aggregator does NOT receive a tenant_id parameter; it's implicit from the session context.
- [ ] AC-4: `kind` enum maps to each underlying table name (`decision`, `evidence`, `exception`, `sample`, `audit_period`, `aggregation_rule`, `feature_flag`, `me`, `walkthrough`). New domain audit tables added in future slices SHOULD extend this enum + the UNION ALL query — document the extension pattern in the package doc comment.

### HTTP endpoint

- [ ] AC-5: New endpoint `GET /v1/admin/audit-log/unified` accepts query params: `from=<RFC3339>` (required), `to=<RFC3339>` (required), `actor=<uuid>` (optional), `kind=<enum>` (optional, comma-separated), `cursor=<opaque>` (optional). Validates `to - from <= 90 days`; returns 400 otherwise.
- [ ] AC-6: Response shape is `{entries: [...], next_cursor: "..."}` where `entries` has hard cap 1000 rows. Cursor is opaque (base64-encoded `{occurred_at, target_id, kind}` tuple). Pagination is unidirectional (no `prev_cursor`).
- [ ] AC-7: Endpoint enforces admin OR auditor role via OPA: `data.atlas.adminauditlog.unified_query.allow`. The policy file lives at `policies/admin/audit-log-unified.rego`. Non-admin/non-auditor caller gets 403 before any DB query.
- [ ] AC-8: New OPA policy unit tests cover admin-allow / auditor-allow / member-deny / service-account-deny matrix.

### Tests

- [ ] AC-9: Integration test (`internal/audit/unifiedlog/handler_integration_test.go`) seeds rows in EVERY one of the 9 audit-log tables for Tenant A + Tenant B, queries the unified endpoint as Tenant A, asserts result set contains ONLY Tenant A's rows across ALL 9 kinds. Repeat for Tenant B. NO row from the other tenant in either result. Use slice 033's `WithTenant` test helper for context plumbing.
- [ ] AC-10: Meta-audit verification — every unified-log query writes one row to `me_audit_log` with `action="audit_log_query_unified"` + the request params serialized into `payload_json`. Integration test asserts the row exists after each query.
- [ ] AC-11: Pagination test — seed 1500 rows for one tenant across 3 tables, page through, assert (a) first page = 1000 rows, (b) next_cursor non-empty, (c) second page = 500 rows + empty next_cursor, (d) no duplicates across pages, (e) ordering is `occurred_at DESC` stable across page boundary.

### Database

- [ ] AC-12: Migration `migrations/sql/_NNN_unified_audit_log_indexes.sql` adds composite index `(tenant_id, occurred_at DESC)` on every one of the 9 audit-log tables that doesn't already have one (audit first; some tables may already have the index from earlier slices — don't duplicate). Migration is idempotent + reversible.
- [ ] AC-13: `EXPLAIN ANALYZE` on the UNION ALL query in the integration-test fixture data shows index-scan paths on each table (no sequential scans). Document the `EXPLAIN` output in the slice's decisions log.

### Code-shape

- [ ] AC-14: Aggregator package has zero exported types that wrap a `*pgx.Conn` or `tx` — keeps the package boundary clean. Caller uses `unifiedlog.Query(ctx, db, params)` pattern.
- [ ] AC-15: Wire endpoint via the established `internal/api/httpserver.go` Mount-append pattern (slices 014/017/018/019/024/036/009 are reference). No second `chi.NewRouter().Mount("/", ...)` (chi panics).
- [ ] AC-16: CHANGELOG entry under `[Unreleased] / Added` mentioning the new endpoint + the spillover slices.

## Constitutional invariants honored

- **Tenant isolation via PostgreSQL RLS, not application code** (canvas §5.4) — aggregator query goes through `tenancy.ApplyTenant` + executes as `atlas_app`; RLS auto-enforces. P0-A4 explicitly forbids `BYPASSRLS`.
- **Append-only evidence/audit ledger** (canvas §4.3) — aggregator is read-only; P0-A1 enforces.
- **Audit-period freezing** (canvas §8.4 + constitutional invariant #10) — when a query window falls within a frozen `AuditPeriod`, the result respects `observed_at ≤ frozen_at` because the underlying tables already do so; aggregator inherits the property via UNION ALL.
- **One control, N framework satisfactions** (canvas §3, invariant #1) — not directly touched; aggregator doesn't write to controls or anchors.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` (audit-log writes are the spiritual sibling of evidence-ledger writes — same append-only pattern)
- `Plans/canvas/05-scopes.md` §5.4 (RLS layer — the tenant-isolation contract this slice depends on)
- `Plans/canvas/08-audit-workflow.md` §8.4 (audit-period freezing — the aggregator inherits this from underlying tables)
- `internal/api/adminauditlog/handler.go` (existing per-table audit-log endpoint — the new endpoint is a sibling, NOT a replacement)
- Slice 036 (four-policy RLS pattern — the reference for any audit-log RLS reasoning)
- Slice 108 (`me_audit_log` table — the meta-audit destination)

## Dependencies

- None — all 9 underlying audit-log tables are already merged; this slice is purely additive.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT write to any audit-log table from this package. Aggregator is read-only. The only write surface is the `me_audit_log` meta-audit row (AC-10), and that goes through the existing `meaudit` writer, not a new path.
- **P0-A2**: Does NOT create a new physical audit-log table. The UNION view is virtual (query-time). Materialized view is explicit scope creep — file as a separate slice if perf demands it.
- **P0-A3**: Does NOT change the schema of any of the 9 existing audit-log tables. Each is owned by its domain slice; cross-cutting schema change is a separate concern.
- **P0-A4**: Does NOT use `BYPASSRLS` or any role that has it. Aggregator MUST execute as `atlas_app` (the RLS-enforced role). P0 because the entire tenant-isolation guarantee depends on this.
- **P0-A5**: Does NOT accept a tenant_id parameter at the API or aggregator surface. The tenant is implicit via the session context. Accepting an explicit tenant_id parameter would create a privilege-escalation surface.
- **P0-A6**: Does NOT skip the OPA role check on any code path. AC-7's matrix test verifies. P0 because a single bypass = audit-log leak.
- **P0-A7**: Does NOT use vendor-prefixed test fixture tokens — neutral `test-*` only (carry-over per slice 069 convention).
- **P0-A8**: Does NOT export a writer/insert function from the unifiedlog package — type-system enforcement of read-only contract.

## Skill mix

- sqlc + UNION ALL query authoring (slice 109's `_enums.sql` is the reference for sqlc edge cases)
- Postgres index design + EXPLAIN analysis
- chi Mount-append pattern (slices 014/017/018/019/024/036/009)
- `internal/db/integration_test.go` four-policy RLS test helper (`WithTenant`)
- OPA Rego policy authoring (slice 035 + `policies/admin/*` are the reference)
- Cursor-based pagination (no existing in-tree reference; slice 099's keyset-pagination for evidence-list is closest analog)

## Notes for the implementing agent

- **The aggregator package belongs at `internal/audit/unifiedlog/`** (NEW package). It is a peer to `internal/audit/` (which already exists for audit-period workflow). The HTTP handler can live in `internal/api/adminauditlog/` (extending the existing package) or as a new `internal/api/adminauditlog/unified.go` file. Pick whichever keeps `handler.go` under 500 LOC; if the existing file is small, just extend. Document the choice in the slice's decisions log if it's not obvious.
- **The 9 underlying audit-log tables don't have a uniform shape** — `decision_audit_log` has columns the others don't (and vice versa). The UNION ALL query MUST project each table to the canonical `Entry` shape, using `NULL` for columns the source table doesn't have. The `payload_json` field is the catch-all for table-specific data; serialize the source-specific extras into it.
- **The maintainer's user-facing feature ask is the unified `/audit-log` page** (slice 125) and **the external sink** (slice 126). Slice 124 unblocks both. If during implementation an out-of-scope finding emerges that would benefit 125 or 126, FILE IT (per Amendment 2) — don't bolt it onto 124.
- **OPA policy naming**: `policies/admin/audit-log-unified.rego` with package `data.atlas.adminauditlog.unified_query`. Rule `allow` returns true if role ∈ {"admin", "auditor"}. The OPA policy file is small (~10 lines); the matrix unit test is what gives confidence.
- **For the meta-audit (AC-10)**: write to `me_audit_log` via the existing `meaudit` package (slice 108). Do NOT inline a new INSERT. Reuse keeps the audit row format consistent.
- **Index naming convention**: `idx_<table>_tenant_occurred_at` to match the prefix-based naming in existing migrations (slices 002, 036).
- **For the UNION ALL query**: write it as 9 SELECT branches in a single sqlc query named `ListUnifiedAuditLog`. Don't try to be clever with `UNION` (vs ALL) — `UNION ALL` is correct because the 9 tables have disjoint primary keys; deduplication isn't needed and `UNION` would force a sort.

## Grill output (Phase 2 self-grill from /idea-to-slice)

The grill surfaced three terminology / scope issues that shaped the final draft:

1. **"Audit trail" vs "audit log"** — internal canonical is "audit log" (every table is `*_audit_log`, every package is `adminauditlog` / `audit`). User-facing copy can say "audit trail"; backend/database/code uses "audit log". The slice's title and ACs use "audit log".

2. **Aggregation table vs aggregation view** — early draft proposed a physical aggregation table updated via triggers. Rejected: violates the constitutional principle that audit-log writes happen at their domain slice's INSERT site, not via a cross-cutting trigger. The aggregation is query-time (UNION ALL), not denormalization. P0-A2 codifies this.

3. **Three-surface bundle vs split** — initial maintainer framing offered "one big slice OR 3-way split". Split chosen because (a) backend aggregation is the dependency for both other surfaces, (b) each surface has its own clean tracer-bullet, (c) splitting allows the engineer to ship the unblock-er without waiting on the UI design that slice 125 will require.

## Pressure-test (Phase 5 inline Red Team)

Verified after drafting:

- **Splitting test**: every AC is binary-testable (`integration test passes`, `query returns N rows`, `endpoint returns 400`). None bundle compound requirements.
- **AC count**: 16 ACs for a 2d slice — within the 10-20 band for 1-2d.
- **Anti-criteria specificity**: P0-A1 through P0-A8 are concrete. P0-A4 (no BYPASSRLS) and P0-A5 (no tenant_id parameter) are the load-bearing two.
- **Test ACs cover every code AC**: AC-1 (package) → AC-9 (integration test); AC-5 (endpoint) → AC-7+AC-8 (role tests); AC-12 (indexes) → AC-13 (EXPLAIN).
- **Constitutional invariants enforced (not just claimed)**: tenant isolation via RLS is enforced by AC-3 (ApplyTenant) + AC-9 (integration test verifies the property end-to-end). Append-only enforced by P0-A1 + P0-A8 (type-level no exported writer).
- **Threat model gap re-check**: no new auth surface beyond the OPA role check; the meta-audit closes the partial repudiation gap; external sink (the FULL repudiation closure) is correctly scoped out to slice 126.

## Out-of-scope (filed as spillover stubs)

- **Slice 125 (`not-ready`)** — Frontend `/audit-log` page that consumes this endpoint. Filters + drill-down + cursor pagination UI. Mockup may need to be filed as part of 125 or as its own sibling per slice 093's pattern. Deps on 124 merging.
- **Slice 126 (`not-ready`)** — External-file sink. JUDGMENT slice — engineer picks among 4 sink mechanisms (JSONL-to-disk, syslog, OTel-logs-via-collector, S3-cosign). Maintainer's lean: OTel-logs-via-collector (composes with slice 121's OTel SDK), but the engineer documents the tradeoff. Deps on 124 merging.
