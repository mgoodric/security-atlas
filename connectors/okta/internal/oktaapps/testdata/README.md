# Fixtures

Built from Okta's management OpenAPI description
(`okta/okta-management-openapi-spec`, `dist/current/management-minimal.yaml`),
read 2026-11-21 (#2828). Nothing here was checked against a live org.

| File                               | Source                                                                                                                                                                                                                 |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `access_policy_rules_default.json` | `list-all-access-policy-rule-response` (List all policy rules, ACCESS_POLICY), verbatim minus `_links`: the catch-all rule of a new app sign-in policy, ALLOW with `factorMode` 1FA                                    |
| `access_policy_rules_mfa.json`     | Same shape: a 2FA rule built from `AssuranceMethod` (its constraint is the `createpolicyrule` example's), an INACTIVE 1FA rule, and the catch-all set to DENY (`AccessPolicyRuleApplicationSignOnAccess`)              |
| `access_policy_rules_chain.json`   | Same shape: a rule whose `verificationMethod.type` is `AUTH_METHOD_CHAIN` (`AuthenticationMethodChainMethod`), and a DENY catch-all                                                                                    |
| `apps.json`                        | `Application` objects with `_links.accessPolicy` (`ApplicationLinks.accessPolicy`, an `HrefObject`). The spec has no list example carrying that link, so the href shape `<base>/api/v1/policies/{policyId}` is assumed |
