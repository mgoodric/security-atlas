# 034 — OIDC RP for SSO + local users for solo deployments

**Cluster:** Multi-tenancy / auth
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement OIDC Relying Party authentication: users sign in via their org's IdP (Google, Azure AD, Okta). For solo deployments without an IdP, support local-user authentication with password + recovery codes. The OIDC flow uses authorization code + PKCE; tokens are short-lived; refresh tokens stored encrypted. On successful sign-in, the platform establishes session and resolves tenant + role context (consumed by slice 033 RLS plumbing and slice 035 OPA). The slice delivers value because real users can sign in — without this, the rest of the platform is unreachable.

## Acceptance criteria

- [ ] AC-1: `/auth/oidc/login` initiates OIDC flow against configured IdP; PKCE used
- [ ] AC-2: `/auth/oidc/callback` exchanges code for tokens; creates user if new; establishes session
- [ ] AC-3: `/auth/local/login` accepts username + password for local users (solo deploy mode)
- [ ] AC-4: Sessions stored server-side; cookie is opaque session id
- [ ] AC-5: Refresh-token flow keeps sessions alive without re-auth (sliding window)
- [ ] AC-6: Logout invalidates session both client and server side
- [ ] AC-7: OIDC config supports multiple IdPs (per-tenant: tenant A uses Google, tenant B uses Azure AD)

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
