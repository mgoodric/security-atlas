# #2129: risk and vendor owners are users — decisions log

Risk owners (`risks.treatment_owner`) and vendor owners (`vendors.owner_user`)
were free text, so "mine" and "unowned" could not be computed (#2072).
Both gain `owner_user_id`, set with the user picker, and a migration links
the existing text to users where it can.

- detection_tier_actual: none
- detection_tier_target: none

## D1: keep the free-text columns; derive "Owner (unlinked)" from them

The new column sits beside the old one, which is not dropped or renamed.
While `owner_user_id` is NULL, the free text is the "Owner (unlinked)" the
API returns as `owner_unlinked`. Once a user is picked, the edit clears the
text, so a record never shows both.

Considered: a new `owner_unlinked` column filled by the migration. Rejected:
it duplicates text the record already holds, and the down migration could
then not restore the original text by simply dropping columns. Keeping the
text where it was makes the up migration write only `owner_user_id`, and
the down migration restores every migrated record exactly.

`treatment_owner` and `owner_user` stay on the wire for older clients (the
MCP tools, the exports). New clients read `owner_user_id` and
`owner_unlinked`.

## D2: matching rules

Text is linked when it equals exactly one **active** user's `display_name`
or `email` in the record's **own tenant**, compared case-insensitively after
trimming spaces. Every join names the record's `tenant_id`; another
tenant's user is never considered.

- Two or more matching users is ambiguous and stays unlinked. That covers a
  shared display name, and text equal to one user's name and another's
  email.
- A disabled user is not matched. The API refuses a disabled owner (422),
  so linking one would create a record no edit could keep.
- Empty text stays empty.

`scripts/owner-link-dry-run.sql` applies the same predicate read-only and
prints counts per tenant (matched, ambiguous, unmatched, empty), never
text, names or emails.

## D3: the down migration names a picked owner in the empty text

A risk or vendor whose owner was picked after the migration has empty free
text (D1). Dropping `owner_user_id` would lose that owner, so the down
migration first writes the owner's display name (else email) into empty
text. Text that is set is never overwritten, which keeps every
migration-linked record exact.

## D4: validation and audit

- Create and edit refuse an `owner_user_id` that is not an active user of
  the tenant with 422, through the same `UserExistsInTenant` query the
  control owner assignment uses (#2128). A non-UUID is 400.
- A vendor edit checks the owner only when it changes, so editing another
  field of a vendor whose owner has since been disabled is not refused.
- Risk: an owner change is a `fields_changed` risk audit row with the
  `owner_user_id` key (and `treatment_owner` when picking clears text). The
  unified log's `changed_fields` (#2367) names those keys; values stay on
  `GET /v1/risks/{id}/audit-log`.
- Vendor: vendors had no audit table, so an owner change writes one
  `me_audit_log` row, action `vendor_owner_update`, before/after
  `{vendor_id, owner_user_id, owner_unlinked}`, as the tenant-setting
  changes do (#2201, #2224). The unified-log payload gate (#2379) treats
  `me` rows as visible only to their actor or an admin, so the payload is
  not widened.

## D5: the web forms

The risk form's "Treatment owner" text input and the vendor form's "Owner
(email)" input become the entity picker (`kinds=["user"]`). The vendor
form's email check (ATLAS-032) is removed with the input it checked. A
risk needs an owner to save, but an unlinked owner stands in for one, so
an edit of another field on a migrated risk is not blocked. The risk page
shows the unlinked text with a "Pick the user" control that saves the
owner at once.

## Follow-ups not done here

- The MCP `create_risk` and treatment proposal still take a free-text
  `treatment_owner`. On a risk with an owner user it is stored but not
  shown. Moving MCP to `owner_user_id` is a separate change.
- The risk History section shows an `owner_user_id` change as raw ids.
- The risk and vendor exports carry the free text only.
