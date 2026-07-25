# 468 — Server-backed bulk-assign-owner + saved filter-views — decisions log

JUDGMENT slice. Claude made the build-time design calls below (the owner-
assignment data model, the saved-views schema, the authz amplifier shape, the
localStorage→server migration story) and recorded them here; the slice ships
when CI is green (no human sign-off gate). This is the BACKEND completion of
slice 448's frontend shell — see `docs/audit-log/448-bulk-ops-saved-views-decisions.md`
D1/D3/D5 for why the backend was deferred to here.

- detection_tier_actual: integration
- detection_tier_target: integration

A latent bug WAS surfaced + caught DURING the slice at the integration tier
exactly where the spec targets it (the AC-11 amplifier test): the
deliberate-weakening run (commenting the per-item `ControlExistsInTenant`
guard in `assignOwnerInTx`) made `TestBulkAssign_CrossTenant_Amplifier` FAIL
(got 500 FK-violation instead of 404), proving the test bites. The shipped
code keeps the guard; the proof was run locally and reverted, never committed
weakened. No other bug surfaced.

---

## Decisions made

### D1 — Owner data model: an ADDITIVE owner-USER assignment table, NOT extending `owner_role`. **(Confidence: high — THE central JUDGMENT call)**

**The problem.** Slice 448 presumed a single-item assign-owner authz path to
reuse as the bulk path's per-item check. None existed: `controls.owner_role`
(`migrations/sql/20260511000000_init.sql:184`) is a read-only TEXT **role
string** (canvas §2 "who owns it (RACI)"), rendered read-only with no assign
affordance; the only control-mutation endpoint was the whole-bundle replace.

**Alternatives weighed:**

| Option                                                        | Shape                                     | Why not                                                                                                                                                                                                                                                                                                                       |
| ------------------------------------------------------------- | ----------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(a) Overwrite `owner_role`** with a user id                 | reuse the existing column                 | Conflates the RACI role label with a concrete person; breaks every existing reader of `owner_role` (attest gate, OSCAL POA&M owner, checklist role-split, demoseed); a destructive change to a load-bearing column for a narrow feature.                                                                                      |
| **(b) Add an `owner_user_id` FK column to `controls`**        | one new nullable column                   | Widens the hot `controls` table + every `SELECT` projection (15-17 columns already); couples a triage feature to the core control schema; re-uploading a bundle (whole-row replace) would clobber it.                                                                                                                         |
| **(c) CHOSEN — a separate `control_owner_assignments` table** | `(tenant_id, control_id) → owner_user_id` | Minimal, additive, non-destructive: `owner_role` keeps its RACI meaning untouched; the assignment is a clean side-table the bulk path re-checks per item; survives bundle re-upload; FK to `controls(tenant_id, id)` (the slice-002 D3 cross-tenant-safe target) means a row can never reference a control in another tenant. |

**Chosen: (c).** It is the "minimal coherent shape the bulk path can re-check
per-item" the directive asked for. The table carries `owner_user_id` (the
assigned person), `assigned_by` (the actor, for repudiation), and timestamps;
re-assigning UPSERTs onto the `(tenant_id, control_id)` PK (one owner at a
time). The target owner is validated against a tenant-GUC read of `users`
(RLS hides cross-tenant users → "owner is not a tenant user") rather than a
composite FK, because `users` carries no `UNIQUE(tenant_id, id)` target and
the handler-side validation is the same tenant boundary the slice-011 attest
path already established.

This did **not** balloon the single-item path into a broad schema/UX change,
so the slice did NOT split (see D6) — it shipped the coherent whole.

### D2 — The bulk path REUSES the single-item per-item check; one source of truth. **(Confidence: high — AC-11 / P0-467-1, the entire reason the slice exists)**

`assignOwnerInTx(ctx, q, tenant, actor, control, owner)` is the ONE place an
owner-assignment is authorized + applied per control. The single-item handler
(`POST /v1/controls/{id}/owner`) calls it once; the bulk handler
(`POST /v1/controls:bulk-assign-owner`) calls it **per id in a loop inside one
transaction**. There is no parallel authz path that could drift (P0-467-1).

Authorization is layered:

1. **ROLE gate (middleware).** Both routes resolve to `action="write",
resource="controls"` at the shared authz middleware — admitted only for
   admin / grc_engineer / control_owner, denied for viewer / auditor,
   IDENTICALLY. The `:bulk-assign-owner` colon-suffix is deliberately NOT a
   transition verb (it is not in `transitionActions`), so the bulk route gets
   the same write-on-controls decision the single route does.
2. **PER-ITEM tenant + existence check (handler).** `assignOwnerInTx` reads
   each control through the tenant-GUC tx; RLS hides cross-tenant rows, so a
   control id in another tenant is rejected (AC-7 / threat-model E). This is
   the amplifier defense the middleware CANNOT provide — it sees the request
   once; the per-control set is the handler's responsibility.
3. **TARGET-OWNER validation (handler).** `owner_user_id` must be an active
   tenant user (threat-model T).

The actor id is the verified credential's user (the `jwtmw.SubjectUserID`-
stripped UUID), NEVER the request body — a caller cannot forge who performed
the assignment (threat-model R).

### D3 — Unauthorized/missing item FAILS THE WHOLE BATCH (no skip-and-report). **(Confidence: high — P0-448-2)**

The spec's threat-model E gives a documented choice: fail the whole batch, or
skip-and-report the offending item. **Chosen: fail the whole batch.** The bulk
handler runs every item inside ONE transaction; if ANY per-item check fails,
the transaction rolls back and the caller gets a 4xx naming the offending id —
"no silent partial apply" in the strongest form (nothing is written, not even
the items that would have succeeded). Rationale: a bulk owner-assign is a
deliberate triage action; a partial apply with a buried "3 of 12 skipped"
report invites the operator to believe the batch succeeded. All-or-nothing is
the honest, auditable outcome — and the audit row (D4) then references exactly
the set that WAS applied (the whole submitted set, since a partial never
commits).

### D4 — Audit: ONE append-only row per assign event, referencing the SET. **(Confidence: high — threat-model R / P0-448-4)**

`control_owner_assignment_audit_log` is an append-only ledger (SELECT+INSERT
RLS only, no UPDATE/DELETE — the slice-013/062 shape). A single-item assign
writes one row with `control_ids = {the one id}`; a bulk assign writes ONE row
with `control_ids = {the whole applied set}` + `is_bulk=true` (the "one event
referencing N items" option the spec permits). A later "who reassigned all
these?" question is answerable. The middleware's `decision_audit_log` already
records the allow/deny per request; this business ledger adds the
owner+actor+affected-set the decision log does not carry.

### D5 — Saved-views: tenant via RLS, USER via the query predicate; localStorage→server migration is a silent one-time upload. **(Confidence: high)**

**Isolation (P0-448-5).** `saved_views` is RLS-scoped on `tenant_id` (the
four-policy FORCE shape). There is no `app.current_user` GUC at v1, so the
per-USER cut is the mandatory `user_id` predicate on every query, sourced from
the verified credential and NEVER the request body — the exact shape
`user_notification_preferences` (slice 016) established. A caller can never
read, create-for, or delete another user's view: the id it could pass is
matched against `user_id = <caller>`, so a foreign id resolves to "not found".
Proven by `TestSavedViews_PerUserIsolation` (Bob reads zero of Alice's views;
Bob's delete of Alice's view 404s; Alice's view survives).

**Filter validation (threat-model T).** The persisted `filters` payload is
narrowed to EXACTLY the slice-224 controls-filter keys
(`framework/family/result/freshness/scope`) before INSERT and re-narrowed on
read — no arbitrary JSON round-trips into a stored view that could become a
query fragment. This is the server analogue of slice 448's client-side
`sanitizeFilters` (448 D5).

**AC-467-3 — localStorage→server migration: silent one-time upload, NOT start-
fresh.** On the first server-backed load the page uploads each view a user
saved CLIENT-SIDE during the slice-448 interim, best-effort (a duplicate-name
409 or any failure is swallowed so one bad view never blocks the rest or the
page), then CLEARS the localStorage key so the migration is idempotent. A user
who curated views in the interim keeps them; the server is the source of truth
thereafter. The slice-448 injected `SavedViewStore` seam made this a one-place
swap. Alternative (start fresh) was rejected — it silently discards a user's
curated interim views with no signal.

### D6 — Bulk-assign-owner FE v1 = "assign to me", richer picker is a follow-on. **(Confidence: medium)**

The bulk-assign action needs a target owner. A full "assign to any tenant
user" picker needs a tenant-user roster endpoint readable by any control-write
role — none exists on `main` (`/v1/admin/users` is admin-tier only). Building
that roster is a separate authz surface (scope creep). **Chosen:** v1 assigns
the selected set to the CURRENT USER ("Bulk assign-owner to me"), resolving the
owner from `/v1/me` — which is exactly the dominant triage case the slice-448
narrative names ("assign all these 12 unowned controls to me"). The BACKEND is
fully general (it takes any `owner_user_id`); only the v1 FE trigger is
self-targeted. A richer assign-to-any-user picker (gated on a control-write-
readable user roster endpoint) is a documented follow-on. This kept the FE
bounded and honest (a working button, not a vapor picker) — the slice-448 D3
UI-honesty discipline preserved, now satisfied by a real action.

### D7 — Did NOT split the slice. **(Confidence: high)**

The directive allowed splitting the single-item owner-assign into its own
slice if it ballooned. It did not: the additive-side-table model (D1) made the
single-item path small (one table + one shared function), and the bulk +
saved-views rode on top cleanly. The coherent whole shipped in one PR. The
ONLY spillover filed is the e2e seed-harness follow-on (slice 743) — the
controls-list Playwright spec is fully quarantined behind the slice-082 seed
harness, so its assertions stay quarantined (bodies updated to the slice-468
working-button reality) rather than turned on un-observed (CLAUDE.md e2e
discipline: do not relax/claim un-observed; file the seed spillover).

---

## AC-11 amplifier proof (the load-bearing test)

`internal/api/controls/owner_assign_integration_test.go`
`TestBulkAssign_CrossTenant_Amplifier`, run against a live Postgres with the
RLS-enforcing `atlas_app` pool (`dbtest.NewAppPool`):

- Tenant A owns a control; tenant B's server tries to bulk-assign it.
- Result: **404**, the transaction rolls back, and the migrate (BYPASSRLS)
  pool confirms tenant A's control has **zero** owner assignments and tenant B
  wrote **zero** audit rows. The bulk path is provably NOT weaker than the
  single-item path (`TestSingleAssign_CrossTenant_NotFound` proves the single
  path 404s the same control).
- **Deliberate-weakening proof (ran locally, reverted):** commenting the
  per-item `if !exists { return assignErrControlNotFound }` guard in
  `assignOwnerInTx` made the test FAIL (`AMPLIFIER BREACH: … expected 404, got
500 … foreign key constraint "control_owner_assignments_control_fk"`). The FK
  is defense-in-depth (the cross-tenant write is still blocked at the DB
  layer), but the explicit per-item guard is what gives the clean 404 + the
  no-silent-apply guarantee. The test bites.

All seven slice-468 integration tests pass (AC-10 applies+audits, AC-11 cross-
tenant amplifier, single-item cross-tenant, bad-owner-fails-batch no-partial-
apply, AC-13 over-cap rejected, AC-12 saved-view isolation, duplicate-name 409).

---

## Revisit once in use

1. **(D6 — medium)** Build the assign-to-any-user picker once a control-write-
   readable tenant-user roster endpoint exists (or extend `/v1/saved-views`'s
   self-service pattern to a roster read). Until then "assign to me" covers the
   dominant triage case.
2. **(D1 — low)** If product direction makes per-control owner-USER the primary
   model (displacing `owner_role`), reconcile the two — the side-table is the
   migration-friendly base for that, but the RACI role string stays useful.
3. **(D5 — low)** The 20-view per-user cap + 60-char name cap mirror the
   slice-448 client caps; raise/lower from observed usage.
4. **(D7 — high)** Land slice 743 (controls-list e2e seed fixture +
   un-quarantine) so the multi-select / bulk-assign / saved-view UI flows join
   the gated CI Playwright rotation.

---

## Confidence summary

| Decision                                                                 | Confidence |
| ------------------------------------------------------------------------ | ---------- |
| D1 — additive owner-assignment table                                     | high       |
| D2 — bulk reuses single-item check (one source of truth)                 | high       |
| D3 — fail-whole-batch, no silent partial apply                           | high       |
| D4 — one audit row per event, referencing the set                        | high       |
| D5 — tenant-RLS + user-predicate; silent one-time localStorage migration | high       |
| D6 — FE v1 "assign to me", richer picker a follow-on                     | medium     |
| D7 — did not split; e2e seed spillover (743) only                        | high       |
