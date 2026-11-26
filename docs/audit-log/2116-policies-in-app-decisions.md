# Issue #2116 — Policies in the app: create, edit, workflow and new versions

The policy workflow existed only on the API. This adds `/policies/new`, a
draft edit form, a publish form, and the shared detail header on
`/policies/[id]` with one lifecycle action per status. The platform already
enforced the approval rule (`checkApprover`, #1807 / #2101), the publish
block on a policy with no linked control (`ErrOrphanPublish`) and draft-only
discard (#1987). It had no way to edit a draft, so this adds
`PATCH /v1/policies/{id}`.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Draft edit is a whole-record PATCH, draft only, same role as create

**Decision.** `PATCH /v1/policies/{id}` replaces title, body, owner and
approver roles, linked controls and acknowledgment roles. The UPDATE matches
`status = 'draft'` only; any later state answers 409, so a published policy
changes only through a new version (`predecessor_id`). Version, predecessor
and source attribution are fixed at create. The authz derivation is the same
`write` on `policies` as create and discard (pinned in
`internal/authz/policy_discard_1987_test.go`).

**Why.** The form always holds every field, so a partial-update shape buys
nothing and adds a "which fields changed" rule to test.

## D2 — A draft edit writes no policy_audit_log row (superseded by #2446)

**Decision (original).** Edits were not audit-logged; transitions (submit,
approve, self-approval), review-date changes and discards already were.

**Correction (#2446).** The reason given here was wrong. It said the
transition's audit row pins the submitted text, but that row pins the
title, version and state, not the body, so nothing recorded who wrote the
text the approver saw. That left a gap in separation of duties: an author
could have someone else submit their draft and then approve it unflagged.
#2446 records every draft editor, appends a `draft_edited` row with the
body's sha256 on each edit, records the hash and the editor set on submit,
and counts editors as authors in the approval rule. See
`docs/audit-log/2446-policy-draft-editors-decisions.md`.

## D3 — "Create new version" is a prefilled create form, not a one-click copy

**Decision.** On a published policy the header links to
`/policies/new?predecessor=<id>`. The form starts from the published policy
with its version bumped (`nextVersion`: the last number +1, editable) and
creates the draft with `predecessor_id`. The published row is never edited;
publishing the draft supersedes it in the existing store transaction.

**Why.** The platform requires a version on create, and the operator usually
changes the text before saving. A one-click copy would have to invent a
version and then send the operator to Edit anyway.

## D4 — Publish sends the draft's own version

**Decision.** The publish form asks for the effective date (today by
default) and the next review date. `new_version` is sent as the policy's
existing version and is not an editable field.

**Why.** The store requires `new_version` to be non-empty but publishes the
row with the version it was created with; an editable field would accept a
value that is then ignored.

## D5 — Acknowledgment roles are the five platform roles

**Decision.** The form offers `admin`, `grc_engineer`, `control_owner`,
`auditor` and `viewer` as checkboxes. Owner and approver roles stay free
text, as the API and the seeded policies use organisational names
(`security_lead`, `cto`).

**Why.** Acknowledgment matches the required roles against the user's own
platform roles (`rolesIntersect`), so any other value can never be met.

## D6 — The approve e2e uses the #2428 super-admin workaround

**Decision.** `web/e2e/policy-lifecycle.spec.ts` signs the second user in
with a JWT minted with `super_admin: true`, labelled
`// WORKAROUND (remove with #2428)`, as in #2419.

**Why.** Policy approve and publish check `cred.IsApprover || cred.IsAdmin`,
which tenant-admin JWTs do not carry until #2428 lands. The approver is a
different person from the submitter, so the approval rule still runs and the
spec asserts the policy is not marked self-approved.
