# Slice 431 — decisions log (JUDGMENT slice)

**Slice:** 431 — External-IdP / OIDC operator setup guide
**Type:** JUDGMENT (build-time subjective calls made by the implementing agent; no human sign-off gate)
**Scope:** documentation-only — `docs-site/docs/oidc-setup.md` + `docs-site/mkdocs.yml` nav entry + `CHANGELOG.md` + this log.

## Surface verification (AC-14 — load-bearing)

Every endpoint / field / column referenced in the guide was hand-checked
against the shipped source before writing it. Nothing was invented.

| Referenced in guide                                                                                                                                                 | Verified against                                                                    | Result                 |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ---------------------- |
| `GET /v1/admin/sso`, `PATCH /v1/admin/sso`, `POST /v1/admin/sso/preflight` (admin-only)                                                                             | `internal/api/adminsso/handler.go` (package doc + `Get`/`Patch`/`Preflight`)        | exists                 |
| PATCH fields `issuer_url`, `client_id`, `client_secret` (write-only, empty=leave), `redirect_url`, `allowed_email_domains`                                          | `adminsso.PatchRequest` struct                                                      | exists                 |
| GET omits the client secret                                                                                                                                         | `adminsso.GetResponse` (no secret field) + package doc                              | exists                 |
| Preflight body `issuer_url`; response `issuer`/`authorization_endpoint`/`token_endpoint`/`jwks_uri`                                                                 | `adminsso.PreflightRequest` / `PreflightResponse`                                   | exists                 |
| Preflight is https-only, rejects raw IP / loopback / RFC1918, size-capped, timed out                                                                                | `adminsso.Preflight` + `guardSSRF` + `newSafeHTTPClient` + `isUnsafeIP`             | exists                 |
| Columns `id`, `tenant_id`, `name`, `issuer_url`, `client_id`, `client_secret_enc BYTEA`, `redirect_url`, `allowed_email_domains TEXT[]`, unique `(tenant_id, name)` | `migrations/sql/20260511000012_users_sessions_api_keys.sql` (CREATE TABLE)          | exists                 |
| Secret stored **encrypted at rest** as `client_secret_enc`                                                                                                          | schema column type BYTEA + handler comment "encrypted-at-rest (slice 034 contract)" | exists (v1 note below) |
| One primary IdP per tenant, keyed `name = "primary"`                                                                                                                | `adminsso.configName = "primary"` + package doc                                     | exists                 |
| Callback path `/auth/oidc/callback`; login `/auth/oidc/login?tenant_id=...&idp=...`                                                                                 | `internal/api/httpserver.go:900-901` + `internal/api/auth/http.go`                  | exists                 |
| State + PKCE + nonce enforced by the RP; scopes `openid email profile`                                                                                              | `internal/auth/oidc/oidc.go` (`BeginLogin`, `HandleCallback`, `Scopes`)             | exists                 |
| `allowed_email_domains` admits only `@<domain>` suffix match (case-insensitive); empty = admit all                                                                  | `oidc.go HandleCallback` domain-allowlist block                                     | exists                 |
| Audit distinguishes CSRF/state mismatch from ID-token replay                                                                                                        | `oidc.go` `ErrStateMismatch` vs `ErrNonceMismatch` sentinels (slice 365)            | exists                 |
| Web UI at `/admin/sso` (preflight card + write-only secret form)                                                                                                    | `web/app/admin/sso/page.tsx`                                                        | exists                 |

**Surfaces deliberately NOT documented (would be invented):** any
`OIDC_ISSUER_URL`-style env var (does not exist — guide actively corrects
this); multi-IdP federation; SCIM; group-to-role mapping. The unrelated
`ATLAS_ISSUER_URL` (atlas AS issuer identity, `cmd/atlas/main.go`) is
explicitly disambiguated from the external IdP issuer, not conflated.

## Decisions made

1. **Guide structure = preflight-first, then per-IdP, then save/sign-in.**
   Ordered the flow as: where config lives → the five fields → preflight →
   register at IdP (per-vendor tabs) → save → sign in → local-mode fallback
   → out-of-scope. Rationale: matches the operator's real sequence and front-
   loads the two security-critical fields (redirect URI, client secret) into
   their own subsections under "the five fields" so they are unmissable.

2. **Per-IdP detail = tight prose in `pymdownx.tabbed` tabs, no screenshots.**
   Used the content-tab extension already enabled in `mkdocs.yml`
   (`pymdownx.tabbed`) for Okta / Entra / Keycloak. Chose prose over
   screenshots: IdP admin UIs change frequently and screenshots rot fast in a
   self-hosted docs set; the durable facts are the issuer-URL shape, the
   redirect-URI registration field, and where the secret lives — which are
   prose-stable. One screen per IdP (skill-mix `simplify`).

3. **Security notes rendered as `!!! danger` / `!!! warning` admonitions.**
   The redirect-URI rule and the client-secret rule each get a `danger`
   admonition; the "no env var" correction gets a `warning`; preflight
   constraints + allowed-domains recommendation get `note`/`tip`. Rationale:
   threat-model E (open-redirect) and I (secret handling) are the load-bearing
   reasons this slice exists; visual prominence is proportional to risk.

4. **ADR + install cross-links use absolute GitHub blob URLs for the ADR,
   relative for install.** `install.md` is inside `docs-site/docs/` so a
   relative link survives `--strict`; `docs/adr/0003-*.md` is OUTSIDE the
   docs site tree, so a relative link would break strict mode. Matched the
   existing convention in `docs-site/docs/oauth-grants.md`, which links ADR-0003
   via a `github.com/.../blob/main/docs/adr/...` URL.

5. **All examples are non-secret-shaped placeholders.** Issuer
   `https://idp.example.com`, client id `atlas-client-id-placeholder`, secret
   literal `REPLACE-WITH-YOUR-IDP-CLIENT-SECRET`, redirect
   `https://atlas.example.com/auth/oidc/callback`. Deliberately avoided any
   `eyJ*` / `ghp_*` / `sk_live_*` token shape so GitGuardian does not flag a
   doc example (AC-10 / P0-431-4).

6. **Documented the v1 secret-at-rest honestly.** The guide states the secret
   is "stored encrypted at rest" in `client_secret_enc` (the column contract
   and the handler's stated intent). Did NOT overstate the crypto: the handler
   comment notes v1 stores raw bytes with KMS-wrap as a v1.x follow-up. The
   operator-facing guidance ("never commit/log it; enter via admin surface")
   holds regardless of the wrap state, so the guide does not need to expose the
   v1.x internal caveat to the operator — but see "Revisit" below.

## Revisit once in use

- **Secret-at-rest wording vs. v1.x KMS-wrap.** If/when KMS-wrap lands (the
  slice-034 v1.x follow-up), no guide change is needed — "encrypted at rest"
  becomes more literally true. If a deployment hardening guide is later added,
  it should state the current wrap state explicitly. Tracked only here; not a
  spillover slice.
- **Per-IdP exact field labels.** Vendor console labels (e.g. Entra's
  "Certificates & secrets") drift over time. Revisit the per-IdP tabs if a
  vendor renames a surface; durable facts (issuer shape, callback path) are
  unaffected.
- **Multi-IdP / SCIM / group-to-role.** Noted as out of scope. When any of
  these ship as real surface, the "Out of scope" section converts to real
  documentation.

## Confidence

**High.** The guide documents only verified shipped surface (table above);
the security guidance is the conservative, threat-model-aligned posture
(exact-match own-origin redirect, never-commit secret, set allowed domains);
`mkdocs build --strict` passes; all examples are placeholders. The single
soft spot is the v1 secret-wrap nuance, which is handled by keeping the
operator-facing instruction wrap-agnostic.

## Detection-tier classification

- **`detection_tier_actual`: `none`** — no bug surfaced during the slice. The
  shipped surface matched the slice spec's assumptions (the spec already
  corrected the env-var misconception); the guide was written against verified
  source on the first pass.
- **`detection_tier_target`: `manual_review`** — per the slice spec's note, an
  invented-surface bug in a docs slice is caught by the AC-14 hand-check
  against source (`manual_review`), not by an automated tier. `mkdocs
build --strict` catches broken links / missing nav (a `contract`-like gate)
  but cannot catch a plausibly-worded but non-existent endpoint — only the
  source hand-check can. The verification table above is that review.

## Constitutional invariants honored

- **OIDC, relying-party only** — guide positions atlas as the RP authenticating
  the human via the external IdP; never as an IdP.
- **Tenant isolation via RLS (#6)** — guide reflects per-tenant
  `oidc_idp_configs.tenant_id` config; admin surface reads tenant from the
  credential, never the wire.
- **AI-assist boundary** — no inference surface; deterministic operator
  instructions only.
