# Records of Processing Activities (RoPA, GDPR Art. 30)

**Status:** Active governance document.
**Filed:** 2026-08-07 by OE-449 (follow-up F-3 from slice 330 privacy audit).
**Closes:** Slice 330 privacy audit finding **PRIV-3** (No RoPA, and no lawful-basis statement for any processing purpose).
**Owner:** Project maintainer (see [GOVERNANCE.md](../../GOVERNANCE.md)).
**Review cadence:** Annual, co-scheduled with the [data-retention policy](./data-retention.md), [incident-response plan](./incident-response.md), [business-continuity plan](./business-continuity.md), and [access-review plan](./access-review.md) tabletop.

---

## Why this document exists

Article 30 of the EU General Data Protection Regulation (GDPR) requires data controllers and processors to maintain a **Record of Processing Activities (RoPA)** — a structured record enumerating each processing purpose, its specific Article 6 lawful basis, the categories of data subjects and personal data involved, recipients, retention periods, and cross-border transfer mechanisms.

Article 30(5)'s small-organisation derogation does not exempt a deployment of security-atlas: the processing is systematic and includes regular monitoring of workforce access, which falls directly into the carve-out to the derogation.

Slice 330's privacy audit finding **PRIV-3** identified that no documented lawful basis existed for any of the platform's core processing purposes. This document provides the project's canonical RoPA, establishing an explicit, decided Article 6 lawful basis for every processing purpose.

### Scope and distinction from slice 506 (OE-378)

It is critical to distinguish this document from the product feature defined in **slice 506 / OE-378**:

- **This document (`docs/governance/records-of-processing-activities.md`)**: The **project's own governance artifact**. It documents the project's own architecture, standard processing activities, and default data flows for self-host operators and auditors. It is a static governance document requiring zero application code or database schema.
- **Slice 506 / OE-378 (`privacy.processing_activities`)**: The **operator-facing product primitive**. It will ship a first-class CRUD entity, database table (`privacy.processing_activities`), tenant-isolated RLS rules, and UI/API surface allowing an operator to manage _their own_ organisation's custom processing activities. Slice 506 remains gated on the `privacy-v0` demand trigger (Open Question #7).

---

## Processing Activities Register (Art. 30(1) / (2))

The table below enumerates all six required Article 30 columns for each processing purpose in the platform:

1. **Processing Purpose**: The operational objective of the processing.
2. **Data Categories**: Specific personal data fields processed (cross-referenced to the [Personal Data Inventory](../../docs/audits/330-privacy-gdpr-ccpa-audit.md#3-personal-data-inventory)).
3. **Data Subjects**: The categories of individuals whose data is processed.
4. **Recipients**: Internal and external entities receiving or accessing the data.
5. **Retention Period**: The duration for which the data is retained before disposal (cross-referenced to the [Data Retention Policy](./data-retention.md)).
6. **Transfer Mechanism**: Safeguards applied when data crosses EU/EEA boundaries (cross-referenced to Chapter V requirements and F-4 analysis).

| Processing Purpose                                                                                                                                                             | Data Categories                                                                                                                                                                                                                 | Data Subjects                                                                | Recipients                                                                                                 | Retention Period                                                                                                                                 | Transfer Mechanism                                                                                                                                                 |
| :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | :--------------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **1. Operator Account & Authentication Management**<br>_Managing operator access, local credentials, OIDC identity mapping, and user sessions._                                | Direct identifiers (`users.email`, `users.display_name`), workplace identity (`idp_issuer`, `idp_subject`), credentials (`local_credentials.password_hash`), online identifiers (`sessions.user_agent`, `sessions.ip_address`). | Platform operators, security team members, system administrators.            | Platform database (`atlas_app` role), tenant administrators.                                               | Retained for active account duration plus 90 days post-deactivation. Session tokens expire after configured max age.                             | **None (Local/Self-Hosted)**.<br>Data remains inside operator-controlled infrastructure.                                                                           |
| **2. Evidence Ingestion of Workforce Data**<br>_Ingesting evidence records from HRIS, IdP, MDM, GitHub, and Slack connectors for control evaluation._                          | Employment metadata (`work_email`, `employment_status`, `start_date`, `end_date`, `title`, `department`, `manager_assignment_id`), IdP lifecycle (`login`, `primary_email`, `last_login_at`), device posture details.           | Operator's workforce (employees, contractors, workforce users).              | Platform evidence ledger (`evidence_records`), control evaluators, authorized auditors via audit periods.  | **Indefinite append-only ledger** (Canvas Invariant #3). Per-record tombstoning via ADR-0020 upon ratified erasure request.                      | **None (Local/Self-Hosted)**.<br>Connectors execute locally; data ingested directly into self-hosted Postgres.                                                     |
| **3. Security Audit Logging & System Integrity**<br>_Recording administrative actions, authentication exchanges, token revocations, and API requests for security monitoring._ | Actor references (`decision_audit_log.actor_id`, `super_admin_audit_log.actor_user_id`), network metadata (`sessions.ip_address`, `oauth_token_exchanges.ip_address`, `oauth_revoked_tokens.ip_address`), OAuth subject tokens. | Platform operators, API consumers, administrative actors.                    | Security administrators, internal audit sinks, external syslog/SIEM endpoints (if configured by operator). | Audit-log family tables retained indefinitely by default; IP/session metadata subject to 1-year rolling window pruning under PRIV-8 remediation. | **None (Local/Self-Hosted)**.<br>Logs stored in local Postgres / NATS JetStream stream.                                                                            |
| **4. Notification Delivery & Alerting**<br>_Delivering transactional emails, control status alerts, and workflow notifications to configured channels._                        | Contact information (`email_channel_optin.email_address`), delivery metadata (`email_delivery_log.recipient`, `channel_delivery_log.target_channel`).                                                                           | Opted-in operators, control owners, assigned remediators.                    | Configured SMTP gateway, external webhook endpoints (Slack, Microsoft Teams).                              | Delivery logs retained for 90 days post-transmission, then purged via automated cleanup.                                                         | **Operator-Configured Egress**.<br>Egress depends on operator's configured SMTP server or Slack/Teams webhook URLs.                                                |
| **5. AI-Assist Generation & Narrative Drafting**<br>_Generating draft control responses, gap explanations, and board narratives via LLM inference._                            | Prompt inputs (`ai_generations.system_prompt`, `context_inputs`), evidence excerpts containing workforce email/identity snippets, generated drafts, operator edits.                                                             | Operators, cited workforce data subjects (indirectly via evidence excerpts). | Local Ollama instance (default) OR tenant-selected Cloud LLM provider (Anthropic, OpenAI, AWS Bedrock).    | Audit log of prompt/response retained append-only for forensic accountability (Canvas AI-assist boundary).                                       | **Provider Dependent**:<br>- _Default (Local Ollama)_: None.<br>- _Cloud LLM Opt-in_: Transferred to chosen provider in US. See Chapter V analysis (F-4 / OE-450). |

---

## Decided Article 6 Lawful Bases

Under GDPR Article 6(1), processing is lawful only if at least one legal basis applies. The audit in slice 330 noted that these bases were previously undocumented. This document commits to the following decided lawful bases for each purpose:

### Purpose 1: Operator Account & Authentication Management

- **Decided Lawful Basis:** **GDPR Article 6(1)(b) — Contract** (or Article 6(1)(f) — Legitimate Interests for non-contractual workforce operators).
- **Justification:** Provisioning and managing operator credentials and session tokens is necessary to execute the contract between the platform deployment operator and its authorized staff, enabling access to the system.

### Purpose 2: Evidence Ingestion of Workforce Data

- **Decided Lawful Basis:** **GDPR Article 6(1)(c) — Legal Obligation** AND **GDPR Article 6(1)(f) — Legitimate Interests**.
- **Justification:** Operators ingest workforce evidence to satisfy legal, regulatory, and contractual compliance frameworks (e.g., SOC 2, ISO 27001, PCI DSS, HIPAA). Ingesting workforce status is necessary for the legitimate interest of maintaining enterprise information security controls.
- **Legitimate Interests Assessment (LIA) Status:** **Outstanding / Formally Required**.
  - _LIA Necessity:_ While Legal Obligation (Art. 6(1)(c)) covers compliance-mandated controls, processing non-mandatory workforce telemetry relies on Legitimate Interests (Art. 6(1)(f)).
  - _Required LIA Balance Test:_ The operator's DPO must document a formal LIA weighing:
    1. **Purpose Test:** Verifying the legitimate interest in continuous security control monitoring.
    2. **Necessity Test:** Ensuring evidence schemas enforce strict PII minimization (e.g., excluding SSN, salary, personal contact info per schema contracts).
    3. **Balancing Test:** Weighing workforce privacy rights against enterprise security, ensuring safeguards (RLS tenant isolation, role-based access, hash verification) mitigate data subject risk.

### Purpose 3: Security Audit Logging & System Integrity

- **Decided Lawful Basis:** **GDPR Article 6(1)(f) — Legitimate Interests** (read in conjunction with Recital 49).
- **Justification:** Recital 49 explicitly recognizes processing personal data to the extent strictly necessary and proportionate for ensuring network and information security as a legitimate interest. Retaining actor logs and network IP metadata is essential for breach detection, forensics, and operational accountability.

### Purpose 4: Notification Delivery & Alerting

- **Decided Lawful Basis:** **GDPR Article 6(1)(f) — Legitimate Interests**.
- **Justification:** Alerting operators and control owners regarding failing controls, pending approvals, and security events is necessary for the legitimate interest of operational security management.
- _Note on Opt-in:_ The `email_channel_optin` table provides an operational preference toggle (default-off); it is an operational courtesy, not formal GDPR Article 7 consent.

### Purpose 5: AI-Assist Generation & Narrative Drafting

- **Decided Lawful Basis:** **GDPR Article 6(1)(f) — Legitimate Interests**.
- **Justification:** Assisting security leaders with draft narrative generation and gap analysis serves the legitimate interest of efficient governance reporting.
- _Cloud Routing Constraint:_ When cloud providers (Anthropic, OpenAI, AWS Bedrock) are selected, processing involves cross-border transfers. See Transfer Safeguards below.

---

## Transfer Safeguards (Chapter V)

Data processing in standard self-hosted deployments occurs entirely within the operator's private infrastructure (no phone-home, no telemetry, no mandatory cloud dependencies).

For **Purpose 5 (AI-Assist Generation)**, when an operator opts into cloud-based LLM providers, transfers of evidence excerpts to third-party processors occur:

- **Local Inference (Default - Ollama)**: No cross-border transfer occurs. Data does not leave the local deployment.
- **Cloud LLM Providers (Anthropic, OpenAI, AWS Bedrock)**:
  - **Transfer Mechanism:** Transferred to vendor API endpoints (primarily located in the United States).
  - **Safeguard Status:** Chapter V compliance analysis, Standard Contractual Clauses (SCCs), and Transfer Impact Assessments (TIAs) for cloud LLM routing are governed separately by **follow-up F-4 (OE-450 / `docs/governance/cloud-llm-transfer-analysis.md`)**. Operators MUST NOT enable cloud LLM routing in EU jurisdictions without executing appropriate SCCs and ensuring regional data residency.

---

## Verification of Database Entities

As required by OE-449 AC-4, all database tables and columns cited in this RoPA have been verified against the `main` database schema (`migrations/sql/`):

- `users` (`email`, `display_name`, `idp_issuer`, `idp_subject`, `status`) — `20260511000012_users_sessions_api_keys.sql`
- `local_credentials` (`password_hash`, `params`) — `20260511000012_users_sessions_api_keys.sql`
- `sessions` (`user_id`, `idp_issuer`, `idp_subject`, `user_agent`, `ip_address`, `geo_country`, `geo_city`) — `20260518100000_sessions_augment_ua_ip_geo.sql`
- `evidence_records` (`payload`, `observed_at`, `hash`) — `20260511000004_evidence_ledger.sql`
- `decision_audit_log` — `20260511000000_init.sql`
- `super_admin_audit_log` — `20260521030000_super_admins_full.sql`
- `oauth_token_exchanges` — `20260521000020_oauth_token_exchanges.sql`
- `oauth_revoked_tokens` — `20260521000050_oauth_revoked_tokens.sql`
- `email_channel_optin` — `20260607020000_email_delivery_channel.sql`
- `email_delivery_log` — `20260607020000_email_delivery_channel.sql`
- `channel_delivery_log` — `20260608000000_slack_webhook_channels.sql`
- `ai_generations` (`system_prompt`, `context_inputs`, `raw_draft`) — `20260607000000_ai_generations.sql`
- `tenant_llm_routing` — `20260612100000_tenant_llm_routing.sql`
