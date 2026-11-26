# #2668 — Pin the acknowledged version, show "acknowledged" after reload, surface evidence_emission_ok

detection_tier_actual: none
detection_tier_target: none

Follow-ups from the review of #2663 (#2465).

## D1 — the pin compares against the row the id names

A policy row is one version: `policy_version_id` is the row id
(`InsertPolicyAcknowledgment` writes `PolicyVersionID: in.PolicyID`), and
publishing a new version supersedes the old row rather than editing it. So
an ack POSTed to a row that was re-published since page load was already a
409 (`ErrAckPolicyNotPublished`, the slice-023 anti-criterion). The pin adds
the other half: the body names the version the caller was shown, and
`AckStore.Record` refuses with `ErrAckVersionMismatch` (409) when that is not
the row the URL names. The web app sends the pending item's
`policy_version_id`, so the click can only ever record the version the card
displayed. The body stays optional; an empty body acknowledges the id, as
before.

Not done: `GetPolicyForAcknowledge` reads the row without a row lock, so a
publish committing between that read and the ack insert is still a narrow
window. Closing it needs `FOR SHARE` on that query and an sqlc regeneration;
it is left out of this change.

## D2 — `?include=current` on the pending list, not `my_ack` on the policy

The ticket offered either. `ListPendingAcksForUser` already returns the
caller's latest ack for every published policy in their audience, and
`PendingForUser` dropped the fresh ones in Go. `AckStore.ForUser` keeps them
as `Current`, and `GET /v1/me/acknowledgments?include=current` returns them.
That reuses one query and the audience rule, and touches neither the policy
detail handler nor its BFF. The detail page already reads this endpoint, so
it asks for `include=current` and shows "You acknowledged version N on …"
after a reload. The #2465 decisions log D2 named this read as missing; it is
no longer.

`current` is absent without the include, so existing callers see the same
body.

## D3 — the evidence warning is a different toast message

The toast system takes only a message, with no tone. When
`evidence_emission_ok` is false the toast reads "Policy acknowledged, but its
evidence record was not saved", and the card still shows the acknowledgment,
since the ack row is authoritative.
