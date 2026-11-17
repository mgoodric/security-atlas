# Issue #2478: live linked controls at publish; strict person principals

Follow-ups from the review of #2468 (#2459). Publish checked only that
`linked_control_ids` was non-empty, so a policy whose only control was
retired after its last edit could still be published. `isPersonPrincipal`
used lenient `uuid.Parse`, which also accepts 32-hex, `{...}` and
`urn:uuid:` forms.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1: Publish needs at least one live linked control

**Decision.** Publish counts the linked ids with `CountTenantControls`
(non-retired, in the tenant). An empty list keeps the existing 409
`ErrOrphanPublish`. A non-empty list with no live control returns the new
`ErrNoLiveLinkedControl`, 422, the status create and draft edit use for a
bad link.

**Why ≥1 rather than all live.** An approved policy cannot be edited, so
requiring every link to be live would strand a policy that still governs
live controls the moment one of several is retired. The orphan rule asks
only that the policy govern something.

## D2: Submit re-runs the create / draft-edit link check; approve does not

**Decision.** `SubmitForReview` calls `checkLinkedControls`, the same
check create and draft edit run: every linked id must be a non-retired
control in the tenant, else 422 `ErrUnknownLinkedControl`. An empty list
still submits; the orphan warning and the publish block cover it.

**Why submit.** It is the last point at which the draft is editable, so the
author can drop or replace the retired link. Catching it at publish
instead would force a new version.

**Why not approve.** Approve cannot fix anything (the row is not editable),
and publish already refuses a policy with no live link. A check there would
only move the refusal earlier without giving anyone a way to act on it.

## D3: One strict helper for person principals

**Decision.** `identity.UserID` now requires the canonical 36-character
lowercase uuid (parse, then round-trip against `.String()`), with or
without the `user:` prefix. `identity.IsBareUserID` adds the bare-only
form, and `policy.isPersonPrincipal` calls it.

**Why change `identity.UserID` itself.** It is the one rule for reading a
users(id) out of a principal (#2183). `credstore.CallerUserID`, which every
approval check (`approval.Check` via `Approver.UserID`) reads the approver
from, and the audit-note recipient, auditor attrs and authz role lookups
all go through it. Fixing it once covers every separation-of-duties person
check. Every principal the platform writes comes from `uuid.UUID.String()`
or Postgres, both canonical, so no stored person is reclassified. Anything
else now fails closed: it names no user.

`credstore.OwnedByUser` still uses `uuid.Parse`. It gates authentication
(which user-gate lookup a credential takes), not separation of duties, and
a looser match there only sends the credential to the stricter
`UserCanAuthenticate` gate.
