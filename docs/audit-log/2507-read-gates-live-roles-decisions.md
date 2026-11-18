# #2507 — the last `len(OwnerRoles) > 0` read gates count live roles — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2507 (follow-up
from #2505; scope addition from the #2508 review)
**Shape:** one shared integration fixture (`internal/api/liveroletest`), one
new helper (`authz.HasAnyTenantRole`), and the sixteen remaining read gates
moved onto it or onto a narrower helper. The #2505 ban-test allow-list loses
every `readGate2507` entry; the gates join the #2428 agreement table
(`routes2428`) and the #2447 site inventory.

detection_tier_actual: manual_review
detection_tier_target: unit

(The defect, a gate counting a session's role claim, was found by the #2505
scan, a review-time inventory. The #2428 agreement test would have caught
each gate's OPA mismatch had the gates been in its table, which they now are.)

## D1 — the shared fixture

`internal/api/liveroletest` (`//go:build integration`, like `dbtest`) has
`Attach(t, srv, app)` (the real OPA bundle with the DB roles resolver) and
`Bearer(t, srv, tenant, roles...)` (a seeded `users` row, its `user_roles`
rows, Subject `user:<uuid>`). It is the #2556 `liveRoleBearer`, generalised
and split in two: `Attach` must run before `HTTPHandlerForTests`, and a test
that needs several bearers mints them after one `Attach`. `Bearer` refuses a
non-canonical role, so a fixture can no longer carry `"owner"`. The two
`liveRoleBearer` copies (risks, decisions) are gone.

It cannot be used by `package api` tests: it imports `internal/api`. None of
the gates here are reached from those.

## D2 — `HasAnyTenantRole`

`IsTenantApprover(cred)`, or any **canonical** role among
`TenantRoleLabels(cred)`. So: live `user_roles`, plus the credential's own
label only when it names no user (unbound key, client_credentials). The old
gate admitted any non-empty label, including free text; `BuildInput` drops a
non-canonical role before OPA, so OPA already refused such a caller on every
route here. Counting only canonical roles makes the gate agree.

## D3 — gate per route

OPA input: `resource.type` is the first path segment after `/v1/`, action is
`read` for GET. Roles: A admin, G grc_engineer, CO control_owner, AU auditor,
V viewer.

| Gate (file)                                  | Route                                              | Old gate                    | New gate             | OPA admits                                    |
| -------------------------------------------- | -------------------------------------------------- | --------------------------- | -------------------- | --------------------------------------------- |
| actionplans `requireProgramRead`             | GET /v1/action-plans, /{id}                        | any claim/label             | `IsTenantApprover`   | A, G                                          |
| actionplans `requireProgramWrite`            | POST/PATCH/DELETE /v1/action-plans..., link/unlink | any claim/label             | `IsTenantAdmin`      | A                                             |
| actionplans `PlansForRisk`                   | GET /v1/risks/{id}/action-plans                    | any claim/label             | `IsTenantRiskReader` | A, G, V                                       |
| actionplans `PlansForControl`                | GET /v1/controls/{id}/action-plans                 | any claim/label             | `HasAnyTenantRole`   | all five                                      |
| audit `samplesHasProgramRead`                | GET /v1/admin/samples/export                       | any claim/label             | `IsTenantApprover`   | A, G                                          |
| evidence `evidenceHasProgramRead`            | GET /v1/admin/evidence/export                      | any claim/label             | `IsTenantApprover`   | A, G                                          |
| exceptions `exceptionsHasProgramRead`        | GET /v1/admin/exceptions/export                    | any claim/label             | `IsTenantApprover`   | A, G                                          |
| policies `policiesHasProgramRead`            | GET /v1/admin/policies/export                      | any claim/label             | `IsTenantApprover`   | A, G                                          |
| checklist `LoadGeneration`, `ExportMarkdown` | GET /v1/controls/checklist/{id}[/export.md]        | approver or any claim/label | `HasAnyTenantRole`   | all five                                      |
| controldetail `hasControlRead`               | GET /v1/controls/{id}/policies, /risks, /history   | any claim/label             | `HasAnyTenantRole`   | all five                                      |
| (same gate)                                  | GET /v1/evidence, /v1/evidence/{id}                | any claim/label             | `HasAnyTenantRole`   | A, G, CO; AU for its own period's records     |
| controls `controlsHasProgramRead`            | GET /v1/controls/export, /history/export           | any claim/label             | `HasAnyTenantRole`   | all five                                      |
| dashboard `hasProgramRead`                   | GET /v1/activity, /v1/frameworks/posture           | any claim/label             | `HasAnyTenantRole`   | all five (posture: catalog, anyone signed in) |
| evidencesummary `hasControlRead`             | GET /v1/controls/{id}/evidence-summary             | any claim/label             | `HasAnyTenantRole`   | all five                                      |
| evidencesummary `PortfolioEvidenceSummary`   | GET /v1/evidence-summary/portfolio                 | (shared control-read gate)  | `IsTenantApprover`   | A, G                                          |
| gapexplain `hasControlRead`                  | GET /v1/controls/{id}/gap-explanation              | any claim/label             | `HasAnyTenantRole`   | all five                                      |
| oscalcomponents `hasOscalRead`               | GET /v1/oscal/component-definitions[/{id}]         | any claim/label             | `IsTenantApprover`   | A, G                                          |
| oscalprovenance `hasOscalRead`               | GET /v1/oscal/imported-profiles/{id}/provenance    | any claim/label             | `IsTenantApprover`   | A, G                                          |
| personnelsecurity `hasProgramRead`           | GET/POST /v1/personnel-security/...                | any claim/label             | `IsTenantApprover`   | A, G                                          |

Every row but the evidence one is in `routes2428`, where
`TestTenantGates2428_HandlerAgreesWithOPA` checks the handler and OPA admit
the same roles.

## D4 — split shared gates where OPA reads differently

- **Action plans.** One `hasProgramRead` served three resource types. The
  plan list on a risk or a control now follows that record's read gate (it is
  a section of the record's page), and `/v1/action-plans` follows OPA's
  `action-plans` rule. This answers #2563 D5 for `GET
/v1/risks/{id}/action-plans`: viewers read it, as they read the risk.
  Writes are admin only: `action-plans` is not in `grc_writable_resources`,
  so grc_engineer was refused by OPA already.
- **Evidence-summary portfolio.** It reused the single-control guard; OPA
  reads `evidence-summary` for A and G only. It gets its own approver check.
- **controldetail evidence routes** stay on the shared any-role gate. OPA
  refuses V there and admits AU per audit period (resource attrs), which no
  role-only gate can express. OPA is the narrower layer on those two routes;
  `read_gates_2507_test.go` pins the combined result. A handler wider than
  OPA admits nothing OPA refuses, since authzmw runs first on every route.

## D5 — bound API keys (scope addition from #2508)

The comment asks that `IsTenantAdmin/Approver/ControlOwner` and
`TenantRoleLabels` use one rule for a key bound to a user. #2574 (#2541)
already landed that rule before this work: `ownerLabelIsGrant` excludes a
bound key's label, so every helper counts a bound key's owner's live roles
alone. `HasAnyTenantRole` is built from `IsTenantApprover` and
`TenantRoleLabels`, so it inherits the same rule
(`TestReadGates2507_HasAnyTenantRole` "bound key, auditor label"). Nothing
further to reconcile.

## D6 — fixtures moved, and fixtures left

Moved onto `liveroletest` (claim-only bearers on a server without authz that
reached a #2507 gate): controldetail (both suites), dashboard (both),
oscalcomponents (`ownerServer` became `readerServer`, a live grc_engineer:
OPA does not read oscal for control_owner), oscalprovenance, policies
`empty_set`, and the risks/decisions `liveRoleBearer` callers. evidence's
pool-saturation test drives `controldetail` behind a hand-built credential;
it now carries the live role as authzmw stamps it. Non-canonical `"owner"`
claims became `control_owner`.

Left as they are, each still a claim-only bearer, because none reaches a
gate changed here and each asserts the shape it has:

- `policyacks` `empty_set`: a bootstrap credential with a non-UUID user, on a
  tenant with no `tenants` row; a seeded user is the opposite of its point.
- `risks` `transitions` (`nonApprJWT`) and `category_admin` (`owner`): both
  assert a refusal. A claim with no live role is exactly a revoked user's
  token, and is refused by the live-role gates, which is what the tests
  check. Making them live roles would change what they test.
- The `package api` tests and the other packages using `OwnerFor` with
  `AttachAuthz` already (policies `ack_rate`, policyacks, risks
  `lifecycle`) seed users and attach OPA.

## D7 — gate count, and what is left for a follow-up

The ticket counts 17 gates. The #2505 allow-list as merged (2822e908) held
19 `readGate2507` entries, keyed by function, so the two checklist handler
methods and actionplans' separate read and write gates each count. #2563
(c3364af8) moved three of them (risks, decisions, orgunits), leaving 16;
all 16 moved here, and the allow-list has no `readGate2507` entry left.

Not done here, and left for a follow-up ticket: folding the other
hand-rolled live-role fixtures into `liveroletest` (`issueRoledUserBearer`
in policies `ack_rate`, the `user_roles` seeding in policyacks and risks
`lifecycle`, `roleUser` in risks `unified_audit`), and the policies suite's
claim-only bearers (`discard_draft`, `import`, `review_date`, which run on
`setupWith(t, false)`). None of them reaches a gate this PR changed, and
each already passes; moving them is a fixture refactor with its own review,
not a step of the gate swap.

## D8 — the ban test

`ownerRolesReads2505` has no `readGate2507` entries left. What remains is the
non-gate reads (minting, display, persistence, policy-ack free text), each
with its reason, unchanged from #2505.
