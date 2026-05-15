# 041 — Control detail view + UCF mini-viz — decisions log

Slice 041 is `Type: AFK` in its frontmatter — its acceptance criteria
are mechanically verifiable. But the slice surfaced three genuine
build-time judgment calls (a vocabulary drift across three sources, and
two acceptance criteria that name backend endpoints not yet on main).
This log records them in the JUDGMENT-slice format so the maintainer can
re-evaluate the calls once the view is in real use against a real
platform.

## Decisions made

### 1. STRM relationship-type vocabulary: open string, not a closed union

**Options considered:**

- **(A) Reuse slice 005's closed 3-value union** (`"equal" | "subset_of"
| "intersects"` from `web/lib/api.ts` `RequirementWithMapping`).
- **(B) Hardcode the canvas's 5-value vocabulary** as a closed union
  (`equal`, `subset_of`, `superset_of`, `intersects_with`,
  `no_relationship`).
- **(C) Type `relationship_type` as an open `string`** plus a
  known-value style map with a neutral grey fallback.

**Chosen: (C).**

**Rationale.** Three sources disagreed:

- `internal/db/dbx/models.go` — the `StrmRelationshipType` enum has
  **five** values: `equal`, `subset_of`, `superset_of`,
  `intersects_with`, `no_relationship`. This is ground truth — it is
  what the slice-008 `/coverage` handler emits for `relationship_type`.
- `Plans/mockups/control.html` — renders **three** labels, and uses the
  shorthand `intersects` (no `_with`).
- `web/lib/api.ts` (slice 005) — declares a closed 3-value union that
  predates the full enum.

Option (A) would silently drop `superset_of` rows — a real STRM type the
backend can return — which violates the slice's P0 anti-criterion
against fabricated/incomplete mappings. Option (B) hardcodes a snapshot
of the enum that rots the next time SCF's crosswalk vocabulary changes.
Option (C) renders exactly what the backend returns: known values get a
styled badge + edge color (`web/components/control/strm.ts`), anything
unrecognized gets a neutral grey fallback rather than crashing or being
dropped. The `no_relationship` value is filtered out by the slice-008
traversal SQL, so the view in practice sees at most four — but the map
covers all five plus the fallback.

**Confidence: high.** The DB enum was read directly; the
open-string-with-fallback pattern is strictly safer than any closed
union and cannot regress on a vocabulary change.

### 2. Evidence stream (AC-4): empty-state placeholder, not a blocker

**Options considered:**

- **(A) Return to the caller** — AC-4 names a backend endpoint that does
  not exist; surface it as a blocker.
- **(B) Scope-creep into backend Go** — add a `GET /v1/evidence` list
  endpoint as part of this frontend slice.
- **(C) Ship the section as an empty-state** naming the missing
  endpoint + follow-up, bind everything else, mark AC-4 PARTIAL.

**Chosen: (C).**

**Rationale.** AC-4 reads "Evidence stream paginates last 30 days from
`/v1/evidence?control_id=...`". Codebase verification found only
`POST /v1/evidence:push` on main (`internal/api/httpserver.go`,
`internal/api/evidence/http.go`) — there is no evidence-list read
endpoint. This is the exact slice-060 situation: slice 060 hit three
missing backend surfaces (SSO CRUD, users list, audit-log read model)
and shipped them as empty-state placeholders that name the missing
endpoint + follow-up slice, documenting the gap inventory in its PR
rather than blocking. Option (B) is explicitly out of scope for a
frontend slice and would couple a Go endpoint's review into a `web/`-only
PR. Option (A) treats a known, documented pattern as a novel blocker.
Option (C) matches precedent: the `evidence-stream-section` renders an
`Alert` naming `GET /v1/evidence?control_id=…` and the follow-up; no
ledger entries are fabricated (P0-3 honored); the other six ACs all bind
to merged backends. AC-4 is recorded as PARTIAL.

**Confidence: high.** The endpoint absence was verified by grep across
`internal/` and `cmd/`; the slice-060 precedent is explicit and recent.

### 3. Freshness clock (AC-5): bind to slice 012 `/state`, not slice 016

**Options considered:**

- **(A) Block on slice 016** — AC-5 says "binds to slice 016
  `valid_until`"; slice 016 is not merged, so block.
- **(B) Bind to slice 012's `/state`** — `freshness_status`,
  `last_observed_at`, `freshness_class` — the merged freshness surface.

**Chosen: (B).**

**Rationale.** `_STATUS.md` shows slice 016 (Evidence freshness + drift
detection) as `ready`, not started — it is not on main. But slice 012's
`GET /v1/controls/{id}/state` (merged) returns, per scope cell,
`freshness_status`, `last_observed_at`, `evidence_count_in_window`, and
`freshness_class` — everything the freshness clock needs to render. Slice
016's `valid_until` / drift-detection layer is _additive_ over that, not
a prerequisite for a freshness clock. Blocking the whole slice on 016
when the clock can be fully built against a merged endpoint would be
friction without cause. When 016 lands, the `valid_until` / drift overlay
is a small additive change to `web/components/control/freshness-clock.tsx`.

**Confidence: high.** `/state`'s wire shape was read directly from
`internal/api/controlstate/handlers.go`; it carries every field the clock
binds.

### 4. `/state` aggregation: worst-status + most-recent-observed

`/state` returns one entry per scope cell. The clock shows a single ring

- readout, so the per-cell entries must be aggregated. Chosen: show the
  **most recent** `last_observed_at` (the freshest signal the control has)
  and the **worst** `freshness_status` across all cells (the weakest link).
  A naive average could hide one stale cell behind otherwise-fresh cells —
  which would be misleading for a compliance surface.

**Confidence: medium.** The aggregation is sound, but "worst cell" vs
"latest cell" is a presentation choice a real user might want to tune
(e.g. a per-cell breakdown on hover/expand).

### 5. Coverage "strength bar" shows mapping strength, not weighted coverage

The mockup's table has a "Coverage" column (`strength × effectiveness`,
intersected with framework scope). The issue's AC-2 says "STRM types +
strengths visible per row". The slice renders the **mapping strength**
bar, labeled as strength — it does _not_ recompute
`strength × effectiveness` per row. Fabricating the weighted number
client-side risks a value that disagrees with whatever the framework
dashboard (slice 008/012 territory) computes server-side; that is a P0
anti-criterion risk. The honest, verifiable number is the mapping
strength the `/coverage` endpoint returns.

**Confidence: medium.** Correct for now, but when a server-side weighted
coverage value exists, the table should show that instead.

### 6. Linked policies / risks / audit-log rail: mockup-layout empty-states

The mockup's right rail includes linked-policies, linked-risks, and
audit-log sections. No per-control read endpoint for any of these is on
main. Following the same precedent as decision 2, these render the
mockup's section layout as labelled empty-states naming the dependency,
rather than being omitted (keeps the layout faithful to the mockup) or
fabricating data.

**Confidence: high.** Same pattern as decision 2; no data invented.

## Revisit once in use

- **Re-bind the evidence stream** once a `GET /v1/evidence?control_id=…`
  list endpoint ships. The `evidence-stream-section` placeholder in
  `web/app/(authed)/controls/[id]/page.tsx` is the seam; add the client
  fn + BFF route following the four already in this slice.
- **Add the slice-016 drift overlay** to `freshness-clock.tsx` once
  slice 016 (`valid_until` / drift detection) merges — `valid_until` and
  "records past valid_until" (a count the mockup shows) become available
  then.
- **Re-evaluate the freshness aggregation** (decision 4) — if users want
  to see _which_ scope cell is stale, add a per-cell breakdown rather
  than only the worst-status rollup.
- **Swap the strength bar for server-side weighted coverage** (decision 5) if/when the framework-dashboard coverage computation exposes a
  per-(control, requirement) weighted number through an endpoint.
- **Wire the linked policies / risks / audit-log rail** (decision 6)
  once per-control policy-link, risk-link, and control-history read
  endpoints exist.
- **Install `@playwright/test`** and run `web/e2e/control-detail.spec.ts`
  for real — today it is a static `ifPlaywright` contract (the
  repo-wide pattern). Installing the runner touches `web/package.json`
  (a spine file) and is a shared follow-up across the frontend slices.
- **Reconcile slice 005's `RequirementWithMapping.strm_type` 3-value
  union** with the 5-value DB enum — out of scope for this slice, but
  the drift documented in decision 1 will eventually bite the
  `catalog/scf/[id]` view the same way if not fixed there.

## Confidence summary

| Decision                              | Confidence |
| ------------------------------------- | ---------- |
| 1 — STRM open-string typing           | high       |
| 2 — evidence stream placeholder       | high       |
| 3 — freshness clock binds to `/state` | high       |
| 4 — `/state` worst-status aggregation | medium     |
| 5 — strength bar = mapping strength   | medium     |
| 6 — rail sections as empty-states     | high       |

The two `medium`-confidence calls (4 and 5) are the top of the revisit
list — both are presentation choices that a real user iterating against
real data may want changed; neither is a correctness risk.
