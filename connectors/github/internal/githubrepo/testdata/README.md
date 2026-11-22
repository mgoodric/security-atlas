# Fixtures

Trimmed from the example responses in GitHub's REST API description
(`github/rest-api-description`, `descriptions/api.github.com/api.github.com.json`,
apiVersion 2022-11-28), read 2026-11-21. Fields the connector does not read
are dropped; the values and field names are GitHub's. Where an example
lacks a rule type the tests need, that rule is built from the type's
documented schema (`repository-rule-pull-request`,
`repository-rule-required-signatures`).

| File                     | Source                                                                                                              |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| `org_repos.json`         | `minimal-repository-items` (List organization repositories)                                                         |
| `branch_protection.json` | `branch-protection` (Get branch protection)                                                                         |
| `branch_rules.json`      | `repository-rule-items` (Get rules for a branch), plus a `pull_request` and a `required_signatures` rule            |
| `ruleset.json`           | `repository-ruleset` (Get a repository ruleset), its id and source set to the org ruleset `branch_rules.json` names |
| `branch.json`            | `branch-get` (Get a branch)                                                                                         |
