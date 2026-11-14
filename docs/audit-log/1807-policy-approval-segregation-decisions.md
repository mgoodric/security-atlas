# Policy approval segregation of duties — decisions log (#1807)

Policy approval checked only that the policy was `under_review`, so one
person could author, submit, approve and publish an audit-binding policy.
This change refuses the submitter, refuses any approver who is not a
person, and handles policies submitted before submitters were recorded as
people. The organisation-level self-approval opt-in and the
`self_approved` marker are #2101's scope (Spec B, #2071 decision C3).

detection_tier_actual: manual_review
detection_tier_target: integration

The first head compared raw identities, and the PR review caught two
gaps. A JWT carries `user:<uuid>` while a key bound to the same user
carries the bare `<uuid>`, so they looked like different people. And
`submitted_by` values recorded as `jwt:<token id>` before the change
could never match any approver. Integration tests now cover both.

## Decisions

**D1: The rule lives in the store, in the approval's transaction.**
`Store.Approve` reads the row and applies `checkApprover` before the
`ApprovePolicy` update, in one transaction. `submitted_by` is written only
on submit, and no transition returns a policy to draft, so the read and
the update cannot race without a row lock. `checkApprover` is the only
approval rule. #2101 extends it with the opt-in instead of adding a
second check.

**D2: Identity is the person, normalised.** Submitter and approver are
recorded as `credstore.MinterUserID(cred)`, the bare `users(id)` uuid,
whether the caller is a JWT (`user:<uuid>`) or a key bound to the user
(`<uuid>`). A JWT credential's id is `jwt:<jti>`, unique per token, and
the two `UserID` shapes differ, so comparing either raw would let one
person submit through one and approve through another. A submit from a
credential with no user still records the credential id. As a side
effect, `submitted_by` and `approved_by` hold user ids for JWT callers
from now on, where they used to hold `jwt:<jti>`.

**D3: Only people approve (Matt's decision, 2026-11-14).** An approver
with no user identity (`MinterUserID == ""`) is refused with 403
(`ErrApproverNotPerson`), whatever its role flags. That covers an
unbound `key_` machine key, including an approver key an admin mints
through `POST /v1/admin/credentials`, an `oauth_client:` credential and
the bootstrap admin. This replaces the narrower review suggestion of
refusing only a key whose `MintedBy` equals the submitter. A key bound to
a user approves as that user, and D1 still applies to it. The
`is_approver` field docs and the OpenAPI summary of
`PATCH /v1/policies/{id}/approve` say so.

**D4: A pre-#1807 session submitter must re-submit (409).** A
`submitted_by` starting with `jwt:` names a session, not a person, so no
approver can be shown to differ from it. Approval refuses it with 409
(`ErrSubmitterUnknown`), and the message asks for a re-submit. No
transition returned a policy to draft, so a re-submit would have been
impossible and the policy stranded. `SubmitPolicyForReview` therefore
also accepts an `under_review` row whose `submitted_by LIKE 'jwt:%'`, and
records the person who re-submits it. Any other `under_review` row still
answers 409 on submit. No migration was needed: the query changed, and
sqlc v1.31.1 regenerated `internal/dbx`.

**D5: A pre-#1807 key submitter is still caught.** Before the change, a
key bound to a user recorded its key id as `submitted_by`. `Approver`
carries the credential id too, and `checkApprover` refuses when it equals
the submitter, so that user cannot approve their own in-flight policy
with the same key.

**D6: The submitter, not the author, is compared.** The ticket suggested
the approver should probably also differ from `created_by`. #1807 asks
for approver ≠ submitter, and #2101 defines the "requester" across
modules, so the author check is left to it.

**D7: Key-bound approvals are tested at the handler.** On `/v1` the HTTP
stack admits only JWTs: `jwtmw` is the sole auth middleware and
`requireCredential` refuses other bearers. The key cases therefore call
the real handler and store with a key-shaped credential in the request
context: a `key_` id, and a `UserID` that is either the bare user uuid
or the key's own id. JWT cases go through the full HTTP stack.
