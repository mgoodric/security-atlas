# 479 — Admin user-management UI: decisions log

JUDGMENT slice. The subjective calls are the UX of the assignment flow and
the self-assign affordance. Per the slice workflow, Claude made these calls
with pattern-matched judgment (against the slice-143 tenants page and the
slice-142 super-admins page, the two nearest super-admin-gated admin
surfaces) and recorded them here. This log does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The within-tenant revoke tenant_id gap —
see Decision 5 — was caught at design time during the build, not by a test
failure, so it is a design call, not a defect.)

## Decisions made

### D1. Role selection = checkboxes, not a Select. **Confidence: high**

The repo has no `Select` primitive under `web/components/ui/` (verified:
only alert/badge/button/card/checkbox/dialog/input/table). Roles are
inherently a multi-select (a user can hold several), and the slice-143
tenants page already uses `Checkbox` for its one boolean. A checkbox group
inside a `<fieldset>`/`<legend>` is the accessible, dependency-free shape for
multi-select and matches the existing admin idiom. Options considered:
(a) build a multi-select Select primitive — rejected as over-engineering for
one form (Article VIII anti-abstraction); (b) comma-separated text input —
rejected as un-discoverable and error-prone. Chosen: a labelled checkbox per
canonical role, sourced from the slice-060 `ROLES` constant so the matrix and
the picker can never drift.

### D2. super_admin scope is derived from the response SHAPE, not a separate probe. **Confidence: high**

The 478 backend returns two different list shapes: the super_admin
cross-tenant shape tags every row with a non-empty `tenant_id`; the
tenant-admin within-tenant shape omits it. Rather than add a second
round-trip (e.g. read `super_admin` off `/v1/me` or decode the JWT), the BFF
derives a `cross_tenant` boolean from the rows and the page gates the
cross-tenant controls (the Tenant column + the "Add me to a tenant" button)
on it. This keeps the server as the single source of authority truth
(P0-479-1) — the UI literally cannot show cross-tenant controls unless the
server already returned cross-tenant data. Options considered: (a) probe
`/v1/me` for the `super_admin` claim — rejected as a redundant round-trip and
a second source of truth that could disagree with the list; (b) hardcode by
route — not possible, the same page serves both roles. The one edge: an
EMPTY list derives `cross_tenant=false`, hiding the self-assign button for a
super_admin on an empty platform. This is the safe-by-default direction
(never over-promises authority) and is effectively unreachable in practice
(a super_admin always has at least their own membership row). Logged on the
revisit list.

### D3. Self-assign re-auth = full re-login, NOT token-exchange. **Confidence: medium**

AC-4 requires the UI to explain that a re-auth is needed so the new tenant
appears in the switcher. The existing tenant-switch path
(`/api/auth/switch-tenant`) uses the RFC 8693 token-exchange grant, which
validates the target tenant against the SUBJECT token's
`available_tenants[]`. A tenant just self-assigned is NOT yet in the current
token's `available_tenants` (the claim was minted before the membership
existed), so a token-exchange would be rejected. The honest path is therefore
a full re-login (OIDC sign-out → sign-in), which re-runs the slice-192
membership resolver and mints a fresh token carrying the new tenant. The
re-auth notice links to `/login?from=/admin/users` (the app's canonical
sign-in entry — verified: `web/app/page.tsx` redirects unauth users to
`/login?from=...`; there is no `/oauth/login` route). We do NOT auto-switch
(P0-479-3) and do NOT attempt a silent token-exchange that would fail.
Confidence is medium because the precise re-issue behaviour (does a fresh
local-auth login pick up the synthetic-key membership immediately?) is worth
confirming against a real running stack — see revisit list.

### D4. Mock the BFF in the e2e spec; do NOT seed real cross-tenant rows. **Confidence: high**

Mirrors `admin-tenants.spec.ts` verbatim. The 478 cross-tenant assign writes
platform-global rows under the BYPASSRLS pool — rows the Playwright harness
cannot clean up between specs (and the seed harness, per
`web/e2e/README.md`, applies additive fixtures, not BYPASSRLS writes). The
real DB behaviour is owned by 478's Go integration tests. The e2e spec
therefore mocks `/api/admin/users` (+ `/revoke`) and `/api/me` via
`page.route()` and asserts the UI contract (render, refetch-on-success,
confirm step, re-auth notice, authz-honest hiding, inline 403, a11y labels).
No seed-harness extension is needed, so no spillover is filed.

### D5. Within-tenant revoke uses the session tenant from /api/me as the tenant_id fallback. **Confidence: medium**

The within-tenant list shape carries no `tenant_id` on its rows, but the 478
revoke endpoint requires a `tenant_id` in the body. For a tenant-admin
revoking within their own tenant, the page reads the session tenant from
`/api/me` (which returns `tenant_id`) and uses it as the fallback. If
`/api/me` is unavailable the revoke button is disabled with an inline
explanation rather than firing a malformed request. The cross-tenant shape
uses the row's own `tenant_id` directly. Confidence medium: the within-tenant
revoke path is exercised by unit + the authz-honest e2e case, but not by a
full real-stack within-tenant revoke (the e2e mocks the BFF).

### D6. Copy tone: factual, no superlatives. **Confidence: high**

The alert/dialog copy follows the CLAUDE.md tone discipline — measured,
factual, no banned phrases ("robust"/"leverage"/superlatives). The re-auth
notice explains the WHY (the token predates the membership) rather than just
instructing, so the operator understands the constraint.

## Revisit once in use

1. **Empty-list super_admin edge (D2).** Confirm a super_admin always has a
   self-membership row in practice so the self-assign button is never hidden.
   If a genuinely empty cross-tenant list can occur, switch the scope signal
   to an explicit flag (e.g. a `cross_tenant` field the 478 list could return,
   or the `/v1/me` super_admin claim) so the button shows regardless of row
   count.
2. **Self-assign re-issue behaviour (D3).** Against a real running stack,
   confirm that after self-assign + full re-login the new tenant appears in
   the switcher (the slice-192 resolver picks up the new membership and the
   synthetic local-auth key from 478 D1). If a single re-login is NOT
   sufficient, update the notice copy.
3. **Token-exchange-for-newly-assigned-tenant (D3).** If 478 (or a future
   slice) ever lets the token-exchange grant mint a token for a
   just-assigned tenant without a full re-login, replace the re-login link
   with a one-click switch and drop the sign-out step.
4. **Within-tenant revoke real path (D5).** Verify a tenant-admin revoke
   against a real stack now that the tenant_id fallback comes from
   `/api/me`. Consider whether 478 should accept an omitted tenant_id on the
   within-tenant revoke (defaulting to the session tenant) so the BFF need
   not synthesize it.
5. **Pagination UX.** The list consumes `next_cursor` from 478 but the page
   renders only the first page (no "load more" control yet). Real operators
   with many tenants/users will want pagination controls; deferred as a
   follow-up rather than guessing the interaction now.
6. **Per-role revoke.** AC-3 says "per membership/role"; this slice revokes
   ALL roles for a user-in-tenant (the 478 revoke endpoint deletes all
   user_roles for the pair). If operators need to drop a single role while
   keeping others, that needs either a 478 change (role-scoped revoke) or a
   client-side re-assign of the remaining roles. Recorded as a revisit; the
   current shape matches the 478 contract.
