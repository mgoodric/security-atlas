- **frontend:** slice 229 — dashboard header tenant + snapshot
  subtitle. Closes the parity gap surfaced by slice 204's audit fleet
  (dashboard slug, finding F-204D-2): the mockup at
  `Plans/mockups/dashboard.html` lines 117-120 renders a tenant chip
  next to the H1 (`"Sentinel Labs"`) plus a freshness subtitle
  (`"evidence freshness 87% within window"`). The live build rendered
  only generic marketing copy that communicated neither which tenant
  the operator was viewing nor the aggregate freshness posture. New
  module `web/components/dashboard/dashboard-header-subtitle.tsx`
  ships two client components mounted in
  `web/app/(authed)/dashboard/page.tsx`: `<TenantContext>` reads
  `/api/me/tenants` (the existing slice 192 BFF, same source as the
  `TenantSwitcher`) and renders the current tenant's name (AC-1);
  `<DashboardHeaderSubtitle>` reuses the existing
  `["dashboard", "freshness"]` TanStack Query key (so the subtitle
  piggybacks on the cache the `EvidenceFreshnessPanel` already
  populates — no double-fetch, P0-229-1: no new platform endpoint)
  and binds to the four state branches per AC-2/3/4/5: skeleton on
  loading, `"Snapshot unavailable"` on error, `"No evidence ingested
yet"` on `total === 0` (P0-229-2: honest empty state, never
  `"100% fresh of 0"`), and `"evidence freshness {pct}% within
window"` on success. Pure helpers `computeFreshnessPct`,
  `formatFreshnessSubtitle`, `formatTenantContext` factored out and
  unit-tested in `dashboard-header-subtitle.test.ts` (12 cases pinning
  the rounding, empty/negative defensive paths, the AC-5
  empty-state boundary, and the AC-1 trim/blank silent-absence
  branches). Playwright spec `web/e2e/dashboard.spec.ts` extended
  with six assertions covering AC-1/2/4/5 + P0-229-1/2 via mocked
  BFF payloads (the audits-header.spec.ts canonical pattern —
  expanding seed coverage for tenant + freshness shape variants is
  out of scope for a chrome-only slice). JUDGMENT call recorded in
  `docs/audit-log/229-dashboard-header-subtitle-decisions.md` D1: the
  snapshot timestamp half of the mockup's subtitle (`"Snapshot taken
N minutes ago"`) is OMITTED because the
  `/v1/evidence/freshness` wire shape exposes
  `{bucket, buckets[], total, total_stale}` only — no `received_at`
  / `refreshed_at` field. Per the slice spec's hard rule "honest
  about snapshot freshness — if data source has no timestamp, don't
  fabricate one", and per the hard rule "do NOT add new platform
  endpoints", the freshness pct (load-bearing posture signal) ships
  and the timestamp is deferred to a wire-shape-extension follow-on.
