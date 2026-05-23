# 270 — Non-admin activity-ledger surface (`/activity`)

**Cluster:** Frontend (+ minimal backend if needed)
**Estimate:** ~1d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** spillover from slice 204 / precursor to slice 232 (dashboard activity-feed "View full activity ledger" footer link). Filed 2026-05-23 to unblock the spillover identified during the slice 204 audit-fleet aggregate.

## Narrative

The slice 204 audit found that the dashboard mockup's activity-feed panel includes a "View full activity ledger" footer link, but no public surface exists. Slice 067 (admin audit-log page) ships an admin-scoped destination at `/audit-log`; slice 186 ships the role-conditional sidebar pattern. Slice 232 explicitly defers the design choice to a separate slice — this slice owns that choice.

The decision: ship a non-admin `/activity` page that shows the operator's own actions PLUS tenant-public activity (control state changes, evidence ingestion, audit-period freezes — the same shape the dashboard's activity-feed panel surfaces). This is distinct from `/audit-log` (admin-scoped, all-tenant), and it's the natural target for the dashboard's footer link.

The shape:

- **Route**: `/activity` (authed; any tenant member, not admin-gated)
- **Backing endpoint**: the existing slice 124 unified audit-log aggregation API supports per-actor filtering AND public-event filtering via the `subject_module` projection (slice 180). The implementing slice composes existing endpoints; no new endpoint is required.
- **OPA admit**: read-anything-you-can-see (operator's own actions + any tenant-public event). This is more permissive than slice 067's admin gate but RLS-bound at the data layer — operators never see other tenants' rows.
- **UI**: filterable / paginated list, mirror of the dashboard activity-feed panel but with full pagination + per-actor + per-kind filters. Reuses slice 125 audit-log page's React shell.

### What ships in this slice

**Frontend**:

- New route `web/app/(authed)/activity/page.tsx` rendering the activity-ledger list.
- New BFF route `web/app/api/activity/route.ts` forwarding to `/v1/admin/audit-log/unified` with the operator's own filter applied (no `admin` filter; the backend's existing role-conditional projection handles tenant-public vs admin-only visibility).
- Filters: `from`/`to` (date range), `kind` (multi-select chips), `actor` (defaults to "all" but defaults to current operator when navigated from the dashboard footer link).
- Reuses slice 125's `<UnifiedAuditTable>` component verbatim; the data binding differs only in default filter values.
- The dashboard activity-feed footer link from slice 232 will eventually point here.

**Backend (minimal)**:

- Slice 124's `/v1/admin/audit-log/unified` endpoint is admin-gated. Either:
  - **Option A**: extend the OPA admit to include non-admin tenant members + add a backend filter that restricts non-admin callers to "their own actions + tenant-public events" (recommended)
  - **Option B**: ship a new `/v1/activity/unified` endpoint with the non-admin admit + restricted projection
- The engineer picks A or B with documented rationale in the decisions log. **D1** is filed as a build-time call.

**No new schema, no new migration.** This is a frontend page + a (minimal) backend admit / filter extension.

## Threat model

| STRIDE                       | Threat                                                                                                                         | Mitigation                                                                                                                                                                                                                                                                                                                             |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — standard JWT auth.                                                                                                       | Inherits slice-190 jwtmw.                                                                                                                                                                                                                                                                                                              |
| **T** Tampering              | n/a — read-only page.                                                                                                          | n/a                                                                                                                                                                                                                                                                                                                                    |
| **R** Repudiation            | Operator's own activity is visible to them — same data the activity-feed panel already surfaces.                               | No new repudiation surface; just a more thorough view of existing audit-log rows.                                                                                                                                                                                                                                                      |
| **I** Information disclosure | Non-admin sees ANOTHER user's actions in the same tenant, OR sees actions that are admin-only (e.g., super_admin role grants). | The backend filter (Option A) OR the dedicated endpoint (Option B) MUST gate row visibility by `subject_module='public'` OR `actor_id = $current_actor`. Verified by an integration test that issues a non-admin JWT + queries; admin-only rows (super_admin grants, framework_scope predicate edits) MUST NOT appear in the response. |
| **D** DoS                    | Open-ended pagination + filter combination triggers expensive queries.                                                         | Inherits slice 124's pagination cap (1000 rows max) + 90-day window. No additional surface added.                                                                                                                                                                                                                                      |
| **E** EoP                    | Operator uses `/activity` filters to enumerate admin-only data.                                                                | Same as the InfoDisclosure mitigation — backend gates row visibility regardless of filter combination. Filter UI never affects authz.                                                                                                                                                                                                  |

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/activity/page.tsx` renders the unified activity ledger with the operator's own actions + tenant-public events.
- [ ] AC-2: `web/app/api/activity/route.ts` BFF route forwards to the backend endpoint (or extended slice 124 endpoint per D1 outcome) with the operator's actor_id available for default filtering.
- [ ] AC-3: Filters: `from`/`to` date range, `kind` multi-select, `actor` (defaults to "all" for /activity navigation; defaults to current operator when navigated from the dashboard footer link via `?actor=me` query param).
- [ ] AC-4: Pagination via the existing slice 124 cursor (1000 rows max per page).
- [ ] AC-5: Backend (D1 outcome): either (A) extended OPA admit + row-visibility filter on `/v1/admin/audit-log/unified`, or (B) new `/v1/activity/unified` endpoint with the restricted projection. Pick one.
- [ ] AC-6: Cross-actor isolation integration test: tenant A's non-admin operator B queries `/activity`; the response MUST NOT include admin-only rows (super_admin grants, framework_scope predicate edits, etc.).
- [ ] AC-7: Cross-tenant isolation integration test: operator B in tenant A MUST NOT see any rows from tenant C.
- [ ] AC-8: Sidebar conditionally renders the `/activity` link for all authed users (slice 186 pattern); the dashboard activity-feed panel's footer link (slice 232) points to `/activity`.
- [ ] AC-9: Playwright e2e: navigate to /activity, verify table renders, filter by kind, verify URL state-binding (`?kind=evidence_freshness` etc.).
- [ ] AC-10: OPA admit-set test confirms a non-admin caller gets 200 (not 403) on the appropriate endpoint.
- [ ] AC-11: CHANGELOG entry under `Added`. Slice 232's spillover called out as the unblocking target.

## Decisions

- **D1: Extend existing slice-124 endpoint vs new endpoint** — the engineer picks one. Recommendation: extend (Option A) for surface uniformity, but ship the dedicated endpoint (Option B) if the projection logic gets ugly. Document in `docs/audit-log/270-non-admin-activity-ledger-decisions.md`.
- **D2: Default-filter shape**: navigating to `/activity` directly shows all visible activity; navigating from the dashboard footer link's `?actor=me` query defaults the actor filter to the operator. Avoids the "where's my data" problem AND the "I want the full tenant view" problem.
- **D3: Reuse slice 125's `<UnifiedAuditTable>` component verbatim**. No new component; just a new page + BFF route + (minimal) backend admit extension.

## Constitutional invariants honored

- **RLS / tenancy (#6)**: backend filter + RLS together guarantee tenant + admin-only-row isolation.
- **Audit-log integrity (#2)**: read-only; reuses existing audit-log substrate.
- **Manual evidence is first-class (#9)**: n/a — this is the activity ledger, not the evidence ledger.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT show another operator's per-actor actions to a non-admin caller. Verified by AC-6.
- **P0-A2**: DOES NOT show admin-only rows (super_admin grants, framework_scope predicate edits, role-grant-by-cli) to non-admins. Verified by AC-6.
- **P0-A3**: DOES NOT introduce a new schema migration. Reuses slice 124's substrate.
- **P0-A4**: DOES NOT bypass RLS — uses atlas_app + tenancy GUC on every read.
- **P0-A5**: DOES NOT allow filter-combinations to expose otherwise-hidden rows. Backend authz is independent of filter UI.

## Dependencies

- **#067** (admin audit-log page) — merged. Reference pattern for admin-scoped destination.
- **#124** (unified audit-log aggregation API) — merged. Substrate.
- **#125** (frontend /audit-log page) — merged. `<UnifiedAuditTable>` component reuse.
- **#180** (audit-log subject_module) — merged. Public-vs-admin row discrimination.
- **#186** (role-conditional sidebar entry) — merged. Sidebar-link pattern (this slice's link renders for ALL authed users, not gated, but the pattern's still relevant for symmetry).
- **#190** (jwtmw) — merged.
- **#035** (OPA middleware) — merged.

## Unblocks

- **#232** (dashboard activity-feed "View full activity ledger" footer link) — the not-ready audit-fleet spillover. After this slice merges, #232 flips `not-ready` → `ready`.

## Skill mix

- Next.js page + BFF route
- React component composition (reusing slice 125's `<UnifiedAuditTable>`)
- Go HTTP handler extension OR new handler (per D1)
- OPA admit-set extension
- RLS-aware integration tests
- Playwright e2e

## Notes for the implementing agent

- The trickiest bit is D1 — extending slice 124's endpoint vs forking a new one. Slice 124's package is `internal/api/auditlog/unified.go` (approximate). Read it carefully before choosing.
- The `subject_module` column (slice 180) is the key discriminator: rows with `subject_module='public'` are tenant-visible to any member; rows with `subject_module='admin'` (super_admin grants, framework_scope edits) are admin-only.
- The current operator's actor_id is available via the slice 192 `/v1/me` response or the JWT subject claim (`sub: "user:<uuid>"`). The BFF route can derive it from the request's authed context.
- Per-actor filtering shouldn't allow a non-admin to filter by "show me admin X's actions" — that's an InfoDisclosure threat. The filter UI should restrict the `actor` dropdown to "all" (which is row-visibility-filtered server-side) OR "me" (the operator). Other operators' actions appear in "all" view only via `subject_module='public'` events.
