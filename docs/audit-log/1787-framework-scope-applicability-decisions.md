# framework-scope applicability decisions — decisions log (#1787)

First slice of the Statement of Applicability (#1744). Adds
`framework_scope_applicability`: per requirement of a framework version, a
decision (`applicable` | `excluded`), a justification (required when
excluded) and an implementation reference, belonging to one framework-scope
revision. Builds on the workflow log from #1786.

detection_tier_actual: integration
detection_tier_target: integration

One bug surfaced during the work: the first draft of the guard trigger also
fired on DELETE, which blocked the ON DELETE CASCADE from `framework_scopes`
and `framework_requirements`. The integration tests' fixture cleanup caught
it (D4).

## Decisions

**D1 — Workflow rules live in triggers, the store checks them first.** A
BEFORE INSERT/UPDATE guard refuses writes on `activated`/`superseded`
revisions, a requirement outside the revision's framework version, and a
revision that isn't visible to the row's tenant. An AFTER trigger sends a
`review`/`approved` revision back to `draft` and clears its approval
columns, the same columns the predicate trigger clears. The store runs the
same checks first so the API returns typed errors (400/404/409). The DB
rules hold even when a caller skips the store, the same reasoning as the
predicate trigger (ADR-0001).

**D2 — Approval covers decisions by reset, not by hash.** The spec words it
as "the approval hash covers predicate plus decisions". No decisions hash
is stored. Every decision write on a revision in review or approved resets
it, so an approved revision's decisions are exactly the ones that were
approved. `predicate_hash_at_approval` stays a predicate-only hash. A later
ticket can add a decisions digest at approval if the SoA export needs one
to prove what was approved.

**D3 — Decisions on activated and superseded revisions are frozen (409).**
The spec doesn't cover edits to an activated revision. The predicate PATCH
allows them in place, without a state change. Decisions refuse them, because
`as_of` reads return the rows of the revision that was active at the time,
and that is only true if those rows can't change afterwards. To change the
SoA, draft a new revision and activate it.

**D4 — No DELETE guard.** No API deletes a decision. DELETE is left
unguarded so the FK cascades and the operator purge
(`internal/auth/oauthclient`, which deletes tenant rows table by table) can
remove rows. The reset trigger still fires on DELETE.

**D5 — Undecided is an absent row.** The list query left-joins every
requirement of the version against the revision's decisions. Rows with no
decision come back `undecided: true` with a null `decision` and are counted
in `counts.undecided`. An `as_of` with no revision in force returns
`framework_scope: null` and every requirement undecided. It doesn't 404,
because undecided means applicable.

**D6 — One log action, `applicability.update`.** Each decision write adds
one workflow-log row. Its `before`/`after` are the scope-row snapshots (so
a reset shows in `from_state`/`to_state` and the cleared approval columns)
with the decision under an `applicability` key. That key is null in
`before` when the requirement was undecided. The action CHECK on
`framework_scope_workflow_log` is extended. The unified admin audit log
picks up the new action unchanged, as kind `framework_scope`.

**D7 — Routes and authorisation.**
`GET /v1/framework-scopes/{id}/applicability`,
`PUT /v1/framework-scopes/{id}/applicability/{requirement_id}`, and
`GET /v1/framework-scopes/applicability?framework_version=&as_of=` (the
`as_of` read, mirroring the scope list's `as_of`). Writes use the same gate
as the predicate PATCH (any tenant credential), since a write can only land
on a draft or reset a revision to draft, and approval stays approver-gated.
Bulk set, CSV import, filters, coverage, contradictions and export belong
to the later #1744 tickets.

**D8 — Fixture.** `seedSoAFixture` in
`internal/frameworkscope/applicability_integration_test.go` is the spec's
fixture: eight requirements with crosswalk edges, three controls (C1 and C2
passing, C3 failing), with R-01 excluded behind a passing control, R-02
excluded with none, R-03 undecided and the rest applicable. The coverage and
contradiction tickets can reuse it.
