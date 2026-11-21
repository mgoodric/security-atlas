# Issue #2842 — Risk import: treatment and acceptance under the create rules

The treatment slice of spec #2812 (Q14), on top of the tracer (#2839),
scores (#2841) and the review page (#2847).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — An accepted row without a justification or until-date is rejected

**Decision.** A row whose treatment is accept must carry a justification and
an accepted-until date. Without either, or with an until-date that is not a
date, the row is rejected at upload with the new `invalid_acceptance` reason.

**Why.** The spec makes an accepted row a proposed acceptance, and a proposal
needs both (`risk.ValidateProposal`, the rule `/acceptance/propose` and
`POST /v1/risks` share). Staging such a row would leave it pending forever,
refused on every approve. Downgrading it to untreated, as mitigate and
transfer are, was considered and not chosen: the spec names only those two
downgrades, and the `treatment_downgraded` flag means exactly them. A
rejected row is listed with its reason and can be fixed and re-uploaded.

An until-date in the past is not checked at upload. The create path checks
it against the day of approval, so the row stays pending and the approve
reports the refusal. Checking it at upload as well would put a second copy
of a time-relative rule in the import.

## D2 — The import row's approver is the acceptance's proposer

**Decision.** The person who approves the import row creates the risk, so the
create records them as the acceptance's proposer, as it does for any create
with `acceptance_status=proposed`. The approval rule then refuses that person
as the acceptance's approver unless self-approval is on.

**Why.** The uploader may be a GRC engineer or a script, and the spec routes
every risk creation through a person's approval. That person is the one who
put the proposal into the tool. The integration test asserts the import
approver is refused (403) and a second approver succeeds.

## D3 — The register's accepter is history on the staged row, not on the risk

**Decision.** The accepter column is kept in `historical_accepter` on the
staged row and shown on the review page as "Accepter (historical)". It is
never sent to the create, which refuses an accepter (#2371).

**Why.** Risks have no field for a historical accepter, and the spec puts
custom fields on risks out of scope. The staged row is linked to the risk it
created (`risk_id`) and outlives the decision, so the history stays traceable.

## D4 — Linked controls resolve to active controls, by id or bundle id

**Decision.** Each ref in the linked-controls cell (split on `,`, `;` and line
breaks) resolves to the tenant's control whose id it is, when it parses as a
uuid, and otherwise to the control whose `bundle_id` it is, exactly. Only an
active version (`superseded_by IS NULL`) resolves. Refs that match nothing
are kept in `unresolved_controls` and shown; they never reject the row.

**Why.** A superseded version is history; linking a new risk to it would
point the mitigation at a control the tenant no longer runs. Bundle ids are
case-sensitive natural keys, so matching ignores no case. A partly-resolved
cell keeps its resolved links and the treatment, because the risk does have a
mitigation the tool can see.

At approval the staged links are re-read and only controls that still exist
are linked, so a control deleted since upload cannot fail the insert. If that
leaves a mitigate row with no link, the create path refuses it
(treatment validation) and the row stays pending with the reason, which is
now a row refusal rather than a stopping error.

## D5 — Justification and until-date on a row that is not accepted

**Decision.** They are kept on the staged row and not sent to the create.

**Why.** A create refuses a justification or proposed expiry with any status
but proposed. Rejecting the row for a stray cell would cost the risk; sending
them would fail the approve.

## D6 — Export round trip

The risk export's `treatment`, `accepter`, `instrument_reference` and
`accepted_until` columns are now mapped, so they no longer land in unmapped.
An exported risk with an approved acceptance re-imports only with a
justification column added, because the export carries none (D1).
