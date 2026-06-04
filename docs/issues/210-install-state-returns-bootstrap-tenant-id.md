# 210 — `/v1/install-state` returns bootstrap tenant_id (close slice 209's BE/FE contract gap)

**Cluster:** Auth (backend + bootstrap seed)
**Estimate:** ~0.25d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** spillover surfaced 2026-05-23 during atlas-edge sign-in validation. Slice 209 shipped a frontend that gates the email/password form on a `tenant_id` field that `/v1/install-state` never returned. Result: on a fresh install — exactly the deploy shape slice 209 was built for — the email/password card is silently hidden and operators see only the legacy bearer-paste form. This slice closes the contract gap.

## Narrative

`web/app/login/page.tsx:30-47` does an SSR fetch:

```ts
async function fetchBootstrapTenantID(): Promise<string | null> {
  const response = await fetch(`${apiBaseURL()}/v1/install-state`, ...);
  const body: { first_install?: boolean; tenant_id?: string } = await response.json();
  if (body.first_install && body.tenant_id) return body.tenant_id;
  return null;
}
```

The result becomes a guard:

```tsx
{
  bootstrapTenantID ? <Card>email/password form</Card> : null;
}
```

The backend (`internal/api/install_state.go:51-54`) returns only:

```go
type installStateResponse struct {
    FirstInstall bool `json:"first_install"`
}
```

So `body.tenant_id` is `undefined` → `bootstrapTenantID` is `null` → the form never renders → operators see only the legacy bearer-paste card. Live evidence from atlas-edge.home.gmoney.sh on commit `d35dae9` (current `main`):

```
$ curl -s http://atlas-edge.home.gmoney.sh/v1/install-state
{"first_install":true}
```

Slice 209's AC-10 explicitly committed the backend half:

> AC-10: The form auto-populates `tenant_id` from a server-side `/v1/install-state` fetch when `first_install: true`.

The frontend was implemented to AC-10; the backend handler + seed.sql path were not extended. This slice ships the backend half.

### What ships in this slice

**Backend (`internal/platform/status.go`):**

- Add `BootstrapTenantID(ctx) (uuid.UUID, error)` method. Reads `SELECT id FROM tenants WHERE is_bootstrap_tenant = TRUE LIMIT 1`. Returns `uuid.Nil` + nil error when no row matches (graceful — the handler treats `uuid.Nil` as "no bootstrap tenant available"); returns error only on a true DB failure.
- Fallback query for installs that pre-date the slice-144 `tenants` table being populated by `seed.sql` (e.g. the live atlas-edge instance): when the primary query returns no row, fall back to `SELECT tenant_id FROM users ORDER BY created_at ASC LIMIT 1`. The bootstrap user is by construction the oldest user, so its `tenant_id` is the canonical bootstrap tenant by definition. The fallback is a one-call-site convenience for the install-state surface only — it does NOT become a general-purpose "find a tenant" helper.

**Backend (`internal/api/install_state.go`):**

- Extend `installStateResponse` with `TenantID string \`json:"tenant_id,omitempty"\``.
- Extend `PlatformStatus` interface with `BootstrapTenantID(ctx) (uuid.UUID, error)`.
- In `handleInstallState`: when `first := IsFirstInstall(...)` is `true`, call `BootstrapTenantID`. On success with a non-Nil UUID, set `resp.TenantID = id.String()`. On any error, log at WARN level and continue with `tenant_id` omitted — the endpoint stays a best-effort hint; a backend hiccup must not break the login page render.

**Bootstrap seed (`deploy/docker/bootstrap/seed.sql`):**

- Insert a `tenants(id, name, is_bootstrap_tenant)` row at bootstrap time so the primary query in `BootstrapTenantID` hits a row on fresh installs going forward. Idempotent via `ON CONFLICT DO NOTHING`. Update the stale comment at line 8 ("there is no `tenants` table in v1") — slice 144's migration created one.

**Tests:**

- Unit test `TestHandleInstallState_FreshInstall_IncludesTenantID` — fake `PlatformStatus` returns `firstInstall=true` + a tenant UUID; verifies response body contains both fields.
- Unit test `TestHandleInstallState_FreshInstall_BootstrapTenantErrorOmits` — fake returns `firstInstall=true` + an error from `BootstrapTenantID`; verifies response is `{"first_install":true}` (no `tenant_id` field) and HTTP 200 (degraded-gracefully). The handler logs the error at WARN.
- Unit test `TestHandleInstallState_PostFirstInstall_OmitsTenantID` — `firstInstall=false`; verifies `BootstrapTenantID` is NOT called and the response is `{"first_install":false}`.
- Integration test `TestStatus_BootstrapTenantID_PrimaryQueryHitsTenantsRow` — seeds a `tenants` row with `is_bootstrap_tenant=true`; verifies the primary query returns its id.
- Integration test `TestStatus_BootstrapTenantID_FallbackToUsers` — seeds NO `tenants` row, seeds one `users` row with `tenant_id=<x>`; verifies the fallback query returns `<x>`.
- Integration test `TestStatus_BootstrapTenantID_NoBootstrapAtAll` — seeds neither; verifies `(uuid.Nil, nil)` return.

## Threat model

| STRIDE                       | Threat                                                                                                                                                       | Mitigation                                                                                                                                                                                                                                                                                     |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | An attacker who can read `/v1/install-state` learns the bootstrap tenant UUID and uses it to forge a sign-in attempt.                                        | The tenant UUID is NOT a credential. Sign-in still requires email + password. UUIDs leak from many surfaces (URLs, audit logs); the security boundary is the bcrypt-verified password. Endpoint is public by slice-073 design — no regression.                                                 |
| **T** Tampering              | n/a — endpoint is read-only.                                                                                                                                 | n/a                                                                                                                                                                                                                                                                                            |
| **R** Repudiation            | n/a — no state mutation.                                                                                                                                     | n/a                                                                                                                                                                                                                                                                                            |
| **I** Information disclosure | The endpoint exposes the bootstrap tenant_id to an unauthenticated caller.                                                                                   | Acceptable per slice 209 D3 + slice 073's intentional-public stance. Tenant UUIDs are already exposed via OIDC bootstrap path's response headers. Only the BOOTSTRAP tenant_id is returned (a single well-known value); no enumeration of other tenants becomes possible.                      |
| **D** Denial of service      | An attacker hammers `/v1/install-state` to force the new DB query.                                                                                           | Query is an indexed PK / `is_bootstrap_tenant` partial-index scan; cost ≤ 1ms. Endpoint already has slice-087 security headers + slice-088 rate-limit budget (revisit if profile shows hot-path issues, but baseline cost is trivial vs. existing per-render cost).                            |
| **E** Elevation of privilege | A non-bootstrap user could be the "oldest" user in the fallback path and have their tenant_id returned as the bootstrap one (subtle multi-tenant confusion). | The fallback only fires on a corrupted-install path (no `tenants` row at all). On a clean install going forward, seed.sql populates `tenants` and the primary query is authoritative. The fallback is graceful-degradation for the existing atlas-edge instance, not a future-supported state. |

## Acceptance criteria

- [ ] AC-1: `internal/platform/status.go` adds `BootstrapTenantID(ctx) (uuid.UUID, error)` method. Returns `(uuid.Nil, nil)` when no tenant exists; returns error only on DB failure.
- [ ] AC-2: The method's primary query is `SELECT id FROM tenants WHERE is_bootstrap_tenant = TRUE LIMIT 1`. When `pgx.ErrNoRows`, falls back to `SELECT tenant_id FROM users ORDER BY created_at ASC LIMIT 1`. When that ALSO returns `pgx.ErrNoRows`, returns `(uuid.Nil, nil)` — no error, just no bootstrap tenant.
- [ ] AC-3: `internal/api/install_state.go` extends `installStateResponse` with `TenantID string \`json:"tenant_id,omitempty"\``and`PlatformStatus`interface with`BootstrapTenantID(ctx) (uuid.UUID, error)`.
- [ ] AC-4: `handleInstallState` calls `BootstrapTenantID` only when `first == true`. On success with non-Nil UUID, populates `resp.TenantID`. On error, logs at WARN via `s.installLogger()` and omits the field (response remains HTTP 200).
- [ ] AC-5: `deploy/docker/bootstrap/seed.sql` inserts a `tenants` row with `(id=:default_tenant_id, name='Default Tenant', is_bootstrap_tenant=true)` BEFORE the existing scope_dimensions / scope_cells / users inserts. Idempotent via `ON CONFLICT (id) DO NOTHING`. Updates the file-header comment block to reflect the new step.
- [ ] AC-6: Unit test `TestHandleInstallState_FreshInstall_IncludesTenantID` — pinned in `internal/api/install_state_test.go`; fake `PlatformStatus.BootstrapTenantID` returns a fixed UUID; response body is `{"first_install":true,"tenant_id":"<uuid>"}`.
- [ ] AC-7: Unit test `TestHandleInstallState_FreshInstall_BootstrapTenantErrorOmits` — fake returns error; response is `{"first_install":true}` (no `tenant_id`); HTTP 200.
- [ ] AC-8: Unit test `TestHandleInstallState_PostFirstInstall_OmitsTenantID` — fake returns `firstInstall=false`; the fake's `BootstrapTenantID` is asserted NOT to have been called (track call count on the fake); response is `{"first_install":false}`.
- [ ] AC-9: Integration test `TestStatus_BootstrapTenantID_PrimaryQueryHitsTenantsRow` (in `internal/platform/status_test.go` under `//go:build integration`) — seeds a `tenants` row with `is_bootstrap_tenant=true`; primary query returns its id.
- [ ] AC-10: Integration test `TestStatus_BootstrapTenantID_FallbackToUsers` — empty `tenants` table + one `users` row; fallback query returns that user's `tenant_id`.
- [ ] AC-11: Integration test `TestStatus_BootstrapTenantID_NoBootstrapAtAll` — empty `tenants` AND empty `users` tables; returns `(uuid.Nil, nil)`.
- [ ] AC-12: Live edge re-verification (manual, post-merge after Watchtower pulls): `curl http://atlas-edge.home.gmoney.sh/v1/install-state` returns `{"first_install":true,"tenant_id":"<uuid>"}` and the `/login` page renders the email/password card.

## Decisions

- **D1: Two-step query (primary + users fallback)** vs. a single migration that backfills `tenants` from `users`. Picked two-step because the migration approach mutates state on every deploy of every install (idempotent but heavy-handed), and the atlas-edge instance benefits from working without a re-bootstrap. The fallback is annotated in code as a "corrupted-install / pre-slice-210 graceful degradation" path — not a permanent contract.
- **D2: `omitempty` on `TenantID`.** A `null` field would force the FE to handle two negative shapes (`null` and `undefined`). `omitempty` collapses to one. Slice 209's FE already checks `body.tenant_id` truthiness, so both shapes work, but `omitempty` is the cleaner contract.
- **D3: Seed.sql comment freshness.** The seed.sql header comment at line 8 ("there is no `tenants` table in v1") is stale — slice 144 created one. Refresh the comment as part of this slice. This is a 1-line edit; deferring to a future docs slice would compound future-engineer confusion.
- **D4: WARN-log on BootstrapTenantID error, not 500.** The endpoint's slice-073 contract is "best-effort hint to the UI"; failing closed (500) would break the existing post-first-install login render. Stays HTTP 200 with the field omitted.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — one method added, one field added, one response shape extended, one seed.sql section added. No rearchitecture.
- **No new tables / no new RLS surfaces.** Reads from existing `tenants` and `users`.
- **Tenant isolation:** `tenants` is a global table (no `tenant_id` column on itself); the slice-144 RLS migration set the appropriate policy. `users` is tenant-scoped but the fallback query runs on the `atlas_app` pool's `public_read`-equivalent surface — verify the existing migration grants the `SELECT tenant_id FROM users` query the necessary RLS visibility (this slice should NOT alter the policy; if the policy denies the read, prefer adding a more conservative method via the `migrate` pool).

## Anti-criteria (P0 — block merge)

- **P0-A1: Does NOT add a new public column or new endpoint.** Extends the existing `/v1/install-state` response shape only.
- **P0-A2: Does NOT enumerate non-bootstrap tenants.** The query is `LIMIT 1` and gated on `is_bootstrap_tenant = TRUE`. The fallback is also `LIMIT 1`. Returning multiple tenants would break the FE's single-tenant-picker assumption (multi-tenant picker is slice 141).
- **P0-A3: Does NOT change behavior when `first_install = false`.** Post-first-install installs continue returning exactly `{"first_install":false}`. Verified by AC-8.
- **P0-A4: Does NOT alter the `atlas_session` cookie path or the bearer-paste card.** This slice is purely about closing the slice 209 BE/FE contract gap; the legacy bearer-paste affordance stays untouched.
- **P0-A5: Does NOT break the existing unit-test surface that constructs `Server{}` without a wired `PlatformStatus`.** The new method lives on the existing interface; the `nil platformStatus` check in `handleInstallState` already returns 503 before any method dispatch, so adding a method to the interface only affects callers that explicitly attach a `PlatformStatus`.
- **P0-A6: Does NOT run the `users`-fallback query when the primary query succeeded.** The fallback is an `ErrNoRows`-gated second step, not a parallel query.

## Dependencies

- **#209** — merged. This slice closes its BE/FE contract gap.
- **#144** — merged (tenants table exists).
- **#073** — merged (`/v1/install-state` endpoint exists, public-by-design contract).
- **#192** — merged (slice's tenants slug create flow; not directly used but it's the parallel write surface).

## Notes for the implementing agent

- The `Bootstrap` term is overloaded in this repo: `BootstrapResult` (slice 198 OIDC bootstrap), `bootstrap.sh` (compose first-boot), `is_bootstrap_tenant` (slice 144 column), `bootstrap_token_consumed_at` (slice 073 platform_status column). The new method name `BootstrapTenantID` is consistent with the column it reads.
- The slice-073 fake `fakePlatformStatus` in `install_state_test.go:21-27` needs a new field + method. Mirror the existing `markCalls` counter pattern with a `bootstrapTenantCalls` counter so AC-8 can assert "not called".
- Edge re-verification (AC-12) requires the merge → container-publish → Watchtower pull cascade. Build time ~5-10 min; Watchtower poll interval is 5 min. So allow ~15 min before re-curling the live endpoint.
- The stable atlas.home.gmoney.sh deploy is on v1.15.0 today (release-please PR #507 still open for v1.16.0). v1.16.0 will package this fix along with slices 206 + 208. Verifying on stable requires merging #507 first; not in scope here.
- If during implementation the integration test surfaces that `atlas_app` lacks SELECT on `users.tenant_id` from the read pool, fall back to running the query via the migrate pool (BYPASSRLS) — same precedent as `Status.MarkFirstSignin`. Document the RLS-policy reasoning in the decisions log.
