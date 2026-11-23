# #2951 — Okta sign-on MFA: authentication method chains and the global session policy (decisions log)

Follow-up to [#2828](https://github.com/mgoodric/security-atlas/issues/2828)
(`docs/audit-log/2828-okta-app-classification-sign-on-mfa-decisions.md`, D3).

- detection_tier_actual: none
- detection_tier_target: contract

No bug surfaced. The connector reads one more Okta endpoint and one more
rule shape. The tier that guards them is the contract tier:
`TestPull_SignOnMFAFromDocumentedShapes` and
`TestPull_SessionMFAFromDocumentedShapes` replay Okta's documented
examples, and `TestBuildAppAssignmentRecord_PayloadConformsTo120` runs
every record shape through the registry's validator.

The Okta shapes come from Okta's management OpenAPI description
(`okta/okta-management-openapi-spec`, `dist/current/management-minimal.yaml`,
read 2026-11-22). The schemas read were `AuthenticationMethodChainMethod`,
`AuthenticationMethodChain`, `AuthenticationMethod`, `AuthenticatorKeyEnum`,
`OktaSignOnPolicy`, `OktaSignOnPolicyConditions`, `OktaSignOnPolicyRule`,
`OktaSignOnPolicyRuleConditions`, `OktaSignOnPolicyRuleSignonActions`,
`PolicyNetworkCondition`, `PolicyPeopleCondition`,
`PolicyRuleAuthContextCondition` and `IdentityProviderPolicyRuleCondition`.
The examples read were `amc-two-chain`, `list-okta-sign-on-policy-response`
and `list-all-sign-on-policy-rule-response`. The evaluation order was taken
from the Policy tag description: policies, then rules, in priority order,
with priority 1 first; when no rule of a matched policy matches, the next
policy is tried. None of it was checked against a live org.

## D1 — An authentication method chain is MFA when every path takes two factor classes

A chain is a list of steps. Each step lists `authenticationMethods`, and
any one of them satisfies the step. `next` holds the following step
(`AuthenticationMethodChain.next`, at most one item). A rule lists up to
five chains (the spec says 5, not the 3 the ticket quotes), and a sign-in
satisfies one of them. So a sign-in path is one method per step of one
chain.

- **Factor class by authenticator key.** `okta_password` and
  `security_question` are knowledge. `okta_email`, `okta_verify`,
  `phone_number`, `google_otp`, `webauthn`, `security_key`,
  `yubikey_token`, `symantec_vip`, `onprem_mfa` and `custom_app` are
  possession. A possession method with `userVerification: REQUIRED` also
  takes a PIN or biometric, which counts as a second class. That is how
  Okta's `amc-two-chain` example offers Okta Verify alone as the
  alternative to password then SMS.
- **true** when every path of every chain presents two or more classes.
- **false** when some path presents one class, for example a lone
  password, or email then email. This decides false even when another
  chain cannot be read, the same as a 1FA rule (#2828 D3).
- **undecided** when a chain uses `duo`, `external_idp`, `smart_card_idp`
  or `tac`, or a key outside the enum. The factor class of those depends
  on the set-up: Duo and an external IdP may run their own MFA, a smart
  card carries a PIN, and a temporary access code is neither. A chain is
  also undecided when it has more than three steps, a step with no
  method, or more than one `next`.
- **Rejected: count steps.** "Two or more steps" would pass password then
  security question, which is two knowledge factors.
- **Rejected: read `method`.** The method (`push`, `sms`, `signed_nonce`)
  does not change a key's class among the keys classified above, so
  reading it adds nothing.

`ID_PROOFING` stays undecided, as the ticket asks.

## D2 — The global session policy counts, as a separate field

In Identity Engine every sign-in establishes an Okta session under the
global session policy (`OKTA_SIGN_ON`) before any app sign-in policy is
read. If that policy requires MFA for every user, every app's users have
presented two factors. So a 2FA global session counts for cc6_6.

The connector reports it as `session_mfa_required`, with
`session_reason` when it is omitted. It does not fold the verdict into
`sign_on_mfa_required`: the record keeps one verdict per policy, so the
control can say which one it relied on. Schema 1.2.0 of
`okta.app_assignment.v1` adds both fields. They are optional, so 1.2.0 is
additive over 1.1.0 (`TestOktaAppAssignment120_AdditiveOver110`), and the
kind and its routing stay the same (#2828 D1). No `x-assert-format` is
added.

`session_mfa_required` is decided once per run, for the whole org, and
copied onto every app's record:

- **true** when there is an active policy and every reachable active rule
  denies access or sets `requireFactor` true.
- **false** when a reachable active rule allows a session with
  `requireFactor` false or absent (the spec's default is false). Okta's
  `list-all-sign-on-policy-rule-response` example is such a rule. False
  decides even when another rule is undecided.
- **omitted, with `session_reason`**, when there is no active policy, the
  policy or rule list cannot be read, the Everyone group cannot be found,
  or a reachable rule has no `signon` action or an access other than
  ALLOW or DENY.

**Reachable.** The connector does not resolve group membership, so it
counts every active rule as reachable except two cases it can prove from
the policy objects:

- a rule after a catch-all rule in the same policy;
- every policy after one that covers every user and has an active
  catch-all rule. A user who matches no rule of a matched policy goes on
  to the next policy, so the Everyone condition alone does not shadow
  anything.

A policy covers every user only when its sole condition is
`people.groups.include` naming the Everyone group. `PolicyPeopleCondition`
also carries `groups.exclude`, `users.include` and `users.exclude`. A
non-empty one of these, or any other condition, leaves some users to fall
through to a later policy, which may allow one factor. Such a policy
therefore shadows nothing. The first cut checked only `groups.include`,
and the #2997 review found the false pass that followed: an Everyone
policy that excludes a group, with an MFA catch-all, hid a 1FA Default
Policy. `TestPull_SessionMFACases` now covers group, user and network
exclusions.

A rule is catch-all when it has no conditions, or each condition it has
selects everything: `people` with empty user and group lists, `network`
`ANYWHERE`, `authContext` `ANY`, `identityProvider` `ANY` with no ids,
`risk` with no behaviors, `riskScore` `ANY`. The last two are not in
`OktaSignOnPolicyRuleConditions`, but the spec's rule example carries
them. Any other condition counts as not catch-all. When priorities are
missing or two items share one, the order is unknown and nothing is
counted as shadowed.

The Everyone group id comes from the same lookup `okta.mfa_policy.v2`
uses (`GET /api/v1/groups?search=type eq "BUILT_IN"`, #2901), so the
token needs no new scope. The policy list is under `okta.policies.read`.

**Rejected: decide per app from its assigned groups.** That would need
each user's group memberships, plus the users assigned to the app
directly, which the connector does not read. The org-wide verdict errs
toward false. If one reachable rule lets some users establish a session
with one factor, that counts against every app, even an app those users
are not assigned to. The operator can still pass such an app by requiring
2FA in its own sign-in policy, which is Okta's guidance for admin apps.
The control description states this.

**Not read:** `factorPromptMode` and `factorLifetime`. `DEVICE` and
`SESSION` remember a factor for a time. That is the same allowance as an
app sign-in rule's `reauthenticateIn`, which #2828 does not read either.

## D3 — The query takes the better of the two policies

The `mfa` score of each app record (0 fail, 1 inconclusive, 2 pass):

1. A present `sign_on_mfa_required` or `session_mfa_required` that is not
   a boolean scores 0, whatever the other says. This keeps the rule that a
   mistyped field fails.
2. Either one true scores 2.
3. Otherwise, either one absent scores 1.
4. Both false scores 0.

Scope, classification, recency and the combine step are unchanged from
#2828 D4.

**A change in behaviour for older records.** A 1.1.0 record carries no
`session_mfa_required`, so an admin app at `sign_on_mfa_required: false`
is now inconclusive, not failing. Its global session policy may require
MFA, and a 1.1.0 connector did not look. This is the false fail the
ticket describes, and the control no longer reports it. A 1.2.0 connector
reports false when the session policy does not require MFA, and the app
then fails as before.

## D4 — Supersedes the stock bundle

The query and description of `soc2_cc6_6_mfa_external_threats` changed,
so its manifest hash changed. `scripts/gen-stock-control-hashes.sh`
appended the new hash to `stock_manifests_gen.go`, and the list stays
append-only (#2963). The bundle is superseded on the next boot. The
upgrade note is in `changelog.d/2951.changed.md`.
