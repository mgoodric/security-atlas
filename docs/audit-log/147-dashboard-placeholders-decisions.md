# 147 — Dashboard placeholder panels (slice 066 follow-on) — decisions log

Slice 147 is `Type: AFK` — diagnose-heavy. The operator-facing symptom
(v1.10.0 Unraid: two dashboard panels render literal "endpoint does not
exist on main yet" placeholders) had three candidate root causes in the
issue. The diagnosis was deterministic; the fix selected itself. This
log records the diagnosis (D1), the fix-path picked (D2), and the
explicit scope-discipline calls (D3, D4) so a maintainer can re-evaluate
later.

## D1 — Diagnosis: Path B (endpoints exist; frontend was never re-pointed)

**Issue hypothesis (verbatim from `docs/issues/147-...md`):**

> "Hypothesis: slice 066 shipped frontend SHELL updates (panel
> components with TanStack Query wiring) but the backend endpoints
> `/v1/frameworks/posture` and `/v1/activity` either (a) didn't ship,
> (b) ship under different paths, OR (c) ship but return errors the
> frontend treats as 'doesn't exist'."

**What the grep + read actually shows.**

| Surface                                | Status on main (147 branch)                                                                     | Evidence                                                                                                          |
| -------------------------------------- | ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| Backend route `/v1/frameworks/posture` | **REGISTERED** at `internal/api/httpserver.go:603`                                              | `root.Get("/v1/frameworks/posture", dashboardH.FrameworkPosture)`                                                 |
| Backend route `/v1/activity`           | **REGISTERED** at `internal/api/httpserver.go:604`                                              | `root.Get("/v1/activity", dashboardH.Activity)`                                                                   |
| Backend route `/v1/upcoming`           | **REGISTERED** at `internal/api/httpserver.go:605`                                              | `root.Get("/v1/upcoming", dashboardH.Upcoming)`                                                                   |
| Backend handlers                       | **PRESENT** at `internal/api/dashboard/handler.go` (`FrameworkPosture`, `Activity`, `Upcoming`) | wire shapes (`postureWire`, `activityWire`, `upcomingWire`) match slice 066 AC contracts column-for-column        |
| Backend integration tests              | **PASSING** at `internal/api/dashboard/integration_test.go` (real Postgres, RLS-isolated)       | `TestFrameworkPosture_AggregatesAcrossVersions`, `TestActivity_PaginatesNewestFirst`, ISC-23 tenant-isolation arm |
| Backend OpenAPI exposure               | **DECLARED** at `internal/api/openapi/routes.go:36+89`                                          | both endpoints in `RouteSpecs` slice (slice 140's generator)                                                      |
| BFF proxies under `/api/dashboard/*`   | `drift`, `freshness`, `risks`, `upcoming` exist — **NO `framework-posture`, NO `activity`**     | `ls web/app/api/dashboard/` — only four route handlers                                                            |
| Frontend `lib/api.ts` fetcher fns      | **MISSING** for posture + activity                                                              | only `fetchDashboardDrift`/`Freshness`/`Risks`/`Upcoming` exist; no `fetchDashboardFrameworkPosture` / `Activity` |
| Frontend `framework-posture-panel.tsx` | renders `MissingEndpointPanel` (slice 040 placeholder)                                          | hard-codes copy `"A per-framework coverage + freshness composite endpoint with a 90-day trend is needed"`         |
| Frontend `activity-feed-panel.tsx`     | renders `MissingEndpointPanel` (slice 040 placeholder)                                          | hard-codes copy `"A read model over the NATS-driven event-stream archive is needed"`                              |

**Root cause.** Path B. Slice 066's backend shipped clean (gh#109,
`786b8a0`); the slice's own decisions log D6 explicitly recorded the
frontend re-pointing as out-of-scope, citing "the 041→064 / 060→062
precedent" — backend slices ship the endpoint + wire-shape contract;
the small mechanical frontend re-point is a separate follow-on
("Revisit once in use" §1 of `docs/audit-log/066-...-decisions.md`).
That follow-on was filed as slice 147 (this slice).

The operator-facing string "does not exist on main yet" is literally
true relative to the frontend code that ships in v1.10.0: the frontend
panel component never grew a real binding, so it kept rendering its
slice-040 placeholder copy. Backend is honest; frontend is stale. Slice
147 closes the loop.

**Not (a):** endpoints ship, registered, RLS-tested, OpenAPI-declared.
**Not (c):** integration tests show clean 200s on empty tenants (the
empty-set check is the very first arm — `TestFrameworkPosture_Empty` /
`TestActivity_Empty`); the handler returns `{frameworks: [], count: 0}`
or `{activity: [], count: 0, next_cursor: ""}`, never 500. The empty-
install integration test in this slice (AC-5) re-verifies that.

## D2 — Fix: re-point the two `MissingEndpointPanel`s; add no new backend

**Chosen: Path B (frontend re-point only).**

The fix is purely additive on the frontend:

1. Two new BFF route handlers under `web/app/api/dashboard/`:
   `framework-posture/route.ts` and `activity/route.ts`, each a single
   `dashboardProxy(...)` call following the existing slice-040
   four-route template.
2. Three new types in `web/lib/api.ts` (`FrameworkPostureRow`,
   `FrameworkPostureReport`, `ActivityEvent`, `ActivityFeedResponse`)
   mirroring the Go wire shapes exactly.
3. Two new server-side fns (`getFrameworkPosture`, `getActivity`)
   following the `getControlDrift` / `getEvidenceFreshness` template.
4. Two new browser-side fns (`fetchDashboardFrameworkPosture`,
   `fetchDashboardActivity`) following the `fetchDashboardDrift` / etc
   template.
5. Rewrite the two panel components to use `PanelCard` (the bound-state
   chrome) instead of `MissingEndpointPanel`, with TanStack Query
   queries owned by `app/(authed)/dashboard/page.tsx` (matching the
   existing four-panel pattern). The data-free scaffold (six framework
   slots; four filter chips) graduates to render real data.
6. CHANGELOG entry under `[Unreleased]/Added`.
7. Playwright e2e spec updated to assert the new panels render their
   bound rows instead of the placeholder copy.

Zero backend touch. Zero migration. Zero new dependency. The fix is
the minimal mechanical re-point the slice-066 decisions log forecast.

## D3 — Scope discipline: top-risks-sort and upcoming-rollup are OUT of scope

The slice-066 follow-on cluster has four items per the slice-066
decisions log:

1. `framework-posture-panel` → `/v1/frameworks/posture` (this slice)
2. `activity-feed-panel` → `/v1/activity` (this slice)
3. `upcoming-panel` → `/v1/upcoming` (currently bound to
   `/v1/exceptions/expiring`; the broader rollup endpoint exists but
   the panel does not consume it)
4. `top-risks-panel` → re-point to `?sort=residual,age` (currently
   binds bare `?treatment=mitigate` and renders an honest "ranking
   pending" caveat)

**This slice intentionally fixes only #1 and #2.**

The slice-147 narrative + AC list names ONLY the two
`MissingEndpointPanel`s — those are the panels rendering the literal
"endpoint does not exist on main yet" string that the v1.10.0
operator surfaced. The upcoming-panel and top-risks-panel render
real, bound data today with softer "partial-data caveat" footers, not
the full placeholder. Conflating the four would scope-creep this
slice into the "slice 066 follow-on completeness" sweep that
P0-DASH-3 ("NO scope creep into other dashboard panels") explicitly
forbids. The narrow read of P0-DASH-3 names drift + metrics; the
defensive read keeps slice 147 surgical and lets a follow-up slice
handle the two partial-data panels as a coherent batch.

**Spillover filed:** `docs/issues/148-dashboard-upcoming-rollup-and-risks-residual-sort.md`
(see end of this log for the slug). It cites slice 147 as parent and
slice 066 as the endpoint source. Per Amendment 2 the spillover file
is added but `docs/issues/_INDEX.md` is NOT modified — the maintainer
sweeps spillovers in batch.

## D4 — Wire-shape mapping: ts string passthrough, summary as unknown JSON

The Go `activityWire.Summary` is `json.RawMessage` (any JSON value),
and `Ts` is an RFC3339Nano string. The TypeScript type mirrors:

```ts
export type ActivityEvent = {
  ts: string; // RFC3339Nano
  event_type: string;
  actor: string;
  resource_type: string;
  resource_id: string;
  summary: unknown; // forwarded as-is; UI renders no value yet
};
```

The activity-feed panel renders `event_type · resource_type ·
resource_id · relative-time(ts)` per row. `summary` is forwarded to
the client but the panel does NOT render it — the slice-062
admin_audit_log_v evidence branch packs a tenant-specific JSON blob
whose shape varies by event type (push outcome, dedupe key, validation
error, etc); rendering it would require an event-type-aware
formatter that is its own UX slice. Surfacing `summary` only when a
user opens a row detail drawer is a v2 elaboration — out of scope
for the placeholder unstick.

The framework-posture wire is fully typed (float64 trio + two strings):

```ts
export type FrameworkPostureRow = {
  framework_id: string;
  framework_version: string;
  coverage_pct: number; // 0-100, two-decimal in Go
  freshness_composite: number; // 0-100
  trend_delta_90d: number; // signed; +/-100 bounds
};
```

The six-slot scaffold (SOC 2, ISO 27001, NIST CSF, HIPAA, PCI DSS,
GDPR) is REPLACED by a tile per returned row — no name-keyed slot.
A tenant with zero frameworks renders the panel's empty-state copy
("No active framework versions yet"). A tenant with one framework
renders one tile; the grid stays `lg:grid-cols-6` so the lonely tile
sits left-aligned without weird spacing.

## Confidence summary

| Decision                                                         | Confidence |
| ---------------------------------------------------------------- | ---------- |
| D1 — diagnosis Path B (frontend re-point only)                   | high       |
| D2 — fix shape (4 fn + 2 BFF + 2 panel rewrite + types)          | high       |
| D3 — scope discipline (upcoming + risks-sort spilled, not done)  | medium     |
| D4 — `summary` forwarded but not rendered; six-slot tile replace | high       |

D3 is the only `medium` — a maintainer who reads "slice 066 follow-on"
broadly may prefer all four panels re-pointed in one PR. The narrow
read sticks to the literal placeholders the operator reported; the
spillover (slice 148) captures the broader sweep so nothing is lost.

## Spillover filed

- `docs/issues/148-dashboard-upcoming-rollup-and-risks-residual-sort.md`
  — re-point upcoming-panel to `/v1/upcoming` and top-risks-panel to
  `?sort=residual,age`. Cites parent 147 + endpoint source 066. Per
  Amendment 2, the file is added but `_INDEX.md` is NOT modified.
