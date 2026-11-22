# Issue #2843 — Risk import: owners matched by email, unlinked owners flagged

The owner slice of spec #2812 (Q15), on top of the tracer (#2839), scores
(#2841), treatment (#2842) and the review page (#2847).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Only an exact email match links; anything else is unlinked

**Decision.** An owner cell links when, trimmed and compared case-insensitively,
it equals the email of exactly one active user of the uploading tenant. A
display name, a disabled user's address, another tenant's address, or an
address no user has is kept verbatim as the unlinked owner and the row gets
the `owner_unlinked` flag.

**Why.** The spec says "matched by email". Matching display names as well
(as migration `20261226000000_risk_vendor_owner_users` does for legacy text)
was not chosen: names are not unique, and a wrong link silently assigns a
risk to the wrong person, where an unlinked owner is visible and flagged.
`users_email_per_tenant_unique` is case-sensitive, so two users can share an
address up to case; such an address links to neither.

## D2 — `treatment_owner` is an owner alias

**Decision.** The risk export's free-text owner header, `treatment_owner`,
maps to the owner field, so it no longer lands in unmapped on a round-trip.

**Why.** An export re-imports with no column map (#2840). The export's linked
owner is `owner_user_id` (a uuid of the source tenant) and `owner_name`; a
uuid from another tenant cannot be matched by email, so both stay unmapped
and a linked owner does not round-trip as linked. That gap is recorded here,
not fixed: the export carries no owner email column.

## D3 — An owner disabled after upload is created unlinked

**Decision.** Approval re-reads the staged owner. If the user is no longer
active, the risk is created with that user's email as the unlinked owner;
the row is not refused.

**Why.** The create path refuses an inactive `owner_user_id`
(`ErrOwnerNotActiveUser`), which is not a row refusal the approve-all run
skips, so it would abort the batch. The spec's rule is that an owner matching
no active user becomes the unlinked owner; applying it at approval keeps the
name and lets the row through. A user deleted outright leaves the risk with
no owner, as no text for them was staged.
