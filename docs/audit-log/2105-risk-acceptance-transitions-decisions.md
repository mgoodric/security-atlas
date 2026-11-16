# Risk treatment and acceptance transitions — decisions log (#2105)

A risk's treatment and acceptance change through five transitions:
change treatment, and propose, approve, reject and revoke acceptance.
Each has its own route under `PATCH /v1/risks/{id}/...`, and each writes
one `risk_audit_log` row in the same transaction. Approval follows the
shared approval rule (`internal/approval`, ADR 0024). The MCP
`update_risk_treatment` applier goes through the same transitions. The
risk page offers them as its header's lifecycle action and overflow
menu. Parent: #2071 (finish the record lifecycles).

detection_tier_actual: integration
detection_tier_target: integration

One existing test asserted the old contract and was rewritten:
`TestApplyRiskTreatment_UntreatedDefaultAndValidation` (MCP applier)
proposed an acceptance with no justification, approved one from a
machine credential with a free-text accepter, and replaced an approved
acceptance with `avoid`. Under #2105 the state machine and the approval
rule refuse all three. The integration tier caught the change, which is
where it belongs.

## Decisions

**D1: Routes are `PATCH /v1/risks/{id}/treatment` and
`PATCH /v1/risks/{id}/acceptance/{propose,approve,reject,revoke}`.**
PATCH matches the exception transitions (`PATCH /v1/exceptions/{id}/approve`
and so on). The terminal segment derives the OPA action: `approve` and
`revoke` become those actions, and propose, reject and treatment stay
`write`. grc_engineer holds `approve`. Its `revoke` admit had been bound
to exceptions (#2113), so it gains the same admit bound to `risks`
(`policies/authz/grc_engineer.rego`, test
`internal/authz/risk_acceptance_2105_test.go`). Approve, reject and
revoke also require the approver flag in the handler, as the exception
and policy approvals do.

**D2: A rejected or revoked acceptance leaves the risk `untreated`.**
The ticket does not say where a risk lands. Restoring the treatment from
before the proposal would need a stored prior treatment, and that
treatment may no longer validate: a `mitigate` whose links were removed
since. `untreated` means no decision stands. The risk counts as open
exposure (`IsOpenTreatment`) until its owner decides again. The audit row
keeps the ended acceptance in its `before` state: justification,
proposer, proposed expiry, and for a revoke the accepter, expiry and
self-approval.

**D3: A risk with an acceptance in flight changes treatment only by
rejecting or revoking it first (409).** Otherwise change treatment would
end an approved acceptance with no reason recorded, and a proposal with
no approver's decision. Propose is likewise refused while an acceptance
is proposed or approved.

**D4: The acceptance record lives on `risks`.** New columns:
`acceptance_justification`, `acceptance_proposed_until`,
`acceptance_proposed_by`, `acceptance_proposed_at`,
`acceptance_approved_at`, `self_approved`. A risk has at most one
acceptance at a time, and the audit log holds the history, so no
separate table is needed. The approved expiry and approver stay in the
existing `accepted_until` and `accepter`, which the board pack, exports
and the `risks_accept_fields_required` CHECK already read. The approver
becomes the `accepter`, recorded as their users(id). The free-text
accepter the MCP tool used to take is refused.

**D5: The flag is `self_approved`, as on policies, framework scopes and
exceptions.** Naming it the same lets the shared
`self_approval_requires_setting` trigger guard risks too: a direct write
cannot flag a risk self-approved while its organisation has Allow
self-approval off. A CHECK keeps the flag on approved acceptances with an
accepter.

**D6: The proposer is `credstore.PrincipalOf(cred)`, and approval runs
`approval.Check(proposed_by, approver, allowSelf)` in the transition's
transaction.** This is the same requester and approver shape the
exception and policy approvals use. A machine approver is refused with
403 (`only a person can approve`). The proposer's own approval is a 403
unless self-approval is on, and then it is flagged on the risk, on the
wire (`acceptance.self_approved`) and in the audit row. Acceptances
proposed before this change have no recorded proposer, so the rule has
nothing to compare for them. Any person may approve them, as with
exceptions requested before #2101. The transitions' audit rows name the
person too (the principal, not the session's credential id that
`fields_changed` rows carry), so History shows who proposed and who
approved in the terms the rule compared.

**D7: The MCP applier maps its input onto the transitions, and the
confirming person is the actor.** `acceptance_status=proposed` proposes,
with `justification` (new on the tool schema) and `accepted_until` as
the proposed expiry. `approved` approves the risk's proposed acceptance,
with the confirming person as approver. Any other treatment is a change
of treatment. Before calling the applier, `Store.ConfirmAs` sets
`Proposal.Confirmer` (users(id) plus credential id), because an approval
needs a person and `human_approver` records only the credential. An
`accepter` in the input is refused. Rejecting or revoking stays in the
app. The tool's enum has no value for either, and ending an acceptance
should not be an AI-drafted action. A `treatment_owner` in the input is
applied as a record-field edit (`fields_changed`). The old applier wrote
it unconditionally, which cleared the owner when the proposal sent none.
Now an empty owner leaves it alone.

**D8: No aggregation hooks or rule locks on a transition.** Aggregation
rules read scores and themes, not treatment, so a transition takes only
the risk row lock (`LockRiskByID`). That respects the #2335 lock order
(category share, then rule locks, then the risk row), because no other
lock is taken.

**D9: Expiries must be after today (UTC), with no upper cap.** Exceptions
cap at 365 days because the platform evaluates them. The ticket sets no
cap for acceptances. The approver sees the proposed expiry and sets
their own.

## Follow-ups noted, not done here

- `POST /v1/risks` still accepts `treatment=accept` with
  `acceptance_status=approved` and a free-text accepter at create, which
  skips the approval rule. Closing it changes the create form and the
  demo seed, and it is outside #2105's acceptance criteria. Raised with
  the dispatcher.
- A proposed acceptance made through create carries no proposer, so the
  approval rule has nothing to compare (D6).
