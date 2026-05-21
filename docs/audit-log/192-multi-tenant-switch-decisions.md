# Slice 192 — Multi-tenant switch + frontend tenant switcher: decisions log

Slice 192 is the **final** auth-substrate-v2 spine slot. It closes
the vCISO success criterion preserved in slice 141 (PARKED) via a
small frontend feature on top of the slice-188 token-exchange
primitive: a persistent header dropdown that lets an operator
switch between tenants without re-authenticating, plus a login
picker for ≥2 tenants and a backend `GET /v1/me/tenants` directory.

The four engineer decisions enumerated in the slice spec are
resolved below. Confidence is annotated per decision.

---

## D1 — `/v1/me/tenants` cache duration

**Decision:** 60 seconds.

**Rationale:**

- The cache duration balances three forces:
  - **Membership-removed UX latency (P0-192-7):** if an admin
    revokes a user's tenant access, the user sees the banner +
    eviction signal on the NEXT periodic fetch. 60s is "noticeable
    within a minute" — acceptable for a security signal that's
    already eventually-consistent (P0-192-8).
  - **Network cost:** 60s on every authenticated tab in the
    application is one bounded `SELECT id, name FROM tenants WHERE
id = ANY($1)` per minute per active tab. At an operator with
    5 active tabs over an 8-hour day, that's 2,400 lookups — each
    a single PK lookup. Trivial vs. the actual request volume of
    a working session.
  - **Background tabs:** the frontend pauses re-fetch when the
    tab is backgrounded (`document.hidden`) and fires an
    immediate fetch when the tab comes back to the foreground.
    This keeps the network cost honest on tab-heavy workflows
    (auditors with 6+ tabs open) while still surfacing eviction
    promptly when the operator engages.
- The cache is FRONTEND-ONLY. The backend handler does NOT cache —
  every request reads the verified JWT claim's
  `atlas:available_tenants[]` and runs a fresh PK-bounded SELECT
  against `tenants`. The cache lives in the
  `<TenantSwitcher>` component's `setInterval`. Refresh-token
  flows (when v3 lands) will tighten the eviction signal further;
  for v2 this cadence is honest about what OAuth eviction allows.

**Confidence:** HIGH. The 60s default matches the recommendation
in the slice spec and is consistent with the OAuth eventual-
eviction semantics documented in P0-192-8.

**Revisit:** when refresh-token grants land (v3 deferred per slice
188 P0-188-6) — refresh-driven eviction could collapse the
cadence to seconds without DB cost.

---

## D2 — Membership-removed UX shape

**Decision:** Inline banner above the dropdown chrome, with a
default-action button labelled "Switch tenant" that picks the
first available alternative; a secondary "Sign out" action is
NOT included in v0 (operators have the persistent header's "Sign
out" button already; doubling it on the banner would be noise).

**Rationale:**

- The banner copy is short and operational: "Your access to the
  current tenant was removed. Switch to another tenant or sign
  out." Not apologetic — P0-192-8 names the OAuth eventual-
  eviction contract as honest, not a bug. The copy mirrors that
  posture.
- The default action is "switch to first available" rather than
  "let the user pick" because the operator just lost access; the
  fastest path back to a usable state is to land on SOME tenant
  they can still use. The dropdown remains visible for the
  operator to pick a different tenant if they prefer.
- The banner sits IN the persistent header (next to the dropdown,
  not on a separate page) so it survives navigation. If the
  operator navigates while the banner is showing, it follows
  them.
- Eviction-is-eventual is documented in the operator-facing
  `docs-site/docs/admin/tenant-membership.md` (P0-192-8). The
  banner is the in-product surface; the doc is the long-form
  explanation for operators who reach the admin runbook.

**Confidence:** MEDIUM-HIGH. The default-action shape is
defensible but easily revisited (e.g., adding a Sign Out button
on the banner is a one-line change if users want it). The copy
is the more durable decision.

**Revisit:** after vCISO usage patterns surface (probably 6
months post-merge).

---

## D3 — Login picker `prompt=tenant` flag default

**Decision:** Only-when-≥2. The picker page (`/oauth/select-tenant`)
includes a defensive single-tenant redirect — if the operator
lands there with a 1-element tenant list, they are immediately
sent to `/dashboard`. The frontend OAuth client (slice 189's
`oauth-client.ts`) is NOT modified to send `prompt=tenant` on
every login; instead, the picker is reachable via direct link OR
via a post-login redirect when the BFF detects ≥2 tenants.

**Rationale:**

- The spec offered two options: "always send `prompt=tenant`" (so
  the backend `/oauth/authorize` flow always knows) vs
  "only-when-≥2" (so single-tenant users skip the picker).
- Always-sending would require either:
  (a) extending slice 189's `/oauth/authorize` handler to
  recognise the new query param, which violates P0-192-6 (no
  changes to slice 188's token-exchange handler — by symmetry we
  treat the authorize handler the same way during the closing
  slice), OR
  (b) extending the OAuth code-issuance flow to capture a tenant
  choice — substantially more work + outside the slice scope.
- Only-when-≥2 takes the lighter touch: the existing OAuth flow
  works unchanged for single-tenant operators; the picker page
  exists as a destination the frontend can route to AFTER login
  completion when it detects ≥2 tenants in the JWT. The
  one-tenant case never sees the picker (defensive redirect
  guarantees).
- The picker is also reachable as a direct link
  (`/oauth/select-tenant`) — useful for testing + for a future
  "switch via URL" capability without touching the OAuth handler.

**Confidence:** HIGH. Decoupling the picker from the OAuth
authorize flow keeps the spine closure surgical. If a future
slice wants a richer multi-tenant `/oauth/authorize` flow, it can
add `prompt=tenant` then — the picker page already exists to
serve it.

**Revisit:** when the OIDC-first-install bootstrap branch lands
(see spillover slice below). Bootstrap may want to short-circuit
to the picker on the first login for an operator who's a member
of multiple tenants at creation time.

---

## D4 — Slice 141 closure mechanism

**Decision:** The slice 141 row in `docs/issues/_STATUS.md` flips
from `not-ready` (PARKED) to a NEW status string
`merged-via-spine-completion`. This status string is non-standard
— the existing canonical states are `not-ready`, `ready`,
`in-progress`, `in-review`, `merged`, `superseded`.
`merged-via-spine-completion` is documented HERE as the explicit
explanation: slice 141's original spec did NOT ship verbatim;
its substance moved into slices 187-191 (the auth-substrate-v2
spine) and 192 (the frontend wiring). The new status string
records the historical fact.

**Rationale for the new status string:**

- Using `merged` would imply the original spec's code shipped
  (the schema rewrite + `user_tenants` mapping table + `atlas_auth`
  role + `session_tenant_switches` audit table). It did NOT.
  That code never landed because OQ #21 Reading D (2026-05-20)
  resolved the underlying ambiguity at the canvas level rather
  than the slice level.
- Using `superseded` would imply slice 141's GOAL was abandoned.
  It was NOT — the vCISO success criterion is preserved and IS
  fulfilled by the spine.
- Using `merged-via-spine-completion` makes the historical
  trajectory explicit: "this slice's intent shipped, but via the
  spine rather than via the original spec." Future maintainers
  reading `_STATUS.md` will see the status + the notes column
  pointing at slices 187-192 + this decisions log.
- This is the first use of the new status string. If a future
  slice has a similar trajectory (intent fulfilled via a
  different code path), the precedent is documented + reusable.

**Rationale for atomicity (P0-192-11):**

- The status flip + the slice 192 merge happen in the same PR.
  This means: at any point in `main`'s history, either
  - slice 141 is `not-ready` AND slice 192 is unmerged, OR
  - slice 141 is `merged-via-spine-completion` AND slice 192 is
    merged.
- No window where slice 192 is merged but slice 141's row says
  `not-ready` (would confuse the queue). No window where slice
  141 is flipped but slice 192 hasn't shipped (would lie about
  the closure).

**Slices 142, 143, 144 re-evaluation (AC-16):**

- **Slice 142** (super_admins schema + management UI): GATED ON 141. The slice 141 closure unblocks 142 in principle, but
  142's spec assumes a `super_admins` table that this slice
  192's bootstrap branch did NOT create (the slice does not
  add a super_admins table — see spillover note below).
  Slice 142 status remains `not-ready` with a re-scope note in
  the row to acknowledge the spine-completion path. A future
  maintainer picking up 142 should rewrite the slice spec to
  build atop the OAuth AS rather than the original slice 141
  data model.
- **Slice 143** (create-tenant flow): GATED ON 142. Stays
  `not-ready`.
- **Slice 144** (rename-tenant flow): GATED ON 141. The slice
  141 closure unblocks 144 in principle; the rename surface is
  independent of the super_admin question. Status moves from
  `not-ready` to `ready` with a re-scope note pointing to the
  spine architecture for the auth/tenant layer.

**Confidence:** HIGH. The status string is novel but
deliberately greppable; the rationale is documented in two
places (`_STATUS.md` notes column + this decisions log).

**Revisit:** when a maintainer picks up slice 142, 143, or 144 —
they will read the spec, find it pointing at the old data model,
and need to update the spec to build on the OAuth AS. The first
such update will set the canonical pattern for "post-spine"
slice re-scopes.

---

## Out-of-slice surface (spillover candidates)

The slice 192 spec carries a "spillover-as-slice" amendment:
when an out-of-scope finding surfaces during build, it is filed
as a new slice rather than blocking the merge. The following
items surfaced:

### 1. OIDC-first-install bootstrap branch

**Surfaced during slice 192.** Slice 192 spec AC-11/AC-12
described a bootstrap branch in the OIDC callback handler:
"on first user creation, if `count(*) FROM tenants == 0`,
create a default tenant + grant super_admin + tenant_admin".

**Resolution:** the slice DOES NOT modify
`internal/api/auth/http.go`'s OIDC callback to add this branch.
The current OIDC login flow requires a `tenant_id` query param
to enter, which presupposes a tenant exists. For a true
first-install where no tenants exist, the operator currently
uses the slice 073 install-state flow to obtain a bootstrap
admin token + create the first tenant out-of-band. Slice 192's
frontend wiring works correctly once at least one tenant
exists.

A proper OIDC-first-install bootstrap branch requires:

- A path the operator can hit BEFORE any tenant exists.
- An atomic INSERT to create the tenant + the user_roles row
  (matches slice 141 P0-ELEVATE-2 race serialization).
- A schema-level guard against concurrent first-logins (slice
  141 used a partial unique index on `tenants.is_bootstrap_tenant`;
  that's a migration we do NOT add in slice 192).
- A super_admin grant mechanism (slice 141 stubbed a
  `super_admins` table; slice 192 does not).

This is non-trivial — slice 191's precedent (D6 partial cutover

- spillover) is the right shape. **File spillover slice 198:
  OIDC-first-install bootstrap branch** with these requirements.

### 2. super_admins schema + management UI

**Surfaced during slice 192.** Slice 141's spec stubbed a
`super_admins(idp_issuer, idp_subject, granted_at, granted_by)`
table for the bootstrap-grant flow. Slice 192 does NOT create
this table — the JWT's `atlas:super_admin` claim defaults to
`false` and the OAuth AS user resolver does not elevate it.

The slice 142 spec covers the full super_admins table + the
management UI. With the OAuth AS commitment, slice 142's design
must change: super_admin is now an OAuth claim, not a session
attribute. **Slice 142 stays `not-ready` with a re-scope note in
`_STATUS.md`.**

### 3. Cross-tab BroadcastChannel coordination for tenant-switch

**Surfaced during slice 192.** The current implementation: each
tab carries its own atlas_jwt cookie; switching tenant in tab A
does NOT update tab B until tab B's next request. The slice
spec explicitly carves this out as v2.x+ work.

**File spillover slice 199: Cross-tab BroadcastChannel for
tenant-switch sync** for the future. Not blocking.

---

## Verdict

Slice 192 is a tightly-scoped frontend slice on top of an
already-shipped backend (slices 187-191). The four decisions
above are well-grounded; the spillover items make the
boundary between "ship now" and "follow-up" explicit.

The vCISO success criterion from slice 141 — "switch tenants
mid-session without re-authentication" — IS fulfilled by this
slice in combination with the spine. An operator with ≥2
tenants in their JWT's `atlas:available_tenants[]` claim can
click the persistent header dropdown, pick a different tenant,
and the page re-renders against the new tenant scope WITHOUT
re-authenticating. That is the binary success criterion.
