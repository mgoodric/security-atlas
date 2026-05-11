**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 1. Vision and Positioning

## 1.1 Product thesis

> **security-atlas is a control-graph and evidence-pipeline platform that lets you operate one security program against many frameworks, with the same source of truth feeding live posture, audit evidence, and OSCAL exchange — instead of duplicating effort per audit.**

## 1.2 Why not Vanta / Drata / SecureFrame / OpenGRC / eramba

**The commercial incumbents** (Vanta, Drata, SecureFrame, Hyperproof, OneTrust) are vertically-integrated SaaS optimized for "first SOC 2 in 90 days" SMB onboarding. They sell speed-to-attest. Their architectural commitments — proprietary collectors, opaque control libraries, framework-by-framework duplication of evidence — are commercially defensible (lock-in) but engineering-hostile. They model controls as rows in a per-framework grid; security-atlas models them as nodes in a versioned semantic graph. They model evidence as collector output stored in a vendor cloud; security-atlas models it as an append-only stream you own.

The most-cited practitioner pain is the **Year-2 renewal cliff** — quotes on HN and G2 describe invoices jumping 40%+ in renewal, with HIPAA/ISO add-ons disclosed only post-signature ([HN 25808737](https://news.ycombinator.com/item?id=25808737); [SecureLeap 2026](https://www.secureleap.tech/blog/vanta-review-pricing-top-alternatives-for-compliance-automation)). Owning the tool removes that lever entirely.

**The OSS prior art:**

- **eramba** — mature (since 2007), real audit footprint, Community Edition free. Limits: PHP monolith, dated UI, no native cloud-evidence collection. We borrow workflow concepts; we do not build on top.
- **OpenGRC** — useful reference for data-model intuition and framework-seeder packs. **Disqualified as a foundation** for three independent reasons: (1) **CC BY-NC-SA license** — non-commercial share-alike is incompatible with the permissive license we need for an OSS GRC product to be embedded in commercial deployments, (2) **single-tenant Laravel monolith** is the wrong substrate for a control-graph + evidence-pipeline platform, (3) **zero automated connectors** — manual evidence only. We borrow patterns and ship parallel.
- **SimpleRisk** — best-in-class risk register, narrow scope. We support import.
- **CISO Assistant** — rising entrant; cooperate where possible.

We do not pretend OpenGRC or eramba already solve this. They each solve a slice.

## 1.3 Explicit non-goals (v1)

| #   | Non-goal                                                                  | Why                                          |
| --- | ------------------------------------------------------------------------- | -------------------------------------------- |
| 1   | Enterprise content moat (OneTrust-style 10,000-page regulatory libraries) | Different product; doesn't fit OSS economics |
| 2   | Closed proprietary connectors                                             | Defeats the OSS thesis; locks users in       |
| 3   | Replacing SIEM / detection engineering                                    | Detect-as-code is adjacent, not GRC's job    |
| 4   | Replacing IAM, MDM, vuln scanners                                         | Compose them; don't rebuild them             |
| 5   | "Compliance as a service" managed offering                                | We ship software; partners run it            |

## 1.4 Personas

The primary persona for v1 is anchored on a real user: **the solo security leader at a 70-person security-product startup who runs the entire program — risk register, board reporting, SOC 2, ISO 27001 (prospect-driven), vendor reviews, policies, exceptions — alone.** Every design decision is filtered through "does this help that person?" before "does this scale to a 2,000-person enterprise?"

| Persona                                                                        | Role                                                                                                                                                                                                                                               | v1 priority  | Workflow they care about                                                                                                                                                                                          |
| ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Solo Security Leader at a 50–150-person security-product startup** (PRIMARY) | Owns: SOC 2 audit, ISO 27001 (when prospects demand it), HIPAA/PCI as customers require, risk register, board reporting, policies, vendor reviews, access reviews, incident response. No GRC team. May add 1–2 GRC-touching members within a year. | v1 lead      | "Run the entire program from one tool I own. Generate the board deck on the day of the meeting. Survive the SOC 2 audit without consulting hours. Respond to security questionnaires without rebuilding answers." |
| **GRC Engineer at a 100–2,000-person company** (secondary)                     | Hybrid security + platform engineer. Owns control-as-code, evidence pipelines, auditor relationships.                                                                                                                                              | v1 supported | "Author a control once, satisfy it across SOC 2 + ISO + HIPAA, watch evidence stream in, see drift instantly."                                                                                                    |
| **CISO / Head of Security** (secondary)                                        | Reports posture to board, exec, customers. Buys the tool.                                                                                                                                                                                          | v1 supported | "Program-level posture across BUs/frameworks with board-ready narrative, in one screen."                                                                                                                          |
| **Internal / External Auditor**                                                | Read-only sampling + walkthrough + SSP review                                                                                                                                                                                                      | v1 must-have | "Pull a sample of N items for control X over period Y, with provenance, in OSCAL. Comment on findings in-product, not over email."                                                                                |
| **Compliance Analyst**                                                         | Manual control owner, policy custodian                                                                                                                                                                                                             | v2           | "Track policy attestations, vendor reviews, access reviews on a calendar."                                                                                                                                        |

**The solo-operator filter changes specific design points:**

- Setup must be measurable in hours, not weeks — config-as-code seeded from sensible defaults.
- Board reporting is a v1 feature, not a v3 nicety (a CISO at a 70-person co reports to the board quarterly minimum).
- Vendor risk module must work for ~30–80 vendors, not 5,000 — but must include contract dates, DPA status, and review cadence (the spreadsheet escape vector to close).
- Self-host story must work on one mid-size VM. NATS JetStream (single binary), Postgres (single instance), one app server. No required Kafka, no required ClickHouse for v1.
- Manual controls and human attestations are equally first-class as automated — a solo operator cannot author 200 evidence pipelines in a quarter.

**Why this persona over a generic SMB GRC engineer**: a security-product company is the hardest case — your customers will diligence the diligence tool itself. Self-hosting your own GRC platform becomes a trust differentiator ("our compliance evidence does not live in a third party's cloud"). If the design works for that case, it works for less-scrutinized buyers.

## 1.5 "Replacement-grade" — measurable acceptance criteria

A user can credibly drop Vanta/Drata when security-atlas can:

1. Run an end-to-end SOC 2 Type II audit cycle without leaving the tool.
2. Map ≥10 frameworks with shared-control crosswalking such that one piece of evidence satisfies N control instances simultaneously.
3. Provide ≥100 first-party connectors covering AWS, GCP, Azure, GitHub/GitLab, Okta/Azure AD/Google Workspace, Jamf/Intune/Kandji, Jira/Linear, common HRIS, and major SaaS (Slack, 1Password, Datadog, PagerDuty). v1 ships with a focused 12–15-connector subset; full 100+ is a phase-2 milestone.
4. Generate an OSCAL SSP and POA&M that an auditor accepts without manual reformatting.
5. Continuously evaluate ≥80% of _automatable_ in-scope controls without human input — and clearly model the remainder as manual.
6. Survive a third-party security review of multi-tenant isolation in self-host deployments.
7. **Be installable, seeded, and producing first evidence within 4 hours by a solo security leader without consulting help.** This is the single most important acceptance criterion — it determines whether the tool is usable by its primary persona.
8. **Generate a board-ready slide pack** (PDF + editable export) covering posture, top risks aging, control coverage trend, open findings, and an auto-drafted narrative — without a separate trip to PowerPoint.

If any of these eight is missing, the tool is not replacement-grade. Pre-v1, we say so.

## 1.6 Anti-patterns we explicitly reject

Practitioner research surfaced the recurring patterns that erode trust in GRC tools. We commit to _not_ shipping these:

| Anti-pattern                                                                | What it is                                                                             | Why it kills trust                                                                                                                                                                                                           |
| --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Policy template libraries dressed as a feature**                          | 50 pre-written policies "ready to ship"                                                | Auditors don't read them; they check `last_revised` and `last_review` dates. The library encourages cargo culting. We ship 5 high-signal templates with explicit ownership, not 50 placeholder docs.                         |
| **AI-generated policy text and questionnaire answers without human review** | "Auto-draft the policy from your control state"                                        | Hallucinated content makes it into legal-binding artifacts. Auditors privately roll eyes. We support AI assistance for _summarization_ and _gap explanation_; we never generate policy text or audit responses unsupervised. |
| **The collector agent on every laptop**                                     | Drata-style endpoint agent for evidence                                                | Customers of security-product companies ask "do you run a vendor's agent?" — the answer matters. We use osquery / Fleet (open) and read-only API integrations. No proprietary agents.                                        |
| **Vanity trust centers**                                                    | Public posture page nobody visits before sending the same questionnaire                | Skip until v3 unless customers actively demand.                                                                                                                                                                              |
| **"Continuous monitoring" that runs daily, not continuously**               | Marketing language for "we re-poll every 24h"                                          | We commit to event-driven where APIs allow; we name the interval honestly elsewhere.                                                                                                                                         |
| **Per-framework duplicated controls**                                       | A separate `ISO-A.5.15` row from a `SOC2-CC6.1` row even when they're the same control | The whole point of the UCF graph is to refuse this. One control, N framework satisfactions.                                                                                                                                  |
| **Audit-period evidence pollution**                                         | Post-window changes leaking into "as-of" sample populations                            | We freeze evidence at audit-period boundaries (see [§8 Audit Workflow](./08-audit-workflow.md)).                                                                                                                             |

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · **Next:** [2. Domain Primitives →](./02-primitives.md)
