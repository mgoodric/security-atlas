# Fixtures

Built from Okta's management OpenAPI description
(`okta/okta-management-openapi-spec`, `dist/current/management-minimal.yaml`),
read 2026-11-21 (#2828) and 2026-11-22 (#2951). Nothing here was checked
against a live org.

| File                                 | Source                                                                                                                                                                                                                        |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `access_policy_rules_default.json`   | `list-all-access-policy-rule-response` (List all policy rules, ACCESS_POLICY), verbatim minus `_links`: the catch-all rule of a new app sign-in policy, ALLOW with `factorMode` 1FA                                           |
| `access_policy_rules_mfa.json`       | Same shape: a 2FA rule built from `AssuranceMethod` (its constraint is the `createpolicyrule` example's), an INACTIVE 1FA rule, and the catch-all set to DENY (`AccessPolicyRuleApplicationSignOnAccess`)                     |
| `access_policy_rules_chain.json`     | `amc-two-chain` (App sign-in policy - Allow two authentication method chains), minus `_links`, with an id, status and priority added and a DENY catch-all: password then SMS, or Okta Verify with `userVerification` REQUIRED |
| `access_policy_rules_chain_1fa.json` | Same shape: a rule whose `verificationMethod.type` is `AUTH_METHOD_CHAIN` (`AuthenticationMethodChainMethod`) with one chain of one step, the password, and a DENY catch-all                                                  |
| `session_policies.json`              | `list-okta-sign-on-policy-response` (List all policies, OKTA_SIGN_ON), minus `_links`, with the group id set to the Everyone group of `groups_builtin.json`                                                                   |
| `session_rules_default.json`         | `list-all-sign-on-policy-rule-response` (List all policy rules, OKTA_SIGN_ON), verbatim minus `_links`: a catch-all rule with `requireFactor` false                                                                           |
| `session_rules_mfa.json`             | Same, with `requireFactor` true                                                                                                                                                                                               |
| `groups_builtin.json`                | A `Group` of type `BUILT_IN` named Everyone, the shape `okta.mfa_policy.v2`'s lookup reads (#2901)                                                                                                                            |
| `apps.json`                          | `Application` objects with `_links.accessPolicy` (`ApplicationLinks.accessPolicy`, an `HrefObject`). The spec has no list example carrying that link, so the href shape `<base>/api/v1/policies/{policyId}` is assumed        |
