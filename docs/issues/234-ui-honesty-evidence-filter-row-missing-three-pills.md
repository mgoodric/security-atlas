# 234 — UI honesty: /evidence filter row missing three pills (Source, Scope, Since)

**Cluster:** Quality / UI hygiene
**Estimate:** 1.5d (3 pills × ~0.5d each — backend wire already supports two of three)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (UI parity audit fleet)

## Narrative

Surfaced during the slice 204 per-page audit of `/evidence`
(audit log: `docs/audit-log/204-page-audit-evidence.md`).

The mockup at `Plans/mockups/evidence.html` lines 125-184 shows a
**six-pill** horizontal filter row above the ledger table:

| #   | Mockup pill | Live pill         |
| --- | ----------- | ----------------- |
| 1   | Control     | Control (present) |
| 2   | Kind        | Kind (present)    |
| 3   | Result      | Result (present)  |
| 4   | Source      | **MISSING**       |
| 5   | Scope       | **MISSING**       |
| 6   | Since       | **MISSING**       |

The live page at `https://atlas-edge.home.gmoney.sh/evidence` renders
only the first three. Source: `web/app/(authed)/evidence/page.tsx`
lines 198-217.

Backend support:

- **Source** — the `/v1/evidence` handler (slice 106) accepts
  `source_actor_type` and `source_actor_id` query params; the BFF
  forwards them. The pill is one URL-state binding away from
  shipping.
- **Scope** — the `EvidenceRecord` wire already carries `scope_cell`
  (rendered in the table). A scope-cell filter is a new server-side
  predicate but the data is present in the table rows.
- **Since** — the `/v1/evidence` handler accepts `observed_after`
  (slice 106) and the mockup shows preset windows ("Last 7 days",
  "Last 24 hours", "Last 30 days", "Audit period Q2 2026"). The
  preset → timestamp translation is client-side.

Effect on the solo security leader running their first SOC 2 audit:
they expect to scope ledger queries to the audit period (the most
load-bearing preset on the mockup) and currently cannot, because
the Since pill does not exist. They will reach for a CSV export +
spreadsheet to do the time-window slice — exactly the v1 success-
test failure mode CLAUDE.md warns against.

The slice ships three new `FilterPill` entries with the existing
slice 098 URL-state binding pattern. Each pill is a `<select>`
matching the mockup's options.

## Threat model

**Verdict.** **no-mitigations-needed.** The Source + Since pills
bind existing `/v1/evidence` query params behind the BFF; the
filter logic is server-side and respects RLS. The Scope pill
introduces a new server predicate but the read remains
RLS-protected. No new mutating operations.

## Acceptance criteria

- **AC-1.** A `Source` pill is added to the `<FilterPills>` array
  in `web/app/(authed)/evidence/page.tsx`. Options derived from
  the distinct `source.actor_type` + `source.actor_id` tuples in
  the current result set (same pattern as `buildKindOptions`).
- **AC-2.** Selecting a Source pill option sets the
  `source_actor_type` + `source_actor_id` URL params and re-issues
  the `/api/evidence` query.
- **AC-3.** A `Since` pill is added with four options: "Last 24
  hours", "Last 7 days", "Last 30 days", "Audit period (current)".
  The "Audit period (current)" option reads the active audit-
  period boundary from `/v1/audit-periods?active=true` (slice
  048 endpoint) and binds its `started_at` to `observed_after`.
- **AC-4.** A `Scope` pill is added with options derived from
  the distinct `scope_cell` values in the current result set. The
  selection sets a new `scope_cell` URL param; the BFF forwards
  it to `/v1/evidence?scope_cell=...`. Backend addition (small):
  `internal/api/controldetail/handler.go` accepts the new param
  and adds an SQL predicate.
- **AC-5.** All three new pills participate in `clearFilters()`
  and the `isDefault(filters)` check.
- **AC-6.** Playwright spec for `/evidence` is extended: each new
  pill renders, selecting an option narrows the result count, and
  clearing returns to the unfiltered ledger.
- **AC-7.** Slice 204 audit's MOCKUP-STALE finding F-204-E-2 is
  resolved on the next audit run.

## Constitutional invariants honored

- **Invariant 4 (scope is multidimensional).** The Scope pill
  filter must respect that scope is a tuple expression, not a
  free-text label. The pill's options surface only the
  `scope_cell` values that the upstream actually rendered (no
  invented values).
- **Invariant 6 (tenant isolation at DB layer).** New backend
  predicate is added inside the RLS-bound query path.
- **Anti-pattern rejected:** Mockups that promise filter axes
  the product cannot deliver.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — evidence query shape
- `Plans/canvas/05-scopes.md` — scope cells
- `Plans/canvas/08-audit-workflow.md` — audit-period awareness in
  ledger reads
- `Plans/mockups/evidence.html` lines 125-184 — the mockup pills

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent.
- **#106** (evidence list query params) — `merged`. The Source
  - Since pills bind its existing params.
- **#048** (audit-periods backend) — `merged`. The "Audit period
  (current)" option calls its endpoint.

## Anti-criteria (P0 — block merge)

- **P0-234-1.** Does NOT introduce a free-text Scope filter
  (`<input>`). Options come from observed `scope_cell` values
  only.
- **P0-234-2.** Does NOT change the `EvidenceRecord` wire shape;
  only adds a new optional query param.
- **P0-234-3.** Does NOT add the new `scope_cell` SQL predicate
  outside the existing RLS-bound query path.

## Skill mix (3-5)

1. Next.js App Router — adding URL-state-bound filter pills
2. Go HTTP handler — accepting a new optional query param
3. sqlc query update — adding the optional WHERE predicate
4. Playwright spec extension
