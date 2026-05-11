# 034 — OIDC RP for SSO + local users for solo deployments

**Cluster:** Multi-tenancy / auth
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Implement OIDC Relying Party authentication: users sign in via their org's IdP (Google, Azure AD, Okta). For solo deployments without an IdP, support local-user authentication with password + recovery codes. The OIDC flow uses authorization code + PKCE; tokens are short-lived; refresh tokens stored encrypted. On successful sign-in, the platform establishes session and resolves tenant + role context (consumed by slice 033 RLS plumbing and slice 035 OPA).

The slice also owns the `api_keys` table and the minimal CRUD endpoints for credential management (per the D4 review decision, OQ #18 push-credential-issuance UX is deferred to v1.x; v1 ships CLI-only issuance via slice 003 wrapping these admin endpoints). API keys are write-once at issue: the bearer token is returned once, hashed at rest with bcrypt/argon2id, and never retrievable again — rotation is the recovery path.

The slice delivers value because real users can sign in (web flow) and CI pipelines can authenticate (API key flow) — without this, the rest of the platform is unreachable.

## Acceptance criteria

- [ ] AC-1: `/auth/oidc/login` initiates OIDC flow against configured IdP; PKCE used
- [ ] AC-2: `/auth/oidc/callback` exchanges code for tokens; creates user if new; establishes session
- [ ] AC-3: `/auth/local/login` accepts username + password for local users (solo deploy mode)
- [ ] AC-4: Sessions stored server-side; cookie is opaque session id
- [ ] AC-5: Refresh-token flow keeps sessions alive without re-auth (sliding window)
- [ ] AC-6: Logout invalidates session both client and server side
- [ ] AC-7: OIDC config supports multiple IdPs (per-tenant: tenant A uses Google, tenant B uses Azure AD)
- [ ] AC-8: `api_keys` table exists with: `id`, `tenant_id`, `bearer_token_hash` (bcrypt/argon2id), `scope_predicate` (jsonb), `allowed_kinds[]`, `issued_by`, `issued_at`, `expires_at`, `last_used_at`, `revoked_at`
- [ ] AC-9: Admin endpoint `POST /v1/admin/credentials` issues a new key; returns the bearer token **once** in the response (never retrievable again)
- [ ] AC-10: Admin endpoint `POST /v1/admin/credentials/:id/rotate` issues a successor; old key remains valid for an overlap window (default 7 days) before auto-revoke
- [ ] AC-11: Admin endpoint `POST /v1/admin/credentials/:id/revoke` invalidates immediately; subsequent push attempts return 401
- [ ] AC-12: Admin endpoint `GET /v1/admin/credentials` lists active keys (never includes bearer tokens); filterable by tenant
- [ ] AC-13: API-key auth middleware validates bearer token against hash; updates `last_used_at`; rejects revoked/expired keys with 401

## Constitutional invariants honored

- **Tech-stack lock:** OIDC RP only (never an IdP); RBAC + ABAC for AuthZ in slice 035

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.5 (Auth model)
- `Plans/canvas/10-roadmap.md` §10.1 (Auth: OIDC RP + local users)

## Dependencies

- #001

## Anti-criteria (P0)

- Does NOT issue identity tokens (we are RP, not IdP)
- Does NOT store passwords without bcrypt/argon2
- Does NOT skip CSRF protection on state parameter

## Skill mix (3–5)

- OIDC client library (go-oidc)
- Go session middleware
- PKCE + state validation
- Postgres for session + user storage
- Cookie security (HttpOnly, Secure, SameSite)
