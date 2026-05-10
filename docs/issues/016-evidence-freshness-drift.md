# 016 — Evidence freshness + drift detection + stale flagging

**Cluster:** Evidence pipeline
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Compute and expose two leading indicators over the evidence ledger: **freshness** (is the most recent evidence record for each control inside its `freshness_class` window?) and **drift** (controls that flipped pass→fail in the last N days). Both are derived signals — they live in a materialized read model, refreshed on ledger writes. Freshness produces a per-control `valid_until` based on the freshest record's `observed_at + freshness_class.max_age`. Drift produces a signed delta of `controls_passing_today - controls_passing_yesterday`. The slice delivers value because the dashboard's "Evidence freshness" and "Recent drift" panels (per mockup) render real signals.

## Acceptance criteria

- [ ] AC-1: `GET /v1/evidence/freshness?bucket=class` returns freshness distribution by `freshness_class`
- [ ] AC-2: Records past `valid_until` are flagged `stale=true` in the read API but not deleted from the ledger
- [ ] AC-3: `GET /v1/controls/drift?since=7d` returns controls that flipped pass→fail in the window with `delta`, last passing date, current evidence
- [ ] AC-4: Drift recomputes daily at 00:00 UTC and on every ledger write
- [ ] AC-5: Dashboard mockup's "Evidence freshness" panel data (slice 040) is sourced from this slice's endpoint
- [ ] AC-6: Stale records remain queryable for audit replay — never deleted

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** freshness/drift computed from ledger; never writes to ledger

## Canvas references

- `Plans/canvas/02-primitives.md` §2.3 (freshness model table)
- `Plans/canvas/07-metrics.md` §7.1 (KPIs — Evidence freshness, Drift count)
- `Plans/mockups/dashboard.html` (visual reference)

## Dependencies

- #012

## Anti-criteria (P0)

- Does NOT delete stale records
- Does NOT compute freshness without respecting per-control `freshness_class`
- Does NOT lose the drift signal across restarts (persisted, not in-memory only)

## Skill mix (3–5)

- Postgres materialized views or read-model tables
- sqlc + scheduled jobs
- Time-series window queries
- Go background workers
- Integration tests with controlled time
