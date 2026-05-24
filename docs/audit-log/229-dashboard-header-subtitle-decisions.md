# Decisions log — slice 229 (dashboard header tenant + snapshot subtitle)

**Slice:** [`docs/issues/229-ui-honesty-dashboard-header-tenant-snapshot-context-missing.md`](../issues/229-ui-honesty-dashboard-header-tenant-snapshot-context-missing.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-23

---

## Summary

Slice 229 closes the dashboard header parity gap surfaced by slice 204's
audit fleet (finding F-204D-2): the mockup at
`Plans/mockups/dashboard.html` lines 117-120 renders contextual
orientation copy next to the H1 — the live build rendered generic
marketing copy that did not communicate which tenant the operator was
viewing nor what the aggregate freshness posture was.

This log captures the JUDGMENT calls. The slice spec is the source of
intent; this log is the source of "what we actually decided when the
spec said `your call`".

---

## D1 — Snapshot timestamp ("Snapshot taken N minutes ago") omitted from this slice

**Decision:** ship the **freshness pct** half of the subtitle in this
slice (AC-2's `"evidence freshness {pct}% within window"`). Omit the
**snapshot timestamp** half (`"Snapshot taken {relativeTime}"`). Record
the omission honestly via the silent-absence pattern (no
fabricated "just now" / "a few seconds ago" copy).

**Rationale:**

The slice spec's AC-2 text describes the snapshot timestamp as derived
from "the freshness response's most-recent `received_at`" and notes
that "slice 016 added this on the freshness read-model". I verified
against the v1 wire shape on `main`:

| Source                                                                | Field exposed today                                         |
| --------------------------------------------------------------------- | ----------------------------------------------------------- |
| `internal/freshness/freshness.go` `ControlFreshness` struct           | `RefreshedAt time.Time` ✅                                  |
| `internal/api/freshnessdrift/handlers.go` `Freshness()` HTTP response | `{bucket, buckets[], total, total_stale}` — NO timestamp ❌ |
| `web/lib/api.ts` `FreshnessReport` TypeScript shape                   | `{bucket, buckets[], total, total_stale}` — NO timestamp ❌ |

The data IS in the database (the underlying `evidence_freshness` rows
each have a `refreshed_at` column populated by the slice-016
scheduler). It is NOT projected into the HTTP wire shape, so the
frontend has no honest way to render the timestamp.

Per the slice spec's hard rule "honest about snapshot freshness — if
data source has no timestamp, don't fabricate one", and per the slice
spec's other hard rule "Do NOT add new platform endpoints — reuse
existing /v1/me / /api/me OR similar" (which I read as also
encompassing "do not change existing endpoint wire shapes in this
slice"), the right call is to ship the freshness pct (load-bearing
posture signal) and omit the timestamp.

The freshness pct alone closes the LOAD-BEARING gap surfaced by the
slice 204 audit: the operator now sees `"Sentinel Labs"` next to the
H1 and `"evidence freshness 87% within window"` below it. That is the
key orientation signal — "is the platform telling me a posture that
matches what I expect from the last evidence cycle?". The snapshot
timestamp is the secondary "and when was that posture computed?"
signal — useful, but additive.

**Spillover slice filed:** none yet — the maintainer should decide
whether to expand the `/v1/evidence/freshness` wire shape to include
a `refreshed_at` (most recent over the per-row `evidence_freshness.refreshed_at`
column) before filing the follow-on slice. The shape change is a
3-line addition to `internal/api/freshnessdrift/handlers.go` +
`web/lib/api.ts` + a one-line addition to the dashboard subtitle
component to render the relativeTime. Recommend `slice 273 — extend
freshness wire shape with refreshed_at + render snapshot timestamp in
dashboard header subtitle (closes slice 229 deferred half)`. NOT filed
in this PR because the dependency on the wire-shape change crosses the
slice boundary and is a separable decision.

---

## D2 — Environment slug ("· production") omitted

**Decision:** render only the **tenant name** next to the H1, NOT
`"{tenant.name} · {tenant.environment}"`.

**Rationale:**

The mockup's `"Sentinel Labs · production"` chip implies an
`environment` field on the tenant. I checked the wire shape returned
by `/v1/me/tenants` (`internal/api/me/tenants.go` line 66): the schema
is `{id, name, current: bool}` only. There is NO `environment` (or
equivalent) field on the tenant record.

Per the slice spec's AC-1: `'"{tenant.name} · {tenant.environment}" (or "{tenant.name}" only if environment is unset)'`.
Since environment is uniformly unset (the field does not exist on the
v1 schema), this resolves cleanly to the "name only" branch. The
mockup's `"· production"` substring is mockup placeholder copy, not a
data-bound surface.

If `environment` becomes a first-class field on the tenant record in
a future slice, the component can extend without a wire-shape change
visible to consumers — `formatTenantContext` already accepts a single
string and returns a single string.

---

## D3 — Tenant fetch NOT factored into a shared helper

**Decision:** the `useCurrentTenantName` hook in
`dashboard-header-subtitle.tsx` carries its own inline `fetch("/api/me/tenants")`
call rather than calling a shared `fetchMeTenants` helper.

**Rationale:**

The `TenantSwitcher` component (`web/components/auth/tenant-switcher.tsx`)
already does the same inline fetch — and carries enough
switcher-specific logic (periodic re-fetch at 60s via setInterval,
visibility-change pause/resume, cross-tab BroadcastChannel sync,
eviction banner) that lifting the bare fetch primitive into a shared
helper would either (a) couple this slice to the switcher's
re-fetch interval (overkill for the header chip, which re-mounts on
tenant switch via `router.refresh()`) or (b) require designing a new
abstraction now to avoid the coupling.

The slice 213 / 214 precedent is to ship narrow, self-contained client
components and let the maintainer refactor when a third call site
appears. The two-call-site case is not yet evidence of a shared
pattern.

---

## D4 — TanStack Query key reuse for freshness

**Decision:** the `DashboardHeaderSubtitle` component reuses the
EXACT TanStack Query key (`["dashboard", "freshness"]`) that the
parent `DashboardPage` uses for the `EvidenceFreshnessPanel`. The
subtitle and the panel share one cache entry; only one network call
fires.

**Rationale:**

Both surfaces read the same `/api/dashboard/freshness` endpoint and
both want the freshest-on-mount value. Splitting the keys (as slice
214 did for the sidebar badges) would create a second network call
on every dashboard mount. The cost is real but small (one fetch). The
slice 214 case was different — the sidebar badges live on every
authed page, and the parent page's key carries scope/filter state
that would couple the badges to filter toggles. The dashboard
subtitle has no such concern: it lives ONLY on `/dashboard`, alongside
the existing panel, and there is no filter state to couple to.

---

## D5 — Snapshot timestamp anti-criteria not added in this slice

**Decision:** the slice DOES NOT add an anti-criterion that forbids a
future contributor from rendering a snapshot timestamp.

**Rationale:**

D1 is a "ship in two halves" decision, not a "we will never ship the
timestamp" decision. A future slice can add the wire-shape field +
the render in one PR; no anti-criterion is needed (and adding one
would require a future repealer slice). The slice spec's existing
anti-criterion P0-A2 (no `"100% fresh of 0"`) is sufficient honesty
enforcement for the empty state; an additional anti-criterion for
the omitted timestamp would be defensive overreach.

---

## Verification surfaces

| Surface        | Coverage                                                                                                                                                                                                                                                                                                                        |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| vitest unit    | `web/components/dashboard/dashboard-header-subtitle.test.ts` — 12 cases pinning `computeFreshnessPct` + `formatFreshnessSubtitle` + `formatTenantContext` boundaries (empty, negative, round-to-int, clamp, AC-5 empty-state, AC-1 trim/blank).                                                                                 |
| Playwright e2e | `web/e2e/dashboard.spec.ts` — 6 new cases: AC-1 tenant chip via mocked `/api/me/tenants`, AC-2 pct via mocked `/api/dashboard/freshness`, AC-4 "Snapshot unavailable" on aborted freshness, AC-5 "No evidence ingested yet" on total=0, P0-229-2 anti-criterion on "100%", P0-229-1 anti-criterion on the prior marketing copy. |
| Manual         | not run — chrome-only change, the e2e covers the integrated render across the four state branches (loading is implicit via the skeleton testid path).                                                                                                                                                                           |

---

## Files changed

- `web/components/dashboard/dashboard-header-subtitle.tsx` (new)
- `web/components/dashboard/dashboard-header-subtitle.test.ts` (new)
- `web/app/(authed)/dashboard/page.tsx` (mount the two new components in the header)
- `web/e2e/dashboard.spec.ts` (six new cases under the slice 229 banner)
- `CHANGELOG.md` (Unreleased / Added entry)
- `docs/audit-log/229-dashboard-header-subtitle-decisions.md` (this file)
