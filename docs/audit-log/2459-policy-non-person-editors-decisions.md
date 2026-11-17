# Issue #2459: non-person draft editors are unknown authors

Follow-up from the review of #2448 (#2446). `policies.draft_editors`
records each editor as `credstore.PrincipalOf`. For a person that is the
bare users(id) uuid. For an unbound key it is the key's own `key_...` id,
for an OAuth client `oauth_client:<id>`, and for the bootstrap admin
`bootstrap-admin`. `checkApprover` compared those values only with the
approver's own `UserID` / `CredentialID`. A person who edited through an
unbound key could approve through their session, and the approval was
neither refused nor flagged.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1: A non-person editor is an unknown author (option b)

**Decision.** In `checkApprover`, an editor that is not a users(id) uuid
is an unknown author. Every approver is refused with `ErrEditorNotPerson`
(403) when self-approval is off. When it is on, the approval goes through
and is flagged `self_approved`, so its audit row is `self_approved` too.
The submitter rule and the person-editor rule are unchanged.

**Why not (a), recording `api_keys.minted_by` as the editor.**

- It covers only one of the three kinds. An OAuth client and the bootstrap
  admin have no minter. `credstore.MinterUserID` returns "" for both, and
  for a key minted by the bootstrap admin.
- Where it does apply, the minter is not the holder. A key is a bearer
  token: whoever has the token wrote the edit. Naming the minter would
  refuse the wrong person and clear the real author.
- `PrincipalOf` already prefers the minter for a key bound to a user.
  The unbound case is exactly the one with no person to name.

(b) covers all three kinds with one rule, and matches the submitter rule
from #2200, where a `key_` submitter refuses every other approver. It
differs in one respect, because the ticket says so: with self-approval on,
the approval is allowed and flagged, the same as for a person editor. A
non-person submitter stays refused whatever the setting, because a
re-submit can name the person. A draft's edit history can't be rewritten.

**Detection.** A person is a principal that parses as a uuid
(`isPersonPrincipal`). This is a positive check, so any other shape counts
as unknown, including a future credential kind and a pre-#1807 `jwt:`
value. It fails closed.

## D2: Ordering of create and edit errors

**Decision.** `UpdateDraft` reads the row first. A missing row is 404 and
a non-draft row is 409, both before the linked-control check (422).
`Create` checks linked controls after the insert, inside the same
transaction, so an unknown (400), unpublished (409) or taken (409)
predecessor is reported first. A bad link then rolls the insert back.

**Why after the insert.** A taken predecessor is detected only by the
`policies_predecessor_unique_when_set` index. Checking links after the
insert puts it ahead of the 422 without a second read of the chain.

## D3: Retired controls do not count as links

**Decision.** `CountTenantControls` ignores controls with
`lifecycle_state = 'retired'`. Every other state counts: `draft`,
`proposed`, `active` and `deprecated`. Controls are never soft-deleted,
because the table has no `deleted_at`. `ErrUnknownLinkedControl` now says
"does not exist in this tenant or is retired".

**Why.** A retired control governs nothing, so it should not let a policy
avoid the orphan-publish block. A deprecated control is still in force
while it is phased out. A draft or proposed control is one a policy can
govern from the start.

**Not changed.** Publish still checks only that `linked_control_ids` is
non-empty. If a policy was linked to a control that was retired later, it
can still be published until its next edit. Closing that gap would change
publish, which this ticket doesn't ask for.

## `/v1/approvals` agreement

#2453 (the Approvals list) is not on `main` at the time of writing. It
calls the policy approval rule, so it gets this rule unchanged. Its
agreement test belongs to that PR, or to a follow-up once both have
landed.
