# 431 — External-IdP / OIDC operator setup guide

**Cluster:** Docs
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**Why.** The published install guide (`docs-site/docs/install.md`) covers **local mode only** — it explicitly says "no external IdP required for first sign-in. OIDC is a later configuration step, not a prerequisite." [ADR-0003](../adr/0003-oauth-authorization-server.md) + the README describe the OIDC RP architecture (atlas authenticates the human via an external IdP, then mints the atlas JWT), and the platform already ships a full admin SSO surface — `GET / PATCH /v1/admin/sso`, `POST /v1/admin/sso/preflight`, a web UI at `/admin/sso`, and a per-tenant `oidc_idp_configs` table. But there is **no operator guide** for the "connect your Okta / Entra (Azure AD) / Keycloak" step that every production self-host eventually needs. The capability exists; the documentation for driving it does not.

**What.** A new published page `docs-site/docs/oidc-setup.md` that walks an operator through connecting an external IdP, grounded in the **actual** configuration path:

- The config is **per-tenant and stored in the database** (`oidc_idp_configs`: `issuer_url`, `client_id`, `client_secret_enc`, `redirect_url`, `allowed_email_domains`), set via the admin SSO surface (`PATCH /v1/admin/sso` and the `/admin/sso` web UI) — **not** via environment variables. (This corrects a common misconception; see implementer notes.)
- The fields an operator gathers from their IdP (issuer URL, client ID, client secret, redirect URI, allowed email domains).
- The `POST /v1/admin/sso/preflight` server-side discovery-fetch step that validates the issuer before saving.
- Per-IdP notes for **Okta**, **Entra ID (Azure AD)**, and **Keycloak** (where each surfaces the issuer URL, how to register the redirect URI, where the client secret lives).
- The fallback: local mode remains available; OIDC is additive, and connecting an IdP does not disable the local default user unless the operator chooses to.

**Scope discipline.** Documentation-only, against the **shipped** admin-SSO surface. It does NOT add env-var-based IdP config (that path does not exist and must not be invented). It does NOT change the `oidc_idp_configs` schema, the adminsso handler, or the web UI. It documents one primary IdP per tenant (matching the current `PATCH /v1/admin/sso` "primary IdP" semantics), not multi-IdP federation. Multi-IdP, SCIM provisioning, and group-to-role mapping are out of scope (note them as future work if they surface).

## Threat model

Docs slice STRIDE pass — and a load-bearing one, because an OIDC setup guide that recommends an insecure configuration creates a real auth vulnerability in every deployment that follows it. The project has prior open-redirect history (slices 086 / 161), so the redirect-URI guidance is especially load-bearing.

**S — Spoofing (load-bearing).** The guide MUST get the OIDC security primitives right: the redirect URI must be **exact-match allowlisted** at the IdP (no wildcards), state + PKCE + nonce are the RP's CSRF/replay defenses (already shipped — slices 365 etc.), and `allowed_email_domains` constrains which IdP principals may sign in. _Threat:_ a guide that tells operators to register a wildcard redirect URI, or to skip the allowed-domain restriction, weakens the auth boundary. _Mitigation:_ the guide documents exact-match redirect URIs and recommends setting `allowed_email_domains`.

**T — Tampering.** N/A (docs).

**R — Repudiation.** The guide notes that OIDC sign-ins are audit-logged (the existing audit surface distinguishes a CSRF/state-mismatch attempt from an ID-token-replay attempt — slice 365's distinct sentinels). It does not add a new audit surface.

**I — Information disclosure (load-bearing).** The **client secret** is the sensitive value. The guide MUST document that it is stored **encrypted at rest** (`client_secret_enc BYTEA`), entered via the admin UI / `PATCH /v1/admin/sso` (not committed to a repo, not placed in `.env`, not logged). _Anti-criterion:_ the guide MUST NOT instruct operators to put the client secret in `docker-compose.yml`, `.env`, or any committed file. All example secrets/IDs are placeholders.

**D — Denial of service.** The `preflight` step makes a server-side outbound fetch to the issuer's discovery document — the guide notes the issuer must be reachable from the atlas host. No new unbounded surface.

**E — Elevation of privilege (load-bearing — open-redirect class).** The redirect URI is the open-redirect attack surface (the project's slice 086 / 161 history). The guide MUST document that the redirect URI registered at the IdP and stored in `redirect_url` must point only at the atlas callback path (`/auth/oidc/callback`) on the operator's own origin — never an attacker-controllable host, never a wildcard. _Threat:_ a permissive redirect URI enables token interception / open redirect. _Mitigation:_ exact-match, own-origin redirect URI guidance, called out as a security note.

## Acceptance criteria

- [ ] **AC-1.** New file `docs-site/docs/oidc-setup.md` exists, written for an operator connecting an external IdP to a running deployment.
- [ ] **AC-2.** The guide documents the **actual** config path: per-tenant DB-backed `oidc_idp_configs`, set via the `/admin/sso` web UI and `PATCH /v1/admin/sso` — explicitly NOT environment variables.
- [ ] **AC-3.** The guide lists the fields an operator gathers (issuer URL, client ID, client secret, redirect URI, allowed email domains) and maps each to its `oidc_idp_configs` column / admin-SSO request field.
- [ ] **AC-4.** The guide documents the `POST /v1/admin/sso/preflight` discovery-validation step before saving.
- [ ] **AC-5.** Per-IdP notes for Okta, Entra ID (Azure AD), and Keycloak: where each exposes its issuer URL, how to register the exact-match redirect URI, and where the client secret is obtained.
- [ ] **AC-6.** The guide documents the redirect URI as exact-match, own-origin, pointing at the atlas callback path — with an explicit security note against wildcards / foreign hosts (threat-model E / open-redirect).
- [ ] **AC-7.** The guide documents the client secret as stored encrypted at rest and entered via the admin surface, never committed to `.env` / compose / repo (threat-model I).
- [ ] **AC-8.** The guide documents `allowed_email_domains` as the principal-restriction control and recommends setting it.
- [ ] **AC-9.** The guide documents the fallback: local mode remains available; connecting an IdP is additive.
- [ ] **AC-10.** All example issuer URLs, client IDs, secrets, and redirect URIs are placeholders — no real tenant value.
- [ ] **AC-11.** A nav entry pointing at `oidc-setup.md` is added to `docs-site/mkdocs.yml`.
- [ ] **AC-12.** The guide cross-links to the install guide (local-mode first boot) and to [ADR-0003](../adr/0003-oauth-authorization-server.md) (the RP + AS architecture).
- [ ] **AC-13.** `mkdocs build --strict` passes from `docs-site/`.
- [ ] **AC-14.** Every endpoint, field name, and column the guide references is verified against `internal/api/adminsso/handler.go` + the `oidc_idp_configs` schema + `internal/auth/oidc/oidc.go` — no invented surface.

## Constitutional invariants honored

- **OIDC, relying-party only** (tech stack: AuthN) — the guide documents atlas-as-RP authenticating the human via the external IdP; it does not position atlas as an IdP.
- **Tenant isolation via RLS** (#6) — the guide reflects that IdP config is per-tenant (`oidc_idp_configs.tenant_id`), respecting the tenant boundary.
- **AI-assist boundary** — no inference surface; the guide is deterministic operator instructions.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — AuthN (OIDC RP) + Authorization Server rows.
- [ADR-0003](../adr/0003-oauth-authorization-server.md) — OAuth AS layered on the OIDC RP.

## Dependencies

- **#034 / #062** (OIDC RP + admin SSO surface `/v1/admin/sso`) — `merged`. The config surface the guide documents.
- **#187-#192** (auth-substrate-v2 spine: AS, JWT, OAuth flows) — `merged`. The RP→AS architecture the guide references.
- **#365** (OIDC nonce defense) — `merged`. The replay defense the guide cites as already-shipped.

## Anti-criteria (P0 — block merge)

- **P0-431-1.** Does NOT invent an env-var-based IdP config path (`OIDC_ISSUER_URL` etc.) — that does not exist; config is DB-backed via `/admin/sso` (threat-model accuracy + P0-431-5).
- **P0-431-2.** Does NOT recommend a wildcard or foreign-host redirect URI — exact-match, own-origin only (threat-model E / open-redirect, slice 086/161 history).
- **P0-431-3.** Does NOT instruct operators to place the client secret in `.env`, `docker-compose.yml`, or any committed/logged location (threat-model I).
- **P0-431-4.** Does NOT include a real issuer URL, client ID, client secret, or redirect URI — placeholders only.
- **P0-431-5.** Does NOT document any endpoint / field / column that is not present in the shipped `adminsso` handler + `oidc_idp_configs` schema (AC-14 is the guard).
- **P0-431-6.** Does NOT change the adminsso handler, the schema, or the web UI — docs-only.

## Skill mix (3-5)

- `grill-with-docs` — align the guide against `internal/api/adminsso/handler.go`, `internal/auth/oidc/oidc.go`, the schema, and ADR-0003.
- `Security` — verify the redirect-URI and client-secret guidance is sound (open-redirect class).
- `simplify` — keep the per-IdP notes tight; one screen per IdP.
- `verify` — confirm every referenced endpoint/field exists (AC-14).

## Notes for the implementing agent

- **Accuracy correction (load-bearing).** The original idea framed this as "required env `OIDC_ISSUER_URL`, client id/secret, redirect URIs". That is **wrong** for the shipped platform: OIDC IdP config is **per-tenant, DB-backed** in `oidc_idp_configs` and set via the admin SSO surface (`internal/api/adminsso/handler.go`: `GET / PATCH /v1/admin/sso`, `POST /v1/admin/sso/preflight`; web UI `web/app/admin/sso/page.tsx`; BFF `web/app/api/admin/sso/route.ts`). There is no `OIDC_ISSUER_URL` env var. Write the guide to the real surface; do not document the non-existent env path. (`ATLAS_ISSUER_URL` in `cmd/atlas/main.go` configures the atlas **AS issuer** identity — a different thing from the external IdP issuer; do not conflate them.)
- The `oidc_idp_configs` columns are the spine of AC-3: `name`, `issuer_url`, `client_id`, `client_secret_enc` (encrypted), `redirect_url`, `allowed_email_domains TEXT[]`, unique `(tenant_id, name)`. The handler exposes a "primary IdP" model (PATCH upserts the tenant's primary) — document one-primary-per-tenant, not federation.
- The `preflight` endpoint does a server-side fetch of the IdP's discovery document — that is the natural "validate before save" step in the guide's flow.
- Per-IdP issuer-URL forms to get right: Okta `https://<org>.okta.com` (or custom auth-server `/oauth2/<id>`); Entra `https://login.microsoftonline.com/<tenant>/v2.0`; Keycloak `https://<host>/realms/<realm>`. Verify against the project's OIDC discovery handling rather than asserting blind.
- Detection-tier: an invented-surface bug here is `target=manual_review` (AC-14 hand-check against source). Note in decisions log.
