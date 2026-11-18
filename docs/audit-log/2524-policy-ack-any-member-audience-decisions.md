# 2524 — Any member acknowledges a policy aimed at them; audience is a role or everyone: decisions log

Issue: mgoodric/security-atlas#2524, Matt's decision from the review of #2508
(#2505). OPA let only admin and grc_engineer `POST /v1/policies/{id}/acknowledge`
(a `policies` write), and a policy's free-text audience ("employee") never
matched a session, whose roles are the canonical enum.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2508.
- **detection_tier_target:** integration.
  `TestAcknowledge2524_RoleMatrix` runs viewer, auditor and control_owner
  through the real authz middleware with live `user_roles`. With the rego
  rule removed it fails (403 on every targeted ack, everyone rate 0/5); with
  it, it passes.

## Already done before this change

- **#2530 (#2523)** moved the attestation owner-role gate onto live roles. It
  did not touch policy acknowledgment; the "who can acknowledge" box was still
  open, so this change adds the rego rule.
- **#2505 / #2508** made the ack match `authz.AckRoleLabels` (live roles, never
  a session's role claim). **#2574 (#2541)** dropped a bound key's canonical
  label unless its owner holds the role live. Both are used as they are.

## Decisions

### D1 — one narrow rego rule in `defaults.rego`, mirrored

Any credential with at least one role may `POST /v1/policies/{uuid}/acknowledge`,
anchored by a regex like the #2110 and #2523 path rules. It lives in
`defaults.rego` beside the other every-role rules (`/v1/people`,
`/v1/approvals`), not in each role file, and is appended byte-identical to
`policies/authz/defaults.rego` (#2410). No other `policies` write is granted:
`TestDecide2524_AcknowledgeRuleIsNarrow` pins create, PATCH, DELETE and
look-alike paths as still denied for viewer, auditor and control_owner.

### D2 — self-only by construction

The handler records the ack for `credstore.CallerUserID(cred)` and reads no
user id from the body. The matrix test posts `{"user_id": <another user>}` and
checks the row names the caller. No on-behalf-of path is added.

### D3 — "everyone" is a stored value, matched by membership

The enum is the five canonical roles plus `everyone` (`policy.AudienceEveryone`).
A caller matches `everyone` when it is a member, meaning `authz.TenantRoleLabels`
is non-empty (a live role, or an unbound key's own label). `policy.WithEveryone`
appends `everyone` to the caller's ack labels, so the pending-list SQL (`&&`),
`rolesIntersect` and therefore the badge count all match it with no new query.
A bound key whose only label is free text, and whose owner holds no live role,
is not a member.

The rate roster (denominator and numerator, both the per-policy endpoint and
`?include=ack_rate`) counts every live key holder for an `everyone` policy, so
its rate is not always 0. The roster is still the slice-023 `api_keys` stand-in;
moving it onto `user_roles` is outside this ticket.

### D4 — untargeted policies are refused (403)

A published policy with an empty audience is aimed at no one, so a non-admin's
ack is refused (`ErrAckNotRequired`, 403), as it already was. An ack should mean
"this was aimed at me". The `IsAdmin` (super-admin flag) wildcard is unchanged:
`TestRolesIntersect` pins it, and it is not a tenant role.

### D5 — the enum is enforced on create and PATCH; legacy values stay

`POST /v1/policies` refuses any audience outside the enum (400). `PATCH` refuses
a value outside the enum unless the draft already carries it, so a draft written
before #2524 stays editable without first changing its audience. The admin
import (`/v1/admin/policies/import`), the stock seed and demoseed are not gated:
import carries a historical library as it was, and the UI flags what it brings.
Gating import is a possible follow-up.

The web form offers the roles plus "Everyone (all members)", shows any legacy
value it finds as a ticked box marked "unrecognised audience" (unticking drops
it; the form never adds one), and refuses a create or new version that still
carries one. The detail page gains an Audience row with the same flag.

### D6 — no live data migration; stock templates updated

Nothing rewrites an existing policy. `scripts/dry-run-2524-policy-audiences.sql`
is read-only and prints only distinct values and counts. The proposed mapping
("employee", "all", "all_staff", "contractor" → `everyone`; anything else left
and flagged) is in the PR for Matt; applying it is a separate step.

The stock templates are new-tenant seed content, not live data. Access Control
and Information Security targeted "employee, contractor" and now target
`everyone`. Change Management (engineer, engineering_lead, sre), Incident
Response (on_call, security_lead, engineering_lead) and Vendor Management
(procurement_owner, security_lead) name team roles with no obvious enum
equivalent; they are left, will show as unrecognised, and are raised in the PR.

### D7 — `policy_ack_due` producer not added

Matt's comment on the ticket suggests a `policy_ack_due` notification producer
now that any member can acknowledge. It is a separate feature (a new producer,
recipient gating per #2175), so it is left for its own ticket.
