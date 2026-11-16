# Issue #1987 — discard a policy draft

Build-time calls made for
[#1987](https://github.com/mgoodric/security-atlas/issues/1987).
A draft created with `predecessor_id` holds its predecessor's single
successor slot (`policies_predecessor_unique_when_set`). Before this change
an abandoned next-version draft blocked every further version with 409, and
nothing in the API could remove it (#1931 review, D13/D14 of
`1747-policy-import-decisions.md`).

`detection_tier_actual = none` · `detection_tier_target = integration`
(covered by `internal/api/policies/discard_draft_integration_test.go`).

## D1 — `DELETE /v1/policies/{id}`, not a `discard` transition

The ticket allows either. A discard leaves no row behind, so it is a delete
of the resource rather than a state change; a `discarded` status would have
needed a new CHECK value, a filter on every list and calendar read, and it
would still hold the successor slot unless the unique index changed too.
`DELETE` frees the slot by removing the row. Success is 204 with no body.

## D2 — Drafts only, enforced in the statement

`DeleteDraftPolicy` is `DELETE ... WHERE status = 'draft' RETURNING *`, so
the rule holds even against a concurrent submit: a row that left draft
does not match. When nothing matches, the store probes the id to tell 404
(absent, or another tenant's under RLS) from 409 (any other state).
Under-review and approved rows are refused too, following the ticket's
"drafts only". Someone abandoning one of those can move it back through the
workflow first; widening the rule is a separate decision.

## D3 — The role is the one that writes policies

There is no edit route for a draft; the routes that change one (create,
submit) have no handler-level role check and rely on the authz middleware's
`write` on `policies`, which admits admin and grc_engineer. `DELETE`
derives the same action and resource (`internal/authz/input.go`
`actionFromMethodAndPath`), so it gets the same admit set without a new
rego rule. `internal/authz/policy_discard_1987_test.go` pins that the
discard and the create admit exactly the same roles. No approver check is
added: a draft is not yet an audit-binding artifact.

## D4 — The audit row carries the discarded title and version

`policy_audit_log.policy_id` has no FK, so the audit row outlives the policy
row. On its own the id would then point at nothing. Migration
`20260921000000_policy_draft_discard.sql` adds nullable `policy_title` and
`policy_version` columns, written only by the discard, alongside the new
`draft_discarded` action. The actor is the user behind the credential, else
the credential id, the same rule as the #1806 review-date audit. The table
stays append-only; its trigger is unchanged.

## D5 — The 409 names both ways forward

`ErrPredecessorTaken` now says to continue the existing successor, or, if it
is still a draft, discard it with `DELETE /v1/policies/{id}` and create the
new version again. The message does not include the blocking draft's id;
`GET /v1/policies/{id}?versions=true` on the predecessor finds it.
