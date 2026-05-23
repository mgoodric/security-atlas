# 211 — Bootstrap seed grants user_roles + super_admins (close slice 209's authz gap)

**Cluster:** Auth (bootstrap seed)
**Estimate:** ~0.25d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover surfaced 2026-05-23 on atlas-edge.home.gmoney.sh post slice 210 fix. After signing in via the slice 209 email/password form, the user's JWT carries `atlas:roles={}` and `atlas:super_admin=false`. Every admin/auditor-gated endpoint (most of `/v1/*` and most `/api/*` BFF surfaces) returns 403. Dashboard panels render "Could not load this panel · 403 Forbidden".

## Narrative

The slice-198 OIDC first-install bootstrap path (`internal/auth/users/users.go:BootstrapFirstInstallOrUpsert`) writes FIVE rows in one transaction when the FIRST user signs in via OIDC:

1. `tenants` (is_bootstrap_tenant=true) — slice 144 row
2. `users`
3. **`user_roles`** (tenant_id, user_id, role='admin', granted_by='system:bootstrap_first_install')
4. **`super_admins`** (user_id, granted_via='bootstrap_first_install')
5. `me_audit_log` (forensic anchor)

The local-credential bootstrap path via `deploy/docker/bootstrap/seed.sql` only inserts rows 1, 2, plus `local_credentials`. **It never grants user_roles or super_admins.** Slice 209 shipped the email/password sign-in surface assuming the seed path had granted authz parity with OIDC; it hadn't.

Result on a fresh self-hosted install:

- Sign-in: ✓ (users + local_credentials rows exist; password verifies)
- JWT mint: ✓ (slice 209's JWT signing path runs)
- JWT claims: `atlas:roles={}`, `atlas:super_admin=false` (because user_roles + super_admins are empty)
- Every `/v1/*` admin/auditor-gated endpoint: **403 Forbidden**
- Every `/api/*` BFF surface that proxies to those: **403 Forbidden** (panels render the error card)

### What ships in this slice

**`deploy/docker/bootstrap/seed.sql`:**

- Insert a `user_roles` row granting the bootstrap user role='admin' in the bootstrap tenant. `granted_by='system:bootstrap_seed'` (distinct from the slice-198 OIDC path's `'system:bootstrap_first_install'`, so the audit trail clearly distinguishes "first-install via local credentials" from "first-install via OIDC").
- Insert a `super_admins` row granting the bootstrap user platform-global super_admin. `granted_via='bootstrap_first_install'` (re-uses the existing slice-198 CHECK constraint's only-permitted value — semantically correct since this IS a first-install grant). The constraint is NOT extended in this slice; if future maintainer-CLI grants ever ship, that slice extends the CHECK.
- Both INSERTs use sub-SELECT from `users` rather than hardcoding the user UUID, so future changes to the seed user's id stay correct.
- Both INSERTs are idempotent: `user_roles` via `ON CONFLICT (tenant_id, user_id, role) DO NOTHING`; `super_admins` via `ON CONFLICT (user_id) DO NOTHING`. Re-running bootstrap.sh after deploy IS the backfill path for the live atlas-edge instance — see "Live backfill" below.

**No code changes outside the seed file.** The atlas Go binary already reads `user_roles` + `super_admins` via the slice-192 `DBUserResolver.ResolveForOAuth` (`internal/api/oauth/userresolver.go`) at slice 209's JWT-mint time; this slice merely populates what that resolver reads.

### Live backfill — atlas-edge.home.gmoney.sh

After this PR merges + Watchtower pulls the new `atlas-bootstrap-edge` image (~5-10 min), the operator re-runs the bootstrap container manually:

```
ssh root@unraid \
  'docker compose -f /mnt/user/appdata/atlas-edge/docker-compose.edge.yml \
     run --rm atlas-bootstrap-edge'
```

The bootstrap container is `restart: "no"` (one-shot), so Watchtower's image pull alone doesn't re-trigger it. The `run --rm` invocation re-executes the bootstrap pipeline; every INSERT is idempotent so migrations / SCF catalog / scope_dimensions / users / local_credentials are all no-ops on re-run, and only the two new `user_roles` + `super_admins` rows actually land.

The user-visible verification (~30 seconds after the re-run completes):

1. Re-sign in at `https://atlas-edge.home.gmoney.sh/login` (existing session still works but is JWT-cached; a fresh sign-in mints a JWT with the new claims)
2. Inspect the JWT's `atlas:roles` and `atlas:super_admin` claims — should be `{<tenant_id>: ["admin"]}` and `true` respectively
3. Dashboard panels: 200, no more "403 Forbidden" cards

## Threat model

| STRIDE                       | Threat                                                                                                                                              | Mitigation                                                                                                                                                                                                                                                                                               |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | The bootstrap-seed grant elevates a "default" user that operators may forget to disable.                                                            | Pre-existing concern; slice 209's narrative documents this in "Next actions" (password rotation). The bootstrap user's existence + admin grant is by design — every self-hosted GRC platform ships an admin-on-bootstrap path (Vanta, Drata, OpenGRC). Mitigation is the operator's password discipline. |
| **T** Tampering              | n/a — seed runs once during bootstrap; no runtime mutation.                                                                                         | n/a                                                                                                                                                                                                                                                                                                      |
| **R** Repudiation            | `granted_by='system:bootstrap_seed'` doesn't write a `me_audit_log` row the way the slice-198 OIDC path does.                                       | Out of scope for this fix-forward — adds risk of audit-trail gap but matches the existing seed.sql shape (no me_audit_log writes from seed). Future slice can add a forensic-anchor row symmetric with slice 198's pattern.                                                                              |
| **I** Information disclosure | n/a — no new endpoints; no new data shape exposed.                                                                                                  | n/a                                                                                                                                                                                                                                                                                                      |
| **D** Denial of service      | n/a — bootstrap is one-shot.                                                                                                                        | n/a                                                                                                                                                                                                                                                                                                      |
| **E** Elevation of privilege | The seed grants super_admin to the local-credential bootstrap user. This IS an elevation, but it's the documented + intentional bootstrap contract. | Acceptable. Identical authority shape to the slice-198 OIDC bootstrap path. No new privilege class created.                                                                                                                                                                                              |

## Acceptance criteria

- [ ] AC-1: `deploy/docker/bootstrap/seed.sql` adds a `user_roles` INSERT granting role='admin' to the bootstrap user in the bootstrap tenant. `granted_by='system:bootstrap_seed'`. Idempotent via `ON CONFLICT (tenant_id, user_id, role) DO NOTHING`.
- [ ] AC-2: `deploy/docker/bootstrap/seed.sql` adds a `super_admins` INSERT granting platform-global super_admin to the bootstrap user. `granted_via='bootstrap_first_install'` (re-uses the existing slice-198 CHECK value). Idempotent via `ON CONFLICT (user_id) DO NOTHING`.
- [ ] AC-3: Both INSERTs use sub-SELECT from `users` (matched on tenant_id + email) rather than hardcoding the user UUID. If the seed user's id ever changes, the grants follow.
- [ ] AC-4: The seed.sql file remains end-to-end re-runnable without error (covered by the existing bootstrap.sh idempotency contract).
- [ ] AC-5: docs/getting-started note (or runbook entry under docs/runbooks/) explains the manual `docker compose run --rm atlas-bootstrap-edge` command for existing-install backfill. Minimum 2 sentences; not a full runbook page.
- [ ] AC-6: Self-host bundle e2e CI verifies the bootstrap user gets admin role: existing `Self-host bundle · end-to-end (bundled)` job already exercises bootstrap → atlas → sign-in → API call. The new grant means a previously-403'ing call should now succeed. (No new CI step required; the existing job will fail if the grants don't land.)

## Decisions

- **D1: `granted_via='bootstrap_first_install'`** for the super_admin grant. The existing slice-198 CHECK constraint only admits this value. Extending the CHECK with `'bootstrap_seed'` requires a migration; the seed path is semantically a "first install" grant; reusing the value is the smallest correct change.
- **D2: `granted_by='system:bootstrap_seed'`** for the user_roles grant. user_roles.granted_by has no CHECK constraint, so I'm using a distinct value from the OIDC path (`'system:bootstrap_first_install'`) so the audit trail clearly tags WHICH bootstrap shape the operator used. Distinguishable provenance is cheap and useful for forensic correlation.
- **D3: Sub-SELECT instead of hardcoded UUID** for the user_id lookup. The seed.sql still has the hardcoded `c0000000-0000-4000-8000-000000000001` for the users INSERT (existing pre-slice-210 convention), but the grants reference users by (tenant_id, email) pair. If a future slice changes the seed user's id, the grants don't need updating in lockstep.
- **D4: No me_audit_log row.** seed.sql does not currently write to `me_audit_log` for any of its INSERTs; adding one for these grants would diverge from the established pattern. Acceptable for the fix-forward; a future slice can normalize the seed path to write audit-log entries symmetric with the OIDC path.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — two INSERTs added; no new tables, no new role surfaces, no rearchitecture.
- **No new RLS surface.** `user_roles` + `super_admins` already exist in main with established policies.
- **AI-assist boundary** — n/a (no AI surface touched).

## Anti-criteria (P0 — block merge)

- **P0-A1: Does NOT extend the super_admins.granted_via CHECK constraint.** Re-uses the existing `'bootstrap_first_install'` value. If a future slice needs new provenance values, that's a separate concern with its own migration.
- **P0-A2: Does NOT touch the slice-198 OIDC bootstrap path.** This slice fixes only the local-credential bootstrap seed; the OIDC path already grants both rows correctly.
- **P0-A3: Does NOT hardcode the user UUID in the new INSERTs.** Both grants resolve the user via (tenant_id, email) sub-SELECT.
- **P0-A4: Does NOT alter the slice 210 BootstrapTenantID query.** That fix-forward (PR #514) is independent.
- **P0-A5: Does NOT bypass the user_roles `role` CHECK constraint.** The role enum is locked at 5 values (admin/auditor/grc_engineer/risk_owner/control_owner); the grant uses 'admin' which is the canonical bootstrap role.
- **P0-A6: Does NOT add me_audit_log writes from seed.sql.** Aligns with existing seed convention; a future slice can normalize.

## Dependencies

- **#198** — merged. Defines the OIDC bootstrap pattern this slice mirrors.
- **#209** — merged. Local-credential sign-in surface that depends on the grants.
- **#210** — merged. `/v1/install-state` returns tenant_id.
- **#144** — merged. `tenants` table exists.
- **#035** — merged. `user_roles` table + OPA gates exist.

## Notes for the implementing agent

- The seed.sql INSERT for `users` uses `ON CONFLICT (tenant_id, email) DO NOTHING` — that's the only unique key on users. The new user_roles + super_admins INSERTs need their OWN ON CONFLICT clauses matched to their actual unique keys: `user_roles` PK is `(tenant_id, user_id, role)`; `super_admins` PK is `(user_id)`.
- `user_roles.user_id` is **TEXT** not UUID (per the slice 018 schema's design choice — see comment in `internal/auth/users/users.go:347-349`). The sub-SELECT must cast: `SELECT ..., u.id::text, ...`.
- `super_admins.user_id` is **UUID** (PK). No cast needed.
- The bootstrap.sh hash-password step expects `ATLAS_DEFAULT_USER_PASSWORD` env. That env is what the operator set during compose provisioning; not in scope here, but it's the operative reason the local user exists at all.
- After merging, verify on atlas-edge by:
  1. Waiting ~10 min for Watchtower to pull the new image
  2. SSH to Unraid + `docker compose run --rm atlas-bootstrap-edge`
  3. Re-sign in at `https://atlas-edge.home.gmoney.sh/login`
  4. Inspect a previously-403'ing dashboard panel — should now render data
