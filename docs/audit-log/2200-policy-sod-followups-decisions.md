# Policy segregation-of-duties follow-ups — decisions log (#2200)

Follow-ups from the second review of #2180 (#1807). A policy submitted
before #1807 by a key recorded the key id as `submitted_by`, and its
owner could approve it through a JWT or another key. Submit and approve
also left no audit row of their own.

detection_tier_actual: manual_review
detection_tier_target: unit

The gap was found in PR review, not by a test. The `checkApprover` table
test could have caught it with one case: the key owner approving through
a JWT.

## Decisions

**D1: The legacy-key rule lives in `policy.checkApprover`, not in
`approval.Check`.** `approval.Check` is shared with exceptions and
framework scopes, where a `key_` requester may still be current (a
machine requester). Only policies have the #1807 history, so only
`policy` treats `key_%` as legacy. The same key is still refused first
(`ErrSelfApproval`), so a holder of the submitting key cannot approve
it, whoever the key now names. Any other approver gets
`ErrSubmitterUnknown` (409), which asks for a re-submit.

**D2: Re-submit accepts `key_%` rows.** `SubmitPolicyForReview`'s legacy
predicate is `submitted_by LIKE 'jwt:%' OR submitted_by LIKE 'key\_%'`.
The underscore is escaped, because `_` is a single-character wildcard in
`LIKE`.

**D3: Three new audit actions, two new columns.** Migration
`20261108000000_policy_transition_audit.sql` adds `submitted`,
`resubmitted` and `approved` to `policy_audit_log_action_check`, keeping
every existing value, and adds nullable `previous_state` / `new_state`.
The rows are written by `insertTransitionAudit` inside the transaction
that changed the status, so a rolled-back transition leaves no row and a
refused approval writes nothing.

**D4: A self-approval writes `self_approved`, not `approved` as well.**
One approval is one row. The #2101 `self_approved` row now also carries
the previous and new state. A reader counting approvals counts both
actions.

**D5: The shared identity helper was already in place.** The ticket's
last item asked to switch `actorOf` and `Approve` to
`credstore.CallerUserID` / `identity.UserID` once #2186 landed. On
`main`, `actorOf` is `credstore.PrincipalOf`, which goes through
`MinterUserID`, and `MinterUserID` is a wrapper over `CallerUserID`,
which calls `identity.UserID` (`internal/api/credstore/credstore.go`). No
change was needed.

**D6: Tests that are not about the submitter now submit as a person.**
Several `internal/policy` integration tests submitted as `"key_author"`.
Under D1 that reads as a legacy key submitter, so they now use a person
uuid (`submitterPerson`). The legacy behaviour has its own test,
`TestApprove_LegacyKeySubmitter`.
