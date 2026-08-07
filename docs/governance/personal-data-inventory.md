# Personal data inventory

**Status:** Active governance document.
**Filed:** 2026-08-07 by OE-448 (follow-up F-2 from slice 330 privacy audit).
**Closes:** Slice 330 privacy audit finding **PRIV-4** (no personal data map / operator-facing privacy docs) and **PRIV-1** (no registry of what counts as personal data).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [data retention policy](./data-retention.md), [access-review plan](./access-review.md), [incident-response plan](./incident-response.md), and [business-continuity plan](./business-continuity.md) tabletop. Next review: 2027-05-28.

---

## Why this document exists

An operator standing up security-atlas must be able to inform their Data Protection Officer (DPO) and answer data subjects regarding what personal data the tool collects, processes, and stores. Before this document, personal data handling details were distributed across 192 database migration files and JSON schema definitions in the schema registry.

Slice 330's privacy audit (GDPR + CCPA/CPRA) identified two related High-severity findings:

- **PRIV-4 (High)** — `docs/SELF_HOSTING.md` contained no references to personal data or privacy governance, leaving self-host operators without a PII inventory to present to auditors or DPOs.
- **PRIV-1 (High)** — The platform lacked a single-source-of-truth registry defining which database tables and columns contain personal data, creating a prerequisite blocker for Data Subject Access Request (DSAR) compliance.

This document promotes §3 and §4 of the slice 330 privacy audit (`docs/audits/330-privacy-gdpr-ccpa-audit.md`) into a maintained governance artifact. Every field entry is explicitly anchored to a database migration file or JSON schema object.

### Downstream specification role

This document serves as the formal written specification for:

1. `privacy.PersonalDataSurfaces` in code (slice 504 / OE-376) — the single-source-of-truth Go allow-list for right-to-erasure tombstoning/redaction.
2. The DSAR export surface registry (slice 505 / OE-377) — the catalog of personal data fields extracted during an Article 15 access request.

---

## 1. Controller / Processor determination

The regulatory roles under GDPR Article 4(7)-(8) and CCPA/CPRA are established as follows:

### 1.1 The Self-Host Operator — CONTROLLER

The self-host operator acts as a **Controller** under GDPR Article 4(7) (or "Business" under CCPA/CPRA). The operator determines both the **purposes** (verifying security control posture for SOC 2, ISO 27001, GDPR Art. 32) and the **means** (connector selection, evidence ingestion schedules, retention policies, optional cloud LLM routing).

This controller relationship applies across three distinct data subject populations:

1. **Platform Users** (`users`, `sessions`) — The operator's administrative and security staff using the platform interface.
2. **Workforce Data Subjects** (`evidence_records.payload`) — Employees and contractors ingested via HRIS (e.g., BambooHR, Rippling), IdP (e.g., Okta), MDM/EDR (e.g., Jamf, Fleet), and developer tools (e.g., GitHub, Slack). _Note: These data subjects do not hold platform logins or direct visibility._
3. **Vendor Contacts** (`vendors.owner_user`) — Third-party business points of contact.

_Multi-Tenant Consultancy Exception:_ When an operator runs security-atlas as a managed service for third-party clients (vCISO / consultancy deployment), the operator acts as a **Processor** for those clients' tenant data.

### 1.2 The security-atlas Project / Maintainer — NEITHER

The open-source project and its maintainers act as **neither Controller nor Processor** regarding self-hosted operator deployments.

- The software is purely open-source (pure community OSS).
- There is zero telemetry, analytics, error reporting, phone-home, or licensing phone-home built into the product (verified against code and dependencies).
- Deployments operate completely disconnected from the project maintainers.

The project maintainer acts as a Controller only for a separate, minimal surface: contributor identities recorded in git commit history via DCO sign-offs (`Signed-off-by:`) per [GOVERNANCE.md](../../GOVERNANCE.md) and security vulnerability reporters per [SECURITY.md](../../SECURITY.md).

### 1.3 Hosted Offering

No official hosted SaaS offering exists for security-atlas. Open Question #5 is resolved as pure community open-source with a scheduled re-evaluation trigger in 2028. Should a hosted offering be established in the future, the operating entity will assume a Processor role under Article 28.

---

## 2. Personal data inventory

The inventory is divided into four distinct operational sections. Every entry is anchored to its source definition in `migrations/sql/` or `internal/api/schemaregistry/schemas/`.

### 2.1 Identity, authentication, and session data

Primary account identifiers, credentials, and session telemetry for operators accessing the platform UI or API.

| Table                   | Column(s)                                                                                                   | Data category                                               | Art. 9? | Anchoring migration / Schema                                                                                        | Regulatory surface       |
| :---------------------- | :---------------------------------------------------------------------------------------------------------- | :---------------------------------------------------------- | :------ | :------------------------------------------------------------------------------------------------------------------ | :----------------------- |
| `users`                 | `email`, `display_name`, `idp_issuer`, `idp_subject`, `status`                                              | Direct identifier, workplace identity                       | No      | `migrations/sql/20260511000000_init.sql:20-30`                                                                      | DSAR, Erasure            |
| `local_credentials`     | `password_hash`, `params`                                                                                   | Credential (Argon2id)                                       | No      | `migrations/sql/20260511000012_users_sessions_api_keys.sql:15-25`                                                   | Erasure (cascades)       |
| `sessions`              | `user_id`, `idp_issuer`, `idp_subject`, `user_agent`, `ip_address`, `geo_country`, `geo_city`               | Online identifier, device fingerprint, approximate location | No      | `migrations/sql/20260511000012_users_sessions_api_keys.sql` & `20260518100000_sessions_augment_ua_ip_geo.sql:46-49` | DSAR, Erasure, Retention |
| `oidc_idp_configs`      | `allowed_email_domains`                                                                                     | Org identifier (indirect)                                   | No      | `migrations/sql/20260511000000_init.sql:70-80`                                                                      | Administrative           |
| `api_keys`              | `issued_by`                                                                                                 | Actor reference                                             | No      | `migrations/sql/20260511000012_users_sessions_api_keys.sql:80-95`                                                   | DSAR                     |
| `oauth_auth_codes`      | `idp_issuer`, `idp_subject`                                                                                 | Pseudonymous identifier                                     | No      | `migrations/sql/20260521000010_oauth_auth_codes.sql`                                                                | Retention                |
| `oauth_token_exchanges` | `subject_token_iss`, `subject_token_sub`, `ip_address`                                                      | Online identifier                                           | No      | `migrations/sql/20260521000020_oauth_token_exchanges.sql`                                                           | DSAR, Retention          |
| `oauth_revoked_tokens`  | `revoked_by`, `ip_address`                                                                                  | Online identifier                                           | No      | `migrations/sql/20260521000050_oauth_revoked_tokens.sql`                                                            | Retention                |
| `oauth_device_codes`    | `approved_by_user_id`, `approved_by_idp_issuer`, `approved_by_idp_subject`, `approved_by_current_tenant_id` | Identity + entitlement                                      | No      | `migrations/sql/20260521000060_oauth_device_codes.sql`                                                              | DSAR                     |
| `scim_credentials`      | `issued_by`                                                                                                 | Actor reference                                             | No      | `migrations/sql/20260612020000_scim_provisioning.sql`                                                               | Administrative           |
| `scim_audit_log`        | `actor_credential_id`, `target_user_id`, `detail` (JSONB)                                                   | Provisioning history                                        | No      | `migrations/sql/20260612020000_scim_provisioning.sql`                                                               | DSAR                     |
| `scim_groups`           | `display_name`, external id                                                                                 | Group membership                                            | No      | `migrations/sql/20260612020000_scim_provisioning.sql`                                                               | DSAR                     |
| `user_roles`            | `user_id`, `granted_by`                                                                                     | Entitlement                                                 | No      | `migrations/sql/20260511000000_init.sql:40-50`                                                                      | DSAR                     |
| `super_admin_audit_log` | `actor_user_id`, `actor_tenant_id`                                                                          | Privileged-access history                                   | No      | `migrations/sql/20260521030000_super_admins_full.sql`                                                               | DSAR                     |

---

### 2.2 Audit-log actor fields (append-only)

Action trails and attribution records generated during platform usage. Actor references are stored as `TEXT` plain strings or UUID references across audit logs.

| Table                                      | Column(s)                                                                            | Data category                            | Art. 9? | Anchoring migration                                                  | Regulatory surface       |
| :----------------------------------------- | :----------------------------------------------------------------------------------- | :--------------------------------------- | :------ | :------------------------------------------------------------------- | :----------------------- |
| `decision_audit_log`                       | `user_id`, `user_roles[]`, `action`, `resource_id`, `request_path`, `request_method` | OPA authorization decision — behavioural | No      | `migrations/sql/20260511000001_decision_audit_log.sql:15-30`         | DSAR, Erasure, Retention |
| `evidence_audit_log`                       | `credential_id`                                                                      | Machine actor reference                  | No      | `migrations/sql/20260511000004_evidence_ledger.sql:120-135`          | DSAR                     |
| `decisions_audit`                          | `actor`, `detail`                                                                    | Actor + free-text diff                   | No      | `migrations/sql/20260511000030_decisions_audit.sql`                  | DSAR, Erasure            |
| `artifact_access_log`                      | `actor`                                                                              | Access / download record                 | No      | `migrations/sql/20260511000014_artifacts.sql`                        | DSAR                     |
| `artifacts`                                | `uploaded_by`                                                                        | Upload attribution                       | No      | `migrations/sql/20260511000014_artifacts.sql`                        | DSAR                     |
| `exception_audit_log` / `exceptions`       | `actor`; `requested_by`, `approved_by`, `denied_by`, `activated_by`                  | Exception approval chain                 | No      | `migrations/sql/20260511000018_exceptions.sql`                       | DSAR, Erasure            |
| `sample_audit_log` / `audit_samples`       | `actor`; `created_by`, `annotated_by`                                                | Sample collection attribution            | No      | `migrations/sql/20260511000010_audit_samples.sql`                    | DSAR                     |
| `audit_period_audit_log` / `audit_periods` | `actor`; `frozen_by`, `created_by`                                                   | Audit freeze attribution                 | No      | `migrations/sql/20260511000020_audit_periods.sql`                    | DSAR                     |
| `aggregation_rule_audit_log`               | `actor`; `activated_by`                                                              | Rule activation attribution              | No      | `migrations/sql/20260511000025_aggregation_rules.sql`                | DSAR                     |
| `feature_flag_audit_log` / `feature_flags` | `actor`; `last_changed_by`                                                           | Feature toggle attribution               | No      | `migrations/sql/20260511000016_feature_flags.sql`                    | DSAR                     |
| `me_audit_log`                             | `user_id`, `action`                                                                  | Behavioural self-service record          | No      | `migrations/sql/20260520020000_audit_log_subject_module.sql`         | DSAR                     |
| `walkthrough_audit_log` / `walkthroughs`   | `actor`; `created_by`, `uploaded_by`                                                 | Walkthrough attribution                  | No      | `migrations/sql/20260511000022_walkthroughs.sql`                     | DSAR                     |
| `audit_sink_failures`                      | `entry_actor`                                                                        | Failed audit sink attribution            | No      | `migrations/sql/20260518000000_audit_sink_failures.sql:40`           | DSAR                     |
| `action_plan_audit_log`                    | `actor_id`, `before_state` / `after_state` (JSONB)                                   | Actor + state diff (Trigger-immutable)   | No      | `migrations/sql/20260612070000_action_plans.sql:345-356`             | DSAR                     |
| `control_owner_assignment_audit_log`       | `actor_user_id`, `owner_user_id`                                                     | Control assignment history               | No      | `migrations/sql/20260612060000_control_owner_assign_saved_views.sql` | DSAR                     |
| `imported_catalog_audit_log`               | `actor`; `imported_by`                                                               | OSCAL catalog import attribution         | No      | `migrations/sql/20260606010000_oscal_imported_catalogs.sql`          | DSAR                     |
| `framework_version_audit`                  | `actor_id`, `reviewer_id`                                                            | Framework versioning attribution         | No      | `migrations/sql/20260612090000_framework_versioning.sql`             | DSAR                     |
| `csf_assessment_audit`                     | `actor`; `rated_by`, `created_by`                                                    | NIST CSF rating attribution              | No      | `migrations/sql/20260608080000_csf_tier_profile.sql`                 | DSAR                     |
| `group_role_audit_log`                     | `created_by`                                                                         | IdP group role attribution               | No      | `migrations/sql/20260612030000_idp_group_role_mappings.sql`          | DSAR                     |

---

### 2.3 Domain content

Functional GRC entities, policy documents, vendor management records, and AI interaction artifacts.

| Table                                        | Column(s)                                                                                | Data category                              | Art. 9?               | Anchoring migration                                              | Regulatory surface      |
| :------------------------------------------- | :--------------------------------------------------------------------------------------- | :----------------------------------------- | :-------------------- | :--------------------------------------------------------------- | :---------------------- |
| `vendors`                                    | `owner_user` (email address), `notes`                                                    | Business contact                           | No                    | `migrations/sql/20260511000006_vendor_lite.sql:81`               | DSAR, Erasure           |
| `vendor_reviews_ledger`                      | `reviewer`                                                                               | Reviewer attribution                       | No                    | `migrations/sql/20260511000006_vendor_lite.sql`                  | DSAR                    |
| `risks`                                      | `treatment_owner`                                                                        | Risk owner attribution                     | No                    | `migrations/sql/20260511000005_risk_register.sql`                | DSAR                    |
| `policies`                                   | `owner`, `approver`                                                                      | Policy ownership / approval                | No                    | `migrations/sql/20260511000015_policies.sql`                     | DSAR                    |
| `framework_scopes`                           | `approved_by`, `approval_evidence`                                                       | Scope attestation identity                 | No                    | `migrations/sql/20260511000003_scope.sql`                        | DSAR                    |
| `policy_acknowledgments`                     | `user_id` (composite FK → `users`)                                                       | Attestation record                         | No                    | `migrations/sql/20260511000015_policies.sql`                     | DSAR, Erasure (FK pin)  |
| `audit_notes` / `auditor_assignments`        | `author_user_id`; `granted_by`                                                           | Auditor content & assignments              | No                    | `migrations/sql/20260511000021_auditor_role.sql`                 | DSAR                    |
| `questionnaire_answers`                      | `authored_by`, `narrative` (free text)                                                   | Authored prose (incidental PII)            | Possible (incidental) | `migrations/sql/20260520000000_questionnaire_answers.sql`        | DSAR                    |
| `board_narrative_sections`                   | `authored_by`, `human_approver`, `raw_draft`, `operator_edit`, `final_text`, `citations` | Authored + AI-generated prose              | Possible (incidental) | `migrations/sql/20260612050000_board_narrative_ai.sql`           | DSAR, Erasure           |
| `board_packs`                                | `published_by`                                                                           | Board pack publisher                       | No                    | `migrations/sql/20260511000032_board_packs.sql`                  | DSAR                    |
| `ai_generations`                             | `system_prompt`, `context_inputs` (JSONB), `raw_draft`, `model_provider`                 | Prompt corpus containing evidence excerpts | Possible (incidental) | `migrations/sql/20260607000000_ai_generations.sql`               | DSAR, Erasure, Transfer |
| `mcp_write_proposals`                        | `created_by`, `human_approver`                                                           | MCP proposal attribution                   | No                    | `migrations/sql/20260520030000_mcp_write_proposals.sql`          | DSAR                    |
| `metrics` / `metric_values`                  | `owner_user_id`; `entered_by_user_id`                                                    | Metric ownership & entry                   | No                    | `migrations/sql/20260516000001_metrics_catalog.sql`              | DSAR                    |
| `notifications`                              | `recipient_user_id`                                                                      | Delivery record                            | No                    | `migrations/sql/20260607020000_email_delivery_channel.sql`       | DSAR                    |
| `email_channel_optin` / `email_delivery_log` | `user_id`; `recipient_user_id`                                                           | Preference & delivery log                  | No                    | `migrations/sql/20260607020000_email_delivery_channel.sql:38-43` | DSAR, Consent-adjacent  |
| `channel_delivery_log`                       | `recipient_user_id`                                                                      | Webhook delivery log                       | No                    | `migrations/sql/20260608000000_slack_webhook_channels.sql`       | DSAR                    |
| `staleness_rollup_log`                       | `recipient_user_id`                                                                      | Digest delivery record                     | No                    | `migrations/sql/20260609000000_staleness_rollup_log.sql`         | DSAR                    |
| `schema_registry`                            | `owner`, `created_by`                                                                    | Custom schema ownership                    | No                    | `migrations/sql/20260511000008_schema_registry.sql`              | DSAR                    |
| `action_plans`                               | `owner_id` (FK → `users`)                                                                | Action item assignment                     | No                    | `migrations/sql/20260612070000_action_plans.sql:81`              | Erasure (FK pin)        |

---

### 2.4 Evidence ledger payload (third-party workforce data)

The load-bearing section. `evidence_records.payload` (JSONB) stores raw evidence ingested from automated connectors and manual uploads. These payloads contain personal data regarding **third-party data subjects** (the operator's employees and contractors) who hold no user accounts on the platform.

Anchored to `migrations/sql/20260511000004_evidence_ledger.sql` and `internal/api/schemaregistry/schemas/`.

| Evidence Kind (JSON Schema)                                             | Personal Data Admitted                                                                                      | Art. 9?                         | Schema Anchor & Privacy Notes                                                                                                                                  |
| :---------------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------- | :------------------------------ | :------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `hris.worker_lifecycle`                                                 | `work_email`, `employment_status`, `start_date`, `end_date`, `title`, `department`, `manager_assignment_id` | No (explicitly excluded)        | `schemas/hris.worker_lifecycle/1.0.0.json`. Excludes SSN, compensation, home address, health/benefits, ethnicity, and DOB explicitly in schema contract.       |
| `hris.manager_hierarchy`                                                | Manager-report ID mappings                                                                                  | No                              | `schemas/hris.manager_hierarchy/1.0.0.json`. Opaque ID organizational structure graph.                                                                         |
| `okta.user_lifecycle`                                                   | `login`, `primary_email`, `last_login_at`, `activated_at`, `deactivated_at`, `mfa_enrolled`                 | No                              | `schemas/okta.user_lifecycle/1.0.0.json`. Behavioural timing signals (`last_login_at`).                                                                        |
| `github.scim_user`                                                      | `user_name`, `email`, `active`                                                                              | No                              | `schemas/github.scim_user/1.0.0.json`. Developer account identity.                                                                                             |
| `slack.workspace_member`                                                | `user_id`, `handle`                                                                                         | No                              | `schemas/slack.workspace_member/1.0.0.json`. Communication handle.                                                                                             |
| `endpoint.device_posture`                                               | `owner_assignment_id`, `owner_display_name`, `device_name`                                                  | No (explicitly excluded)        | `schemas/endpoint.device_posture/1.0.0.json:9,67`. Schema explicitly forbids owner personal email, phone, home address, geolocation, and app browsing history. |
| `osquery.host_posture`                                                  | `hostname`                                                                                                  | No                              | Hostnames frequently encode workforce employee names (e.g. `jane-macbook-pro`).                                                                                |
| `access_review.completion`                                              | `completed_by`, `reviewer_role`, `notes`                                                                    | No                              | Access review attestation attribution.                                                                                                                         |
| `policy.acknowledgment`                                                 | `user_id`, `acknowledged_at`                                                                                | No                              | Policy compliance attestation.                                                                                                                                 |
| `github.audit_event`, `slack.admin_audit_event`                         | `actor`, event timestamp & action                                                                           | No                              | Behavioural audit stream.                                                                                                                                      |
| `pagerduty.oncall_coverage` / `.incident_summary` / `.response_metrics` | On-call responder assignment & response latency                                                             | No                              | Working-time and response timing data (Art. 88 workforce sensitivity).                                                                                         |
| `jira.ticket_evidence`                                                  | Ticket assignees, reporters, commenters                                                                     | No                              | Issue tracking attribution.                                                                                                                                    |
| `manual.upload` / `manual.attestation`                                  | `uploaded_by`, `filename`, `description`, + raw binary file artifact                                        | **Unbounded (Art. 9 possible)** | Object storage upload path. Uncontrolled PII risk (e.g., uploaded HR letters, medical accommodations, or signed contracts).                                    |

#### Special Category Data (Art. 9) Assessment

Standard automated evidence kinds explicitly exclude Article 9 special category data by schema design (with enforced `additionalProperties: false`). Potential Article 9 exposure is strictly confined to:

1. Unstructured binary attachments uploaded via `manual.upload`.
2. Free-text fields (`notes`, `narrative`, `description`).
3. Prompts or raw drafts in `ai_generations` derived from free-text inputs.

---

## 3. Schema Anchor Verification & Drift Notes

During the transcription of this inventory from the initial audit baseline (slice 330, HEAD `ba2de9b1`) against `main`:

1. **Migration Anchor Verification:** All 192 migration references were verified against `migrations/sql/`. Table and column definitions match `main`.
2. **`evidence_records` Immutability Policy:** Verified in `migrations/sql/20260511000004_evidence_ledger.sql:100-115`. Row-Level Security policy `tenant_read` (SELECT) and `tenant_insert` (INSERT) are active; UPDATE and DELETE policies are omitted by design, enforcing role-level append-only constraints for `atlas_app`.
3. **`subject_module` Audit Log Column Drift (PRIV-7):** Nine core audit log tables carry `subject_module` per `migrations/sql/20260520020000_audit_log_subject_module.sql`. Eleven audit log tables added in subsequent migrations currently lack `subject_module` (tracked under OE-451).

---

## 4. Cross-References

- [`docs/audits/330-privacy-gdpr-ccpa-audit.md`](../audits/330-privacy-gdpr-ccpa-audit.md) — The initial privacy audit report.
- [`docs/governance/data-retention.md`](./data-retention.md) — Platform data retention windows and tombstone disposal specification.
- [`docs/adr/0020-right-to-erasure-vs-append-only-ledger.md`](../adr/0020-right-to-erasure-vs-append-only-ledger.md) — Ratified right-to-erasure design (tombstoning & scoped deferral).
- [`docs/SELF_HOSTING.md`](../SELF_HOSTING.md) — Self-hosting operational guide linking this inventory for DPO review.
