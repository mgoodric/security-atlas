# 130 — Extend `/api/admin/me` BFF + `/v1/admin/credentials` backend with role enumeration

**Cluster:** Backend + Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Spillover from slice 125 (frontend `/audit-log` page). Filed 2026-05-18 by
the slice 125 implementing engineer.

The slice-060 `/api/admin/me` BFF returns only `{is_admin: boolean}`. The
slice-125 page route-guard therefore gates on `is_admin === true`, which
excludes auditor and grc_engineer callers — even though slice 124's
backend OPA policy allows them (admin OR auditor OR grc_engineer). Today
the consequence is: an auditor signed-in user navigating to `/audit-log`
is redirected to `/dashboard?error=admin-only` even though the backend
would happily serve them.

Two paths:

- (a) Extend `/api/admin/me` (and its backend `/v1/admin/credentials`
  origin) to return `{is_admin: boolean, roles: string[]}`. Frontend
  guard becomes
  `roles.includes("admin") || roles.includes("auditor") || roles.includes("grc_engineer")`.
- (b) Add a new endpoint `/api/me/roles` that returns just the role list,
  keeping `/api/admin/me` unchanged.

Slice 125 decisions log D9 picks (c) — strict `is_admin` for the v1
route guard — and files THIS slice as the follow-up. The slice doc
defers the (a) vs (b) JUDGMENT call to the implementing engineer.

## Threat model

| STRIDE                       | Threat                                                            | Mitigation                                                                                                                                                                          |
| ---------------------------- | ----------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Caller crafts a request claiming a role they do not have          | Roles come from the backend's `user_roles` table — same table slice 124's OPA gate reads. No client-supplied claim.                                                                 |
| **T** Tampering              | n/a — read-only                                                   | n/a                                                                                                                                                                                 |
| **R** Repudiation            | n/a                                                               | n/a                                                                                                                                                                                 |
| **I** Information disclosure | Endpoint leaks role list to the caller                            | The caller already has these roles — the endpoint returns only the CALLER's own roles, no other user's                                                                              |
| **D** Denial of service      | Endpoint hits the database on every request                       | RBAC lookups are O(small) per user; same query the OPA middleware already runs                                                                                                      |
| **E** Elevation of privilege | Caller uses the role list to bypass the backend's per-route gates | n/a — the role list is informational. The backend remains the single source of authorization truth on every API call. The frontend uses the role list ONLY for UI render decisions. |

## Acceptance criteria

- [ ] AC-1: Engineer picks (a) or (b) and records the choice + rationale
      in `docs/audit-log/130-api-admin-me-role-enumeration-decisions.md`
      D1. (a) is simpler — one endpoint already exists; recommended.
- [ ] AC-2: Backend extension to `/v1/admin/credentials` (or new
      `/v1/me/roles`) returns the caller's role list under
      `tenancy.ApplyTenant` (so RLS on `user_roles` filters tenant
      correctly).
- [ ] AC-3: BFF surface (`web/app/api/admin/me/route.ts` extended OR
      new `web/app/api/me/roles/route.ts`) follows the slice-060 cookie
      → bearer pattern. ALL responses use the slice-110 narrow-scope
      forwarding rule: bearer only, no atlas_session cookie.
- [ ] AC-4: vitest cases cover (i) missing-cookie 401, (ii) admin
      caller returns `["admin", ...]`, (iii) auditor caller returns
      `["auditor"]`, (iv) 403 / 401 from backend pass through.
- [ ] AC-5: Frontend follow-up: slice 125's `web/app/audit-log/layout.tsx`
      guard upgrades from `is_admin === true` to
      `roles.some(r => ["admin", "auditor", "grc_engineer"].includes(r))`.
      Lands in the same PR as the BFF change.
- [ ] AC-6: ISC integration test asserts cross-tenant isolation — Tenant
      A's caller cannot see Tenant B's roles.

## Dependencies

- **060** (admin layout) — already merged.
- **124** (unified audit-log aggregation API) — already merged.
- **125** (frontend `/audit-log` page) — should land first so AC-5 is a
  pure upgrade.

## Anti-criteria (P0)

- **P0-A1**: Does NOT broaden the slice-110 atlas_session forward
  surface. New endpoint forwards bearer only.
- **P0-A2**: Does NOT trust client-supplied role claims. Roles come
  exclusively from the backend `user_roles` table.
- **P0-A3**: Does NOT change the per-endpoint authorization model. The
  role list is informational for the FRONTEND only; every backend route
  continues to gate independently.

## Notes

The slice-125 layout has a comment near the `is_admin` check pointing at
this slice as the follow-up.
