# 066 — Dashboard backend read endpoints

**Cluster:** Metrics / backend
**Estimate:** 2-2.5d
**Type:** AFK

## Narrative

Surface the missing backend read endpoints that slice 040 (Program dashboard view) needs to fully ship. Slice 040 shipped the `/dashboard` UI plus four BFF proxies for the surfaces that already exist on main (recent drift, evidence freshness, top risks in treatment, expiring exceptions), and **binding empty-state placeholders** for four surfaces with no backend. Slice 040's decisions log (`docs/audit-log/040-program-dashboard-view-decisions.md`) records each placeholder with a full gap inventory.

This slice fills the four missing endpoints. It mirrors the 041→064 / 060→062 precedent: the frontend slice shipped UI shells + wire-shape contracts as binding placeholders, and the backend slice fills the real endpoints behind them.

Four endpoints:

1. **`GET /v1/frameworks/posture`** — per-framework-version posture: coverage percentage, a freshness composite, and a 90-day trend delta. `internal/api/ucfcoverage` is per-control only today; this aggregates to the framework-version grain. Unblocks slice 040 AC-2 (framework tiles + trend arrows).
2. **`GET /v1/activity`** — paginated read model over the NATS event-stream archive (slice 015 ingestion). No activity/event archive read endpoint exists in `internal/api/`. Unblocks slice 040 AC-6 (activity feed + infinite scroll).
3. **`sort=residual,age` on `GET /v1/risks`** — `ListRisks` today supports only `treatment` / `category` / `methodology` filters and `residual_score` is an opaque `json.RawMessage`. Extend the risk list query so the dashboard can rank "top risks aging" by residual score then age. Unblocks slice 040 AC-3.
4. **`GET /v1/upcoming`** — unified upcoming-items rollup: merges expiring exceptions (slice 021), policy-ack expirations (slice 023), vendor reviews (slice 024), and audit-period milestones (slice 028) into one date-sorted, paginated feed. Today only `/v1/exceptions/expiring` exists. Unblocks slice 040 AC-5.

The slice adds no new product capability and no migration — it surfaces existing data behind dashboard-grained read paths. It delivers value because slice 040's program dashboard — already merged and the v1 morning home screen — can bind its four placeholders to real data.

## Acceptance criteria

- [ ] AC-1: `GET /v1/frameworks/posture` — returns one row per active framework version: `{framework_id, framework_version, coverage_pct, freshness_composite, trend_delta_90d}`. Coverage aggregates slice-008 UCF coverage to the framework grain; freshness composite reuses slice-016 freshness; the 90-day trend is computed from `control_evaluations` history (slice 012).
- [ ] AC-2: `GET /v1/activity` — paginated read model over the slice-015 evidence-ingest event archive. Row shape `{ts, event_type, actor, resource_type, resource_id, summary}`; `?cursor=` + `?limit=` (max 200, default 50), newest-first.
- [ ] AC-3: `GET /v1/risks` accepts `?sort=residual,age` — extends `ListRisks` to rank by `residual_score` (descending) then risk age (oldest first). `residual_score` must become a sortable scalar, not an opaque `json.RawMessage`, for the sort path.
- [ ] AC-4: `GET /v1/upcoming` — unified rollup across expiring exceptions (021), policy-ack expirations (023), vendor reviews (024), and audit-period milestones (028). Row shape `{due_date, category, title, resource_type, resource_id}`; date-sorted ascending; `?cursor=` + `?limit=`; `?category=` filter optional.
- [ ] AC-5: every endpoint is tenant-scoped through the standard RLS path — slice 033 middleware is the sole tenant-context setter; no endpoint accepts `tenant_id` in query or body. Read authz reuses the existing dashboard/program-read role check.
- [ ] AC-6: all four endpoints mounted via the `httpserver.go` mount-append pattern (known-safe). Wire shapes match slice 040's four placeholder contracts — slice 040's merged PR (gh#101) + its decisions log are the spec.
- [ ] AC-7: integration test per endpoint (≥6 tests, real Postgres): framework posture aggregates correctly across versions · activity feed paginates newest-first · risks `sort=residual,age` orders correctly · upcoming rollup merges all four sources date-sorted · all four return 403 for an unauthorized role · all four are RLS-isolated across tenants.
- [ ] AC-8: `CHANGELOG.md` entry under `[Unreleased]/Added`.

## Follow-up (out of scope — noted, not an AC)

Re-pointing slice 040's four frontend placeholders (`framework-posture-panel`, `activity-feed-panel`, `top-risks-panel` sort, `upcoming-panel`) to these endpoints is a small mechanical frontend change. Slice 040's decisions log identifies the seams. It is left as a follow-up frontend touch — this slice ships the endpoints + wire-shape contracts only, keeping 066 single-language and AFK. Slice 040's AC-2/3/5/6 flip PARTIAL → PASS once the frontend is re-pointed.

## Constitutional invariants honored

- **Invariant 2 (ingestion/evaluation separated, append-only ledger):** the activity endpoint (AC-2) and the posture trend (AC-1) are pure reads over append-only ledgers — no writeback.
- **Invariant 6 (RLS):** every endpoint reads through standard tenant-scoped tables; RLS policies fire on each underlying SELECT. No new table, no `BYPASSRLS` path.
- **Slice 033 D1** (tenancy middleware is the sole tenant-context setter): no endpoint accepts `tenant_id` in query or body.
- **Invariant 1 (one control, N framework satisfactions):** framework posture aggregates through SCF anchors, not per-framework duplicated controls.

## Canvas references

- `Plans/canvas/07-metrics.md` §7.1, §7.5 (program metrics + dashboard surfaces)
- `Plans/canvas/04-evidence-engine.md` (evidence-ingest event stream — activity archive source)
- `docs/issues/040-program-dashboard-view.md` + `docs/audit-log/040-program-dashboard-view-decisions.md` (the frontend slice + its placeholder gap inventory)

## Dependencies

- **008** (UCF graph traversal / coverage — framework-posture aggregation source)
- **012** (control state evaluation — `control_evaluations` history for the 90-day trend)
- **015** (NATS JetStream ingestion — the event archive the activity feed reads)
- **016** (evidence freshness — the freshness composite in framework posture)
- **019** (risk CRUD — the `ListRisks` query extended for `sort=residual,age`)
- **021** (exception/waiver workflow — upcoming-rollup source)
- **023** (policy acknowledgment — upcoming-rollup source)
- **024** (vendor lite module — upcoming-rollup source)
- **028** (audit-period freezing — upcoming-rollup source)

All dependencies merged.

## Anti-criteria (P0 — block merge)

- Does NOT bypass tenant RLS on any of the four reads.
- Does NOT fabricate posture, activity, or rollup data — every value resolves through an existing merged table or query path.
- Does NOT accept `tenant_id` in query or body (slice 033 D1).
- Does NOT permit a role without dashboard/program-read authz to reach any endpoint.
- Does NOT introduce an N+1 — each endpoint is one query (the upcoming-rollup is one `UNION ALL`, not four round-trips).
- Does NOT add a migration — this slice is read-only over existing schema. (If `residual_score` genuinely cannot be sorted without a generated column or index, surface that as a follow-up index/migration slice rather than smuggling a migration in here.)

## Skill mix (3–5)

- Go HTTP read handlers + `httpserver.go` mount-append
- sqlc query layer (cursor-paginated reads, `UNION ALL` rollup)
- Postgres aggregation (framework-grain rollup from per-control coverage)
- RLS-aware read endpoints + role-gated authz
- Cursor pagination over heterogeneous sources
