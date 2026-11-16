# #2371: risk create cannot approve an acceptance — decisions log

`POST /v1/risks` and MCP `create_risk` accepted `treatment=accept` with
`acceptance_status=approved` and a free-text `accepter`. That recorded an
approval that never went through the shared approval rule (`internal/approval`,
ADR-0024). The review of #2370 (#2105) added three items to this ticket: an
empty proposer, the MCP proposer, and the MCP confirmer fallback.

- detection_tier_actual: manual_review
- detection_tier_target: integration

## D1: create may propose, never approve

A create may set `acceptance_status=proposed` and nothing beyond it.
`approved`, `accepter` and `accepted_until` are refused with 400
(`treatment_validation_failed`, the code create already uses). The check is
`risk.ValidateCreateAcceptance`, called by `Store.Create` and by the MCP
`create_risk` applier. The MCP tool schema offers only `proposed`.

Refusing `accepted_until` along with `accepter`: both are recorded only by
the approval, so a create naming either is asking to skip it.

Seeds are not a path. The demo seed and the e2e fixtures never set
`acceptance_status`, and the DB CHECK `risks_acceptance_status_iff_accept`
means they cannot create an accepted risk without setting it. No other
insert path writes `acceptance_status`.

## D2: the creator is the proposer

A risk created with a proposed acceptance records its creator
(`credstore.PrincipalOf`, the value the propose transition records) in
`acceptance_proposed_by`, with `acceptance_proposed_at`. The approval rule
then refuses the creator as approver unless the organisation allows
self-approval. `Store.Create` refuses a proposed acceptance with no
proposer, so no new row can reach D3's case.

No justification or proposed expiry is required at create, which the
propose transition does require. That would be a breaking change to create
beyond this ticket, and the approver sets `accepted_until` regardless.

## D3: an acceptance with no comparable proposer is refused at approval

`approval.Check` returns `(false, nil)` for an empty requester, which admits
any approver, the creator included. The risk acceptance now refuses it, as a
framework scope with no submitter is refused: `ErrAcceptanceProposerUnknown`,
409, nothing written. A pre-#1807 session proposer (`jwt:...`,
`approval.ErrRequesterUnknown`) maps to the same error. Before this change
#2370 mapped it to the self-approval refusal (403), which named the wrong
cause.

The way out is to reject the acceptance and propose it again, which records
a proposer. The error says so. `approval.Check` itself is unchanged: policies
deliberately treat an empty submitter differently (`policy.ErrSubmitterUnknown`
covers only some approvers).

The check order puts "only a person approves" first, so a machine credential
gets the same refusal whatever the proposer.

Migration `20261206000000` says that existing proposed acceptances with no
proposer "admit any person". Migrations are not edited after they ship; this
log supersedes that sentence.

## D4: the MCP proposer is the confirming person

An MCP proposal's `created_by` is the credential id it was filed under. For a
browser caller that is a per-session `jwt:<jti>`, which names no person, and
the approval rule reads that prefix as unknown. The person who confirms the
AI-assisted proposal is the one deciding to make it. So the confirmer is the
proposer of record, for both `create_risk` and `update_risk_treatment`
(propose). The approver must then be a different person, unless
self-approval is on.

## D5: one actor value across REST and MCP

The MCP applier recorded the actor as `Confirmer.UserID`, else
`Confirmer.CredentialID`. REST records `credstore.PrincipalOf`: the person,
else `cred.UserID` (an unbound key's id, `oauth_client:<id>`,
`bootstrap-admin`), else `cred.ID`. For a person the two agree. For a machine
they differ: a key's credential id rather than its bound principal.

`writeproposals.Confirmer` now carries `Principal`, which the confirm handler
sets with `credstore.PrincipalOf`, and the appliers record
`Confirmer.Actor()`. `Store.Confirm`, used by callers with only a credential
id, falls back to that id.

## Existing data: counts only, no change

Run read-only against a deployment. Nothing here was run against
production, and any data change needs Matt's approval of the exact counts.

```sql
-- Approved acceptances, by tenant:
--   approved_outside_rule: never went through the #2105 approve transition
--     (no acceptance_approved_at): approved at create, by PATCH or by the
--     MCP treatment tool before #2105.
--   approved_at_create_upper_bound: of those, the ones with no audit row
--     touching acceptance_status. Upper bound: writes before
--     20261202000000 (fields_changed) left no audit row.
SELECT r.tenant_id,
       count(*) AS approved_total,
       count(*) FILTER (WHERE r.acceptance_approved_at IS NULL) AS approved_outside_rule,
       count(*) FILTER (
         WHERE r.acceptance_approved_at IS NULL
           AND NOT EXISTS (
             SELECT 1 FROM risk_audit_log l
             WHERE l.tenant_id = r.tenant_id AND l.risk_id = r.id
               AND l.after_state ? 'acceptance_status')
       ) AS approved_at_create_upper_bound
FROM risks r
WHERE r.acceptance_status = 'approved'
GROUP BY r.tenant_id
ORDER BY approved_total DESC;

-- Proposed acceptances that D3 now refuses until re-proposed:
SELECT tenant_id, count(*) AS proposed_without_proposer
FROM risks
WHERE acceptance_status = 'proposed'
  AND (acceptance_proposed_by = '' OR acceptance_proposed_by LIKE 'jwt:%')
GROUP BY tenant_id;
```
