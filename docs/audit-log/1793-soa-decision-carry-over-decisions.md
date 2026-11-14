# SoA decision carry-over to a new framework version — decisions log (#1793)

Part of the Statement of Applicability (#1744). When a framework
moves from version N to N+1, a tenant carries its applicability decisions
into its draft revision on N+1 for every requirement whose carry-over was
approved in the version-migration queue (ADR 0019).

detection_tier_actual: none
detection_tier_target: integration

No bug surfaced. Two mutations were run against the integration tests (in
the PR body); both were caught.

## Decisions

**D1: Approving a queue row gates the copy; it does not perform it.** The
version-migration queue is catalog-wide, and since the #1757 amendment only
a super_admin decides its rows, on a store that sets no tenant GUC. Copying
decisions inside that call would mean enumerating every tenant and writing
into its scopes from a catalog path: a cross-tenant write with no precedent
in the codebase. Instead the copy is the tenant's own act,
`POST /v1/framework-scopes/{id}/applicability:carry-over`, run under its RLS.
Only rows with `match_kind = exact_code` and `status = approved` are copied.
The dispatcher made this call on Matt's standing instruction.

**D2: A distinct workflow-log action, `applicability.carry_over`.** Migration
`20261011000000` widens the action CHECK, keeping every existing value, so an
auditor can filter carried decisions apart from hand edits. The row's
`after` image carries `carried_over_from`: source framework version id and
string, source revision id, source requirement id and the queue row id.

**D3: One write path.** The copy goes through `writeDecisions`, the same
upsert, frozen-revision refusal and per-decision log row as the single,
bulk and import writes (#1790 D1). An unexported `carriedFrom` on
`DecisionInput` switches the logged action; nothing outside the package can
set it.

**D4: The target must be a draft (409 otherwise).** Carried decisions land
only in a draft, never in review, approved or an activated revision.
Activation semantics are unchanged (#2078 is open with Matt).

**D5: Source revision = the tenant's activated revision on N, else its
newest.** The activated revision's decisions are the ones in force. A
tenant that never activated N still has a working draft whose decisions are
its best statement, so the newest revision is used. A tenant with no
revision on N copies nothing and gets 200 with every approved row reported
`source_undecided`.

**D6: The source version comes from the queue.** N is the `from_version_id`
of the queue rows whose `to_version_id` is the draft's version. No rows is
409 (nothing leads into this version); rows from more than one prior
version is 409 rather than a guess.

**D7: Idempotent, and hand edits win.** A requirement the draft already
decides is left alone and reported `already_decided`, so a re-run writes no
decision and no history row, and a decision edited after a carry-over is
not overwritten.

**D8: What is not copied is reported, not guessed.** Each queue row that is
not copied is returned with a reason:

| Reason                | Meaning                                          |
| --------------------- | ------------------------------------------------ |
| `new_requirement`     | `added` row: new in N+1, stays undecided         |
| `removed_requirement` | `removed` row: not in N+1                        |
| `carry_over_pending`  | exact match not yet reviewed                     |
| `carry_over_rejected` | a reviewer rejected the carry-over               |
| `source_undecided`    | approved, but no decision on N to copy (skipped) |
| `already_decided`     | the draft already has a decision (skipped)       |

The first four are `flagged` (need review); the last two are `skipped`.

**D9: Authz is the bulk write's gate.** The route is a POST under
`/v1/framework-scopes`, so the OPA role gate treats it as a `write` on
`framework-scopes`: grc_engineer and admin pass, viewer gets 403.
