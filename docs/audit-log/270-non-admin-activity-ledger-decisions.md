# Slice 270 — Non-admin `/activity` ledger surface — decisions log

**Slice spec:** [`docs/issues/270-non-admin-activity-ledger-surface.md`](../issues/270-non-admin-activity-ledger-surface.md)
**Branch:** `backend/270-non-admin-activity-ledger`
**Status:** in-review

This log captures the JUDGMENT calls the implementing agent made while
building the slice. The slice spec recorded one decision slot (D1) to be
settled by the engineer; this file is the record. Six additional
engineer-side calls (D2–D7) were made during implementation and are
recorded here for traceability.

---

## D1 — Extend slice-124 endpoint vs new endpoint: **fused A/B — new route, shared aggregator** (deviation from spec recommendation)

The spec offered two pure choices: (A) extend slice 124's admin endpoint
by widening its OPA admit and adding a row-visibility filter, or (B)
ship a brand-new `/v1/activity/unified` endpoint with its own
projection. The implementer picked a third option that combines the
best of both.

**Decision:** ship a NEW route `GET /v1/activity/unified` that lives in
the same `internal/api/adminauditlog` package as slice 124, calls the
SAME underlying aggregator (`unifiedlog.Query`), and shares the SAME
SQL — but the SQL is extended with two new optional parameters
(`caller_is_privileged BOOLEAN` + `caller_user_id TEXT`) that gate one
extra WHERE predicate. The slice 124 endpoint passes
`caller_is_privileged=true` and short-circuits the predicate; the new
slice 270 endpoint passes `caller_is_privileged=false` and the
predicate restricts the result set.

**Rationale for the fusion:**

1. **Don't disturb slice 124's contract.** Pure Option A widens the
   `/v1/admin/audit-log/unified` OPA admit-set from {admin, auditor,
   grc_engineer} to all-signed-in-tenant-members. That breaks slice
   124's OPA matrix test (TestSlice124_UnifiedAuditLogAccess pins
   viewer + control_owner at deny), breaks the admin-only semantic of
   the `/v1/admin/*` prefix, and ripples to slice 125's
   `canReachAuditLog` route guard which is keyed on the privileged
   three-role set.
2. **Don't duplicate the aggregator.** Pure Option B duplicates the
   ~250-line aggregator + cursor logic + meta-audit + sink emit, all
   of which are load-bearing slice 124 / slice 126 / slice 135
   infrastructure. A duplication doubles the maintenance burden when
   slice 180-style projection extensions land in the future.
3. **The fused approach gets surface uniformity AND endpoint isolation.**
   One SQL query, one Go aggregator function, two HTTP handlers with
   different OPA admits. The handlers differ in: (a) which role probe
   they perform, (b) what value they pass for `caller_is_privileged`,
   and (c) which OPA `resource.type` symbol they emit
   (`audit-log-unified` vs `activity-unified`).
4. **Future-proofs the slice-180 `subject_module` lever.** When the
   privacy sibling module lands and tags writes with
   `subject_module='privacy'`, the same WHERE clause extends with one
   more predicate without forking the handler again.

**P0-A5 (filter-combination authz independence) honored:** the
non-admin WHERE filter is applied at the SQL layer keyed on the
`caller_is_privileged` Go-side bind parameter, derived from the
caller's credential + user_roles probe — NOT from any URL filter the
caller controls. A non-admin who passes `?actor=<some-admin-uuid>`
still gets only their own me-rows because the `actor_id =
caller_user_id` predicate is conjunctive with the user-controlled
filter, not disjunctive.

**Trade-off accepted:** two HTTP routes share one SQL query. If the
slice-180 lever or a future visibility predicate change the WHERE
clause shape, BOTH endpoints' wire shapes shift uniformly — which is
the desired behavior (surface uniformity).

**OPA resource type:** the new route lives under `/v1/activity/...`,
which `internal/authz.resourceFromPath` derives as
`resource.type = "activity"` — the same type slice 156 added for the
`/v1/activity` dashboard activity-feed panel. The existing admit
covers all five tenant-member roles (admin + auditor + grc_engineer +
viewer + control_owner) via wildcard-read rules (admin / grc_engineer)
and explicit `"activity"` enumeration (auditor / viewer /
control_owner). Slice 270 therefore adds NO new OPA resource-type
symbol — the new route mounts under the existing admit and is verified
by `TestSlice270_ActivityUnifiedOPAAdmit` which exercises the full
five-role matrix on `resource.type = "activity"` with
`request.path = "/v1/activity/unified"`.

---

## D2 — Public-vs-admin discrimination: **action-allowlist on the `me` kind only** (deviation from spec text)

The spec text says: "the `subject_module` column (slice 180) is the
key discriminator: rows with `subject_module='public'` are
tenant-visible to any member; rows with `subject_module='admin'`
(super_admin grants, framework_scope edits) are admin-only."

**This was a spec-author miscall.** Slice 180 added
`subject_module TEXT NOT NULL DEFAULT 'core'` for FUTURE module
separation (`core` vs `privacy`), not for visibility tagging. Every
audit-log row across all nine tables currently carries
`subject_module='core'` — the spec's `'public'`/`'admin'` values do
not exist in the schema. Adding them now would require a migration,
which P0-A3 explicitly forbids.

**The actual discriminator that exists:**

- **Eight of the nine kinds** (decision, evidence, exception, sample,
  audit_period, aggregation_rule, walkthrough, plus feature_flag) are
  state-change events on tenant program artefacts. Of these,
  `feature_flag` flips are admin-only program-configuration events; the
  other seven reflect day-to-day operator work and are tenant-public.
- **The `me` kind** is the per-actor self-audit table.
  `me_audit_log.user_id` IS the actor; `me_audit_log.action` enumerates
  ~22 distinct values today (profile.update, *_export queries, plus
  admin-only actions like super_admin_grant, tenant_create,
  bootstrap_first_install, tenant_rename, demo_seed_*). For a non-admin
  caller, the right cut is "show only rows where `user_id = caller`."
  The admin-only actions are filtered out automatically because a
  non-admin never took them.

**Rationale for the cut:**

1. **No schema change.** Honors P0-A3.
2. **Honors the spec's intent.** The threat-model row 'I — Information
   disclosure' calls out super_admin_grant and framework_scope edits
   as the admin-only-row class. super_admin_grant lands in
   me_audit_log; framework_scope edits land in… actually NOWHERE in
   the unified aggregator today (no framework_scope_audit_log table,
   slice 005 / 049 audit-trail integration is a v2+ slice). So the
   admin-only-row class today is exactly: feature_flag flips +
   me-rows whose action is in the admin-only set.
3. **Self-actor own-action visibility is the right behavior.** If a
   non-admin somehow took a super_admin_grant action (impossible at v1
   — only admins can grant; the row exists only if the caller WAS the
   admin who took it), they should see their own audit trail. The
   cross-actor isolation test (AC-6) seeds an admin actor + a separate
   non-admin actor, then queries as the non-admin and asserts the
   admin's rows do not appear.

**Implementation:** the SQL adds one WHERE predicate (gated on a Go-
side `caller_is_privileged` bind parameter):

```sql
AND (
    sqlc.arg('caller_is_privileged')::boolean = true
    OR (
        unified.kind <> 'feature_flag'
        AND (unified.kind <> 'me' OR unified.actor_id = sqlc.arg('caller_user_id')::text)
    )
)
```

When `caller_is_privileged = true` the predicate short-circuits and
the result set is unchanged from slice 124. When false, feature_flag
rows are hidden and me-rows are restricted to the caller's own.

---

## D3 — Reuse slice 125's `<UnifiedAuditTable>` component verbatim: **yes, by composing the existing page** (no change from spec D3)

The spec says reuse the slice 125 component verbatim. Slice 125's
component is the `AuditLogPageClient` island; it owns its own URL
state, BFF route hard-coded to `/api/audit-log/unified`, and route
guards. Reusing it verbatim from `/activity` means the URL state would
sync to `/audit-log?...` instead of `/activity?...` (router.replace
target), breaking the AC-9 URL-binding contract for the new route.

**The right interpretation of "verbatim reuse":** lift the row-rendering
table + filter chips + export bar into composable pieces, parameterise
the BFF route + URL base, and consume from both pages.

But this is more refactor than the slice wants. The slice's bar is
"minimal backend admit extension + frontend page." The pragmatic call:

- **Frontend:** create a thin client island `ActivityPageClient` in
  `web/app/(authed)/activity/page-client.tsx` that has the SAME shape
  as `AuditLogPageClient` (filter bar, kind chips, table, pagination)
  but with the BFF URL parameterised to `/api/activity` and the
  router.replace target parameterised to `/activity`. Implementation
  duplicates ~80% of `AuditLogPageClient`; the alternative is a
  slice-126-flavored refactor that is out of scope for slice 270.
- **Tracking:** D3-follow-up flagged in this log — a follow-on slice
  can extract the shared shell into a `<UnifiedAuditTable>` component
  in `web/components/audit-log/` consumed by both pages. The
  duplication is contained: when the slice 125 page changes, the
  slice 270 page WILL drift, and a maintainer must update both.

**Mitigation for D3 drift:** the duplicated client island carries a
clear header comment pointing at slice 270's decisions log + the
slice 125 page-client.tsx as the canonical source for any UX change.
The duplication is small enough that it surfaces in PR review when
either page changes.

---

## D4 — Sidebar entry rendering: **render for all authed users, no role gate** (spec AC-8 compliance)

Slice 186 introduced the role-conditional sidebar pattern for `/admin`.
The slice 270 spec says the `/activity` entry "renders for all authed
users, not gated, but the pattern's still relevant for symmetry."

**Decision:** the `/activity` entry is added to `NAV_BASE` (the
always-rendered set) in `web/components/shell/sidebar.tsx`. No role
probe needed; every authenticated user lands on a page they have access
to (the backend OPA admit-set widens accordingly under D1).

**Placement:** between `/dashboards/metrics` and `/controls` in the
canonical Plans/canvas/12 nav order — it sits alongside the dashboard /
calendar / metrics cross-business "at-a-glance" cluster because that is
the cluster it semantically belongs to (program-pulse surfaces).

---

## D5 — Default filter behavior for `?actor=me` query string: **resolve `me` server-side, not client-side** (spec D2 implementation detail)

The spec D2 says navigating from the dashboard footer link's `?actor=me`
defaults the actor filter to the operator.

**Decision:** the BFF route `/api/activity` resolves the literal string
`me` to the caller's user_id BEFORE forwarding to the platform. The
client island treats `actor=me` as a sentinel that means "current
user." The actor input box shows the literal string `me` for that URL
state (with a helpful label `(your activity)`) but the request body
carries the resolved UUID.

**Rationale:** keeps the resolution off the page client (which would
otherwise need to fetch /api/me synchronously before rendering), keeps
the backend a literal-id-only contract (matches slice 124's actor_filter
semantics), and lets the dashboard footer-link target stay
`/activity?actor=me` (a stable URL the slice 232 unblocking target can
hard-code).

---

## D6 — `feature_flag` admit on `me-actor=me`: **always hide for non-admins** (P0-A2 reinforcement)

Edge case: what if a non-admin somehow flips a feature flag (impossible
today; feature-flag flip is admin-only)? Should they see their own row?

**Decision:** non-admins NEVER see feature_flag rows. Even self-flip
rows. The unconditional kind-exclusion is simpler than a kind-conditional
actor check, and the closed-set assumption (admins flip flags) holds at
v1. If a future feature-flag write surface admits a non-admin (would
require a separate slice + threat-model review), this exclusion would
need to flip to actor-conditional then.

---

## D7 — Meta-audit action name: **reuse `audit_log_query_unified`** (no migration)

The new `/v1/activity/unified` handler writes one me_audit_log row per
successful query (mirroring slice 124 AC-10). Slice 270 reuses the
same `audit_log_query_unified` action value because the underlying
query IS the same query (slice 124 unified aggregator); the only
difference is the row-visibility WHERE filter, which is part of the
query's parameter shape — not a different operation kind.

**Rationale:** no me_audit_log.action CHECK extension needed (honors
P0-A3). The before-blob distinguishes the activity surface from the
admin surface via a new `surface` field in `metaAuditParams`
(`"admin"` for slice 124, `"activity"` for slice 270). Forensic
review can split by that field.

---

## Verification

- P0-A1 cross-actor isolation: AC-6 integration test
  `TestSlice270_NonAdminCrossActorIsolation` in
  `internal/api/adminauditlog/unified_integration_test.go`.
- P0-A2 admin-only-row exclusion: covered by AC-6 (feature_flag rows +
  cross-actor me-rows both verified hidden).
- P0-A3 no migration: `git diff main -- migrations/` is empty (verified
  pre-PR).
- P0-A4 RLS enforced: the unified aggregator continues to run under
  `atlas_app` + tenancy GUC; the new WHERE predicate is additive on top
  of RLS, not a bypass.
- P0-A5 filter-combination independence: AC-6 also asserts that a
  non-admin passing `?actor=<admin-uuid>` does NOT widen visibility (the
  Go-side `caller_user_id` bind parameter is independent of the URL
  filter).
- AC-7 cross-tenant isolation:
  `TestSlice270_NonAdminCrossTenantIsolation` (RLS proof).
- AC-9 Playwright e2e: stubs in `web/e2e/activity.spec.ts` matching the
  slice-125 quarantine convention.
- AC-10 OPA admit: `TestSlice270_ActivityUnifiedOPAAdmit` in
  `internal/authz/slice270_test.go` confirms viewer / control_owner /
  grc_engineer / auditor / admin all receive `allow=true` on
  `resource.type = "activity"` with `request.path = "/v1/activity/unified"`.
