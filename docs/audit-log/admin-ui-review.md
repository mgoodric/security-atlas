# Admin settings UI spot-check audit log

> Pre-merge HITL gate for slice 060 (Admin settings UI). Three surfaces
> in this slice require human spot-check before merge per AC-9:
>
> 1. **Role-permission matrix** rendered on `/admin/users`
> 2. **SSO callback URL preflight** behavior on `/admin/sso`
> 3. **Feature flag descriptions** rendered on `/admin/features`
>
> The agent-authored DRAFT copy on each surface ships as `community_draft`
> until the reviewer signs below. PR #060 is held open at `in-review`
> until each section is checked off.

## Review status

**Status:** awaiting human review
**Reviewer:** _(awaiting Matt Goodrich signature)_
**Review date:** _(awaiting)_
**Canonical role-enum source:** `web/components/admin/roles.tsx` (frontend rendering) ←→ `migrations/sql/20260511000018_rbac_authz.sql` (backend CHECK constraint)
**Canonical feature-flag descriptions:** `internal/featureflag/seed.go` (backend seed)
**Source attribution:** `community_draft` (agent-authored, slice 060)

---

## 1. Role-permission matrix sign-off

The matrix on `/admin/users` is the operator-facing source of truth for
what each RBAC role can do at the coarse level. Slice 035 ships the
enum + the OPA Rego cells; this slice ships the human-readable rendering.

For each role below, the reviewer confirms:

- [ ] **admin** — full configuration + role assignment. The one role
      that can change SSO, issue API keys, toggle flags, and reassign
      other users' roles.
- [ ] **grc_engineer** — authors controls, mappings, policies. Reads
      all evidence. Cannot change role assignments.
- [ ] **control_owner** — operates owned controls. Reads evidence for
      owned controls. Cannot change controls, mappings, or other users'
      roles.
- [ ] **auditor** — read-only external audit access, ABAC-narrowed to
      a specific `audit_period` scope. Can annotate samples. Cannot
      change controls, evidence, scopes, or roles.
- [ ] **viewer** — read-only stakeholder access. Cannot read raw audit
      log, API keys, or SSO config. Cannot change anything.

If the rendered descriptions on the page drift from the backend Rego
cells (slice 035), the page is wrong — update both in the same PR.

(awaiting human review: per-role sign-off on `web/components/admin/roles.tsx`)

---

## 2. SSO callback URL preflight sign-off

`/admin/sso` runs a client-side discovery preflight against an IdP's
`/.well-known/openid-configuration` endpoint before the operator
commits the configuration. The reviewer confirms:

- [ ] The preflight shows the parsed `authorization_endpoint`,
      `token_endpoint`, and `jwks_uri` from the discovery document.
- [ ] The preflight never persists state — it's a pure read in the
      operator's browser. Failure does not block save (operator may
      know their IdP is fine but their browser is offline).
- [ ] The OIDC config form scaffold puts `client_secret` in a
      `type="password"` input with `autoComplete="new-password"` —
      the secret is write-only and the UI never reads it back.
- [ ] The form is currently disabled because the backend save
      endpoint (`POST /v1/admin/sso`) does not ship until slice 060.5.
      Reviewer agrees this is the correct stopgap (vs. shipping a
      half-working form that silently no-ops).

(awaiting human review: per-page sign-off on `web/app/admin/sso/page.tsx`)

---

## 3. Feature flag descriptions sign-off

`/admin/features` reads flag metadata from `/v1/admin/features` (slice
059). Each flag carries a `description` field that is the
operator-facing copy. The descriptions live in
`internal/featureflag/seed.go`; this UI surfaces them verbatim.

The reviewer confirms:

- [ ] Disabling copy on the confirmation modal: _"Routes gated by this
      flag will return 404. Existing data is preserved; re-enable any
      time."_ This is the agent-authored slice 060 copy; reviewer
      confirms it matches the slice 059 contract.
- [ ] Enabling copy on the confirmation modal: _"Routes gated by this
      flag will return live data again. Re-evaluation may take a few
      seconds for cached queries to drop."_ Reviewer confirms this
      matches the cache-middleware behavior shipped by slice 059.
- [ ] No flag is auto-flipped by the UI. Every state change requires
      the modal click. (P0 anti-criterion.)

(awaiting human review: per-page sign-off on `web/app/admin/features/page.tsx`)

---

## Reviewer signature

By signing below, the reviewer confirms the role-permission matrix
matches the slice 035 backend, the SSO preflight UX is operator-safe,
and the feature-flag descriptions match the slice 059 backend.

- **Reviewer name:** (signature pending)
- **Reviewer email:** matt@mattgoodrich.com
- **Sign-off date:** (signature pending)
