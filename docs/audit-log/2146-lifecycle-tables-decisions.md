# 2146 — Declared lifecycle transition tables and their JSON export: decisions log

Ticket: [#2146](https://github.com/mgoodric/security-atlas/issues/2146), under
the in-app help spec [#2125](https://github.com/mgoodric/security-atlas/issues/2125).
This file records the build-time calls. No product behaviour changed: the
tables describe what the stores, triggers and authorization bundle already do.

## Detection-tier classification

- **detection_tier_actual:** unit (the role check against the authorization
  bundle refuted the first draft's action-plan roles; see D5).
- **detection_tier_target:** unit.

## Decisions

**D1. Each table lives in the package that owns the moves.** `exception.Lifecycle`,
`policy.Lifecycle`, `frameworkscope.Lifecycle`, `period.Lifecycle` and
`actionplan.Lifecycle` sit next to the stores. `internal/lifecycle` holds only
the shape and `Validate`. `cmd/atlas-lifecycles` collects the tables and
writes `docs/lifecycles/<name>.json`. A central registry inside
`internal/lifecycle` would have to import every domain package.

**D2. The tables describe; they do not drive.** The stores keep their guarded
`UPDATE ... WHERE status = '<from>'` queries and `actionplan.AllowedTransition`
stays the check the action-plan store runs. Rewiring the stores to consult the
table would be a behaviour change, and this ticket rules that out.

**D3. "In the database" means the guarded UPDATE, and the trigger where there
is one.** Only two of the five lifecycles have a trigger that refuses moves:
`action_plans_status_transition_guard` (every state) and
`framework_scopes_predicate_frozen` (out of activated and superseded only, #1965).
Exceptions, policies and audit periods are guarded by their queries' `WHERE`
clauses. So there are two checks:

- `lifecycletest.EveryPair` seeds a row in each state through the real store,
  attempts every move the table names, and expects success exactly where the
  table lists that move. A row that a refused move leaves in place counts as
  refused, whether or not the store returned an error. That covers the exception
  expiry sweep, which skips non-active rows without error. The moves are the
  only write paths into the state column, so a pair that no move reaches is
  refused by construction. The check confirms the set of pairs the moves
  reached equals the table's.
- `lifecycletest.TriggerPairs` sets the state column directly, through the
  owner pool, for every pair out of the states the trigger covers. It expects
  a `check_violation` exactly where the table forbids the pair.

Action plans use `TriggerPairs` only. All their moves are one status PATCH,
so the action names in the table (start, block, resume, reopen...) are labels
on edges, not distinct calls. `TestLifecycle_MatchesAllowedTransition` holds
the table to `AllowedTransition`.

**D4. What counts as a transition.**

- Deleting a draft policy is a delete, not a move, so it is in the policy
  table's doc comment, not its transitions.
- A policy import creates a row already `published`, so `published` is a second
  initial state.
- Superseding is a person's move, made by publishing the next policy version
  or activating another framework-scope revision, in the same transaction. It
  is recorded under the route that causes it.
- A framework scope in review or approved goes back to draft when its predicate
  or an applicability decision changes (the bounce and reset triggers). That
  is the `edit` move, listing every route that can cause it. `EveryPair`
  exercises it through the predicate edit. The applicability reset trigger
  keeps its own tests in `applicability_integration_test.go`.
- Re-submitting a policy or scope that is already in review, which is allowed
  only for a legacy submitter recorded as a session id, is a same-state repair,
  not a transition. It appears as a note on `submit`, because `Validate`
  refuses a move that stays in place.

**D5. Roles are the roles the authorization bundle admits, and a unit test
proves it.** `TestRoles_MatchAuthzBundle` builds each declared route,
runs it through `authz.BuildInput` and the embedded bundle for every canonical
role, and requires the bundle's verdict to equal the table's role list. The
handlers' approver gate (`cred.IsApprover || cred.IsAdmin`) is a further cut,
recorded as `requires_approver`. The people-only and second-person rules from
#2101 are `people_only`, `second_person` and `self_approval_opt_in`. Denial
is second-person with no opt-in. Two places where the handler's own guard is
looser than the bundle:

- **Action plans:** `hasProgramWrite` admits any credential with an owner
  role, but no role other than admin lists `action-plans` as writable, so
  the bundle admits admin alone. The first draft said admin + grc_engineer; the
  role test refuted it.
- **Audit-period create and freeze:** `canWrite` admits grc_engineer, but
  `audit-periods` is not in `grc_writable_resources`, so the bundle admits
  admin alone.

Both are recorded as they behave today. Whether grc_engineer should be able to
make these moves is a product question, out of scope here.

**D6. The export's drift check is a Go test, not a script.**
`TestExport_MatchesCommitted` (in `just test`) fails when
`docs/lifecycles/` and the tables disagree, byte for byte or by file set.
`TestExport_DocumentMatchesTable` decodes each document back into its table.
Regenerate with `just lifecycles-export`.

## Revisit once in use

- If #2148's diagram check wants a different document shape (for example,
  edges grouped by action), change `lifecycle.Table`'s JSON tags and re-export.
  The tests follow the struct.
- The other lifecycles the spec names (risk treatment and acceptance, risk
  open/closed, evidence to control state) get tables in their own tickets.
