# 125 — Frontend `/audit-log` page (consumes slice 124's unified aggregation endpoint)

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Filed 2026-05-17 via `/idea-to-slice` as a spillover from slice 124 (unified audit-log aggregation API). The maintainer's feature ask was "every audit event visible in the app + written to an external sink for tamper-evident retention outside the app." Slice 124 ships the backend; this slice ships the in-app view; slice 126 ships the external sink.

The frontend page lives at `/audit-log` (NOT `/audit` — that's the audit-period workspace per slice 094's URL convention). It displays the unified-audit aggregation as a filterable, paginated table: filters for `from`/`to` (default last 7 days), `actor` (typeahead from `/v1/admin/users`), `kind` (multi-select chips for the 9 audit-log kinds), and infinite-scroll pagination via the cursor returned by the backend.

Each row shows `occurred_at`, `actor_name` (resolved from actor_id via a sibling endpoint or join — implementing engineer's JUDGMENT), `kind`, `target_type`, `target_id`, `action`, and a "details" affordance that expands to show the `payload_json` formatted. Read-only — no inline edit, no bulk action.

The page MUST require admin OR auditor role to render (route-level guard, not just API-level — UI shouldn't even appear to non-admins).

## Threat model

| STRIDE                       | Threat                                                                                | Mitigation                                                                                                                                                     |
| ---------------------------- | ------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Page renders for unauthorized user (e.g., URL crafted to bypass nav-level role guard) | AC: route-level role guard in `web/middleware.ts`; backend 403 is the second line; integration test asserts non-admin gets redirect to `/dashboard` with toast |
| **T** Tampering              | Page lets admin tamper with audit rows                                                | n/a — page is strictly read-only; AC explicitly forbids any mutation affordance                                                                                |
| **R** Repudiation            | Page's queries leave no trace beyond slice 124's meta-audit                           | inherits slice 124's meta-audit; no additional surface                                                                                                         |
| **I** Information disclosure | Page leaks tenant B's rows by mis-issuing the request                                 | inherits slice 124's tenant isolation (the page calls `/v1/admin/audit-log/unified` which is tenant-scoped via session)                                        |
| **D** Denial of service      | Infinite-scroll fetches all 1M rows                                                   | AC: pagination respects backend's 1000-row cap + 90-day window limit; UI degrades to "load more" button after 10 pages (10K rows) to make the cost visible     |
| **E** Elevation of privilege | n/a — page doesn't add new actions                                                    | n/a                                                                                                                                                            |

## Acceptance criteria

- [ ] AC-1: New route at `web/app/audit-log/page.tsx` (server component shell) + `web/app/audit-log/page-client.tsx` (TanStack Query-driven client island)
- [ ] AC-2: Filters: `from` (default 7 days ago), `to` (default now), `actor` (typeahead), `kind` (multi-select). State serialized to URL query params (back/forward + share-link work)
- [ ] AC-3: Table renders `occurred_at` (local time + UTC tooltip), `actor`, `kind`, `target`, `action`, expand-for-payload. Use shadcn `<Table>` + `<Collapsible>`
- [ ] AC-4: Pagination: cursor-driven, infinite-scroll with `<IntersectionObserver>` trigger. After 10 pages auto-loaded, switch to manual "Load more" button
- [ ] AC-5: Route guard: `web/middleware.ts` checks for admin OR auditor role on `/audit-log` paths; redirects to `/dashboard?error=admin-only` otherwise
- [ ] AC-6: BFF route `web/app/api/audit-log/unified/route.ts` forwards the request to the platform backend; passes the bearer + atlas_session cookie (slice 110 pattern)
- [ ] AC-7: vitest cases for the BFF route (auth forwarding, malformed query handling, 403 pass-through)
- [ ] AC-8: Playwright e2e spec `web/e2e/audit-log.spec.ts` covers (a) admin sees rows, (b) non-admin redirected, (c) filters update URL, (d) cursor pagination loads next page

## Dependencies

- **124** (unified audit-log aggregation API) — must merge first; this slice consumes the endpoint
- Slice 110 (BFF cookie forwarding) — already merged
- Slice 082 + 119 + 122 (e2e seed harness pipeline) — already merged or in flight

## Anti-criteria (P0)

- **P0-A1**: Does NOT inline-edit or delete audit-log rows. Read-only UI only.
- **P0-A2**: Does NOT bypass the route-level role guard. Backend 403 is defense-in-depth, not the only line.
- **P0-A3**: Does NOT page through more than 90 days in one URL. The backend rejects this with 400; the UI respects it (date picker has max range validation).
- **P0-A4**: Does NOT use vendor-prefixed test fixture tokens.

## Notes

- Mockup MAY need to be authored first. Check `Plans/mockups/` for an existing `audit-log.html` mockup. If absent, file a sibling slice for the mockup (per slice 093's pattern) before implementing this one.
- The `actor_name` resolution: easier path = backend includes `actor_name` in the response (small extension to slice 124). Harder path = frontend joins via separate calls. Engineer's JUDGMENT; document in decisions log.
