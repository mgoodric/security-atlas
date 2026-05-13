# 060 — Admin settings UI (SSO · users · API keys · feature flags · audit)

**Cluster:** Frontend / admin
**Estimate:** 3d
**Type:** HITL

## Narrative

Build the in-app admin section. Slices 034 (OIDC RP + admincreds), 035 (RBAC roles), 059 (feature flags), and the audit infrastructure across 013/033/036/051 ship the backend; this slice surfaces them in the Next.js frontend so the maintainer (and eventually their team) can configure the platform without curl. Adopters need to wire SSO before users can log in; configure API keys before connectors push evidence; toggle feature flags to scope down to their adoption footprint; assign roles to colleagues; review the audit log when something looks wrong. Without an admin UI, every config step is a command-line task — friction that pushes adopters toward Vanta-shaped competitors.

The slice surfaces — it does NOT add new backend capabilities. Every page binds to existing API endpoints. HITL: a maintainer-level review of the role-permission matrix on the Users page + the SSO callback URL preflight pages, because role assignment errors are how cross-tenant leaks happen.

The slice delivers value because security-atlas becomes self-administered — the maintainer sets up SSO, issues their first API key, assigns roles, and turns off the modules they don't want, all in the browser.

## Acceptance criteria

- [ ] AC-1: `/admin` overview page lists the five admin sub-areas (SSO, Users, API keys, Features, Audit) with current-state summaries (e.g., "SSO: configured for Google · 12 users · 3 API keys · 9 of 12 features enabled").
- [ ] AC-2: `/admin/sso` — configure per-tenant OIDC IdP (slice 034's `oidc_idp_configs`). Form: provider (Google / Azure AD / Okta / custom), client ID, client secret (write-only field), discovery URL, allowed email domains. Preflight that pings the discovery URL and shows the parsed `authorization_endpoint` / `token_endpoint` / `jwks_uri` before save. Test login button that does the full OIDC dance and reports success/failure without persisting a session.
- [ ] AC-3: `/admin/users` — list users with role badges (per slice 035). Role assignment via a per-user dropdown; multi-select for the 5 RBAC roles. Removes-role confirmation modal ("removing 'admin' from your own user is irreversible without DB access — confirm?"). Reads `/v1/admin/users` and `/v1/admin/users/{id}/roles` (slice 035 adds these); writes via `PATCH /v1/admin/users/{id}/roles`.
- [ ] AC-4: `/admin/api-keys` — list/issue/rotate/revoke. The Issue flow returns the bearer token plaintext exactly once (slice 034 AC-9 contract) with a copy-to-clipboard button and a clear "this is the only time you'll see this; copy it now" callout. Rotate UI shows the overlap window countdown (slice 034 AC-10). Revoke confirmation modal lists the connectors that were last seen using the key (best-effort, from `last_used_at`).
- [ ] AC-5: `/admin/features` — toggle feature flags (slice 059). Grouped by category. Each toggle shows: current state, default state, description, "what this gates" line (e.g., "/v1/risks/\*"), and a "flip" affordance. Confirmation modal for flipping `false → true` (re-enabling — usually safe) and especially for `true → false` (disabling — surfaces dependent data state, e.g., "Disabling risk turns off 47 active risks from the dashboard. Data preserved; routes return 404. Re-enable any time.").
- [ ] AC-6: `/admin/audit` — paginated log view across the union of audit logs: `decision_audit_log` (slice 035), `evidence_audit_log` (slice 013), `exception_audit_log` (slice 021), `feature_flag_audit_log` (slice 059), `policy_audit_log` (slice 022 if it lands), `framework_scope_workflow_log` (slice 018), `artifact_access_log` (slice 036). Filter by actor, event type, time range. Each row links to the relevant entity detail.
- [ ] AC-7: All admin pages gated by slice 035 RBAC role `admin`. A logged-in `viewer` / `auditor` / `grc_engineer` / `control_owner` accessing `/admin/*` sees a 403 page with a clear "this section is admin-only — contact your tenant admin" message (NOT a 404 — admins exist, viewers just can't access this part).
- [ ] AC-8: Mobile responsive — all admin pages work down to 375px width (iPhone SE baseline per `Plans/mockups/`).
- [ ] AC-9: HITL: role-permission matrix review + SSO config review + feature-flag descriptions review documented in `docs/audit-log/admin-ui-review.md` (reviewer name + date + per-page sign-off).
- [ ] AC-10: E2E test: a fresh admin user can complete the full bootstrap — sign in via local user → configure SSO → toggle a feature flag → issue an API key → assign a role to a second user — all via UI, no CLI fallback. Playwright test under `web/e2e/admin-bootstrap.spec.ts`.

## Constitutional invariants honored

- **Invariant 6 (RLS):** every admin page reads through the standard tenant-scoped API; the UI cannot reach across tenants because the API can't.
- **Slice 033 D1** (tenancy middleware is the sole tenant-context setter): the UI does NOT pass tenant_id in any request body — tenant flows from the bearer cred at the API layer.
- **AI-assist boundary**: no AI-generated role assignments or feature toggles. Every admin action is human-clicked. Audit log captures the actor.

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.3 (Frontend stack — Next.js 15 App Router + shadcn/ui)
- `Plans/canvas/10-roadmap.md` §10.1 (frontend row — self-administration is part of v1 self-host)
- `Plans/mockups/index.html` (iteration-1 visual baseline — admin pages should match the design language)

## Dependencies

- **005** (Next.js + auth shell + SCF browser — provides the layout chrome)
- **034** (OIDC RP + admincreds backend — pages 2 + 4 bind here)
- **035** (RBAC roles — page 7's admin gate AND page 3's role assignment)
- **059** (Feature flags — page 5 binds here)
- Audit-log surfaces from 013, 021, 022, 033, 036, 051 (page 6 reads these)

## Anti-criteria (P0)

- Does NOT permit non-admin role to access `/admin/*` (slice 035 RBAC gate).
- Does NOT show the API-key bearer token after the initial issue response (slice 034 AC-9 contract — write-once secret).
- Does NOT silently re-enable a feature flag that was explicitly disabled (re-enable always requires an admin click; no auto-flip on package upgrades).
- Does NOT bundle SSO secrets in the audit log (`client_secret` is write-only at the API; audit log records "SSO config changed" but not the secret value).
- Does NOT permit role assignment without explicit confirmation when the operator is changing their own role (AC-3 self-demotion modal).

## Skill mix (3–5)

- Next.js 15 App Router + shadcn/ui + Tailwind 4
- TanStack Query for the admin-API binding
- OIDC discovery preflight (client-side fetch + JSON inspection)
- Playwright E2E (admin bootstrap test)
- Role-permission matrix design (HITL)
- Audit-log UI patterns (unified view across heterogeneous log tables)
