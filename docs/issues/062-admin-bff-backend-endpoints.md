# 062 — Admin BFF backend endpoints (SSO + Users + Unified audit log)

**Cluster:** Admin / backend
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Surface the missing backend HTTP endpoints that slice 060 (Admin settings UI) needs to fully ship its AC-2, AC-3, AC-6. The slice 060 PR shipped the UI shells + BFF proxy + wire-shape contracts as binding placeholders; this slice fills in the real endpoints behind them so 060 can graduate from PARTIAL to PASS.

Three endpoints are missing:

1. **`/v1/admin/sso`** — CRUD over `oidc_idp_configs` (slice 034 added the table; no HTTP surface yet). UI binds the SSO config form to this.
2. **`/v1/admin/users`** — list/get users with their role assignments (slice 035 added the role enum + OPA Rego but no HTTP surface). UI binds the Users page to this.
3. **`GET /v1/admin/audit-log`** — paginated union across `decision_audit_log` (035), `evidence_audit_log` (013), `exception_audit_log` (021), `feature_flag_audit_log` (059), `policy_audit_log` (022), `framework_scope_workflow_log` (018), `artifact_access_log` (036). UI binds the Audit page to this.

The slice doesn't add new backend capabilities — it surfaces existing data behind the admin gate. Once it merges, slice 060's frontend can flip its PARTIAL ACs to PASS without further frontend changes (the wire-shape contracts in 060's PR are the spec).

The slice delivers value because slice 060 (which is the v1 self-administered admin section) can fully land.

## Acceptance criteria

- [ ] AC-1: `GET /v1/admin/sso` — admin-only — returns the tenant's OIDC IdP config (or 404 if none); `PATCH /v1/admin/sso` upserts. Schema: `provider`, `client_id`, `client_secret` (write-only — never returned in GET), `discovery_url`, `allowed_email_domains`. Persists into `oidc_idp_configs` (slice 034 table).
- [ ] AC-2: `POST /v1/admin/sso/preflight` — admin-only — accepts `{discovery_url}`, fetches it server-side, returns parsed `{authorization_endpoint, token_endpoint, jwks_uri}` for the form preview. NO state-changing side effects.
- [ ] AC-3: `GET /v1/admin/users` — admin-only — paginated list of users in the tenant with `{id, email, name, roles[], last_login_at}`. Reads `users` + `user_roles` (slice 034 + 035 tables).
- [ ] AC-4: `GET /v1/admin/users/{id}` — admin-only — single-user detail with `{id, email, name, created_at, last_login_at, roles[]}`.
- [ ] AC-5: `PATCH /v1/admin/users/{id}/roles` — admin-only — replaces the user's role assignments with the supplied list. Writes through `user_roles` (slice 035 table). Anti-criterion P0: rejects self-demotion from `admin` without an explicit `confirm_self_demotion: true` field in the body (slice 060 AC-3 surfaces the modal that sets this).
- [ ] AC-6: `GET /v1/admin/audit-log` — admin-only — paginated union across the seven source tables. Query params: `?actor=<user_id>`, `?event_type=<type>`, `?since=<iso>`, `?until=<iso>`, `?cursor=<opaque>`, `?limit=<int>` (max 200, default 50). Returns `{rows: [{ts, source_table, event_type, actor, resource_type, resource_id, summary, audit_log_url}], next_cursor}`.
- [ ] AC-7: Audit-log SQL strategy: a `UNION ALL` view (or a materialized lazy union built per-request) over the seven source tables, projecting to a uniform shape: `(ts, source_table, event_type, actor, resource_type, resource_id, summary jsonb)`. The view name `admin_audit_log_v` lives in a new migration `20260511000022_admin_audit_log_view.sql`. The query joins the view to the auth context (cred.IsAdmin) at the handler layer; RLS on the source tables continues to enforce per-tenant filtering.
- [ ] AC-8: Integration test per endpoint (5 tests): GET sso returns config without client_secret · PATCH sso writes through · POST preflight returns parsed endpoints · GET users returns the tenant's users · PATCH user roles rejects self-demotion-without-confirm · GET audit-log returns rows ordered by ts DESC across all 7 source tables.
- [ ] AC-9: Slice 060's PR (gh#66) — after this slice merges, that PR's frontend wire shapes already match. The 060 PR should be rebased + re-tested + its AC-2 / AC-3 / AC-6 flipped from PARTIAL to PASS in its own status row.
- [ ] AC-10: `CHANGELOG.md` entry under `[Unreleased]/Added`.

## Constitutional invariants honored

- **Invariant 6 (RLS):** every endpoint reads through the standard tenant-scoped tables; the `admin_audit_log_v` view does NOT bypass RLS — each source table's RLS policies still fire on the underlying SELECT.
- **Slice 033 D1** (tenancy middleware is sole tenant-context setter): no endpoint accepts `tenant_id` in the body.
- **Slice 034 AC-9 (write-once secret):** `client_secret` is NEVER returned by `GET /v1/admin/sso`. Storage is in `oidc_idp_configs.client_secret` (encrypted or hashed per slice 034's contract).
- **Slice 035 RBAC:** every endpoint gated by `cred.IsAdmin` (or the OPA-equivalent `admin` role check). Non-admin → 403.

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.5 (Auth model — admin role)
- `Plans/canvas/08-audit-workflow.md` (audit log surfaces)
- `docs/issues/060-admin-settings-ui.md` (the frontend slice this unblocks)

## Dependencies

- **034** (oidc_idp_configs table + users + api_keys CRUD)
- **035** (role enum + user_roles + decision_audit_log)
- **013** (evidence_audit_log)
- **018** (framework_scope_workflow_log)
- **021** (exception_audit_log)
- **036** (artifact_access_log)
- **059** (feature_flag_audit_log)
- (slice 022 policy_audit_log: included in audit-log view IF present, else skipped via NULL-safe UNION)

All deps merged except 022 (policy_audit_log) — which is on main but the audit-log table may or may not exist by that exact name; AC-7 handles missing tables via `to_regclass()` guards in the view definition.

## Anti-criteria (P0 — block merge)

- Does NOT bypass tenant RLS in the audit-log view (each source table's policies still fire).
- Does NOT return `client_secret` in any GET (slice 034 AC-9 contract).
- Does NOT permit non-admin role to read or write any /v1/admin/\* endpoint.
- Does NOT permit self-demotion from `admin` without the explicit `confirm_self_demotion: true` body field (slice 060 AC-3 contract).
- Does NOT introduce a per-request N+1 across audit log source tables — the view runs one query.

## Skill mix (3–5)

- Postgres view design (`UNION ALL` across heterogeneous shapes with `to_regclass()` guards)
- Go HTTP handlers + admin gate
- sqlc query layer
- OIDC discovery URL fetching (server-side HTTP client with timeout + size cap)
- RLS-aware audit-log surfaces
