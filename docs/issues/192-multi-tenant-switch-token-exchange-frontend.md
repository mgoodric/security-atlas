# 192 — Multi-tenant switch via OAuth token-exchange + frontend header switcher (closes slice 141 vCISO criterion)

**Cluster:** Backend / Auth + Frontend
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `not-ready` (sixth and final slot in the auth-substrate-v2 spine; gate: 191 merged)

## Narrative

The full closure of OQ #21 Reading D — and the slice 141 PARKED-since-2026-05-20 vCISO success criterion. Slice 188 shipped the `grant_type=token-exchange` primitive at `/oauth/token`. Slice 189 + slice 190 + slice 191 wired the user-facing OAuth flows + JWT-everywhere middleware + SDK migration. Slice 192 puts the **frontend tenant-switcher dropdown** on top of all of it.

**The vCISO use case** (slice 141 framing): a security consultant hosts the atlas instance for multiple client tenants. They log in once via OIDC and need to switch between client contexts from a persistent header dropdown — without re-authenticating. Under the OAuth AS commitment, this is no longer a "session model rewrite" — it's a small frontend feature backed by the token-exchange RFC.

**What this slice ships:**

1. **Tenant-switcher React component** at `web/components/auth/tenant-switcher.tsx`. Mounted in the persistent header (next to the user avatar). HIDDEN when `available_tenants.length === 1` per canvas §11 #13 (the UI doesn't show tenant chrome for single-tenant operators). Visible when ≥2 tenants. Renders a Stripe/Linear-style dropdown showing current tenant name + the other available tenants.
2. **Frontend tenant-switch action** at `web/lib/auth/switch-tenant.ts`: TypeScript function that calls `POST /oauth/token` with `grant_type=token-exchange` + the current JWT as `subject_token` + the target `atlas:target_tenant_id`. Replaces the JWT cookie with the new one + does a full client-side router refresh to re-render all server components with the new tenant scope. (Per slice 190, the JWT middleware sets `app.current_tenant` GUC from the JWT claim; the new JWT means new GUC means RLS re-scopes.)
3. **Tenant directory backend** `GET /v1/me/tenants` that returns the caller's `atlas:available_tenants[]` enriched with tenant metadata (id + name + maybe icon URL). The dropdown reads this on mount.
4. **Login picker for ≥2 tenant cases**: when a user logs in via OIDC + has ≥2 tenants in `available_tenants[]`, the OAuth `/oauth/authorize` flow renders an additional step BEFORE the redirect-back-to-client: a tenant picker page. (Single-tenant users auto-select and skip the picker.) The picker writes the chosen tenant into the `oauth_auth_codes` row's `current_tenant_id` field.
5. **Membership-removed eviction signal**: when an admin removes a user from a tenant (via a future slice — out of scope here; the trigger surface is the `user_roles` table DELETE on the tenant_id matching the user's current_tenant_id), the user's NEXT token-refresh will get an `available_tenants[]` that no longer includes the removed tenant. The frontend tenant-switcher gracefully handles this: on a refresh of `/v1/me/tenants`, it diffs the response against the current JWT's `available_tenants` and prompts the user to switch to a still-available tenant if their current one is gone. Honest "eventual eviction" semantics from slice 190 documented + surfaced in UX.
6. **Bootstrap tenant flow**: when the atlas instance has ZERO tenants (first-install scenario), the OIDC callback creates a "Default Tenant", grants the bootstrap user super_admin + tenant_admin in it, and the user is automatically scoped to that tenant. Slice 141's "first-install" branch retained here. Engineer reads slice 141's escalation memo at `~/.claude/MEMORY/STATE/continuous-batch-escalation.md` for the historical context.

**This slice closes slice 141.** When this lands, slice 141's status flips from `not-ready` (PARKED) to `merged-via-spine-completion` in a final reconcile.

**SCOPE DISCIPLINE — what's deliberately out:**

- Inviting users to tenants (the admin-side flow that adds someone to `user_roles[tenant]`). The vCISO has all client tenants already; the invite flow is a v2.x slice.
- Super_admin management UI (full table CRUD). Bootstrap grant works; comprehensive UI deferred.
- Cross-tenant audit-log views (a super_admin seeing all tenants' activity). Deferred.
- Tenant-create UX beyond the bootstrap flow. Deferred to slice 143-shape work.
- Tenant-rename / delete. Deferred.
- Tenant slug for URL routing (`/{tenant_slug}/dashboard`). Deferred to v3.
- Cross-tab BroadcastChannel notifications when one tab switches tenants. The current model: each tab carries its own JWT cookie; switching in tab A doesn't reflect in tab B until refresh.

## Threat model

**S — Spoofing.** Caller switches to a tenant they don't have access to.

- Mitigation: slice 188 AC-13 already enforces this server-side. The frontend dropdown only LISTS tenants from the JWT's `available_tenants[]`; even if a malicious client modifies the request, slice 188's check rejects.

**T — Tampering.** Frontend modifies the JWT cookie to claim a different tenant.

- Mitigation: JWT signature validation in slice 190's middleware rejects tampered tokens.

**R — Repudiation.** Tenant-switch events need audit trail.

- Mitigation: slice 188's `oauth_token_exchanges` table captures every switch with subject_token's jti + sub + iss + from_tenant + to_tenant.

**I — Information disclosure.** `GET /v1/me/tenants` reveals tenant names the user shouldn't see.

- Mitigation: the endpoint returns ONLY tenants in the verified JWT's `available_tenants[]`. No DB scan; pure claim-driven.

**D — Denial of service.** Tenant-switch hammered.

- Mitigation: slice 188's per-client rate limit on `/oauth/token` already covers.

**E — Elevation of privilege.** Caller switches to a tenant + obtains roles they don't have there.

- Mitigation: the JWT's `atlas:roles` claim is a map keyed on tenant_id. After switch, the role-resolution middleware reads `claims.Roles[claims.CurrentTenantID]` — only the roles for the new tenant apply. RLS still enforces tenant scope at DB.

**Verdict:** `has-mitigations`. The slice's bulk is frontend wiring; the security checks are inherited from slice 188 + 190.

## Acceptance criteria

### Tenant directory backend

- **AC-1.** NEW handler `GET /v1/me/tenants` at `internal/api/me/tenants.go` (or extending an existing `me` package). Authenticated via JWT middleware (slice 190).
- **AC-2.** Reads the verified JWT's `atlas:available_tenants[]` from context. Loads tenant metadata (id + name) from the `tenants` table for those IDs. Returns JSON shape: `{"tenants": [{"id":"<uuid>","name":"<str>","current": true|false}, ...]}`. The `current` field marks the active tenant.
- **AC-3.** Does NOT do a full `SELECT * FROM tenants` — bounded to the claim's tenant list. Index-only scan via PK lookups.

### Tenant-switcher component

- **AC-4.** NEW component `web/components/auth/tenant-switcher.tsx`. Default export a server component that fetches `/v1/me/tenants` on mount. If `tenants.length <= 1`: returns `null` (no UI rendered). If ≥2: renders dropdown.
- **AC-5.** Dropdown UX: button shows current tenant name + chevron. Click opens menu listing all available tenants with the current one marked. Click on a non-current tenant calls the switch action (AC-6).
- **AC-6.** NEW client-side function `web/lib/auth/switch-tenant.ts` `switchTenant(targetTenantId: string): Promise<void>`. Calls `POST /oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` + the current JWT as `subject_token` + `atlas:target_tenant_id` form param. On success: replaces the JWT cookie via a BFF route handler `web/app/api/auth/switch-tenant/route.ts` (server-side cookie set per slice 189 D1). Then calls `router.refresh()` to re-render all server components with new tenant scope.
- **AC-7.** Header layout updates: mount `<TenantSwitcher />` in the persistent layout `web/app/(authed)/layout.tsx` (or wherever the header lives). Component handles its own visibility (null-renders when single-tenant).

### Login tenant picker (≥2 tenant case)

- **AC-8.** Slice 189's `/oauth/authorize` flow extended: after OIDC session is established, if the user has ≥2 tenants in `available_tenants[]` AND no `atlas:current_tenant_id` is set in the request (a new query param `prompt=tenant` from the frontend forces this), render a tenant picker page at `web/app/oauth/select-tenant/page.tsx`. User selects → form POST to a new handler that writes the choice into the `oauth_auth_codes` row + completes the redirect back to the client.
- **AC-9.** Single-tenant users skip the picker — the existing tenant auto-fills.
- **AC-10.** Frontend OAuth client (slice 189's `oauth-client.ts`) ALWAYS sends `prompt=tenant` on login. Operators with one tenant don't see the picker (single-tenant auto-skip in AC-9); operators with N tenants do.

### Bootstrap tenant + super_admin grant

- **AC-11.** Slice 034 OIDC callback handler extended: on first user creation, if `count(*) FROM tenants == 0`, create a default tenant named "Default Tenant" + grant the user `super_admin = true` + `tenant_admin` role in the new tenant. The minted JWT has `current_tenant_id = <default-tenant-id>` + `available_tenants = [<default-tenant-id>]` + `super_admin = true`.
- **AC-12.** Subsequent OIDC logins: if the user exists + has ≥1 tenant in `user_roles`, skip bootstrap branch and proceed to normal login (single-tenant auto-select OR picker if ≥2).

### Membership-removed UX

- **AC-13.** The tenant-switcher component periodically re-fetches `/v1/me/tenants` (every 60s when the tab is foreground; pause when backgrounded). If the response's tenant list no longer includes the current `atlas:current_tenant_id`, the component renders an alert banner: "Your access to <CurrentTenant> has been removed. Switch to a different tenant or log out." with a default-action button to switch to the first available alternative.
- **AC-14.** Honest "eventual eviction" doc note at `docs-site/docs/admin/tenant-membership.md` — admins removing a user from a tenant doesn't immediately invalidate that user's existing tokens; they need to `/oauth/revoke` the JWT for instant eviction. Documented; not a bug.

### Slice 141 closure

- **AC-15.** `_STATUS.md` row for slice 141 flipped from `not-ready` (PARKED) to `merged-via-spine-completion` with reference to slice 192's merge commit. Notes column updated to explain: "The vCISO success criterion (switch tenants mid-session without re-authentication) is fulfilled by slice 192's frontend wiring of slice 188's token-exchange primitive. Slice 141's original spec is preserved as historical context; the implementation moved to the auth-substrate-v2 spine."
- **AC-16.** Slice 141's downstream slices (142, 143, 144) re-evaluated. If still applicable, their gate is unblocked. If superseded, mark accordingly.

### Tests + docs

- **AC-17.** Integration test for `GET /v1/me/tenants`: caller with 1 tenant gets 1 entry; caller with 3 tenants gets 3 entries.
- **AC-18.** Integration test for frontend switch flow: vitest + msw-mocked `/oauth/token` + assert cookie set + assert `router.refresh()` called.
- **AC-19.** Playwright e2e: log in as vCISO user (seed: user in 3 tenants) → switcher visible → click another tenant → URL stays same but dashboard re-renders with new tenant data. Quarantine spec per slice 082 pattern if the seed-harness can't establish multi-tenant fixtures yet.
- **AC-20.** Operator-facing doc at `docs-site/docs/user-guide/tenant-switching.md` — how vCISOs use the switcher.
- **AC-21.** JUDGMENT decisions log at `docs/audit-log/192-multi-tenant-switch-decisions.md`: D1 (`/v1/me/tenants` cache duration), D2 (membership-removed UX shape), D3 (login picker prompt flag default), D4 (slice 141 closure mechanism — `merged-via-spine-completion` status string is non-standard; document why).
- **AC-22.** ADR-0003 final addendum: spine completion. Mark OQ #21 Reading D as fully implemented.
- **AC-23.** `CHANGELOG.md` entry highlighting the v2 milestone: "Multi-tenant: switch between client tenants from the persistent header dropdown without re-authentication. Closes the v2 vCISO operator persona."

## Constitutional invariants honored

- **Tenant isolation at DB layer** (invariant #6): every tenant switch issues a new JWT; the JWT middleware sets `app.current_tenant` GUC; RLS enforces. No application-layer tenant filtering required.
- **AI-assist boundary**: not touched.

## Canvas references

- OQ #21 RESOLVED (Reading D) — this slice completes the implementation half of the resolution.
- `Plans/canvas/01-vision.md` §6 "Survive a third-party security review of multi-tenant isolation in self-host deployments" — this slice ships the user-facing surface that proves the multi-tenant model works.
- `Plans/canvas/11-open-questions.md` item 13 RESOLVED 2026-05-11 — "build multi-tenant from day one; UI MAY hide tenant chrome when tenant_count == 1". AC-4 enforces.
- `docs/adr/0003-oauth-authorization-server.md` (slice 187 + addenda from 188, 189, 190, 191).
- Slice 141 (PARKED) — closed by this slice.

## Dependencies

- **#187, #188, #189, #190, #191** — entire auth-substrate-v2 spine. **Gate: 191 merged.**
- **#034** OIDC RP. The bootstrap branch + login picker extend slice 034's callback handler.
- **#103** Settings page or wherever the persistent header lives.

## Anti-criteria (P0 — block merge)

- **P0-192-1.** Tenant-switcher MUST be HIDDEN when `available_tenants.length === 1`. Canvas §11 #13 commitment.
- **P0-192-2.** `GET /v1/me/tenants` MUST return ONLY tenants from the verified JWT's `available_tenants[]`. No general SELECT on `tenants` table.
- **P0-192-3.** Frontend MUST call slice 188's `/oauth/token` for the switch — NEVER bypass via a custom backend endpoint that mutates session state. The token-exchange RFC is the contract.
- **P0-192-4.** Bootstrap branch (count(\*) FROM tenants == 0) MUST be atomic within a single transaction. Race against concurrent first-logins must not create multiple "Default Tenant" rows.
- **P0-192-5.** Login picker MUST NOT show tenants the user doesn't have access to. Source the list from `user_roles` join, not from the full `tenants` table.
- **P0-192-6.** Does NOT touch the slice 188 token-exchange handler beyond consuming it. No backend changes in `/oauth/token` from this slice.
- **P0-192-7.** Membership-removed UX MUST NOT silently fail. The banner per AC-13 is non-optional.
- **P0-192-8.** Eviction-is-eventual MUST be documented for operators. The honest semantics are non-negotiable; "remove user → tokens still work until expiry" is the OAuth norm, not a bug.
- **P0-192-9.** Does NOT introduce per-tenant URL routing (`/{tenant_slug}/dashboard`). Tenant context flows entirely via the JWT claim. v3 spillover.
- **P0-192-10.** Does NOT modify slice 187's keystore + tokensign + jwt packages. All signing/verifying goes through them unchanged.
- **P0-192-11.** Slice 141's status flip + downstream-slice (142, 143, 144) re-evaluation happens IN THIS PR. The spine closure is atomic.

## Skill mix (3-5)

- `grill-with-docs`
- `tdd` (frontend vitest + backend integration)
- `security-review` (multi-tenant switching is high-risk if any check is missed)
- `simplify`
- `ship-gate`

## Notes for the implementing agent

### This is the closing slice — slice 141 finally lands

Read slice 141's spec + the escalation memo at `~/.claude/MEMORY/STATE/continuous-batch-escalation.md`. The original 141 had a complex schema rewrite ambitions; THIS slice ships the vCISO success criterion via a much smaller frontend feature on top of the OAuth AS. The bulk of 141's substance moved into 187-191; only the UI wiring is left.

### The dropdown is the visible surface

Most of this slice's substance is in the backend slices (187-191). The frontend dropdown is small + delightful. Match the existing header design (Stripe/Linear-style dropdowns; look at the user-avatar popover for the pattern).

### Honest eventual-eviction semantics

This is the conversation security reviewers will have with you. The honest answer ("tokens are valid until expiry or explicit revocation; admin removal is eventual") is the OAuth-standard answer. Document it. Don't apologize for it. If the operator wants instant eviction, they can revoke via slice 190's `/oauth/revoke`.

### Closure of slice 141

Per AC-15, this slice's PR flips slice 141's row in `_STATUS.md`. Be specific in the notes column — future maintainers will look at slice 141 and need to know exactly what closed it.

### Spillover candidates

- Tenant invite flow (admin adds a user to a tenant via `user_roles`).
- Super_admin management UI.
- Cross-tenant audit-log view (super_admin sees all tenants).
- Tenant slug for URL routing.
- Cross-tab BroadcastChannel for tenant-switch sync.
- All v2.x or v3.

### Provenance

Filed 2026-05-21 as auth-substrate-v2 spine slot 6 (final). Closes OQ #21 Reading D + slice 141.
