# #2828 — Okta app assignments emit classification and sign-on MFA; cc6_6 MFA on admin apps (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
ADR 0028 check 17 (`docs/adr/0028-payload-checks-run-in-sql-rego-stays-result-only.md`).
The query shape follows the latest stock SQL check, cc8_1 (#2827 / #2946).

- detection_tier_actual: none
- detection_tier_target: contract

No bug surfaced while building. The connector reads an Okta endpoint and
link it had not read before, so the tier that guards them is the contract
tier. `TestPull_SignOnMFAFromDocumentedShapes` replays Okta's documented
shapes, and `TestBuildAppAssignmentRecord_PayloadConformsTo110` runs the
built record through the registry's validator.

Okta's shapes come from Okta's management OpenAPI description
(`okta/okta-management-openapi-spec`, `dist/current/management-minimal.yaml`,
read 2026-11-21). The schemas read were `ApplicationLinks.accessPolicy`
(`AccessPolicyLink`, an `HrefObject`), `AccessPolicyRule`,
`AccessPolicyRuleApplicationSignOn`,
`AccessPolicyRuleApplicationSignOnAccess` (`ALLOW`, `DENY`),
`VerificationMethod` with `PolicyRuleVerificationMethodType` (`ASSURANCE`,
`AUTH_METHOD_CHAIN`, `ID_PROOFING`), `AssuranceMethod`, and
`AssuranceMethodFactorMode` (`1FA`, `2FA`). Also read was the
`list-all-access-policy-rule-response` example, whose only rule is the
catch-all: ALLOW at 1FA. Nothing here was checked against a live org.

## D1 — Schema 1.1.0 of `okta.app_assignment.v1`, not a new kind

The four new fields are optional, so 1.1.0 is additive over 1.0.0
(`CheckAdditiveOver`, pinned by `TestOktaAppAssignment110_AdditiveOver100`).
As with `github.repo_protection.v2` 2.1.0 (#2827 D1), the kind stays
`okta.app_assignment.v1`. The query keeps declaring it, so 1.0.0 records
already in the ledger stay readable, and the kind's routing (#2924) is
unchanged. The connector pushes 1.1.0.

| Field                  | Type                                      | Source                                                      |
| ---------------------- | ----------------------------------------- | ----------------------------------------------------------- |
| `classification`       | `admin` \| `infrastructure` \| `standard` | `--admin-apps`, `--infrastructure-apps` (D2)                |
| `sign_on_policy_id`    | non-empty string                          | the policy id in the app's `_links.accessPolicy.href`       |
| `sign_on_mfa_required` | boolean                                   | that policy's rules, `GET /api/v1/policies/{id}/rules` (D3) |
| `reason`               | non-empty string                          | why `sign_on_mfa_required` is omitted                       |

Each field is omitted when the connector has no value for it, never sent
as a default, the same rule osquery 1.1.0 (#2912) and GitHub 2.0.0 (#2925)
follow.

## D2 — Classification is a connector-side mapping of app ids

The ticket leaves the source open: Okta app labels, or a mapping on the
connector.

- **Connector mapping, chosen.** Two repeatable flags take Okta app ids:
  `--admin-apps` and `--infrastructure-apps`. Once either is set, every
  other app is `standard`. This works on every Okta org and SKU, and the
  operator states the classification in one place, next to the token.
- **Okta-side labels, rejected.** The Application object has no tag or
  label field in the management API. Its `label` is the display name. I
  did not check Okta Identity Governance's resource labels. They would be
  a separate API, and the connector could not assume that product.
- **Ids, not names.** An app's `label` can be renamed, and two apps can
  share one. The id is what `app_id` already keys on, the same choice as
  GitHub's `repo_id` (#2925 D1).
- **Unconfigured means unclassified, not standard.** With both flags
  empty, no app carries `classification`. Otherwise a connector run without
  flags would report every app `standard`, and the control would read
  that as "no admin apps".
- **An id on both lists is admin.** Both classes are in scope for the
  check, so which one wins does not change the verdict.
- **An id that matches no app goes to stderr**, not into a record. The
  platform cannot see an app the connector did not report (the same gap
  as the GitHub inventory, #2834).

## D3 — `sign_on_mfa_required` from the app sign-in policy's rules

Identity Engine links each app to one app sign-in policy (`ACCESS_POLICY`)
through `_links.accessPolicy`. The connector reads that policy's rules.
Each policy is read once per run, because a new app gets the default
policy, which many apps then share.

- **true** when every active rule denies access, or allows it with
  `verificationMethod.type` `ASSURANCE` and `factorMode` `2FA`.
- **false** when any active rule allows access at `factorMode` `1FA`,
  whatever its conditions. Each active rule is a path some sign-in can
  take, so one 1FA rule means some users reach the app with one factor.
  Okta's own default policy is exactly this: one catch-all rule at 1FA
  (the spec's `list-all-access-policy-rule-response`).
- **omitted, with `reason`**, in these cases:

  - the app has no `_links.accessPolicy`. This is a Classic Engine org, or
    an API service app with no policy assigned (Okta's policies concept
    page says such apps get none by default).
  - the href is not of the form `<base>/api/v1/policies/{id}`.
  - the rules could not be read.
  - an active rule's method is `AUTH_METHOD_CHAIN` or `ID_PROOFING`, it
    has no `factorMode`, it has no `appSignOn`, or its access is not
    ALLOW or DENY.
  - there is no active rule.

  A 1FA rule still decides false when another rule is undecided.

Inactive rules are skipped. Rule `priority` is not used, because any
active rule can match some sign-in. `constraints` are not read either:
`factorMode` alone says how many factors a rule requires, and constraints
narrow which factors count.

The href shape is an assumption. The spec types `accessPolicy` as an
`HrefObject` but gives no example of it, so `<base>/api/v1/policies/{id}`
is inferred from the policies resource path. If a live org sends another
shape, every app reports `reason: unrecognised _links.accessPolicy href`.
The control is then inconclusive, never wrongly passing.

The token needs no new scope. `GET /api/v1/policies/{id}/rules` is under
`okta.policies.read` (spec `listPolicyRules`), which `okta.mfa_policy.v2`
already uses.

Not done, filed as [#2951](https://github.com/mgoodric/security-atlas/issues/2951):

- deciding `AUTH_METHOD_CHAIN` rules, which can require two factors;
- counting MFA that the global session policy requires.

As things stand, an admin app that relies on the global session policy
for MFA fails. Paging the rules list is in
[#2915](https://github.com/mgoodric/security-atlas/issues/2915), which
covers every Okta list call.

## D4 — Query shape (cc8_1 pattern)

- **Rows:** records whose `app_id` is not a non-empty string are left out
  (cc4_1 / cc8_1 D7). `rank()` over `observed_at` takes each app's latest
  records, and a tie keeps every tied record, so the worse verdict wins.
- **Scope:** an app is in scope when its `status` is ACTIVE and its
  `classification` is not `standard`. Okta signs no one in to an INACTIVE
  app. The ticket names admin and infrastructure apps, so a standard app
  is outside the check.
- **Verdict:** each condition is three-valued, 0 fail, 1 inconclusive, 2
  pass:
  - an absent `sign_on_mfa_required` is 1, true is 2, and anything else
    (false, a mistyped value) is 0;
  - `admin` or `infrastructure` takes that MFA score;
  - a mistyped `classification` is 0;
  - no `classification` is `greatest(mfa, 1)`: such an app may be admin,
    so it passes only if it requires MFA, and is otherwise inconclusive.
- **Combine:** with no in-scope app the control is inconclusive. Otherwise
  the minimum decides: 0 fail, 1 inconclusive, 2 pass.
- **Time:** the check has no window of its own. It reads the in-window
  records the engine hands it, and orders them by `observed_at` only to
  pick each app's latest. No payload timestamp is read, so the RFC 3339
  guard (cc4_1) does not apply.

An org where every app requires MFA passes even unclassified. That is
true of its admin apps, so the pass is not vacuous. An org with only
standard apps is inconclusive, not passing: the check saw no admin app to
test.

## D5 — Supersedes the stock bundle

`soc2_cc6_6_mfa_external_threats` changes query language (Rego to SQL) and
description, so its manifest hash changes. The bundle is superseded on the
next boot, and `stock_manifests_gen.go` was regenerated. The upgrade note is
in `changelog.d/2828.changed.md`. Until the connector is upgraded and given
the flags, 1.0.0 records carry neither field and the control stays
inconclusive, as it was before.
