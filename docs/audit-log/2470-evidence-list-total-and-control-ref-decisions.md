# #2470 — per-control evidence list: filtered total, control_id resolution for scoped callers

Follow-up to #2424 (`2424-auditor-evidence-list-window-decisions.md`), found
in the #2467 review.

detection_tier_actual: manual_review
detection_tier_target: integration

### D1 — `total` is the filtered count on the per-control branch, for every role

`GET /v1/evidence?control_id=` carried the slice-236 tenant-wide ledger
count as `total`. The per-control branch now reads `CountEvidenceForControlWindow`,
which applies exactly the list's predicates before the cursor: tenant,
control resolution (D2 included) and the `[since, until]` window. The
per-control list ignores `scope_cell_id`, so the count does too; "scope" in
the ticket is therefore the control resolution plus the window.

This applies to admins and grc_engineers as well, not only auditors. One
meaning for `total` per branch is simpler than a role-dependent one. The
control-detail page renders `count` and `next_cursor` only
(`web/app/(authed)/controls/[id]/page.tsx`). The `/evidence` page reads
`total` for its "Showing N of M records" line, and when it is filtered by
control it hits this branch: M is now the control's count in the window
rather than the tenant ledger, which matches the rows shown. One
consequence: a control with no records in the window renders "No records
in ledger yet" (`recordCountMeta`, `format.ts`), copy written for an empty
tenant ledger. That copy is left for a follow-up rather than widened into
this fix. ISC-236-2's test, which pinned the tenant-wide value on this
branch, is replaced by `TestEvidence_PerControlTotalIsFilteredCount`.

The tenant-wide branch (no `control_id`) keeps the slice-236 ledger total.
Auditors are denied that branch outright (#2157), so it leaks nothing to
them, and the `/evidence` page uses it deliberately (P0-236-2).

### D2 — narrow the list for scoped callers, rather than widen the hydrator

The handler matched `control_id = X OR control_ref = X`, while
`audit.Store.ControlPeriods` finds populations by `control_id` only, and
the population sample draw resolves evidence by `control_id` too. So a
record with `control_ref = X` but `control_id = Y` reached an auditor
admitted on X's populations, though those populations never contain it.

Two options:

- **Include `control_ref` in the hydrator lookup.** Would not close the gap:
  the hydrator decides which periods cover control X; the stray record
  belongs to Y's populations, if any. Admitting X's list would still show a
  record no X population draws.
- **Narrow the list for scoped callers (chosen).** A caller that is not an
  unbounded reader resolves the control by `control_id` alone
  (`ListEvidenceForControlPaged` / `CountEvidenceForControlWindow` gain an
  `id_only` argument). The rows an auditor sees are then exactly the
  `control_id` set its authz decision was computed over.

### D3 — who is unbounded, and failing closed

`readsUnscopedEvidence` (controldetail/authz.go) is
`authz.IsTenantApprover || authz.IsTenantControlOwner`: tenant admin,
approver flag, grc_engineer, control_owner — the roles whose rego admits
the per-control list without a period bound. Everyone else is scoped, so an
unknown or absent role can only narrow the rows. As with the #2428 helpers,
a JWT role claim does not count, only the live `user_roles` row
(`TenantRoles`) or an API key's label; a user demoted from grc_engineer to
auditor gets the narrowed rows on the next request, not when the token
expires. `IsTenantControlOwner` is new, mirroring `IsTenantAdmin`. The call
site is listed in the #2447 gate-site table as a non-route (it narrows
rows, it never refuses).

Unbounded readers keep the `control_id OR control_ref` resolution, so no
row they saw before is hidden.
