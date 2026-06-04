# 141 — Multi-tenant login + tenant picker + persistent header switcher

**Cluster:** Backend / Frontend / Multi-tenancy
**Estimate:** 3-4d
**Type:** JUDGMENT (one D1 + several smaller calls; pre-grilled architecture in CONTEXT.md)
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` from a maintainer use-case: vCISOs host atlas for multiple clients (one operator → N tenants). Today every session is bound to ONE tenant at issuance time. A vCISO with 12 clients today must run 12 atlas instances OR log out + log back in to switch clients. Neither is operable.

Canvas §11 #13 RESOLVED 2026-05-11 already endorses this design: "build multi-tenant from day one — no separate 'solo mode' facade. The UI MAY hide tenant chrome when `tenant_count == 1`, but the data model and authz never branch on tenant count." Slice 141 makes the SESSION layer match the data layer's multi-tenant model.

**What this slice ships:**

- NEW `user_tenants(idp_issuer, idp_subject, tenant_id, joined_at)` — non-RLS global mapping table. Auth-layer reads it to enumerate tenants for an OIDC subject.
- NEW `atlas_auth` PostgreSQL role — minimal-privilege role granted SELECT on `user_tenants` ONLY. Does NOT have BYPASSRLS. Single purpose-built table; doesn't broaden the BYPASSRLS surface.
- NEW `session_tenant_switches(session_id, from_tenant_id, to_tenant_id, switched_at, switched_by)` — append-only audit-log table. Visible from BOTH from-tenant and to-tenant via RLS policy.
- Migration: `sessions.tenant_id` → `sessions.current_tenant_id` (rename + backfill in single transaction).
- OIDC callback bootstrap on first install: atomic transaction creates "Default Tenant" + grants caller super_admin (global) + admin (in new tenant) + writes user_tenants row.
- `POST /v1/me/current-tenant` — switch endpoint; membership-validated; same-transaction audit-log write; returns updated current_tenant + available_tenants for atomic frontend state update.
- R2 eviction middleware: caller removed from current tenant → redirect to login picker on next request.
- Frontend login picker (rendered when caller has ≥2 tenants; skipped when caller has 1).
- Frontend persistent header tenant switcher (hidden when `tenant_count == 1` per canvas §11 #13).

**Scope discipline (what is OUT):**

- **`super_admins` table full schema + management UI** — slice 141 STUBS the table (single-row inserted by bootstrap) so the bootstrap path compiles; slice 142 owns the full surface + demotion + management page.
- **Create-tenant flow** — slice 143 (gated on 142's super_admin role).
- **Rename-tenant flow** — slice 144 (gated on 141; per-tenant admin can rename their own tenant).
- **Tenant deletion** — out of scope; future slice (data-purge semantics + retention policy are independent design decisions).
- **Tenant slug / per-tenant URL routing** — out of scope (e.g. `tenant-acme.atlas.example.com/dashboard`-style hosting is a different slice).
- **Cross-tab BroadcastChannel coordination** — out of scope at v1; user with multiple tabs sees stale chrome until next navigation.
- **Cross-tenant audit-log view for super_admin** — out of scope (slice 124 stays per-tenant; super_admin viewing "across my 12 client tenants" is a future slice).
- **Invite-link subsystem** — out of scope; established-install unknown-user renders contact-admin page only.

## Threat model

Pre-grilled via the Security skill. Full per-STRIDE-category analysis at `docs/audit-log/141-grill-notes.md` (to be created at pickup); load-bearing summary here:

| STRIDE                       | Threat                                                                                                                                                             | Mitigation                                                                                                                                                                                                                                                                                                                                                 |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Compromised OIDC → expanded blast radius (was 1 tenant, now N). Stolen session cookie → cross-tenant access.                                                       | Inherits slice 034 OIDC validation. Slice 110 cookie + bearer two-factor preserved on `POST /v1/me/current-tenant`. Expanded blast radius is intrinsic accepted-risk of the vCISO design.                                                                                                                                                                  |
| **T** Tampering              | IDOR — caller forges `tenant_id` in switch body. `user_tenants` sync drift (if writes via application code skip the mirror).                                       | Strict membership check before mutation; same-transaction write; **DB trigger** enforces `user_tenants` sync from `user_roles` (load-bearing — application code is fallible). Nightly integrity sweep.                                                                                                                                                     |
| **R** Repudiation            | Tenant switch with no audit trail leaves super_admin cross-tenant actions scattered across N tenant audit logs.                                                    | `session_tenant_switches` append-only table, written SAME-TRANSACTION as `sessions.current_tenant_id` mutation. RLS policy: visible from BOTH from-tenant AND to-tenant (no single tenant_id splits the trail). New 10th `kind` in slice 124's unified audit-log aggregator.                                                                               |
| **I** Information disclosure | `atlas_auth` role + non-RLS `user_tenants` is the deliberate RLS-discipline relaxation point. Misuse leaks "which OIDC subjects belong to which tenants" globally. | `atlas_auth` grants are EXCLUSIVELY `SELECT ON user_tenants` (no other table). CI integration test pins the privilege set. Tenant-id enumeration timing leak: switch membership check is a single indexed SELECT on `user_tenants` only — NO join to `tenants` (would create branch-timing variance).                                                      |
| **D** DoS                    | Switch-flood causes `session_tenant_switches` bloat + `sessions` UPDATE thrashing. R2 middleware adds one DB query per request.                                    | Rate-limit ≤1 switch / 2s / session (server-enforced; 429 with Retry-After). R2 middleware uses index-only-scan path; integration test asserts EXPLAIN ANALYZE plan.                                                                                                                                                                                       |
| **E** Elevation of privilege | Bootstrap race — two concurrent first installers both believe they're first → 2 super_admins, 2 bootstrap tenants. OPA role caching stale after switch.            | Partial unique index `tenants.is_bootstrap_tenant WHERE is_bootstrap_tenant = true` makes second inserter lose on conflict + fall through to established-install branch. OPA gate re-evaluates `user_roles` against current tenant per request — slice 035 already correct; slice 141 MUST NOT cache role at session-issue time. Integration test asserts. |

**Verdict: HAS-MITIGATIONS** — 13 P0 anti-criteria (below) are the load-bearing mitigations. Three threats (S-1 expanded blast radius, S-2 cookie theft cross-tenant, I-3 picker tenant-name reveal) are accepted-risk-of-the-design.

## Acceptance criteria

### Schema migration

- [ ] **AC-1:** NEW migration `migrations/sql/<TS>_multi_tenant_session.sql` adds: `user_tenants` table (4 columns; PK on `(idp_issuer, idp_subject, tenant_id)`; indexed for the membership-check hot path); `session_tenant_switches` table (5 columns; append-only with 2-policy RLS under FORCE per slice 036 pattern; RLS SELECT policy USING `current_tenant_matches(from_tenant_id) OR current_tenant_matches(to_tenant_id)`); MINIMAL-stub `super_admins(idp_issuer, idp_subject, granted_at, granted_by)` table (PK only + INSERT grant to `atlas_auth`; NO SELECT/DELETE grants; NO CHECK constraints; NO indexes beyond the PK — sufficient ONLY for the one-shot bootstrap INSERT in AC-10). Slice 142 ADDs the rest (CHECKs, indexes, SELECT/DELETE grants, management endpoints) — enumerated in slice 142's AC list.
- [ ] **AC-2a (Phase A migration):** ADD `sessions.current_tenant_id UUID` column. Create AFTER INSERT OR UPDATE trigger on `sessions` that copies `tenant_id` → `current_tenant_id` if the latter is NULL (dual-write so existing rows + ongoing inserts populate `current_tenant_id`). Backfill existing rows with `UPDATE sessions SET current_tenant_id = tenant_id WHERE current_tenant_id IS NULL`. DO NOT DROP `tenant_id` in this slice — file a follow-on slice for Phase B (drop) gated on slice 141 having been on `main` for ≥1 release. Rationale: rolling deploys would crash if `tenant_id` were dropped in the same migration as the code rename (old binary in flight references `tenant_id`; new binary references `current_tenant_id` — either order crashes mid-deploy).
- [ ] **AC-2b (Phase B follow-on slice — NOT in this PR):** drop `sessions.tenant_id` column + remove dual-write trigger. Filed as spillover slice; gate is "slice 141 merged for ≥1 release tag".
- [ ] **AC-3:** Migration adds `sessions.last_switched_at TIMESTAMPTZ` for switch rate-limiting (P0-DOS-1).
- [ ] **AC-4:** Migration adds `tenants.is_bootstrap_tenant BOOLEAN NOT NULL DEFAULT false` + partial unique index `WHERE is_bootstrap_tenant = true` (P0-ELEVATE-2 bootstrap race serialization).
- [ ] **AC-5:** Migration creates `atlas_auth` PostgreSQL role; GRANTs EXCLUSIVELY `SELECT ON user_tenants` (P0-INFO-1); explicit `REVOKE ALL ON tenants, sessions, user_roles, ...` (defensive double-write). CI integration test asserts `SELECT grantee, table_name FROM information_schema.table_privileges WHERE grantee='atlas_auth'` returns ONLY the user_tenants row.
- [ ] **AC-6:** Migration creates DB trigger on `user_roles` (AFTER INSERT → INSERT into `user_tenants` ON CONFLICT DO NOTHING; AFTER DELETE → DELETE from `user_tenants` IF NOT EXISTS (SELECT 1 FROM user_roles WHERE tenant_id=OLD.tenant_id AND user_id=OLD.user_id)). Trigger SQL is documented + tested for both INSERT and DELETE paths (P0-TAMPER-2).
- [ ] **AC-6a:** Migration REVOKEs INSERT/UPDATE/DELETE on `user_tenants` from `atlas_app` AND `atlas_auth` (both). The ONLY write path to `user_tenants` is the AC-6 trigger (which runs with the table-owner's privileges, NOT `atlas_app`'s). This enforces the invariant "`user_tenants` is DERIVED from `user_roles`; no orphan rows possible" at the DB layer. Future code paths (e.g. slice-145 "invite user before role assignment") MUST extend the invariant via the user_roles surface, not by writing user_tenants directly. CI integration test asserts the privilege boundary: `INSERT INTO user_tenants` as `atlas_app` raises permission-denied.
- [ ] **AC-7:** Migration is reversible via `.down.sql` (drops trigger first, then tables in reverse-dependency order, restores `sessions.tenant_id`).

### Backend — auth + session model

- [ ] **AC-8:** `internal/auth/sessions/sessions.go` Session struct: rename `TenantID` field → `CurrentTenantID`. Update all call sites. sqlc regen.
- [ ] **AC-9:** NEW `internal/auth/userTenants/` package with `Lookup(ctx, idpIssuer, idpSubject) ([]uuid.UUID, error)` using the `atlas_auth` pool. Single SELECT against `user_tenants`. Used by OIDC callback + R2 middleware.
- [ ] **AC-10:** OIDC callback `internal/api/auth/http.go`: bootstrap branch detects `count(*) FROM tenants == 0`; atomically creates Default Tenant + super_admin grant + admin grant in user_roles. The user_tenants row appears AUTOMATICALLY via the AC-6 trigger on user_roles INSERT — bootstrap code does NOT write user_tenants directly (per the AC-6a invariant). `INSERT … ON CONFLICT DO NOTHING` on the partial-unique-index path for race safety (P0-ELEVATE-2).
- [ ] **AC-11:** OIDC callback established-install branch: enumerate caller's tenants via `userTenants.Lookup`. If empty → 403 "Contact your administrator" page; if 1 → auto-select; if ≥2 → render login picker page with tenant list.
- [ ] **AC-12:** NEW handler `POST /v1/me/current-tenant` in `internal/api/me/`: validates `tenant_id` body via single `SELECT 1 FROM user_tenants` (P0-INFO-2 + P0-TAMPER-1); same-transaction updates `sessions.current_tenant_id` + writes `session_tenant_switches` row + updates `sessions.last_switched_at`; rate-limits via `last_switched_at` (≤1/2s, 429 with Retry-After per P0-DOS-1); returns 200 with `{current_tenant, available_tenants}`. Endpoint requires BOTH bearer + atlas_session cookie (P0-SPOOF-2).

### Backend — R2 eviction middleware

- [ ] **AC-13:** NEW middleware in `internal/api/httpserver.go` (post-auth, pre-handler): `userTenants.Lookup` for caller; if `currentTenantID` not in set → 302 redirect to `/login/tenant-picker?reason=membership-removed`. Bypass list is anchored to a CENTRALIZED `noTenantRequired` set in `internal/api/httpserver.go` (single source of truth shared between auth middleware + R2 middleware), NOT enumerated by hand inside R2. Initial set: `/health`, `/metrics`, `/v1/version`, `/v1/install-state`, `POST /v1/me/current-tenant`, `/login/tenant-picker` (prevents infinite redirect loop), OIDC callback path (pre-session), slice-082 bootstrap-key admin path. Static assets are out-of-scope here (Next.js proxy layer; slice 123's `PUBLIC_STATIC_FILES`). CI test asserts: every chi route is EITHER in `noTenantRequired` OR R2-gated — no third state. Adding a new no-tenant-required endpoint requires updating the centralized set + a sentence in CONTRIBUTING.md, NOT touching R2 middleware directly.
- [ ] **AC-14:** Integration test asserts EXPLAIN ANALYZE on the R2 middleware's `userTenants.Lookup` query shows `Index Only Scan` (not `Index Scan` or `Seq Scan`) per P0-DOS-2.

### Backend — slice 124 unified audit-log aggregator integration

- [ ] **AC-15:** Extend slice 124's `internal/audit/unifiedlog/` aggregator: new `kind` value `session_tenant_switch` enumerated; UNION ALL branch added for `session_tenant_switches`. Dual-tenant_id RLS visibility (from OR to) means a switch row appears in BOTH tenants' aggregator output — but per-tenant audit-log RENDER MUST REDACT the cross-tenant identifier (P0-INFO-4). Render shows "user X arrived from another tenant at 14:32" (Tenant B view) / "user X left to another tenant at 14:32" (Tenant A view); never names the other tenant's UUID or name. The DB row keeps both tenant_ids for forensics; a separate super_admin-only API path exposes the unredacted form (slice 142+ territory). Render-layer redaction lives in the aggregator's `Entry`-to-JSON marshaling step.

### Frontend — login picker

- [ ] **AC-16:** NEW route `web/app/login/tenant-picker/page.tsx` server-component shell + `page-client.tsx` client island (TanStack Query + shadcn `<Card>` per tenant). Lists caller's available tenants (server-side fetched from `/v1/me` extended to return available_tenants — see AC-19). Click → POST `/api/me/current-tenant` → router.push(`/dashboard`).
- [ ] **AC-17:** Picker page handles `?reason=membership-removed` query param: renders banner "You were removed from [previous tenant]. Choose another tenant or contact your administrator." Tenant name comes from the redirect URL (NOT a backend lookup — caller no longer has access to that tenant's data, including its name).
- [ ] **AC-18:** Picker handles empty available_tenants case: renders "You don't have access to security-atlas. Contact your administrator." page. Optional admin contact email from install config (slice 037).

### Frontend — BFF + persistent header switcher

- [ ] **AC-19:** Extend slice 108 `/v1/me` response to include `available_tenants: [{id, name, last_seen_at, my_role}]` AND `current_tenant: {id, name}`. Frontend `web/lib/api.ts` typed client updated.
- [ ] **AC-20:** NEW BFF route `web/app/api/me/current-tenant/route.ts` forwards `POST` to `/v1/me/current-tenant`; bearer + atlas_session cookie per slice 110 pattern.
- [ ] **AC-21:** NEW `web/components/tenant-switcher.tsx` — dropdown component rendered in the slice 091 / slice 130 layout shell. Renders ONLY when `available_tenants.length >= 2` per canvas §11 #13 hidden-when-single-tenant rule. On click, POST → invalidate TanStack Query cache → router.push(`/dashboard`).
- [ ] **AC-22:** Switcher accessibility: keyboard-navigable; ARIA labels; screen-reader announces tenant change.

### Tests

- [ ] **AC-23:** Go integration tests in `internal/api/me/` for `POST /v1/me/current-tenant`: 6 cases — (a) happy-path switch, (b) IDOR rejected with consistent 403 body (P0-TAMPER-1 + P0-INFO-2), (c) rate-limit 429 (P0-DOS-1), (d) bearer-only call rejected (P0-SPOOF-2), (e) same-transaction audit-log row written, (f) `available_tenants` returned reflects post-switch state.
- [ ] **AC-24:** Cross-tenant isolation integration test: caller in Tenant A switches to Tenant B; subsequent reads MUST return ONLY Tenant B data (RLS verification under the switched GUC). Audit-log entry visible from BOTH Tenant A's and Tenant B's slice 124 unified feed.
- [ ] **AC-25:** Bootstrap race integration test: 2 concurrent first-install OIDC callbacks → 1 succeeds with super_admin + bootstrap tenant; 1 falls through to established-install branch with 403 (P0-ELEVATE-2).
- [ ] **AC-26:** `atlas_auth` privilege-pin CI test: asserts `information_schema.table_privileges` for `atlas_auth` grantee returns exclusively the `user_tenants` SELECT row (P0-INFO-1).
- [ ] **AC-27:** OPA gate re-evaluation integration test: session switches A→B; next request's OPA gate reads `user_roles WHERE tenant_id = B`, not cached A roles (P0-ELEVATE-1).
- [ ] **AC-28:** `user_tenants` trigger sync test: INSERT into `user_roles(tenantA, userX, admin)` → `user_tenants(userX, tenantA)` row appears. DELETE the only user_roles row for `(tenantA, userX)` → `user_tenants(userX, tenantA)` row disappears. Second admin role for same user same tenant: DELETE one → user_tenants row REMAINS (other role still present). (P0-TAMPER-2.)
- [ ] **AC-29:** vitest matrix at `web/components/tenant-switcher.test.tsx`: hidden-when-single-tenant (canvas §11 #13); rendered-when-multi-tenant; click triggers POST + cache invalidation + navigation.
- [ ] **AC-30:** Playwright e2e at `web/e2e/multi-tenant-switch.spec.ts`: (a) single-tenant user sees no switcher chrome; (b) multi-tenant user sees switcher + can switch; (c) mid-session removal triggers R2 redirect to picker with banner.

### Decisions log

- [ ] **AC-31:** NEW `docs/audit-log/141-multi-tenant-login-decisions.md` records the 7 grill questions resolved (D1-D7) + sub-decisions. CONTEXT.md is the canonical glossary; decisions log captures the why-this-not-that trail.

## Constitutional invariants honored

- **#4 Multidimensional scope.** Slice 141 doesn't touch scope/applicability; preserves existing scope-cell + framework-scope intersection model.
- **#6 Tenant isolation enforced at DB layer via RLS.** The `atlas_auth` role + `user_tenants` table is INTENTIONAL relaxation of the RLS-everywhere discipline, narrowed to ONE table + ONE purpose. Every other tenant-scoped query continues to use `atlas_app` + RLS. Per-STRIDE I-1 the narrowing is the load-bearing mitigation.
- **#9 Manual evidence is first-class.** Slice 141 doesn't change evidence treatment.
- **AI-assist boundary.** N/A directly. Multi-tenant doesn't change AI-assist semantics.
- **Vision §6: third-party security review of multi-tenant isolation.** Slice 141 is load-bearing for this promise — the session layer becoming multi-tenant aware is what a third-party reviewer will examine first.

## Canvas references

- `Plans/canvas/11-open-questions.md` item 13 (RESOLVED 2026-05-11): "build multi-tenant from day one — UI MAY hide tenant chrome when tenant_count == 1". This slice is the implementation of that resolution.
- `Plans/canvas/01-vision.md` §6: "Survive a third-party security review of multi-tenant isolation in self-host deployments."
- `Plans/canvas/05-scopes.md` — scope-cell model; slice 141 does NOT extend this.
- `Plans/canvas/09-tech-stack.md` — OIDC RP, OPA, RLS commitments; slice 141 builds on all three.

## Dependencies

- **#034** OIDC RP + local users + sessions (merged) — extends.
- **#035** OPA middleware (merged) — slice 141 doesn't modify; relies on per-tenant role re-evaluation per request (P0-ELEVATE-1).
- **#036** Append-only RLS pattern (merged) — `session_tenant_switches` reuses.
- **#108** `/v1/me` profile (merged) — extends with `available_tenants` + `current_tenant` (AC-19).
- **#110** BFF cookie + bearer forwarding (merged) — switch BFF reuses.
- **#124** Unified audit-log aggregator (merged) — new 10th `kind` extends (AC-15).
- **#130** `/api/admin/me` role enumeration (merged) — `tenant-switcher` component reuses the consumer pattern.

## Anti-criteria (P0 — block merge)

13 P0 anti-criteria from the STRIDE pass:

- **P0-SPOOF-1** — OIDC RP validates `iss` against install-config IdP allowlist; auth-layer enumeration MUST NOT trust self-asserted `iss`.
- **P0-SPOOF-2** — `POST /v1/me/current-tenant` requires BOTH bearer + atlas_session cookie; bearer-only call rejected (forces switch through browser session-cookie boundary).
- **P0-TAMPER-1** — server-side membership check on switch is mandatory; consistent 403 body that doesn't differentiate "tenant doesn't exist" vs "no access".
- **P0-TAMPER-2** — `user_tenants` sync enforced via DB trigger on `user_roles`, NOT application code alone.
- **P0-REPUDIATE-1** — every successful switch writes `session_tenant_switches` row in the SAME TRANSACTION as `sessions` mutation.
- **P0-REPUDIATE-2** — `session_tenant_switches` row visible from BOTH from-tenant AND to-tenant via RLS policy (no single tenant_id).
- **P0-INFO-1** — `atlas_auth` role grants EXCLUSIVELY `SELECT ON user_tenants`; nothing else. CI test pins the privilege set.
- **P0-INFO-2** — membership check is single SELECT against `user_tenants` ONLY; NO JOIN to `tenants` (would create branch-timing variance).
- **P0-INFO-3** — login picker shows tenant name + last_seen + role ONLY; NO dashboard data / control counts / risk register summaries previewed (would amplify the leak surface).
- **P0-INFO-4** — cross-tenant identifiers in `session_tenant_switches` (the other tenant's id + name) MUST be redacted from the per-tenant audit-log render. Audit-log API responses for non-super_admin callers show "another tenant" placeholder. Super_admin-only API exposes the unredacted form for forensics. CI integration test asserts: non-super_admin caller's audit-log query against a tenant containing a switch row does NOT include the other tenant's UUID or name in any response field.
- **P0-DOS-1** — `POST /v1/me/current-tenant` rate-limits to ≤1 switch / 2s / session via `sessions.last_switched_at`; 429 with Retry-After.
- **P0-DOS-2** — R2 middleware uses index-only-scan path; integration test asserts EXPLAIN ANALYZE plan.
- **P0-ELEVATE-1** — slice 141 does NOT cache OPA role at session-issue time; per-request re-evaluation against `user_roles WHERE tenant_id = current_tenant_id` (slice 035 behavior preserved).
- **P0-ELEVATE-2** — bootstrap serialized via partial unique index on `tenants.is_bootstrap_tenant`. Integration test asserts 2 concurrent bootstrap attempts → 1 succeeds, 1 falls through.

Plus:

- **P0-SCOPE-1** — NO `super_admins` management UI in slice 141 (slice 142 owns).
- **P0-SCOPE-2** — NO create-tenant flow (slice 143).
- **P0-SCOPE-3** — NO rename-tenant flow (slice 144).
- **P0-SCOPE-4** — NO cross-tab BroadcastChannel coordination at v1.
- **P0-SCOPE-5** — NO vendor-prefixed test fixture tokens — neutral `test-*` only.

## Skill mix

- **`grill-with-docs`** — slice 141 had a full grill at design time (7 questions resolved; CONTEXT.md updated). Engineer re-grills against current code state to verify the design still maps cleanly.
- **`Security`** — STRIDE pass already complete (13 P0 anti-criteria generated). Engineer re-runs if the implementation surfaces new threat vectors.
- **Go testing (integration)** — AC-23 through AC-28 all hit a real Postgres via the slice-082 harness.
- **Playwright (web/e2e)** — AC-30 drives the three switch scenarios end-to-end.
- **OPA matrix tests** — AC-27 asserts per-tenant role re-evaluation.

## Notes for the implementing agent

**Maintainer pre-confirm 2026-05-20 — `atlas_auth` + non-RLS `user_tenants` relaxation.** The single constitutional question the engineer would otherwise need to escalate at design-grill is whether the `atlas_auth` Postgres role + non-RLS `user_tenants` table — the _deliberate_ relaxation of the "RLS on every tenant-scoped table" invariant — is sanctioned. **Maintainer pre-confirms: yes, as designed.** The rationale: the table maps OIDC identity → tenant set and must be readable BEFORE a tenant is selected (i.e., before the `app.current_tenant` GUC can be set), so the read path genuinely cannot go through `atlas_app` + RLS. Mitigations stand as designed (`atlas_auth` grants EXCLUSIVELY `SELECT ON user_tenants`; no BYPASSRLS; no other table access; CI pins the privilege set via AC-26). Engineer does NOT need to surface this at design-grill — proceed and record this pre-confirm in the decisions log as the load-bearing constitutional resolution. Future docs work (out of slice scope): CLAUDE.md "tenant isolation" wording will be amended post-merge to note `user_tenants` as the documented single global exception.

**The grill-with-docs CONTEXT.md is the canonical design.** Read `CONTEXT.md` sections "User-tenant membership", "Session current-tenant model", "OIDC bootstrap semantics", "Super_admin role", "Switch-tenant wire format", "Tenant-membership eviction" BEFORE reading this slice doc. They contain the resolved-glossary terms this slice implements.

**Coordination with slices 142/143/144.** This slice STUBS the `super_admins` table (single-row insert by bootstrap path). Slice 142 promotes the table to full schema + adds management endpoints. The slice ordering is 141 → 142 → 143 (143 depends on 142) and 141 → 144 (144 only depends on 141). 142/143/144 are filed alongside this PR as separate slice docs in `not-ready` state.

**The DB trigger is load-bearing (P0-TAMPER-2).** Application code maintaining `user_tenants` sync via the application layer is the design that fails — engineers will add new role-grant paths in future slices (142/143) and forget the sync. The trigger enforces the invariant at the DB layer. Trigger SQL goes in this slice's migration; tested in AC-28.

**The partial unique index on `tenants.is_bootstrap_tenant` (P0-ELEVATE-2).** This is the load-bearing race serialization. Without it, two concurrent first-install OIDC callbacks BOTH believe they're first → 2 super_admins, 2 bootstrap tenants, broken-install-needs-manual-cleanup. AC-25 asserts the race is correctly serialized via integration test.

**Coordinate with release-please.** Slice 141 ships in v1.11.0 likely. The bootstrap behavior changes affect every fresh deployment going forward; mark as a notable changelog entry. Existing deployments are unaffected (the `users.tenant_id` column is preserved via the rename-not-drop migration).

**Out-of-scope spillover candidates that grill surfaced:**

- Tenant slug / per-tenant URL routing (e.g. `tenant-acme.atlas.example.com`) — future slice.
- Cross-tab BroadcastChannel coordination — future slice if users complain.
- Cross-tenant audit-log view for super_admin — future slice 145+ (slice 124 stays per-tenant at v1).
- Invite-link subsystem with email — future slice; established-install unknown-user renders contact-admin page only.
- Tenant deletion with retention policy + data purge semantics — future slice; needs separate design.
