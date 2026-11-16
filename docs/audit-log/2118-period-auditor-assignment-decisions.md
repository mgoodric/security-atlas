# Issue #2118 — Auditor assignment for audit periods

An admin assigns and removes the auditors of an audit period, over the
slice-025 `auditor_assignments` store. The period page gets an Auditors card.

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The bug found during the work (D5) was caught by an integration check against
the real roles resolver; it predates this ticket and is #2239.

## D1 — "Audit lead" is admin; no new role

**Decision.** All three routes are admin only: `authz.IsTenantAdmin` (a
platform admin, or the `admin` role in the tenant).

**Why.** The ticket says "audit-lead or admin". No audit-lead role exists in
the rego, the handlers or the `user_roles` CHECK; the five canonical roles are
the universe (canvas §9.5). Matt decided in #2291 that audit-period changes
stay admin-only. Inventing a role would need a migration, a rego file and an
ADR. The dispatcher confirmed: audit-lead maps to admin.

The GET is admin only too. OPA admits an auditor's read of the
`audit-periods` resource, so without the handler check one auditor could list
the period's other auditors and the tenant's auditor roster.

## D2 — The picker's list comes from the platform, not `/v1/admin/users`

**Decision.** `GET /v1/audit-periods/{id}/auditors` returns `candidates`: the
tenant's active users holding the auditor role and not yet on the period, with
id, display name and email.

**Why.** `/v1/admin/users` answers a super-admin with the cross-tenant list,
so a picker built on it could offer another tenant's users. The candidate
query is tenant-scoped in SQL (and under RLS), and carries only the name and
email `/v1/admin/users` already shows an admin.

## D3 — Server-side assignee check

**Decision.** Assign refuses (400) a user who is not an active user of the
caller's tenant holding the auditor role, inside the same transaction as the
insert. A period outside the tenant is 404.

**Why.** The composite FK already blocks a cross-tenant period, but nothing
stopped a grant to a viewer, a disabled user, or a uuid from another tenant.
An assignment to a non-auditor would be inert (the attrs resolver runs only
for the auditor role) but would still read as a grant in the log.

## D4 — Logged in the period's own audit log; only real changes log

**Decision.** `audit_period_audit_log` gains `auditor_assigned` and
`auditor_removed` (migration `20261207000000`). The row carries the actor
(the signed-in person's subject, else the credential id) and
`detail.user_id`. A repeat assign answers 200 and a repeat remove 404; neither
writes a row.

**Why.** It is the period's lifecycle log, already in the unified audit log
(slice 124), so the change is visible there with no new table. Logging
no-op repeats would make the log say a grant happened twice.

The actor is the JWT subject rather than `cred.ID`, which for a JWT is
`jwt:<jti>` and is `jwt:` when the token has no jti.

## D5 — Real-login auditors depend on #2239

**Decision.** Not fixed here. The Playwright spec signs the auditor in with a
bare-uuid JWT from `/v1/test/issue-jwt`, the `settings-theme.spec.ts` pattern.

**Why.** `authz.DBRolesResolver` looks up `user_roles` by the raw subject.
`/login`, PKCE and the device grant mint `user:<uuid>`; `user_roles` holds the
bare uuid. So a real-login auditor gets no auditor role in OPA and is refused
`/v1/me/audit-periods` (checked against real Postgres: `user:<uuid>` → 403,
bare uuid → 200). The fix changes every role for every `user:` session, so
Matt wants it rolled out with a before/after dry run (#2239). Until #2239
lands, an assigned auditor reaches the period only on a bare-uuid session.

## D6 — Removal takes effect on the next request

**Decision.** No cache. The OPA attrs resolver reads `auditor_assignments` per
request, and `/v1/me/audit-period(s)` reads them per call. The integration
test removes a grant and then asserts the auditor's next note write is 403 and
their period list is empty.
