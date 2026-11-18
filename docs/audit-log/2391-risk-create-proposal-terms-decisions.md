# #2391: a create proposes on the propose transition's terms — decisions log

Follow-ups from the review of #2376 (#2371). This log supersedes the last
paragraph of #2371's D2 ("No justification or proposed expiry is required
at create").

- detection_tier_actual: manual_review
- detection_tier_target: integration

## D1: require the terms, don't refuse `proposed` at create

The ticket offered two shapes: require justification and proposed expiry
at create, or refuse `acceptance_status=proposed` at create and route
through `/acceptance/propose`. Requiring them keeps the web form's
one-step "Accept (proposed, pending approval)" and the MCP `create_risk`
tool working, and a client that already sends `proposed` needs two new
fields rather than a second call.

One rule, shared: `risk.ValidateProposal` is what `ProposeAcceptanceTx`
checks, and `risk.CreateProposal` calls it for both create paths
(`Store.Create`, the MCP `create_risk` applier). The insert records the
justification and proposed expiry in the columns the transition writes.

## D2: the field names

REST create takes `justification` and `proposed_until`, the names
`/acceptance/propose` takes. MCP `create_risk` takes the same names.
`update_risk_treatment` carries a proposal's expiry in `accepted_until`;
`create_risk` refuses `accepted_until` (#2371 D1), so reusing that name
for a proposed expiry would read as the thing create refuses.

## D3: either field without a proposal is refused

A create with `justification` or `proposed_until` and no
`acceptance_status=proposed` is a 400, rather than silently dropping the
text. The treatment check runs first, so the existing refusals keep their
`treatment_validation_failed` code.

## D4: MCP refuses at filing

`validateToolInput` checks a `create_risk` proposal with
`treatment=accept` when it is filed (ErrInvalidInput, 400), so the
assistant gets the error, not the person asked to confirm. The proposer
(the confirmer) is not known at filing; the applier checks again at
confirm with the real one.

## D5: unbound machine credentials may still propose

The dispatcher's brief asked whether an unbound machine credential should
be refused a proposal (as #2597 made exception lifecycle moves
people-only). Risk propose has never been people-only, and approval
already is. Changing who may propose is outside this ticket; it is left
as is and raised for a decision.
