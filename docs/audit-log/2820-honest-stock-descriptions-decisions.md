# #2820 — honest descriptions for the 27 stock SOC 2 checks reduced to a roll-up (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
Implements ADR 0028 decision 6. This is the list #1860's first checkbox asks
for. No migration, and no change to any evidence query.

- detection_tier_actual: manual_review
- detection_tier_target: unit

The bug: after #1858 every one of these 26 bundles evaluated a roll-up of the
connector's per-record `result`. Their top-level descriptions still said the
query asserted the old payload check. It was caught in the #1858 review, not
by a test. No test compares a description with what its query evaluates,
which is why the target tier is `unit`.

## D1 — Only the top-level description changes, so every bundle updates in place

Under #1908, a `control.yaml` that differs from the active version only in the
top-level `description`, `external_refs` or `anchor_fit_note` updates the
active row in place (`manifestsEqualIgnoringMetadata`,
`internal/control/store.go`). Any other change supersedes the bundle and mints
a new control id. Owners, evidence, evaluations, exceptions and links then
stay on the old row (`docs/audit-log/1908-control-description-in-place-decisions.md`).

Three other strings still name the old check:

- the `evidence_queries[].description`, for example cc6_1's "requires a
  phishing-resistant factor for admins";
- the query `id`, for example `every_in_scope_system_reviewed_in_90d`;
- the Rego comment that says the input is `{records: [{result}]}`. Since
  #2886 it is `{records: [{result, observed_at}]}`.

All three are inside `evidence_queries`. Editing any of them would supersede
all 26 bundles on the next boot and strand each control's links.
**Decision (dispatcher, for Matt, 2026-11-21):** leave them. Each tier ticket
below replaces its bundle's expression and supersedes the bundle anyway, so
it corrects that bundle's query description, id and comment at the same
time. Until then, each top-level description says what runs. The control
title also still states the intended test, and a `title` change supersedes
too, so it is left for the same tickets.

The `title` stays phrased as the intent ("Production endpoints have
host-firewall enabled"). The description's first paragraph states it as
"the intended test". Its "What runs today" paragraph says what the roll-up
evaluates. Its "Pending" paragraph names each missing check, the field or
source it waits on, and its ticket.

**Verified in place** two ways, against `HEAD` before the edit:

- the platform's own `manifestsEqualIgnoringMetadata`, called from a
  throwaway test on all 26 HEAD/new manifest pairs (all equal, none
  byte-identical);
- a decode-and-compare script.

## D2 — What each connector's verdict means, read from the source

A roll-up reports the producer's verdict, so each description states what
that verdict decides for its kind. The rule is read from the producer code at
`a1f82152`:

| Evidence kind                               | Producer and verdict                                                                                                                                                                                                   |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `osquery.host_posture.v1`                   | `connectors/osquery/internal/osqueryposture`: pass when disk encryption and screen lock are both on, fail when either is off, inconclusive when host detail could not be fetched or no platform and booleans came back |
| `okta.mfa_policy.v1`                        | `connectors/okta/internal/oktapolicy`: pass when MFA is required with at least one factor, fail when not required, inconclusive when required with no factor                                                           |
| `okta.user_lifecycle.v1`                    | `connectors/okta/internal/oktausers`: pass for ACTIVE with MFA, fail for ACTIVE without MFA and for DEPROVISIONED, SUSPENDED, LOCKED_OUT or PASSWORD_EXPIRED, inconclusive otherwise                                   |
| `okta.app_assignment.v1`                    | `connectors/okta/cmd/atlas-okta/cmd_run.go`: always inconclusive                                                                                                                                                       |
| `github.repo_protection.v1`                 | `connectors/github/internal/githubrepo`: pass when at least one approving review is required, fail when none is required or no protection is configured, inconclusive on an API error                                  |
| `github.audit_event.v1`                     | `connectors/github/cmd/atlas-github/cmd_webhook.go`: always inconclusive                                                                                                                                               |
| `jira.ticket_evidence.v1`                   | `connectors/jira/cmd/atlas-jira/cmd_run.go` `classifyResult`: pass for Done, Resolved, Closed, Cancelled or Completed, inconclusive for any other status (an empty status is rejected at ingest)                       |
| `1password.org_policy.v1`                   | `connectors/1password/internal/opaccount`: pass when two-factor is required and the minimum password length is at least 12, fail otherwise                                                                             |
| `aws.s3.bucket_encryption_state.v1`         | `connectors/aws/internal/awss3`: pass when a default server-side encryption rule exists (any algorithm), fail when none does, inconclusive when the region or encryption could not be read                             |
| `manual.upload.v1`                          | `connectors/manual/cmd/atlas-manual` (local, s3, sftp): always inconclusive                                                                                                                                            |
| `sast.scan_result.v1`                       | no bundled producer; the pusher states the result (`atlas-cli evidence push --result`)                                                                                                                                 |
| `policy.acknowledgment.v1`                  | `internal/api/policyacks/handlers.go`: always pass; reaches cc1_5, cc5_3 and cc7_4 as unbound records since #2869                                                                                                      |
| `security_awareness.training_completion.v1` | `internal/securityawareness/store.go`: always pass                                                                                                                                                                     |
| `access_review.completion.v1`               | `internal/accessreview/store.go`: always pass, written when a campaign completes                                                                                                                                       |

Three kinds are always inconclusive: `github.audit_event.v1`,
`okta.app_assignment.v1` and `manual.upload.v1`. The controls that read only
those kinds can never pass or fail today, whatever is pushed: cc3_4, cc6_5,
cc6_6 MFA, cc7_3 and cc9_1. Manual attestations on cc6_5 and cc9_1 are
`manual.attestation.v1`. Since #2818 a query sees only its declared kind, so
those attestations don't reach these queries either. Each of these
descriptions says so.

Three kinds are always pass. Their controls pass whenever any record of the
kind is in the window: cc1_4 training records, cc1_5, cc5_3, cc6_3 periodic
review and cc7_4. Each description says what is not checked.

`soc2_a1_1_capacity_monitoring` declares `freshness_class: hourly`, which the
engine's table does not map. It falls back to the 90-day monthly window
(`docs/spec/control-bundle.md` §2.2.1), and its description says so. Whether
`hourly` should be mapped or rejected is not decided here.

## D3 — The 27 checks

The old expressions are at `bf99aaa5`, the parent of the #1858 commit. Tiers
and tickets are from ADR 0028 ("The 27 checks, by tier"). "Runs now" is the
roll-up of the kind's verdict from D2.

| #   | Bundle · query (old expression)                                                                                                                                                                                                   | Intended check                                                                    | Runs now                                                               | Tier                                    |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | ---------------------------------------------------------------------- | --------------------------------------- |
| 1   | cc6_1 · [`idp_mfa_policy_enabled`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_1_logical_access_baseline/control.yaml#L26-L37)                                | MFA policy applies to everyone and allows a phishing-resistant factor for admins  | Okta: MFA required with at least one factor                            | 1 (#2821)                               |
| 2   | cc6_6 boundary · [`host_firewall_enabled_on_managed_endpoints`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_6_boundary_protection/control.yaml#L25-L34)       | every host has its host firewall enabled                                          | osquery: disk encryption plus screen lock                              | 1 (#2822)                               |
| 3   | cc4_1 · [`every_endpoint_reported_in_24h`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc4_1_continuous_monitoring/control.yaml#L26-L37)                          | every managed endpoint reported within 24 h                                       | osquery: disk encryption plus screen lock                              | 1 (#2822); inventory half #2834         |
| 4   | cc8_1 change · [`prod_repos_have_branch_protection`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc8_1_change_management/control.yaml#L25-L40)                    | production repos: required reviews, status checks, no force-push, admins enforced | GitHub: at least one approving review required                         | 1 (#2823); rest tier 2 (#2827)          |
| 5   | cc8_1 config · [`iac_repos_require_reviewed_prs`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc8_1_configuration_change_control/control.yaml#L24-L36)            | repos tagged `iac` require reviewed PRs                                           | GitHub: at least one approving review required, on every repo read     | 1 (#2823); `iac` scoping tier 2 (#2827) |
| 6   | cc6_3 review · [`every_in_scope_system_reviewed_in_90d`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_3_periodic_access_review/control.yaml#L24-L36)           | every in-scope system reviewed within 90 days                                     | always pass: any completed review in the 120-day window                | 1 (#2824); inventory half #2834         |
| 7   | cc1_4 · [`training_completion_records_within_365d`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc1_4_competence_training/control.yaml#L49-L58)                   | completions within 365 days from an allowed source                                | always pass: any completion in the 400-day window                      | 1 (#2824); inventory half #2834         |
| 8   | cc6_5 · [`disposal_log_uploaded_within_quarter`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_5_asset_disposal/control.yaml#L26-L35)                           | a disposal log uploaded within 90 days                                            | always inconclusive (manual upload)                                    | 1 (#2824)                               |
| 9   | cc9_1 · [`bcp_reviewed_within_12mo`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc9_1_business_continuity/control.yaml#L24-L33)                                  | a continuity plan uploaded within 365 days                                        | always inconclusive (manual upload)                                    | 1 (#2824)                               |
| 10  | cc6_7 · [`all_s3_buckets_encrypted_at_rest`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_7_encryption_in_transit/control.yaml#L27-L37)                        | default SSE with an allowed algorithm (AES256, aws:kms)                           | AWS: a default SSE rule exists, any algorithm                          | 1 (#2825)                               |
| 11  | cc1_5 · [`all_users_acknowledged_core_policies`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc1_5_accountability_acknowledgment/control.yaml#L25-L48)            | every active user acknowledged all five core policies in their current version    | always pass: any acknowledgement of any policy in the 30-day window    | 1 (#2832); inventory half #2834         |
| 12  | cc5_3 · [`current_policy_versions_acknowledged`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc5_3_policies_procedures/control.yaml#L25-L40)                      | acknowledgements reference the version in force, for every active user            | always pass: any acknowledgement of any policy in the 30-day window    | 1 (#2832); inventory half #2834         |
| 13  | cc7_4 · [`ir_plan_acknowledged_by_on_call`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc7_4_incident_response/control.yaml#L25-L37)                             | everyone on call acknowledged the IR plan                                         | always pass: any acknowledgement of any policy in the 120-day window   | 1 (#2832); inventory half #2834         |
| 14  | a1_1 · [`production_capacity_healthy`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_a1_1_capacity_monitoring/control.yaml#L24-L36)                                 | production hosts below 85% disk, 90% memory, 90% p95 CPU                          | osquery: disk encryption plus screen lock (90-day fallback window)     | 2 (#2826)                               |
| 15  | cc6_8 · [`antimalware_active_and_recent`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_8_malware_protection/control.yaml#L24-L34)                              | anti-malware active with definitions under 7 days old                             | osquery: disk encryption plus screen lock                              | 2 (#2826)                               |
| 16  | cc7_2 · [`edr_running_on_every_managed_endpoint`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc7_2_anomaly_detection/control.yaml#L24-L35)                       | EDR installed, running and healthy on every endpoint                              | osquery: disk encryption plus screen lock                              | 2 (#2826)                               |
| 17  | cc6_6 MFA · [`mfa_enforced_on_admin_apps`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_6_mfa_external_threats/control.yaml#L24-L39)                           | admin and infrastructure apps enforce MFA at sign-in                              | always inconclusive (Okta app assignment)                              | 2 (#2828)                               |
| 18  | cc1_4 · [`training_completion_within_365d`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc1_4_competence_training/control.yaml#L27-L44)                           | every active user has a closed `security-training` ticket within 365 days         | Jira: ticket in a terminal status, any ticket the JQL returns          | 2 (#2829); inventory half #2834         |
| 19  | cc7_1 remediation · [`vuln_tickets_within_sla`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc7_1_vulnerability_remediation/control.yaml#L24-L52)                 | tickets closed within the severity SLA, none open past it                         | Jira: ticket in a terminal status; open tickets never fail             | 2 (#2829)                               |
| 20  | cc6_3 privileged · [`privileged_vault_locked_down`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_3_privileged_account_management/control.yaml#L25-L37)         | privileged vault: member list, sharing off by default, audit events on            | 1Password: two-factor required and minimum password length at least 12 | 2 (#2830); role-count bound #2834       |
| 21  | cc7_1 detection · [`sast_run_recently_no_unsuppressed_criticals`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc7_1_vulnerability_detection/control.yaml#L25-L43) | every in-scope repo scanned in 7 days with no unsuppressed criticals              | whatever result the pusher states                                      | 2 (#2831); in-scope-repo half #2834     |
| 22  | pi1_2 · [`input_validation_taint_rules_clean`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_pi1_2_input_completeness/control.yaml#L24-L46)                         | no unsuppressed findings in the input-validation rules                            | whatever result the pusher states                                      | 2 (#2831)                               |
| 23  | pi1_3 · [`authorization_taint_rules_clean`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_pi1_3_processing_validity/control.yaml#L23-L45)                           | no unsuppressed findings in the authorisation rules                               | whatever result the pusher states                                      | 2 (#2831)                               |
| 24  | cc6_2 · [`every_provision_was_authorized`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_2_new_user_provisioning/control.yaml#L26-L45)                          | every provisioned account has an authorising HRIS record or approval ticket       | Okta: user status and MFA enrolment                                    | needs a design (#2833)                  |
| 25  | cc6_3 revocation · [`terminations_revoked_within_24h`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc6_3_access_modification_revocation/control.yaml#L24-L38)     | every termination followed by deactivation within 24 h                            | Okta: user status and MFA enrolment; any deprovisioned user fails      | needs a design (#2833)                  |
| 26  | cc3_4 · [`protected_branch_pushes_were_pr_reviewed`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc3_4_change_risk_reassessment/control.yaml#L26-L41)             | every protected-branch push had a non-author approval                             | always inconclusive (GitHub audit events)                              | needs a design (#2834 or its own)       |
| 27  | cc7_3 · [`security_github_events_acknowledged`](https://github.com/mgoodric/security-atlas/blob/bf99aaa5b5cd525e332155adcca92b2b5a1bbe9a/controls/soc2/soc2_cc7_3_security_event_evaluation/control.yaml#L25-L54)                 | every security-tagged event acknowledged within 24 h                              | always inconclusive (GitHub audit events)                              | needs a design (#2834 or its own)       |

## Not changed

- Query descriptions, query ids, Rego comments and titles (D1).
- The `hourly` freshness class on a1_1 (D2).
- `internal/api/scfimport/stock_manifests_gen.go` is regenerated with
  `scripts/gen-stock-control-hashes.sh`, as that script requires after any
  stock `control.yaml` edit.
