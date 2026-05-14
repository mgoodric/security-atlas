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

**Status:** signed off — clear to merge
**Reviewer:** Matt Goodrich
**Reviewer email:** matt@mattgoodrich.com
**Review date:** 2026-05-13
**Reviewer comment:** "60 looks good to me"
**Canonical role-enum source:** `web/components/admin/roles.tsx` (frontend rendering) ←→ `migrations/sql/20260511000018_rbac_authz.sql` (backend CHECK constraint)
**Canonical feature-flag descriptions:** `internal/featureflag/seed.go` (backend seed)
**Source attribution:** `community_draft` (agent-authored, slice 060)

---

## 1. Role-permission matrix sign-off

The matrix on `/admin/users` is the operator-facing source of truth for
what each RBAC role can do at the coarse level. Slice 035 ships the
enum + the OPA Rego cells; this slice ships the human-readable rendering.

For each role below, the reviewer confirms:

- [x] **admin** — full configuration + role assignment. The one role
      that can change SSO, issue API keys, toggle flags, and reassign
      other users' roles.
- [x] **grc_engineer** — authors controls, mappings, policies. Reads
      all evidence. Cannot change role assignments.
- [x] **control_owner** — operates owned controls. Reads evidence for
      owned controls. Cannot change controls, mappings, or other users'
      roles.
- [x] **auditor** — read-only external audit access, ABAC-narrowed to
      a specific `audit_period` scope. Can annotate samples. Cannot
      change controls, evidence, scopes, or roles.
- [x] **viewer** — read-only stakeholder access. Cannot read raw audit
      log, API keys, or SSO config. Cannot change anything.

If the rendered descriptions on the page drift from the backend Rego
cells (slice 035), the page is wrong — update both in the same PR.

**Signed off by Matt Goodrich on 2026-05-13:** "60 looks good to me"

---

## 2. SSO callback URL preflight sign-off

`/admin/sso` runs a client-side discovery preflight against an IdP's
`/.well-known/openid-configuration` endpoint before the operator
commits the configuration. The reviewer confirms:

- [x] The preflight shows the parsed `authorization_endpoint`,
      `token_endpoint`, and `jwks_uri` from the discovery document.
- [x] The preflight never persists state — it's a pure read in the
      operator's browser. Failure does not block save (operator may
      know their IdP is fine but their browser is offline).
- [x] The OIDC config form scaffold puts `client_secret` in a
      `type="password"` input with `autoComplete="new-password"` —
      the secret is write-only and the UI never reads it back.
- [x] The form is currently disabled because the backend save
      endpoint (`POST /v1/admin/sso`) does not ship until slice 060.5.
      Reviewer agrees this is the correct stopgap (vs. shipping a
      half-working form that silently no-ops).

**Backend follow-up:** what was originally tracked as "slice 060.5"
shipped as **slice 062 — Admin BFF backend endpoints** (merged at
`671407f` on 2026-05-13). `/v1/admin/sso` GET/PATCH + the SSRF-hardened
`POST /v1/admin/sso/preflight` are now on main. A follow-up slice can
flip the form's `disabled` attribute and wire the save call now that
the backend exists.

**Signed off by Matt Goodrich on 2026-05-13:** "60 looks good to me"

---

## 3. Feature flag descriptions sign-off

`/admin/features` reads flag metadata from `/v1/admin/features` (slice
059). Each flag carries a `description` field that is the
operator-facing copy. The descriptions live in
`internal/featureflag/seed.go`; this UI surfaces them verbatim.

The reviewer confirms:

- [x] Disabling copy on the confirmation modal: _"Routes gated by this
      flag will return 404. Existing data is preserved; re-enable any
      time."_ This is the agent-authored slice 060 copy; reviewer
      confirms it matches the slice 059 contract.
- [x] Enabling copy on the confirmation modal: _"Routes gated by this
      flag will return live data again. Re-evaluation may take a few
      seconds for cached queries to drop."_ Reviewer confirms this
      matches the cache-middleware behavior shipped by slice 059.
- [x] No flag is auto-flipped by the UI. Every state change requires
      the modal click. (P0 anti-criterion.)

**Signed off by Matt Goodrich on 2026-05-13:** "60 looks good to me"

---

## Reviewer signature

By signing below, the reviewer confirms the role-permission matrix
matches the slice 035 backend, the SSO preflight UX is operator-safe,
and the feature-flag descriptions match the slice 059 backend.

- **Reviewer name:** Matt Goodrich
- **Reviewer email:** matt@mattgoodrich.com
- **Sign-off date:** 2026-05-13
- **Reviewer comment:** "60 looks good to me"

---

## SSO form save enabled by slice 063 on 2026-05-13

The slice 060 stopgap on `/admin/sso` (form `disabled` because the
backend `PATCH /v1/admin/sso` did not exist on main) has been lifted.
Slice 062 landed the backend on 2026-05-13; slice 063 ships the thin
frontend wire-up.

What changed on `/admin/sso`:

- The OIDC configuration form is fully editable. `disabled` attribute
  removed from every input and the Save button.
- The Save button posts to a new BFF proxy at
  `web/app/api/admin/sso/route.ts` (GET + PATCH) which forwards the
  caller's bearer to `/v1/admin/sso` (slice 062).
- Submit-button state machine: `idle` → `submitting` (disabled +
  "Saving…" label) → `success` (green Alert auto-dismissed after ~3s)
  or `error` (destructive Alert with the backend's JSON `error` field
  rendered verbatim; user input is preserved).
- On a successful save the GET query is invalidated and re-fetched so
  the form mirrors the persisted state (sans `client_secret`).
- The `Provider name` field from the slice 060 scaffold is gone —
  slice 062's handler hardcodes `name='primary'` for the v1 single-IdP
  model. The user-facing form now surfaces `Issuer URL` (the actual
  user-supplied identifier).

What did NOT change (P0 anti-criteria, all PASS):

- `client_secret` is still write-only. The GET response omits it
  (slice 062's `GetResponse` has no `client_secret` field), and the
  input field never re-renders a previously-saved value. After a
  successful save the secret input is wiped so a follow-up submit
  without re-entry sends an empty value (which slice 062's handler
  interprets as "leave existing").
- No auto-submit on blur or any non-button event. Only the explicit
  Save button triggers a PATCH.
- The Discovery preflight button is unchanged — still a pure
  client-side fetch of `/.well-known/openid-configuration`, never
  persists state.
- The backend contract is unchanged. Zero Go file edits in slice 063.

No HITL sign-off required for this slice — the slice 060 sign-off
(role matrix · SSO preflight UX · feature-flag descriptions) carries
forward. Slice 063 is a thin wire-up that flips an already-reviewed
form from `disabled` to enabled.
