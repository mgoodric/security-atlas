# Slice 130 — `/api/admin/me` role enumeration · decisions log

Filed as part of the slice-130 implementation (branch
`backend/130-admin-me-role-enum`). Every JUDGMENT call the engineer made
while building this slice is recorded here so the post-merge maintainer
iteration is traceable.

The product runtime AI-assist boundary is constitutional and is NOT
mutated by anything in this log; this log is about how the slice was
BUILT, not how the shipped product behaves (see `CLAUDE.md`
"AI-assist boundary (hard)").

---

## D1 — BFF wire shape: extend `/api/admin/me` with `roles: string[]` (option a)

**Decision.** Extend the existing `GET /api/admin/me` BFF route to
additionally return `roles: string[]` alongside the existing
`is_admin: boolean`. No new BFF endpoint is added.

**Trade-off (the JUDGMENT call the slice doc deferred to the engineer).**

| Option                                          | Pros                                                                                                                                     | Cons                                                                                                               |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| **(a) Extend `/api/admin/me` with `roles: []`** | Single source of truth — caller's identity in one round-trip. Existing `is_admin`-only consumers ignore the new field (additive, P0-A2). | If any consumer asserted strict-shape equality on the response, it'd break — verified via grep, none do.           |
| (b) New endpoint `/api/me/roles`                | Narrower surface; keeps `/api/admin/me` literal-stable.                                                                                  | Two endpoints answering near-identical questions; future drift; layout would need two round-trips for role gating. |

**Chose (a).** Verified consumers of `/api/admin/me` in `web/app/admin/layout.tsx`,
`web/app/audit-log/layout.tsx`, `web/app/(authed)/settings/page.tsx`,
`web/app/(authed)/dashboards/metrics/[id]/page.tsx`,
`web/app/(authed)/board-packs/[id]/page.tsx`, and
`web/lib/api.ts:getSessionMe()` — every reader destructures `is_admin`
only (e.g. `body.is_admin === true`). Adding a `roles` field is purely
additive; no consumer breaks. The single-round-trip property is worth
preserving — the layout route guard is server-rendered on every navigation,
so doubling the BFF round-trip count for option (b) has a small but
non-zero cost the operator pays for nothing.

The maintainer's lean in the slice prompt was option (a), and the consumer
audit confirms it.

---

## D2 — Backend origin: extend `/v1/me` profile (NOT `/v1/admin/credentials`)

**Decision.** The role-list source on the platform is the existing slice-108
`GET /v1/me` profile endpoint, extended to additionally return
`roles: []string` sourced from the `user_roles` table. The BFF
(`/api/admin/me`) proxies through `/v1/me`. The previous BFF upstream
(`/v1/admin/credentials` status-code probe) is replaced.

**Why this diverges from the slice-doc title's "extend `/v1/admin/credentials`":**
`/v1/admin/credentials` is **admin-gated** (`requireAdmin` returns 403 to
auditors and grc_engineers). An auditor caller — exactly the user this slice
unblocks — cannot reach that endpoint to discover their own role list.
Extending `/v1/admin/credentials` therefore cannot satisfy AC-2 in a way
that benefits AC-5 (the route-guard widen).

The slice doc body itself acknowledges this with the parenthetical
"(or new `/v1/me/roles`)" in AC-2. The chosen path uses the existing
slice-108 `/v1/me` (already user-reachable, no role gate beyond authn)
rather than adding a third `/v1/me/roles` sibling. The slice-108 profile
shape already carries `is_admin` — adding `roles` to the same response
keeps the platform's self-introspection surface single.

**Trade-off.**

| Option                                                     | Pros                                                                                                                  | Cons                                                                                                                |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| (i) Extend `/v1/admin/credentials`                         | Smallest BFF change (no upstream URL flip).                                                                           | **Blocked for auditors / grc_engineers (admin-gated).** Does not satisfy the slice's purpose.                       |
| **(ii) Extend `/v1/me` profile with `roles: []` (chosen)** | Auditor-reachable. Reuses slice-108's `inTx` + `tenancy.ApplyTenant` shape. One self-introspection endpoint, not two. | BFF upstream URL changes from `/v1/admin/credentials` to `/v1/me`; tests need adjustment (a one-line fixture diff). |
| (iii) New `/v1/me/roles` sibling                           | Narrowest backend addition.                                                                                           | Two endpoints answering near-identical questions; drift risk; adds a route to httpserver.go for no semantic win.    |

**Chose (ii).** Single self-introspection surface; auditor-reachable;
reuses the proven slice-108 pattern. The BFF upstream flip from
`/v1/admin/credentials` to `/v1/me` is the right architectural move
regardless of this slice — the original slice-060 probe was a hack
(loading the credentials list as a side-effect of asking "am I admin?")
that this slice now corrects.

---

## D3 — Role-source query: share `DBRolesResolver`, do NOT duplicate the SQL

**Decision.** The `/v1/me` profile handler delegates role lookup to a
freshly-injected `*authz.DBRolesResolver` — the exact same resolver the
slice-035 OPA middleware uses to populate `Input.UserRoles`. No new
sqlc query, no new SELECT against `user_roles`.

**Why.** P0-A1 + the slice threat-model STRIDE-S row both say
"Roles come from the backend's `user_roles` table — same table slice 124's
OPA gate reads". The literal way to honor that is to share the resolver,
not duplicate its SELECT. The resolver already runs under
`tenancy.ApplyTenant` so RLS on `user_roles` enforces tenant scoping
(STRIDE-I + P0-A3 cross-tenant isolation).

For the bootstrap-admin / API-key credential path where `cred.UserID` is
not a UUID (synthetic profile branch), the resolver returns
`(nil, nil)` — the handler then sets `roles: []` and `is_admin: cred.IsAdmin`
so an admin API-key carrying `IsAdmin=true` still has the right gate. The
synthetic-profile branch is explicit in the existing code; this slice
only adds the empty-roles default to the wire.

---

## D4 — Frontend route guard: `is_admin || roles.includes("auditor") || roles.includes("grc_engineer")`

**Decision.** `web/app/audit-log/layout.tsx` gate upgrades from
`is_admin === true` to
`is_admin === true || roles.includes("auditor") || roles.includes("grc_engineer")`.
The set of permitted roles matches slice 124's `HasUnifiedAuditLogRole`
SQL (admin OR auditor OR grc_engineer) exactly — a sed-able literal.

**Fail-closed posture (P0-A3).** When the BFF returns no `roles` array
(legacy upstream, BFF error, network blip), the layout treats the array as
`[]` and admits only on `is_admin === true`. Non-admins with a missing
`roles` array see `redirect("/dashboard?error=admin-only")` — identical to
the pre-slice-130 behavior. No silent role inference; no "treat null as
all-roles" footgun.

The guard runs server-side (P0-A4) — the layout is a server component;
no client-side state is consulted.

---

## D5 — `/admin/*` route guard: keep `is_admin`-strict; do NOT widen

**Decision.** `web/app/admin/layout.tsx` is **NOT** changed. The slice-060
admin section continues to gate on `is_admin === true` strictly. Only the
slice-125 `/audit-log` layout is widened.

**Why.** The `/admin/*` section is for tenant administration (SSO, users,
API keys, features) — strict admin-only is the right gate. The slice
doc's AC-5 names `web/app/audit-log/layout.tsx` exclusively as the
upgrade target. An auditor needs access to the audit-log surface but
NOT to the admin surface. Out of scope per slice 130; would be a separate
slice if the maintainer ever decides auditors should see the admin tree.

---

## D6 — Vitest fixture matrix: cover the four meaningful response shapes

**Decision.** The vitest test extends with cases for:

1. Upstream 200 with `{is_admin: true, roles: ["admin", "auditor"]}` — full role list flows through.
2. Upstream 200 with `{is_admin: false, roles: ["auditor"]}` — non-admin auditor case; `is_admin=false`, `roles=["auditor"]`.
3. Upstream 200 with `{is_admin: true}` (legacy shape, no `roles`) — `roles` defaults to `[]` (fail-closed compat).
4. Upstream 401 / 403 / 500 — existing error paths still work; `roles` defaults to `[]` in every error response.

**Why.** P0-A3 fail-closed posture is non-trivial to assert without explicit
test coverage. Without a "legacy upstream shape" test, a future refactor could
flip the default to "permissive" silently. The four cases are the minimal
matrix that catches a `roles ?? []` removal.

---

## D7 — Backend integration test: assert tenant isolation (AC-6)

**Decision.** The integration test seeds two tenants A + B, each with a user
holding a distinct role set; asserts that A's `/v1/me` response carries
ONLY A's roles, and B's response carries ONLY B's roles — no role from the
sibling tenant leaks via either response. The cross-tenant call uses the
slice-108 `testServerForUser` pattern so the bearer + tenancy middleware
chain runs end-to-end (not a unit-level harness; the RLS round-trip is
load-bearing).

**Why.** AC-6 calls out cross-tenant isolation as the integration
contract. The `DBRolesResolver` already runs under `tenancy.ApplyTenant`
but the test guards against an accidental future refactor that forgets
the tenancy wrap (slice 065's exact past defect — RLS bypass when the
resolver ran outside a tx).

---

## D8 — E2E (Playwright): keep shimmed pending the slice-079 quarantine

**Decision.** The Playwright spec `web/e2e/audit-log.spec.ts` already has
an `AC-8b: non-admin signed-in caller is redirected to /dashboard?error=admin-only`
case shimmed in. This slice adds a new `AC-8e: auditor signed-in caller can
reach /audit-log` case alongside it, keeping the same shim-with-expect-true
posture pending the slice-079 broader e2e bring-up. The spec body is
preserved verbatim as a reviewable contract.

**Why.** The slice-079 quarantine convention (slice-094 / 098 / 102 / 119
also shimmed) is the project norm for Playwright until the dev-loop
seeds + auth-cookie bring-up resolves. Adding a real-execution spec
here would diverge from the convention without solving the underlying
quarantine. The seed harness in `web/e2e/seed.ts` does not yet model an
auditor-roled user — adding one is the right path to un-shim, but that
is a `web/e2e/seed.ts` refactor (would touch `fixtures/e2e/audit-log.sql`
to also INSERT a `user_roles` row) that the slice doc did not name. The
shim-with-expect-true contract documents the intent for the un-shim PR.

If this proves to be the friction point that blocks the broader e2e
bring-up, the spillover candidate is a "audit-log seed harness: auditor
user fixture" slice that touches `web/e2e/seed.ts` + `fixtures/e2e/audit-log.sql`

- the spec body in one PR.

---

## Spillovers filed

None. The auditor-user seed-harness expansion (D8) is documented as a
candidate spillover but only files if the maintainer chooses to un-shim
the spec — not part of the slice 130 scope.
