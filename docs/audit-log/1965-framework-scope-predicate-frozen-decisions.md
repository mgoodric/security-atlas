# #1965 — Framework scopes: freeze the predicate on activated revisions

**Ticket:** [#1965](https://github.com/mgoodric/security-atlas/issues/1965), follow-up to #1943 (D3 in `1787-framework-scope-applicability-decisions.md`).
**Rule (Matt, 2026-11-13):** activated and superseded scope revisions are immutable. Any change, to the predicate or to the decisions, makes a new revision.

- detection_tier_actual: manual_review
- detection_tier_target: integration

> The first version of the trigger checked only `OLD.state` against the
> predicate columns. Review on #2227 caught that a write could set
> `state = 'draft'` on an activated row and then edit the predicate, and the
> decisions too, because the applicability guard reads the scope's current
> state. `effective_from` was also writable. A second review found more
> writable columns: `framework_version_id`, `tenant_id` and the approval
> provenance. The fix (D1) and integration cases for each bypass landed in
> the same PR, and each set of cases fails on the trigger version before it.

## Decisions

**D1 — The freeze is an allow-list (Matt, second review on #2227).**
`framework_scopes_predicate_frozen` (BEFORE UPDATE) acts only when
`OLD.state` is `activated` or `superseded`. On such a row it refuses a change
to any column except:

- `name`, a label that plays no part in `as_of` reads;
- `superseded_by`, `superseded_at` and `updated_at`, which the supersede
  path (`SupersedePreviousActivated`) writes;
- `state`, and only `activated → superseded`. A step back to `draft` would
  otherwise reopen the predicate and, through the applicability guard, the
  decisions.

The trigger compares `to_jsonb(NEW)` with `to_jsonb(OLD)`, after removing
the allowed columns, so a column added to `framework_scopes` later is frozen
by default. The refusal names the changed columns. Today the freeze covers
the predicate, `effective_from`, `framework_version_id`, `tenant_id`, the
approval provenance (`approver_user_id`, `approved_at`,
`predicate_hash_at_approval`, `approval_evidence_file_url`/`_hash`) and
#2101's `submitted_by` and `self_approved`. The first two versions of this
trigger froze named columns one by one, and each review found another
column left writable. That is why the list is now of what may change.

#2101 redefined the bounce trigger (`framework_scopes_bounce_on_predicate_change`)
and the reset trigger (`framework_scope_applicability_reset_approval`) to
also clear `self_approved`. Both act only on `review` and `approved` rows,
so neither writes to a frozen row. A `from_scope_id` fork inserts with the
column defaults, so it starts with `self_approved = false` and
`submitted_by` NULL. The test asserts both.

**D6 — DELETE is not guarded.** This follows the decision guard (#1787 D4).
No API deletes a scope, and the FK cascades and the operator purge
(`internal/auth/oauthclient`) have to keep working.

**D2 — Store check first, trigger as the guarantee.** `UpdatePredicate`
returns `ErrPredicateFrozen` (409) from the state it reads, mirroring
`writableScope`/`ErrDecisionsFrozen`. A PATCH racing an activation is caught
by the trigger on the locked row and mapped back to `ErrPredicateFrozen` by
constraint name, so the caller still gets 409 rather than 500.

**D3 — The new-revision flow is `POST /v1/framework-scopes` with
`from_scope_id`.** Before this, a new revision could be created with a plain
POST, but it started with no decisions, so every exclusion on the activated
revision would silently revert to undecided (counted as applicable) on
activation. With `from_scope_id` the new draft:

- is on the source's framework version (a different `framework_version_id` is 400);
- takes the source's name and predicate unless the request gives new ones;
- copies every decision on the source, each through the normal decision write
  path so the guard and history apply.

Any source state is accepted, so a draft can be forked too, and a source
can be forked more than once: several drafts may stand side by side, as a
plain POST already allowed. Only one revision per framework version can be
activated (the partial unique index), and activating one supersedes the
current one. Any other drafts stay drafts. A separate
`:revise` route was considered and rejected: it would duplicate create's
validation and logging for no gain.

**D4 — Copied decisions are logged as `applicability.carry_over`.** The
existing carry-over log row already means "decision copied from another
revision"; `carried_over_from` names the source revision and version. A
same-version copy has no version-migration row, so `migration_id` and the
`framework_version` label are now `omitempty` in `carried_over_from` (the
cross-version carry-over still writes both).

**D5 — UI.** The framework-scope page renders an activated or superseded
predicate read-only and shows a "New revision" button on those rows. A 409
from a PATCH shows a banner pointing at the new-revision flow.
