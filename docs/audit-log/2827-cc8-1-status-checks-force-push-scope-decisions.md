# #2827 — GitHub repo protection emits status checks, force pushes, topics and deploys-to-production; cc8_1 completes (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
Finishes ADR 0028 checks 4 and 5, whose first halves landed in #2823 and
moved to `github.repo_protection.v2` in #2925 / #2933
(`docs/audit-log/2823-cc8-1-repo-protection-sql-checks-decisions.md`,
`docs/audit-log/2925-github-repo-protection-v2-decisions.md`). The query
shape follows the latest stock SQL check, cc4_1 (#2932).

- detection_tier_actual: none
- detection_tier_target: contract

No bug surfaced while building. The connector reads fields it had not
read before, so the tier that guards them is the contract tier: tests that
replay GitHub's documented responses (`TestClient_DocumentedShapes`) and
that run the built record through the registry's validator
(`schema_conformance_test.go`).

GitHub's shapes were read from GitHub's OpenAPI description
(`github/rest-api-description`,
`descriptions/api.github.com/api.github.com.json`, read 2026-11-21):
`minimal-repository` (`topics`, `custom_properties`), `branch-protection`
(`required_status_checks`, `allow_force_pushes`),
`protected-branch-required-status-check`,
`repository-rule-required-status-checks`,
`repository-rule-params-status-check-configuration` and
`repository-rule-non-fast-forward`. Nothing here was checked against a
live org.

## D1 — Schema 2.1.0 of `github.repo_protection.v2`, not a new kind

The four new fields are optional, so 2.1.0 is additive over 2.0.0
(`CheckAdditiveOver`, pinned by `TestGitHubRepoProtection210_AdditiveOver200`).
Following osquery's 1.1.0 to 1.3.0, the kind stays `github.repo_protection.v2`
and both queries keep declaring it, so 2.0.0 records already in the
ledger stay readable. The connector pushes 2.1.0.

| Field                    | Type                               | Source                                                                                                                                                                                              |
| ------------------------ | ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `required_status_checks` | array of unique, non-empty strings | classic `required_status_checks.contexts` and `.checks[].context`, plus every `required_status_checks` rule's `parameters.required_status_checks[].context`; sorted; `[]` when nothing requires one |
| `allow_force_pushes`     | boolean                            | false when classic `allow_force_pushes.enabled` is false or a `non_fast_forward` rule is active                                                                                                     |
| `topics`                 | array of strings                   | the org repository listing's `topics`                                                                                                                                                               |
| `deploys_to_production`  | boolean                            | `topics` contains `--production-topic`                                                                                                                                                              |

GitHub's schema requires both `contexts` and `checks` in classic
`required_status_checks`, but its example sends only `contexts`, so the
connector reads the union. A `required_status_checks` rule whose
parameters don't decode leaves the repository unread, the same as an
undecodable `pull_request` rule (#2925 D2).

## D2 — `allow_force_pushes` is three-valued

- **false** when a `non_fast_forward` rule is active, or classic protection
  reports `allow_force_pushes.enabled: false`. A rule blocks force pushes
  whatever classic says, because GitHub enforces each source.
- **true** when neither classic protection nor any rule exists, or classic
  reports `enabled: true` and no rule blocks.
- **omitted** when classic protection is present without
  `allow_force_pushes` (the schema marks it optional) and no rule decides.

## D3 — Bypass of status checks and force-push blocks is not read

Classic required status checks have an `enforcement_level`, and rulesets
have bypass actors, so an admin or a bypass actor may skip a required check
or force-push past a block. The connector does not read either for these
two fields. It reports what the branch requires, not who is exempt.
`enforce_admins` stays scoped to the approving review (#2925 D4). Both
control descriptions say so. Reading bypass for these fields would be a
connector ticket; it is not filed here because neither control's intended
test names it.

## D4 — "Deploys to production" is a repository topic, default `production`

The ticket left the signal open: a topic or a custom property.

- **Topic, chosen.** `GET /orgs/{org}/repos` already returns `topics`
  (`minimal-repository`), so the signal costs no extra request, and every
  GitHub plan has topics. It is the same mechanism configuration change
  control uses (`iac`).
- **Custom property, rejected for now.** `minimal-repository` documents
  `custom_properties`, but GitHub's own example of the listing returns it
  as `null`, and the reliable read is
  `GET /repos/{o}/{r}/properties/values`, one more request per repository.
  Custom properties also need an org-level schema the operator has to
  create first.

The connector flag `--production-topic` (default `production`) names the
topic, because orgs differ (`prod`, `deploys-prod`). The connector derives
`deploys_to_production` and pushes it next to `topics`, so the stock query
reads one boolean and does not hard-code the operator's convention. `iac`
stays fixed in the configuration-change-control query: the ticket names
that topic.

`topics` and `deploys_to_production` come from the listing, so a record
whose protection could not be read still carries them. They are omitted
together when the listing reports no `topics` field.

## D5 — Change management keeps checking every repository; production adds two conditions

The ticket says the check "gains" conditions for production repositories,
and says configuration change control is "limited" to `iac` repositories.
The wording differs, and this PR reads it literally:

- every repository must still require an approving review enforced on
  admins (#2823, #2925);
- a repository with `deploys_to_production` true must also have at least
  one required status check and `allow_force_pushes` false.

Narrowing change management to production repositories only would make a
tenant with no `production` topic pass vacuously, or go inconclusive, on
repositories that require no review today. If Matt wants it narrowed, the
query change is one `CASE` arm.

## D6 — Unknown scope is inconclusive unless the repository passes anyway

For change management, a record without `deploys_to_production` might be a
production repository. If it meets the production conditions anyway, it
passes; if not, it is inconclusive, never a fail (`greatest(..., 1)`). The
same holds for configuration change control when `topics` is missing.
This is the three-valued rule (absent is inconclusive) applied to the
scope as well as the fields. In practice it is how every 2.0.0 record
reads: a connector older than #2827 leaves change management inconclusive
until it is upgraded, unless the repository already meets the production
conditions, which a 2.0.0 record cannot show.

A scope field that is present but mistyped counts as in scope:
`deploys_to_production` other than `false` applies the production
conditions, and `topics` that isn't an array keeps the repository in the
`iac` check. "Present but not true is off" would exempt a malformed record
from the check, which fails open. The schema rejects both shapes, so this
is defence for a record that bypassed validation.

## D7 — The rank() shape of cc4_1, with each condition scored separately

Both queries now match cc4_1 (#2932):

- **Rows.** Records whose `repo_id` is not a number of at least 1 are left
  out (`WHERE CASE ... END`, so the cast is guarded). This changes #2925 D7,
  where such a record failed the control. Bundle cases
  `missing-repo-id-is-left-out`, `non-numeric-repo-id-is-left-out` and
  `keyless-record-is-left-out` pin it. The schema requires an integer
  `repo_id` of at least 1.
- **Latest per repository.** `rank() OVER (PARTITION BY repo_id ORDER BY
observed_at DESC)` keeps every record at the latest instant, and the
  repository takes the lowest verdict among them. A tied false fails.
- **Three values per condition.** Each condition scores 0 (off or
  mistyped), 1 (absent) or 2 (on), and the record's verdict is the
  `least` of them. Before, a `CASE` stopped at the first absent field, so a
  record with `required_reviews` absent and `enforce_admins` false was
  inconclusive. Now the known failure decides (`observed-failure-outranks-unread-field`).
- **Time.** Only `observed_at` orders records. Neither query reads a
  payload timestamp, so no `evaluated_at` or RFC 3339 guard is needed.

For configuration change control, an out-of-scope record scores `NULL`.
`min` skips it, so a tie between an `iac` record and a non-`iac` record
keeps the repository in scope (`tie-iac-and-not-keeps-repo-in-scope`). The
outer `count(verdict)` counts only in-scope repositories, and none leaves
the control inconclusive (`only-non-iac-repos-is-inconclusive`).

## D8 — Supersedes the stock bundles `soc2_cc8_1_change_management` and `soc2_cc8_1_configuration_change_control`

Each query's `expression`, `description` and the bundle `description`
change, so both bundles supersede on the next boot (#1908). The query ids
and bundle titles do not change. Owners, evidence, evaluations, exceptions
and links stay on the old rows (#2820 D1). The PR carries the
`supersedes-stock` label (#2891); the changelog fragment carries the
upgrade note. `stock_manifests_gen.go` is regenerated
(`scripts/gen-stock-control-hashes.sh`).

## D9 — Not changed here

- ADR 0028's check table still names `github.repo_protection.v1` and
  points the remaining halves at #2827. Like #2925, this PR doesn't edit
  the ratified ADR.
- The demo seed keeps writing 2.0.0 records (`internal/demoseed`), as it
  keeps osquery at 1.0.0. Those payloads carry no production fields, so
  if change management evaluates them it reads inconclusive (D6). Not
  exercised against a seeded install here.
- Inventory completeness (a repository that never reported) stays with
  #2834.
