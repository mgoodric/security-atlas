# ADR 0022 — Chapter V Transfer Analysis and Guard for Cloud-LLM Opt-In

**Status:** Accepted

**Date:** 2026-08-07

**Decides:** GDPR Chapter V restricted transfer posture, provider transfer analysis (Anthropic, OpenAI, AWS Bedrock), opt-in disclosure guard, and RoPA sub-processor listing for tenant cloud-LLM routing (`tenant_llm_routing`).

**Resolves:** OPENENGINE-450 / Follow-up F-4 from slice 330 privacy audit ([`docs/audits/330-privacy-gdpr-ccpa-audit.md`](../audits/330-privacy-gdpr-ccpa-audit.md) finding PRIV-5).

**Shipped in this change (docs/analysis):**

- Transfer analysis for Anthropic, OpenAI, and AWS Bedrock (§2 below and [`docs/ai-assist/cloud-routing.md`](../ai-assist/cloud-routing.md)).
- Copy-only transfer guard in **operator docs** ([`docs/ai-assist/cloud-routing.md`](../ai-assist/cloud-routing.md)) and this ADR.
- Candidate sub-processor listing for RoPA cross-border transfers ([`docs/governance/data-retention.md`](../governance/data-retention.md) and candidate RoPA pointers).

**Not yet implemented (follow-up — OPENENGINE-683):** the same copy-only guard has **not** yet been added to the admin endpoint API spec ([`docs/openapi.yaml`](../openapi.yaml), [`internal/api/openapi/routes.go`](../api/openapi/routes.go)) or the UI opt-in flow (`web/app/api/admin/llm-routing/route.ts`). Those surfaces still need the disclosure copy; this ADR records the decision, not a completed API/UI implementation.

---

## Context

Slice 499 ([`migrations/sql/20260612100000_tenant_llm_routing.sql`](../../migrations/sql/20260612100000_tenant_llm_routing.sql)) introduced per-tenant cloud-LLM routing (`local-ollama | anthropic | openai | bedrock`), enabling operators to route AI-assist generations (such as board narratives, questionnaire answer suggestions, and control gap explanations) to third-party cloud providers.

Slice 330's privacy audit finding PRIV-5 (High) identified that while the local Ollama default is non-transferring and off by default, opting a tenant into a cloud provider transmits prompts containing personal data—such as named workforce members' work emails, titles, and employment status in HRIS/Okta evidence excerpts—to third-party providers located in the United States.

Specifically:

1. **No Transfer Mechanism Documented:** The platform provided no Chapter V (GDPR Art. 44–49) analysis, Standard Contractual Clauses (SCCs), adequacy basis, or Transfer Impact Assessment (TIA) guidance for EU operators enabling cloud LLMs.
2. **Provenance Banner vs. Transfer Disclosure:** The existing UI banner (_"AI assist routes to {provider} — your data leaves this deployment"_) serves as a _provenance_ disclosure for the AI-assist boundary (canvas §4.6.5), which is legally distinct from an Art. 13/14/44 _transfer disclosure_ and Art. 28(3) sub-processor notification.
3. **Provider Scoping Differences:** AWS Bedrock supports explicit regional endpoints (e.g. EU regions such as `eu-central-1`), whereas Anthropic and OpenAI process data via US-based cloud infrastructure endpoints.
4. **Anti-SSRF Security Invariant:** The database schema deliberately hard-codes provider enum values and avoids storing arbitrary base URLs in PostgreSQL to prevent SSRF and data exfiltration primitives (`20260612100000_tenant_llm_routing.sql:25-34`).

---

## Decision

### 1. Chapter V Transfer Analysis by Provider

We record a formal Chapter V transfer analysis for each supported cloud provider:

- **Anthropic (US):**

  - _Data Transmitted:_ Assembled AI-assist prompt containing cited evidence excerpts (HRIS/Okta names, emails, titles), policy text, control descriptions.
  - _Destination Jurisdiction:_ United States (third-country transfer under GDPR Art. 44).
  - _Transfer Mechanism:_ EU-US Data Privacy Framework (DPF) adequacy decision (Art. 45) where certified, supplemented by Standard Contractual Clauses (SCCs, Art. 46(2)(c)) in Anthropic's Commercial Terms / DPA.
  - _Prerequisite for EU Operator:_ The operator (as Data Controller) must execute Anthropic's Commercial DPA (including SCCs) and complete a Transfer Impact Assessment (TIA) evaluating US surveillance law access risks (EDPB Recommendations 01/2020).

- **OpenAI (US):**

  - _Data Transmitted:_ Assembled AI-assist prompt containing cited evidence excerpts, policy text, control descriptions.
  - _Destination Jurisdiction:_ United States (third-country transfer under GDPR Art. 44).
  - _Transfer Mechanism:_ EU-US Data Privacy Framework (DPF) adequacy decision (Art. 45) where certified, supplemented by OpenAI Business DPA with Standard Contractual Clauses (Art. 46(2)(c)).
  - _Prerequisite for EU Operator:_ The operator must execute OpenAI's Business DPA, ensure zero-data-retention (ZDR) or business privacy terms are enabled, and complete a TIA.

- **AWS Bedrock (Region-Scoped / EU Option):**
  - _Data Transmitted:_ Assembled AI-assist prompt containing cited evidence excerpts, policy text, control descriptions.
  - _Destination Jurisdiction:_ EU region (e.g., `eu-central-1` Frankfurt, `eu-west-1` Ireland) when configured for an EU AWS endpoint; US regions if configured for US endpoints.
  - _Transfer Mechanism:_ If routed to an EU Bedrock endpoint, processing remains within the EEA (no Chapter V restricted transfer occurs). If routed to a US Bedrock endpoint, AWS GDPR DPA incorporating SCCs applies under Art. 46(2)(c).
  - _Prerequisite for EU Operator:_ The operator should select an EU AWS Bedrock region endpoint to avoid cross-border transfer entirely, or execute the AWS GDPR DPA with SCCs if using US endpoints.

### 2. Decision on Guard Mechanism: Prominent Copy & Administrative Disclosure Guard (Option b)

We evaluate the two guard options specified in the F-4 work order:

- _Option (a) Residency Column:_ Adding a DB migration for region selection.
- _Option (b) Copy-Only Guard:_ Prominent EU operator warning & transfer disclosure in admin endpoints, API specifications, and opt-in documentation.

**We select Option (b) (Copy-Only Guard).**

_Justification:_

1. **SSRF Hardening Preservation:** Keeping base URLs hard-coded in Go adapters prevents database-driven SSRF attacks. Adding a DB region column without an underlying multi-region Go transport refactor creates schema complexity without operational utility, as current Anthropic/OpenAI integrations use uniform global API endpoints.
2. **Legal Duty Alignment:** GDPR Chapter V compliance for self-hosted software operators is fundamentally a Controller duty (executing DPAs/SCCs and TIAs prior to enabling third-party cloud routing). A prominent warning at the exact point of administrative opt-in directly addresses the regulatory notice requirement without altering the underlying database or default local posture.

### 3. Separation of Transfer Disclosure from Provenance Banner

The platform maintains strict separation between two distinct affordances:

- **AI-Assist Provenance Banner:** Displayed to end-users reviewing drafts (_"AI assist routes to {provider} — your data leaves this deployment"_), fulfilling AI transparency and human-in-the-loop validation (canvas §4.6.5).
- **Transfer Disclosure & Sub-processor Guard:** Displayed to Tenant Admins at the `/v1/admin/llm-routing` opt-in boundary and documentation, informing the Controller of their GDPR Chapter V / Art. 28 obligations prior to enabling cloud routing.

### 4. Candidate Sub-processors for RoPA (Art. 30 / Art. 28)

The three cloud providers are formally listed as candidate sub-processors in the platform governance documentation (`docs/governance/data-retention.md` and personal data inventory):

- **Anthropic, PBC** (US) – AI inference provider (Opt-in)
- **OpenAI, L.L.C.** (US) – AI inference provider (Opt-in)
- **Amazon Web Services, Inc. / AWS Bedrock** (US/EU) – AI inference provider (Opt-in)

---

## Consequences

- **Security Posture Maintained:** Provider base URLs remain hard-coded in Go; no arbitrary endpoint URL or SSRF vector is introduced into `tenant_llm_routing`.
- **Default Posture Intact:** Absence of a routing row defaults strictly to local Ollama. No cloud provider is enabled by default.
- **Operator Governance Clarity:** EU self-host operators receive clear, actionable Chapter V transfer requirements and candidate sub-processor listings before enabling cloud LLM routing.
