# #1965 — Framework scopes: freeze the predicate on activated revisions

**Ticket:** [#1965](https://github.com/mgoodric/security-atlas/issues/1965), follow-up to #1943 (D3 in `1787-framework-scope-applicability-decisions.md`).
**Rule (Matt, 2026-11-13):** activated and superseded scope revisions are immutable. Any change, to the predicate or to the decisions, makes a new revision.

- detection_tier_actual: none
- detection_tier_target: none

## Decisions

**D1 — Freeze `predicate` and `predicate_hash` only.** The new
`framework_scopes_predicate_frozen` BEFORE UPDATE trigger refuses a change to
either column when `OLD.state` is `activated` or `superseded`. Other columns
stay writable so the `activated → superseded` transition
(`SupersedePreviousActivated`: `state`, `superseded_by`, `superseded_at`,
`updated_at`) still works on the frozen row. `name` is not frozen: no API
edits it and the ticket names the predicate.

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

Any source state is accepted, so a draft can be forked too. A separate
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
