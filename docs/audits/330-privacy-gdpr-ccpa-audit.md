# 330 — Privacy audit (GDPR + CCPA/CPRA) report

**Slice:** 330
**Date:** 2026-07-25
**Auditor agent:** `voltagent-qa-sec:gdpr-ccpa-compliance`
**Audit type:** Read-only-with-findings; JUDGMENT slice (no code changes)
**Audit scope:** `main` at HEAD `ba2de9b1`
**Audit posture:** **operator-side deployability** — "can an EU or California
operator run this compliantly?" — NOT the meta-audit posture of slice 329
("can the project itself pass an audit?")

> **Provenance note (read this first).** This slice carried
> `Status: merged (status reconciled 2026-06-03 — backlog drained per _STATUS.md
SoR; loop terminated batch 184)` from 2026-06-03 until 2026-07-25. That was an
> **administrative bulk-reconcile, not a real completion**: no deliverable for
> slice 330 existed in `docs/audits/`, which held reports for slices 327, 328,
> 329, 331, 332, 333, 334, 335, 337, 348 and 351 but not this one. The audit was
> never run. This report is the first execution of the slice. See
> `docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md` D0.
>
> **Blast radius of the error.** Slices 504, 505 and 506 each cite
> "**#330** (privacy GDPR/CCPA audit) — `merged`" in their Dependencies section
> (`504:156`, `505:134`, `506:125`), and slice 504's `AC-7` (`504:138`) plus
> `P0-504-4` (`504:149`) implement against "slice 330 AC-3's ratified ADR." That
> ADR did not exist. Three privacy slices were built on a dependency that had
> never been delivered. This slice delivers it:
> [ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md).
>
> **Correction to the OE-394 work-order's framing.** The work-order states that
> slices 504, 505, 506 **and 507** each cite #330 as satisfied. Verified against
> the files: **507 does not.** `docs/issues/507-breach-notification-workflow-implementation.md:135-145`
> lists #446, #180, #372, #445 and the privacy-v0 greenlight; slice 330 appears
> nowhere in that file. 507 is gated on ADR-0017/#446, not on this slice. The
> blast radius is three slices, not four.

---

## Executive summary

The platform's **privacy-by-design discipline at the ingestion boundary is
genuinely excellent** — better than the sibling audits would lead you to expect.
The evidence-kind JSON Schemas carry written, enforced PII exclusion lists with
`additionalProperties: false`; the device-posture schema explicitly refuses
geolocation and personal contact details; vendor emails are masked on every
export format behind a constitutional unit test; there is no telemetry, no
analytics SDK, no error-reporting phone-home anywhere in the product; and local
inference is the default with cloud LLM off unless a tenant opts in.

The gap is on the **other side of the ledger**. The platform actively ingests
and permanently retains structured personal data about **third-party data
subjects** — the operator's employees, pulled through the HRIS, Okta, MDM,
GitHub and Slack connectors — into an append-only store, and ships **zero**
capability, documentation, or data map for Art. 15 access, Art. 17 erasure,
Art. 30 records, or Art. 6 lawful basis. These people have no account, no login,
and no visibility into the system that holds their employment status, hire date
and termination date.

**Headline finding (Critical).** Right to erasure is structurally
unexerciseable. `evidence_records` denies UPDATE and DELETE to `atlas_app` by
construction; the only user-deletion verb in the codebase
(`SCIM DELETE /scim/v2/Users/{id}`) is contractually forbidden from deleting;
there is no `DELETE FROM users` anywhere in the sqlc query set. An operator
receiving an Art. 17 request from a terminated employee has no lawful path short
of hand-writing BYPASSRLS SQL that silently breaks the content-hash chain with
no audit trail. Compounding this,
[`docs/governance/data-retention.md`](../governance/data-retention.md) currently
asserts that "the platform supports per-record deletion via tenant-controlled
operations" — **that claim is not accurate against the code**.

**The structural observation that outranks any individual finding.** All three
privacy follow-up slices (504 erasure, 505 DSAR, 506 RoPA) are gated on a
privacy-v0 greenlight tied to prospect demand (OQ #7). But the Art. 15 and
Art. 17 exposure is created by connectors that ship on `main` **today**. The gate
is on the wrong side of the risk. The documentation half of the remedy — the
personal-data map, the project RoPA, the transfer analysis, the corrected
retention claim — requires no product work and should not inherit the module's
demand gate. That is the shape of this audit's follow-up fan-out.

| Severity      | Count | Notes                                                                                                                           |
| ------------- | ----- | ------------------------------------------------------------------------------------------------------------------------------- |
| Critical      | 1     | Right to erasure structurally unexerciseable while the platform holds third-party workforce PII (PRIV-2)                        |
| High          | 4     | No DSAR capability; no RoPA + no lawful basis; no personal-data map; undocumented Chapter V transfer via cloud-LLM opt-in       |
| Medium        | 3     | Controller/processor determination unstated + unrepresentable in schema; `subject_module` pre-commitment drift; no identity TTL |
| Low           | 2     | DCO contributor-identity notice; `vendors.owner_user` naming + hard-delete asymmetry                                            |
| Informational | 2     | Breach-notification design pressure (recorded, NOT decided); the verified-positives register                                    |

**Follow-ups filed:** 6 Open Engine issues (see [§8](#8-follow-up-fan-out)).
DSAR, erasure and RoPA are **not** re-filed — slices 505, 504 and 506 already
own them (P0-330-4 honoured; no bundling).

**Load-bearing deliverable.** AC-3's erasure-design decision is ratified as
[ADR-0020 — Right to erasure vs. the append-only evidence
ledger](../adr/0020-right-to-erasure-vs-append-only-ledger.md). It **decides**:
**tombstone-by-default with a scoped refuse-with-recorded-basis branch**. It
**confirms the design** slice 504 assumed — no pivot, `P0-504-1` stands — but it
**revises the mechanism** in seven material ways, four of which change slice
504's migration. So slice 504 needs no _design_ revision and does need seven
_spec_ amendments; ADR-0020 §7 enumerates them. Do not read "tombstone
confirmed" as "504 is ready to build as written."

---

## Methodology

The audit visited the nine privacy surfaces named in the slice doc narrative
(`docs/issues/330-privacy-gdpr-ccpa-audit.md`), plus a tenth surface the slice
doc implies but does not name: the **personal-data inventory** — where personal
data actually lands in the schema. That inventory ([§3](#3-personal-data-inventory))
is the load-bearing raw material for every other finding, and it did not exist
in any form before this audit.

Method: static read of `migrations/sql/` (192 files),
`internal/db/dbx/models.go`, `internal/db/queries/*.sql`,
`internal/api/schemaregistry/schemas/`, `internal/**`, `web/**`, `connectors/**`,
plus the governance corpus (`GOVERNANCE.md`, `SECURITY.md`, `CONTRIBUTING.md`,
`docs/governance/`, `docs/adr/`). No live deployment was touched (slice-doc
boundary). Every finding cites a file path and line read during the audit.

**Severity rubric** (operator-deployability posture; recorded as D2 in the
decisions log):

- **Critical** — a data-subject right is structurally unexerciseable **and** the
  platform actively holds the data. Regulator-visible violation for a deploying
  operator.
- **High** — a required GDPR/CCPA artifact or capability is absent and an
  EU/California operator could not deploy compliantly without building it
  themselves.
- **Medium** — the capability exists but is undocumented, partial, or
  discoverable only by reading code.
- **Low** — present and workable; hardening or documentation sharpening
  recommended.
- **Informational** — correctly deferred, N/A, or a strong baseline worth
  recording.

Note the deliberate asymmetry with slice 329's rubric. 329 asked "would a
third-party reviewer flag this?"; 330 asks "could an operator lawfully deploy?"
The two questions reach different verdicts on the same facts — most visibly on
Art. 30, which 329 classed as a correct deferral and this audit classes as a
live High. That divergence is intentional and is reconciled in
[§7](#7-cross-reference-with-slice-329).

---

## 1. Per-surface verdicts

| #   | Surface                                             | Verdict                                       | Evidence pointer                                                                                                                                                                                                                                                                          | Gap                                                                                                                                                                                                           |
| --- | --------------------------------------------------- | --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **DSAR / right of access** (Art. 15, §1798.110)     | **ABSENT**                                    | No endpoint, query, or doc. `docs/issues/505-privacy-v0-dsar-export-workflow.md:6` — `Status: not-ready`                                                                                                                                                                                  | No subject-correlation surface across ~40 personal-data-bearing tables plus unbounded `evidence_records.payload` JSONB, and no registry of which columns count as personal data.                              |
| 2   | **Right to erasure** (Art. 17, §1798.105)           | **ABSENT — structurally blocked**             | `migrations/sql/20260511000004_evidence_ledger.sql:100-115`; `internal/scim/store.go:343-346`; no `DELETE FROM users` in `internal/db/queries/`                                                                                                                                           | No erasure or redaction code path exists for any personal-data column. `users` rows are permanent by explicit anti-criterion (P0-508-1). See [§5](#5-the-erasure-vs-append-only-reconciliation).              |
| 3   | **Consent management** (Art. 7)                     | **N/A — verified; GOVERNANCE.md claim holds** | Grep for `posthog\|segment\|sentry\|google-analytics\|gtag\|plausible\|mixpanel\|amplitude\|phone_home` across `web/`, `go.mod`, `package-lock.json`, `internal/**`: zero genuine hits (the only matches are the English word "plausible" in prose). `GOVERNANCE.md` telemetry commitment | No consent-requiring processing exists. The one opt-in surface (`email_channel_optin`, `migrations/sql/20260607020000_email_delivery_channel.sql:38-43`) is default-off and is a courtesy, not consent.       |
| 4   | **Records of processing / RoPA** (Art. 30)          | **ABSENT**                                    | No RoPA document, table, or template anywhere. `docs/issues/506-privacy-v0-records-of-processing-activities.md:6` — `not-ready`                                                                                                                                                           | Neither the project's own RoPA nor a product primitive for the operator's. Art. 30(5)'s small-organisation derogation does not rescue an operator: the processing is systematic, not occasional.              |
| 5   | **Lawful basis** (Art. 6)                           | **ABSENT**                                    | No `lawful_basis` token in any of the 192 files under `migrations/sql/`; no per-purpose statement in `GOVERNANCE.md`, `SECURITY.md`, `docs/governance/data-retention.md`, or `README.md`                                                                                                  | Nothing states the basis for the platform's five obvious processing purposes. The Art. 6 taxonomy only arrives with slice 506, which is gated.                                                                |
| 6   | **Controller / processor** (Art. 4(7)-(8), Art. 28) | **PARTIAL**                                   | `GOVERNANCE.md` (no hosted SaaS / no telemetry / no license server); `Plans/canvas/11-open-questions.md` OQ #5; `docs/governance/data-retention.md:99-107`                                                                                                                                | The determination is inferable but never stated, and `tenants` carries no role field — so a deployment cannot record or enforce which tenants are controller-side and which processor-side.                   |
| 7   | **Cross-border transfer** (Chapter V)               | **PARTIAL — a live restricted-transfer path** | `migrations/sql/20260612100000_tenant_llm_routing.sql:81-118` (provider enum `local-ollama\|anthropic\|openai\|bedrock`, **no region column**); `migrations/sql/20260607000000_ai_generations.sql`                                                                                        | Self-host default is genuinely transfer-free. But the per-tenant cloud opt-in ships evidence excerpts containing named individuals to US processors with no SCC reference, no TIA, no residency lever.        |
| 8   | **Privacy by design / by default** (Art. 25)        | **PARTIAL**                                   | Strong: `internal/api/schemaregistry/schemas/hris.worker_lifecycle/1.0.0.json:9`; `.../endpoint.device_posture/1.0.0.json:9,67`. Weak: `migrations/sql/20260520020000_audit_log_subject_module.sql` covers nine tables                                                                    | The slice-180 pre-commitment has drifted — 11 audit-log tables have landed since 2026-05-20 without `subject_module`. `sessions.geo_country`/`geo_city` are a provisioned-but-unpopulated collection surface. |
| 9   | **Breach notification** (Art. 33-34, §1798.82)      | **DEFERRED — correctly**                      | `Plans/canvas/11-open-questions.md` OQ #10 ("**This OQ stays OPEN**"); `docs/adr/0017-breach-disclosure-72h-handoff.md:3` (`ADOPT-DEFERRED`); `docs/issues/507-...md:6` `not-ready`                                                                                                       | None. Design pressure recorded at [PRIV-9](#priv-9--breach-notification-design-pressure-recorded-not-decided); **not pre-decided**, per AC-8 / P0-330-3.                                                      |

---

## 2. Findings

### PRIV-2 — Right to erasure is structurally unexerciseable while the platform holds third-party workforce PII

**Severity: Critical** · GDPR Art. 17; CCPA §1798.105 · **Follow-up: slice 504
(exists, gated) + [OE: retention-doc correction](#8-follow-up-fan-out)**

**Anchors.**

| Anchor                                                                 | What it establishes                                                                                                                                                |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `migrations/sql/20260511000004_evidence_ledger.sql:100-115`            | `evidence_records` has `tenant_read` (SELECT) + `tenant_insert` (INSERT WITH CHECK) and **deliberately no UPDATE or DELETE policy**. The comment says so verbatim. |
| `migrations/sql/20260511000000_init.sql`                               | `FORCE ROW LEVEL SECURITY` on `evidence_records` — the missing policy is therefore a hard deny for `atlas_app`.                                                    |
| `internal/scim/store.go:343-346`                                       | "Delete soft-disables the user (AC-4 / P0-508-1): DELETE never hard-deletes … the row retained so the actor's historical records survive (invariant #2)."          |
| `internal/db/queries/` (whole set)                                     | No `DELETE FROM users` exists anywhere. sqlc is the sole DML source for all DB access.                                                                             |
| `migrations/sql/20260612070000_action_plans.sql:81`                    | `owner_id UUID NOT NULL REFERENCES users(id)` pins the user row; `policy_acknowledgments`' composite FK does the same.                                             |
| `internal/api/schemaregistry/schemas/hris.worker_lifecycle/1.0.0.json` | `work_email`, `employment_status`, `start_date`, `end_date`, `title`, `department`, `manager_assignment_id` land in `evidence_records.payload`.                    |
| `internal/api/schemaregistry/schemas/okta.user_lifecycle/1.0.0.json`   | `login`, `primary_email`, `last_login_at` land in the same ledger.                                                                                                 |

**What's wrong.** Both limbs of the Critical rubric are met.

_(a) Structurally unexerciseable._ No shipped code path can erase or redact any
personal-data column on any table. The one deletion verb that exists is
contractually forbidden from deleting. The one hard-delete against a
personal-data table in the entire query set is `DeleteVendor`
(`internal/db/queries/vendors.sql:40-41`) — an asymmetry noted separately at
PRIV-11.

_(b) The platform actively holds the data._ The HRIS and Okta connectors put a
named employee's work email, employment status, hire date and termination date
into an append-only ledger **by design**. A terminated employee of an EU
operator has an unambiguous Art. 17 right. The operator has no lawful way to
honour it short of connecting on the BYPASSRLS `atlas_migrate` DSN and issuing a
raw `UPDATE`, which silently invalidates `evidence_records.hash` and any
previously cosign-signed export bundle, with no audit trail of what changed.

**Compounding: an inaccurate governance claim that contradicts itself.**
`docs/governance/data-retention.md:109-110` states, verbatim: "The platform
supports per-record deletion via tenant-controlled operations." No such
operation exists for personal-data fields. Two aggravating details:

1. **It is load-bearing, not incidental.** The sentence sits inside §1 "What
   this policy does not cover" (heading at `:98`) and is the stated _reason_ for
   excluding Art. 17 from the policy's scope. A false capability claim used to
   justify a scope exclusion is worse than a bare false claim: it means the gap
   is not merely undocumented, it is documented as already handled elsewhere.
2. **The same document contradicts it.** §4.5 `:512-514` states: "The original
   record is never deleted — canvas invariant #3 forbids it." One merged
   governance document asserts both that per-record deletion is supported and
   that records are never deleted. Cite both lines in the correction.

A DPO reading `:109-110` would conclude the Art. 17 capability is present. This
is the single most urgent correction in the finding set.

**Remediation.** Two tracks, neither of which should wait on privacy-v0:

1. **Immediately** — correct the `data-retention.md` claim so it does not assert
   a capability the code lacks, and state the honest current position. Filed as
   an un-gated OE.
2. **Design** — ratified here as
   [ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md), which
   closes AC-3 and unblocks slice 504. The ADR additionally recommends
   **un-gating slice 504 from the privacy-v0 demand trigger**: Art. 17 exposure
   begins the moment the first EU self-host runs an HRIS connector, not when a
   prospect asks for a privacy module. That re-gating is a maintainer call, not
   an audit finding — surfaced, not decided.

---

### PRIV-1 — No DSAR / right-of-access capability, and no registry of what counts as personal data

**Severity: High** · GDPR Art. 15 + Art. 12(3) (one-month deadline); CCPA
§1798.110 + §1798.130 (45-day deadline) · **Follow-up: slice 505 (exists,
gated)**

**Anchors.** Absence across `internal/db/queries/*.sql` and `internal/api/**`;
`docs/issues/505-privacy-v0-dsar-export-workflow.md:6`.

**What's wrong.** The platform holds personal data about three distinct subject
populations — the operator's staff (`users`, `sessions`), the operator's
**employees** ingested via HRIS/IdP/MDM connectors (`evidence_records.payload`),
and vendor contacts (`vendors.owner_user`) — across roughly 40 tables plus
unbounded JSONB. There is no correlation query, no export endpoint, and
critically **no registry of which columns count as personal data**. An operator
receiving an Art. 15 request cannot answer it without reverse-engineering 192
migration files.

Slice 505 is filed but `not-ready` and gated on a greenlight that has not fired,
so the backlog contains no near-term remedy.

**Remediation.** Slice 505 owns the product surface and stays gated. But the
**registry** does not need to be: slice 505's own §1 already names
`privacy.PersonalDataSurfaces` as the single source of truth shared with slice
504's erasure. [§3](#3-personal-data-inventory) of this report is that registry
in documentation form. Publishing it un-gated as
`docs/governance/personal-data-inventory.md` gives the operator something to
work from today and gives slices 504 and 505 a shared spec that cannot drift.
Filed as an un-gated OE.

---

### PRIV-3 — No RoPA, and no lawful-basis statement for any processing purpose

**Severity: High** · GDPR Art. 30, Art. 6, Art. 5(2) accountability ·
**Follow-up: slice 506 (product primitive, exists, gated) + [OE: project RoPA
document](#8-follow-up-fan-out) (un-gated)**

**Anchors.** No `lawful_basis` token in any of the 192 files under
`migrations/sql/`; `docs/issues/506-privacy-v0-records-of-processing-activities.md:6`;
`docs/audits/329-compliance-meta-audit-report.md` (classes Art. 30 as a correct
deferral and explicitly **not** a Finding).

**What's wrong.** Art. 30(5)'s small-organisation derogation does not rescue an
operator here: the processing is systematic and includes regular monitoring of
workforce access, which is exactly the carve-out to the derogation. Yet neither
the project nor the product offers a RoPA, and the five obvious purposes have no
documented lawful basis:

| Processing purpose                   | Data                                                                 | Plausible basis (undocumented today)                                                   |
| ------------------------------------ | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Operator account management          | `users`, `sessions`, `local_credentials`                             | Contract (operator↔staff) or legitimate interests                                     |
| Evidence ingestion of workforce data | `evidence_records.payload` (HRIS / Okta / MDM)                       | Legal obligation (regulatory duty) + legitimate interests — **needs a documented LIA** |
| Security audit logging               | `decision_audit_log`, `oauth_token_exchanges`, `sessions.ip_address` | Legitimate interests (Art. 6(1)(f), Recital 49 network security)                       |
| Notification delivery                | `email_channel_optin`, `email_delivery_log`                          | Legitimate interests (opt-in default-off is a courtesy, not Art. 7 consent)            |
| AI-assist generation                 | `ai_generations.system_prompt` / `context_inputs` / `raw_draft`      | Legitimate interests — **but see PRIV-5 when routed to a cloud provider**              |

**Remediation.** Split the remedy along the gate. Slice 506's **product
primitive** (a first-class RoPA CRUD entity for the operator's own register)
stays gated on privacy-v0 — correctly, it is real product work. The **project's
own six-column RoPA governance document** (purpose · data categories · data
subjects · recipients · retention · transfer mechanism), using the rows above,
does not need product work and should not inherit the gate. It is the cheapest
artifact in the finding set and it partially unblocks PRIV-1, PRIV-4 and PRIV-6.
Filed as an un-gated OE. **This is not a re-file of slice 506** — different
artifact, different gate, different audience (P0-330-4 honoured).

---

### PRIV-4 — No shipped personal-data map or operator-facing privacy documentation

**Severity: High** · GDPR Art. 13/14 (the operator's transparency duty), Art. 24;
ISO 27001 A.5.34 · **Follow-up: [OE: personal-data inventory](#8-follow-up-fan-out)**

**Anchors.** `docs/SELF_HOSTING.md` contains **zero** occurrences of
`personal data`, `PII`, `privacy`, or `GDPR` (verified by case-insensitive
count). No file matches `**/*privacy*` outside `docs/issues/` and
`docs/audit-log/180-*`. `docs/governance/` contains no privacy document.

**What's wrong.** An operator standing up security-atlas cannot tell their DPO
what the tool collects. The knowledge exists — distributed across 192 migration
files and the evidence-kind schema registry — but has never been assembled. This
is the prerequisite artifact for PRIV-1, PRIV-2 and PRIV-3, and unlike them it
requires no product work at all.

**Candidate dedupe with slice 329.** 329's ISO 27001 A.5.34 row records "no
operator-side PII handling document." Same gap, different altitude. **330 should
own it** — 329 identified it, 330 produces the inventory that closes it. Do not
file twice.

**Remediation.** Publish [§3](#3-personal-data-inventory) as
`docs/governance/personal-data-inventory.md` and link it from
`docs/SELF_HOSTING.md`.

---

### PRIV-5 — Cloud-LLM opt-in is an undocumented Chapter V restricted transfer

**Severity: High** · GDPR Art. 44, Art. 46, Art. 28(3); Schrems II / EDPB
Recommendations 01/2020 · **Follow-up: [OE: transfer analysis + residency
lever](#8-follow-up-fan-out)**

**Anchors.**

- `migrations/sql/20260612100000_tenant_llm_routing.sql:81-118` — closed provider
  enum `local-ollama | anthropic | openai | bedrock`, with **no region column**.
  The migration header (lines 25-34) explains that the base-URL is hard-coded in
  Go rather than stored, precisely to avoid turning the opt-in into an SSRF /
  exfiltration primitive. That is correct security reasoning — but it leaves no
  residency lever at all, and Bedrock in particular is region-scoped in reality
  while being region-blind in this table.
- `migrations/sql/20260607000000_ai_generations.sql` — `system_prompt`,
  `context_inputs` JSONB and `raw_draft` persisted append-only.
- `migrations/sql/20260612050000_board_narrative_ai.sql` — `raw_draft`,
  `operator_edit`, `final_text`, `citations`.
- `CLAUDE.md` "Board-narrative AI-assist" D1 — the input shape is a rollup
  **plus cited evidence excerpts for every claim**.

**What's wrong.** The default posture is excellent: absence of a routing row
means local Ollama, there is no backfill, and the feature is off by default. But
when an EU operator flips the per-tenant opt-in, evidence excerpts — which, per
the HRIS and Okta schemas, contain named individuals' work emails and employment
status — are transmitted to a US-based provider. No transfer mechanism is named
anywhere in code, ADR, or operator docs. No region selection is expressible in
the schema. `CLAUDE.md` promises "a visible banner indicating routing" — that is
a **provenance** banner for the AI-assist boundary, not a transfer disclosure,
and it is a different obligation.

Secondarily, the operator becomes a controller using a US sub-processor with no
Art. 28(3) contract surface and no sub-processor list.

**Remediation.** (a) Document a transfer analysis for each of the three cloud
providers, surfaced at the opt-in flow rather than buried in a doc; (b) add
either a region/residency column or an explicit "EU operators: do not enable
without an SCC and a transfer impact assessment" guard in the admin endpoint
copy; (c) name the three providers as candidate sub-processors in the RoPA
transfer column. Filed as an un-gated OE.

---

### PRIV-6 — Controller/processor determination is inferable but never stated, and the schema cannot express it

**Severity: Medium** · GDPR Art. 4(7)-(8), Art. 26, Art. 28 · **Follow-up:
stated in [§4](#4-controllerprocessor-determination) of this report; the schema
half is recorded as an input to privacy-v0, not filed as a change**

**Anchors.** `GOVERNANCE.md` (governance model + telemetry commitment);
`Plans/canvas/11-open-questions.md` OQ #5 (RESOLVED — pure-community OSS);
`docs/governance/data-retention.md:99-107`; the `tenants` table carries no role
field.

**What's wrong.** The determination ([§4](#4-controllerprocessor-determination))
is correct and defensible, but it lives nowhere an operator would find it.
Worse, the multi-tenant model means one deployment may host a tenant where the
operator is a **controller** and another where the operator is a **processor**
for its own customer — the vCISO-consultancy case the multi-tenant model was
explicitly built for. There is no `controller_role` field on `tenants` to record
which. That matters directly for slices 504 and 505: an erasure request routed
to a processor-tenant must be **forwarded to the controller**, not actioned
locally.

**Remediation.** [§4](#4-controllerprocessor-determination) states the
determination; publishing it in `docs/governance/` rides along with the
personal-data-inventory OE. The missing `tenants.controller_role` field is
recorded here as a **design input to privacy-v0**, deliberately not filed as a
change today — adding a role column with no workflow to consume it is the kind
of speculative schema that slice 180's own P0-180-1 rejected.

---

### PRIV-7 — The slice-180 `subject_module` pre-commitment has drifted

**Severity: Medium** · GDPR Art. 25(1) · **Follow-up: [OE: extend or narrow the
pre-commitment](#8-follow-up-fan-out)**

**Anchors.** `migrations/sql/20260520020000_audit_log_subject_module.sql` — nine
`ALTER TABLE … ADD COLUMN IF NOT EXISTS subject_module` statements, and
`P0-180-7`: "touches ONLY the nine audit-log tables."
`docs/issues/180-privacy-module-foundation.md:10` — the rationale: the column is
"cheap to add today and expensive to retrofit later."

**What's wrong.** Twenty-three audit-log-family tables now exist in the schema.
Nine carry `subject_module`. Three predate slice 180 and were scoped out
deliberately (`artifact_access_log`, `decisions_audit` — both 2026-05-11 — and
`audit_sink_failures`, `20260518000000_audit_sink_failures.sql:40`). The
remaining **eleven landed after 2026-05-20 without the column**. Grep confirms
`subject_module` appears in no migration other than
`20260520020000_audit_log_subject_module.sql` and its `.down.sql`, so a table
absent from the nine provably lacks the column:

| Table                                | Creating migration                                    |
| ------------------------------------ | ----------------------------------------------------- |
| `super_admin_audit_log`              | `20260521030000_super_admins_full.sql`                |
| `imported_catalog_audit_log`         | `20260606010000_oscal_imported_catalogs.sql`          |
| `email_delivery_log`                 | `20260607020000_email_delivery_channel.sql`           |
| `channel_delivery_log`               | `20260608000000_slack_webhook_channels.sql`           |
| `csf_assessment_audit`               | `20260608080000_csf_tier_profile.sql`                 |
| `staleness_rollup_log`               | `20260609000000_staleness_rollup_log.sql`             |
| `scim_audit_log`                     | `20260612020000_scim_provisioning.sql`                |
| `group_role_audit_log`               | `20260612030000_idp_group_role_mappings.sql`          |
| `control_owner_assignment_audit_log` | `20260612060000_control_owner_assign_saved_views.sql` |
| `action_plan_audit_log`              | `20260612070000_action_plans.sql`                     |
| `framework_version_audit`            | `20260612090000_framework_versioning.sql`             |

The retrofit cost slice 180 was paying a migration to avoid is now being
incurred, at greater scale than the original slice. The current state — a
documented pre-commitment that new code silently does not follow — is the worst
of the available positions.

**Remediation.** Pick one: extend the column to the eleven (a mechanical,
idempotent migration in the slice-180 shape), or explicitly narrow the
pre-commitment in `CONTRIBUTING.md` to the nine and record why. Either is
defensible; the drift is not. Filed as an OE.

---

### PRIV-8 — IP address, User-Agent and OIDC subject retained indefinitely with no TTL

**Severity: Medium** · GDPR Art. 5(1)(e) storage limitation · **Follow-up: [OE:
identity-surface retention windows](#8-follow-up-fan-out)**

**Anchors.**

- `migrations/sql/20260518100000_sessions_augment_ua_ip_geo.sql:46-49` —
  `sessions.user_agent`, `ip_address`, `geo_country`, `geo_city`.
- `migrations/sql/20260521000020_oauth_token_exchanges.sql` —
  `subject_token_iss`, `subject_token_sub`, `ip_address`, append-only, no expiry.
- `migrations/sql/20260521000050_oauth_revoked_tokens.sql` — `revoked_by`,
  `ip_address`.
- `docs/governance/data-retention.md` §3.1 Gap 1 — concedes the unified audit log
  is "unbounded today … over-retains."

**What's wrong.** `data-retention.md` honestly names the over-retention and
judges it acceptable "from a compliance posture." That judgment is **true for
SOC 2 and ISO 27001, and false for GDPR Art. 5(1)(e)**, where over-retention of
IP addresses and identity data is itself the violation rather than a
conservative choice. `sessions` rows are never pruned — only `revoked_at`-marked
— and the OAuth audit tables are append-only with no purge job.

The `geo_country` / `geo_city` columns compound it: provisioned, documented as
"populated by a future enrichment slice," currently shipping NULL. That is a
dormant collection surface with no stated purpose — the precise shape Art. 5(1)(b)
purpose-limitation exists to prevent.

**Remediation.** Set and enforce retention windows for `sessions`
(revoked/expired rows), `oauth_token_exchanges` and `oauth_revoked_tokens`.
Either populate the geo columns with a stated purpose or drop them. Filed as
an OE.

---

### PRIV-9 — Breach-notification design pressure (recorded, NOT decided)

**Severity: Informational** · GDPR Art. 33-34; CCPA §1798.82 (Cal. Civ. Code) ·
**No follow-up — OQ #10 stays open**

**Anchors.** `Plans/canvas/11-open-questions.md` OQ #10 ("**This OQ stays
OPEN**"); `docs/adr/0017-breach-disclosure-72h-handoff.md:3` (`ADOPT-DEFERRED`,
held for maintainer review); `docs/issues/507-...md:6` (`not-ready`).

Per **AC-8** and **P0-330-3**, this audit records design pressure only and
proposes no shape. Four pressures surfaced that ADR-0017 does not currently
name:

1. **The immutability primitive for the 72-hour clock anchor exists as a
   pattern, but not as a shared helper.** ADR-0017 requires
   `breach_confirmed_at` to be immutable once set. Exactly one table carries an
   unconditional `BEFORE UPDATE OR DELETE` trigger — `action_plan_audit_log`
   (`migrations/sql/20260612070000_action_plans.sql:345-356`) — and two more
   (`board_packs`, `framework_requirements`) carry conditional `BEFORE UPDATE`
   triggers explicitly justified as surviving `atlas_migrate`'s BYPASSRLS. Every
   other append-only table, `evidence_records` included, is append-only **only
   for `atlas_app`**. A legally load-bearing timestamp needs the trigger, and
   while the pattern is now precedented three times, it is hand-rolled at each
   site with no shared function or migration template. See [§5.1](#51-what-the-ledger-enforces-precisely)
   point 3.
2. **Art. 34 is downstream of the personal-data map, not parallel to it.**
   Notifying affected data subjects requires enumerating them — the same
   registry that DSAR (505) and erasure (504) need. Sequencing pressure: the map
   should land **before** the workflow.
3. **The subject population inside `evidence_records.payload` has never been
   enumerated.** A breach of one tenant's ledger exposes the operator's
   _employees_ — HRIS rosters — not just the operator's platform users.
   ADR-0017's notification-target register assumes the targets are knowable;
   today they are not.
4. **The cloud-LLM opt-in creates an Art. 33(2) processor-notification leg** with
   no contractual or technical surface in `tenant_llm_routing`.

**OQ #10 stays OPEN.** No shape is proposed here.

---

### PRIV-10 — DCO mandates contributor identity into immutable public git history with no notice or stated Art. 17 position

**Severity: Low** · GDPR Art. 13, Art. 17(3) · **Report-only** (Low findings are
documented for maintainer triage without a follow-up, per the slice-329 /
slice-331 precedent)

**Anchors.** `CLAUDE.md` §Style — "The sign-off email MUST match the author
email … must be the human contributor"; `GOVERNANCE.md` §DCO;
`docs/governance/data-retention.md` — source code and git history retained
"**Indefinite** … None — never disposed."

The project is a controller for contributor name and email, permanently,
publicly and irrevocably by design. This is standard OSS practice, and Art.
17(3)(b) / (e) plus technical-impossibility arguments are defensible — but the
position is stated nowhere, and `CONTRIBUTING.md` gives no notice at the point
of collection. One paragraph in `CONTRIBUTING.md` §DCO stating what is
collected, why, that it is permanent, and the project's Art. 17 position would
close it.

---

### PRIV-11 — `vendors.owner_user` stores an email in plaintext; `vendors` is the only hard-deletable personal-data table

**Severity: Low** · GDPR Art. 5(1)(c) data minimisation · **Report-only**

**Anchors.** `migrations/sql/20260511000006_vendor_lite.sql:81` —
`owner_user TEXT NOT NULL DEFAULT ''`;
`internal/api/adminvendors/mask_email_test.go` —
`TestMaskEmailNeverLeaksLocalPart`; `internal/db/queries/vendors.sql:40-41` —
`DeleteVendor`, the only `DELETE FROM` against a personal-data table in the
entire query set.

The export masking is a genuine privacy-by-design win with a constitutional unit
test behind it. Two inconsistencies are worth recording: the column name
(`owner_user`) does not signal that it holds an email address, and `vendors` is
the single entity in the schema with a real hard-delete path while structurally
identical person-references elsewhere have none. Neither is worth a slice today;
both are worth knowing when slice 504 writes the surface registry.

---

### Verified positives (Informational — worth recording)

Each independently confirmed against the source during this audit, not taken
from documentation claims.

| Observation                                                                                      | Anchor                                                                                                       |
| ------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| No telemetry, analytics, error-reporting or phone-home anywhere in the product                   | Grep across `web/`, `go.mod`, `package-lock.json`, `internal/**` for nine mainstream SDKs: zero genuine hits |
| Evidence-kind schemas carry enforced PII exclusion lists with `additionalProperties: false`      | `internal/api/schemaregistry/schemas/hris.worker_lifecycle/1.0.0.json:9,11`                                  |
| HRIS schema excludes SSN, salary, home address, bank details, benefits/health, DOB, ethnicity    | Same file, description field — the exclusions are written into the schema contract, not just the connector   |
| Device posture explicitly excludes geolocation, app inventory, browsing data, personal contact   | `internal/api/schemaregistry/schemas/endpoint.device_posture/1.0.0.json:9,67`                                |
| Local inference is the default; cloud is a per-tenant opt-in with no backfill                    | `migrations/sql/20260612100000_tenant_llm_routing.sql:15-18`                                                 |
| Cloud provider set is a closed CHECK enum; the endpoint URL is hard-coded in Go as an SSRF guard | `migrations/sql/20260612100000_tenant_llm_routing.sql:25-34,95-103`                                          |
| Tenant isolation at the DB layer with `FORCE ROW LEVEL SECURITY`, denying on missing GUC         | `migrations/sql/20260511000000_init.sql`                                                                     |
| Vendor email masked on every export format, with a leak-proof unit test                          | `internal/api/adminvendors/mask_email_test.go`                                                               |
| Passwords Argon2id at RFC 9106 parameters; bearer tokens HMAC-hashed; LLM API keys AES-256-GCM   | `migrations/sql/20260511000012_users_sessions_api_keys.sql`; `20260612100000_tenant_llm_routing.sql:86-90`   |
| Vendor register already models DPA status with a consistency CHECK                               | `migrations/sql/20260511000006_vendor_lite.sql:77-78,93-94` (`dpa_signed`, `dpa_signed_at`)                  |
| Azure connector carries a structural over-collection guard as a reflective unit test             | `connectors/azure/internal/keyvault/keyvault_test.go` (P0-521-2)                                             |

---

## 3. Personal-data inventory

Every location personal data actually lands. `Art. 9?` = special category under
GDPR Art. 9. This table is the raw material for the `privacy.PersonalDataSurfaces`
registry that slices 504 and 505 must share (slice 505 P0-505-5).

### 3.1 Identity, authentication, session

| Table                   | Column(s)                                                                                                   | Data category                                                   | Art. 9? | Surface                  |
| ----------------------- | ----------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- | ------- | ------------------------ |
| `users`                 | `email`, `display_name`, `idp_issuer`, `idp_subject`, `status`                                              | Direct identifier, workplace identity                           | No      | DSAR, erasure            |
| `local_credentials`     | `password_hash`, `params`                                                                                   | Credential (Argon2id)                                           | No      | Erasure (cascades)       |
| `sessions`              | `user_id`, `idp_issuer`, `idp_subject`, `user_agent`, `ip_address`, `geo_country`, `geo_city`               | **Online identifier, device fingerprint, approximate location** | No      | DSAR, erasure, retention |
| `oidc_idp_configs`      | `allowed_email_domains`                                                                                     | Org identifier (indirect)                                       | No      | —                        |
| `api_keys`              | `issued_by`                                                                                                 | Actor reference                                                 | No      | DSAR                     |
| `oauth_auth_codes`      | `idp_issuer`, `idp_subject`                                                                                 | Pseudonymous identifier                                         | No      | Retention                |
| `oauth_token_exchanges` | `subject_token_iss`, `subject_token_sub`, `ip_address`                                                      | **Online identifier**                                           | No      | DSAR, retention          |
| `oauth_revoked_tokens`  | `revoked_by`, `ip_address`                                                                                  | Online identifier                                               | No      | Retention                |
| `oauth_device_codes`    | `approved_by_user_id`, `approved_by_idp_issuer`, `approved_by_idp_subject`, `approved_by_current_tenant_id` | Identity + entitlement                                          | No      | DSAR                     |
| `scim_credentials`      | `issued_by`                                                                                                 | Actor reference                                                 | No      | —                        |
| `scim_audit_log`        | `actor_credential_id`, `target_user_id`, `detail` (JSONB)                                                   | Provisioning history                                            | No      | DSAR                     |
| `scim_groups`           | `display_name`, external id                                                                                 | Group membership                                                | No      | DSAR                     |
| `user_roles`            | `user_id`, `granted_by`                                                                                     | Entitlement                                                     | No      | DSAR                     |
| `super_admin_audit_log` | `actor_user_id`, `actor_tenant_id`                                                                          | Privileged-access history                                       | No      | DSAR                     |

### 3.2 Audit-log actor fields (append-only)

| Table                                      | Column(s)                                                                            | Data category                                | Art. 9? | Surface                      |
| ------------------------------------------ | ------------------------------------------------------------------------------------ | -------------------------------------------- | ------- | ---------------------------- |
| `decision_audit_log`                       | `user_id`, `user_roles[]`, `action`, `resource_id`, `request_path`, `request_method` | **OPA authorization decision — behavioural** | No      | DSAR, erasure, retention     |
| `evidence_audit_log`                       | `credential_id`                                                                      | Machine actor                                | No      | DSAR                         |
| `decisions_audit`                          | `actor`, `detail`                                                                    | Actor + free-text diff                       | No      | DSAR, erasure                |
| `artifact_access_log`                      | `actor`                                                                              | Access / download record                     | No      | DSAR                         |
| `artifacts`                                | `uploaded_by`                                                                        | Actor                                        | No      | DSAR                         |
| `exception_audit_log` / `exceptions`       | `actor`; `requested_by`, `approved_by`, `denied_by`, `activated_by`                  | Approval chain                               | No      | DSAR, erasure                |
| `sample_audit_log` / `audit_samples`       | `actor`; `created_by`, `annotated_by`                                                | Actor                                        | No      | DSAR                         |
| `audit_period_audit_log` / `audit_periods` | `actor`; `frozen_by`, `created_by`                                                   | Actor                                        | No      | DSAR                         |
| `aggregation_rule_audit_log`               | `actor`; `activated_by`                                                              | Actor                                        | No      | DSAR                         |
| `feature_flag_audit_log` / `feature_flags` | `actor`; `last_changed_by`                                                           | Actor                                        | No      | DSAR                         |
| `me_audit_log`                             | user id + `action` (including read events)                                           | **Behavioural record**                       | No      | DSAR                         |
| `walkthrough_audit_log` / `walkthroughs`   | `actor`; `created_by`, `uploaded_by`                                                 | Actor                                        | No      | DSAR                         |
| `audit_sink_failures`                      | `entry_actor`                                                                        | Actor                                        | No      | DSAR                         |
| `action_plan_audit_log`                    | `actor_id`, `before_state` / `after_state` (JSONB)                                   | Actor + state diff                           | No      | DSAR — **trigger-immutable** |
| `control_owner_assignment_audit_log`       | `actor_user_id`, `owner_user_id`                                                     | Assignment                                   | No      | DSAR                         |
| `imported_catalog_audit_log`               | `actor`; `imported_by`                                                               | Actor                                        | No      | DSAR                         |
| `framework_version_audit`                  | `actor_id`, `reviewer_id`                                                            | Actor                                        | No      | DSAR                         |
| `csf_assessment_audit`                     | `actor`; `rated_by`, `created_by`                                                    | Actor                                        | No      | DSAR                         |
| `group_role_audit_log`                     | `created_by`                                                                         | Actor                                        | No      | DSAR                         |
| crosswalk mapping tier                     | `reviewer_id`                                                                        | Actor                                        | No      | DSAR                         |

### 3.3 Domain content

| Table                                        | Column(s)                                                                                | Data category                                  | Art. 9?               | Surface                     |
| -------------------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------- | --------------------- | --------------------------- |
| `vendors`                                    | `owner_user` (holds an email), `notes`                                                   | Business contact                               | No                    | DSAR, erasure               |
| `vendor_reviews_ledger`                      | `reviewer`                                                                               | Actor                                          | No                    | DSAR                        |
| `risks`                                      | `treatment_owner`                                                                        | Named owner                                    | No                    | DSAR                        |
| `policies`                                   | `owner`, `approver`                                                                      | Named owner / approver                         | No                    | DSAR                        |
| `framework_scopes`                           | `approved_by`, `approval_evidence`                                                       | Attestation identity                           | No                    | DSAR                        |
| `policy_acknowledgments`                     | `user_id` (composite FK → `users`)                                                       | **Attestation record**                         | No                    | DSAR, erasure (FK pin)      |
| `audit_notes` / `auditor_assignments`        | `author_user_id`; `granted_by`                                                           | Authored content                               | No                    | DSAR                        |
| `questionnaire_answers`                      | `authored_by`, `narrative` (free text)                                                   | Authored prose — incidental PII risk           | Possible (incidental) | DSAR                        |
| `board_narrative_sections`                   | `authored_by`, `human_approver`, `raw_draft`, `operator_edit`, `final_text`, `citations` | Authored + AI-generated prose                  | Possible (incidental) | DSAR, erasure               |
| `board_packs`                                | `published_by`                                                                           | Actor                                          | No                    | DSAR                        |
| `ai_generations`                             | `system_prompt`, `context_inputs` (JSONB), `raw_draft`, `model_provider`                 | **Prompt corpus containing evidence excerpts** | Possible (incidental) | DSAR, erasure, **transfer** |
| `mcp_write_proposals`                        | `created_by`, `human_approver`                                                           | Approval chain                                 | No                    | DSAR                        |
| `metrics` / `metric_values`                  | `owner_user_id`; `entered_by_user_id`                                                    | Actor                                          | No                    | DSAR                        |
| `notifications`                              | `recipient_user_id`                                                                      | Delivery record                                | No                    | DSAR                        |
| `email_channel_optin` / `email_delivery_log` | `user_id`; `recipient_user_id`                                                           | **Preference + delivery ledger**               | No                    | DSAR, consent-adjacent      |
| `channel_delivery_log`                       | `recipient_user_id`                                                                      | Delivery record                                | No                    | DSAR                        |
| `staleness_rollup_log`                       | `recipient_user_id`                                                                      | Delivery record                                | No                    | DSAR                        |
| `schema_registry`                            | `owner`, `created_by`                                                                    | Actor                                          | No                    | DSAR                        |
| `action_plans`                               | `owner_id` (FK → `users`)                                                                | Assignment                                     | No                    | Erasure (FK pin)            |

### 3.4 The load-bearing one — third-party data subjects inside the evidence ledger

`evidence_records.payload` / `provenance` / `source_attribution` (all JSONB).
These hold data about people who are **not** platform users and have no account:
the operator's employees and contractors.

| Evidence kind (schema)                                                  | Personal data admitted                                                                                      | Art. 9?                         | Note                                                                                                                                                            |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `hris.worker_lifecycle`                                                 | `work_email`, `employment_status`, `start_date`, `end_date`, `title`, `department`, `manager_assignment_id` | **No — by explicit exclusion**  | The schema description enumerates the excluded fields. `employment_status='on_leave'` plus `end_date` is a weak leave/health proxy. Art. 88 employment context. |
| `hris.manager_hierarchy`                                                | Manager↔report opaque ids                                                                                  | No                              | Org graph                                                                                                                                                       |
| `okta.user_lifecycle`                                                   | `login`, `primary_email`, `last_login_at`, `activated_at`, `deactivated_at`, `mfa_enrolled`                 | No                              | **Behavioural** — `last_login_at` is a working-pattern signal                                                                                                   |
| `github.scim_user`                                                      | `user_name`, email, `active`                                                                                | No                              |                                                                                                                                                                 |
| `slack.workspace_member`                                                | `user_id`, `handle`                                                                                         | No                              |                                                                                                                                                                 |
| `endpoint.device_posture`                                               | `owner_assignment_id`, `owner_display_name`, `device_name`                                                  | **No — by explicit exclusion**  | The schema states "NEVER the owner's personal email, phone, or address" and excludes geolocation and app inventory                                              |
| `osquery.host_posture`                                                  | `hostname`                                                                                                  | No                              | Hostnames often encode a person's name in practice                                                                                                              |
| `access_review.completion`                                              | `completed_by`, `reviewer_role`, `notes`                                                                    | No                              |                                                                                                                                                                 |
| `policy.acknowledgment`                                                 | `user_id`, `acknowledged_at`                                                                                | No                              |                                                                                                                                                                 |
| `github.audit_event`, `slack.admin_audit_event`                         | `actor` + event stream                                                                                      | No                              | **Behavioural**                                                                                                                                                 |
| `pagerduty.oncall_coverage` / `.incident_summary` / `.response_metrics` | On-call assignment + response timing                                                                        | No                              | **Working-time data** — Art. 88 / EU working-time sensitivity                                                                                                   |
| `jira.ticket_evidence`                                                  | Ticket actors                                                                                               | No                              |                                                                                                                                                                 |
| `manual.upload` / `manual.attestation`                                  | `uploaded_by`, `filename`, `description` + **an arbitrary binary artifact in object storage**               | **Unbounded — Art. 9 possible** | This is the one uncontrolled PII channel. A signed HR letter or a screenshot of a health-accommodation ticket is entirely possible and nothing constrains it.   |

**Art. 9 summary.** No evidence kind admits special-category data by design, and
two schemas exclude it explicitly and in writing. The residual Art. 9 exposure is
confined to (a) operator-uploaded artifacts via `manual.upload`, (b) free-text
`notes` / `narrative` / `description` fields, and (c) `ai_generations.raw_draft`
derived from those. That is a **narrow, nameable surface** — worth stating in the
RoPA rather than treating as unbounded.

---

## 4. Controller / processor determination

This is the explicit determination the slice doc requires, and the one slices
504 and 505 reference when describing the self-host operator's role.

### A. The self-host operator — **CONTROLLER** (and, for some tenants, also a processor)

The operator alone determines the **purposes** (demonstrating security-control
effectiveness for SOC 2 / ISO 27001 / GDPR Art. 32) and the **means** (which
connectors to run, which evidence kinds to ingest, which retention to apply,
whether to enable cloud LLM routing). Art. 4(7) is satisfied on both limbs. This
holds for three distinct subject populations:

1. **Platform users** (`users`, `sessions`) — the operator's own staff.
   Controller.
2. **Workforce data subjects ingested via connectors**
   (`evidence_records.payload` — HRIS rosters, Okta accounts, MDM device owners).
   Controller. **This is the population the platform is least equipped to serve**,
   because these people have no account, no login and no visibility.
3. **Vendor contacts** (`vendors.owner_user`). Controller.

Where the operator runs security-atlas **as a service** to a customer — the
vCISO-consultancy case the multi-tenant model was explicitly built for (OQ #13)
— the operator is a **processor** to that customer for that tenant's data. The
schema cannot currently express this (PRIV-6), so the platform cannot route an
erasure request correctly. Recorded as an input to privacy-v0.

### B. The security-atlas project / maintainer — **NEITHER**, with respect to operator deployments

Verified in code and governance, not taken on assertion:

- `GOVERNANCE.md` — no hosted SaaS, no enterprise edition, no dual-license, no
  closed plugins.
- `GOVERNANCE.md` — explicit commitment not to introduce telemetry or
  phone-home; "there is no phone-home; there is no license server."
- **Independently verified**: grep for nine mainstream analytics and
  error-reporting SDKs across `web/`, `go.mod`, `package-lock.json` and
  `internal/**` returns zero genuine hits. The claim is true, not aspirational.

The project ships software; it receives no operator personal data. It is neither
controller nor processor for deployment data, and **Art. 28 does not bite**. This
is the strongest privacy property the project has and it is worth stating
publicly rather than leaving to inference.

The maintainer **is** a controller for a small, separate surface, currently
without a privacy notice:

- Contributor identity in git history, mandated by DCO, retained indefinitely
  (PRIV-10).
- Security-disclosure reporters via `SECURITY.md`.
- The maintainer-operated instances described in
  `docs/governance/data-retention.md` and `docs/operations/edge-deploy.md` — for
  which the maintainer **is** the operator, and everything in §A applies.

### C. A hosted offering — does not exist; determination deliberately not pre-decided

OQ #5 resolved 2026-05-20 to **Option A — pure-community OSS, time-bounded**,
with a re-evaluation trigger at 2028-05-20 or 100 deployed self-hosts. If Option
B (hosted SaaS) ever fires, the project entity becomes a **processor** to each
customer-controller under Art. 28 and would owe: Art. 28(3) DPAs, a documented
sub-processor list, Art. 32 assurances, Art. 33(2) breach notification to
controllers, and Art. 28(3)(e)-(f) assistance with DSAR and DPIA. None of these
exist today.

Flagged as a **dependency of the OQ #5 re-evaluation**, per P0-330-1. **Not
resolved here.**

---

## 5. The erasure-vs-append-only reconciliation

This section is the technical substrate for
[ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md). The ADR makes
the decision; this section establishes what the code actually affords.

### 5.1 What the ledger enforces, precisely

**The append-only property of `evidence_records` is role-scoped, not
structural.**

`migrations/sql/20260511000004_evidence_ledger.sql:100-115` drops the slice-002
`tenant_isolation` policy and installs exactly two — `tenant_read` (SELECT) and
`tenant_insert` (INSERT WITH CHECK). The file's own comment is unambiguous:

> `-- Intentionally NO POLICY for UPDATE or DELETE. RLS-without-a-matching-policy` > `-- under FORCE ROW LEVEL SECURITY blocks the row from being touched.`

Three facts follow, and all three matter for the erasure design:

1. **There is no trigger and no constraint enforcing immutability on
   `evidence_records`.** The `GRANT SELECT, INSERT, UPDATE, DELETE` in
   `20260511000000_init.sql` is still in force; only the missing policy blocks
   the write. The migration comment says as much: "the GRANT remains so a future
   BYPASSRLS migration can still run DDL."
2. **`atlas_migrate` is BYPASSRLS.** A connection on the migrate DSN can
   therefore `UPDATE evidence_records SET payload = …` today. Erasure is
   _mechanically_ available; it is _architecturally_ unbuilt.
3. **One table in the schema is unconditionally immune to that bypass, and the
   pattern has further precedents.** `action_plan_audit_log` installs a real
   trigger (`migrations/sql/20260612070000_action_plans.sql:345-356`) whose
   comment states the reason outright: "so the invariant holds even for a
   privileged (BYPASSRLS / table-owner) role that the missing-policy guard would
   not stop." It is the **only** `BEFORE UPDATE OR DELETE` trigger in
   `migrations/sql/` — i.e. the only one covering deletion as well as mutation.

   Five further tables carry `BEFORE UPDATE`-only triggers
   (`framework_scopes:140`, `board_packs:183`, `tenants:157`,
   `action_plans:159`, `framework_requirements:254`), of which two justify
   themselves in the same BYPASSRLS terms: `board_packs` — "It earns its place
   by holding the invariant for a BYPASSRLS role (atlas_migrate)"
   (`20260511000032_board_packs.sql:160-162`) — and `framework_requirements` —
   "this trigger is the defense-in-depth that also stops the privileged
   atlas_migrate (loader) role" (`20260612090000_framework_versioning.sql:218-222`).
   Both are **conditional** (they fire only on a `published` / status-frozen
   `OLD` row), so neither makes its table unconditionally append-only.

   The load-bearing point stands and is strengthened: the trigger pattern is
   **established practice with three BYPASSRLS-justified precedents**, not a
   one-off, so a privacy surface that needs privilege-proof immutability has a
   pattern to copy rather than a novel mechanism to invent. What remains true is
   that every other append-only table — `evidence_records` included — is
   append-only **only for `atlas_app`**.

**Integrity coupling.** `evidence_records.hash` is a sha256 over canonical JSON
of the payload, and export bundles are cosign-signed. Any in-place redaction of
`payload` therefore breaks the hash and diverges from previously-signed bundles.
That is not a blocker — it is arguably the **right** property, because it makes
redaction detectable rather than silent — but it means a redaction path must
write a new integrity marker, not pretend nothing happened.

**Freezing coupling.** Invariant #10 draws frozen sample populations by
`observed_at <= frozen_at` (`migrations/sql/20260511000020_audit_periods.sql`
carries `frozen_at` / `frozen_by`). A redaction that preserves `id`,
`observed_at`, `control_ref` and `result`, rewriting only PII-bearing JSONB keys,
leaves every frozen population stable — the sample is still drawn, still counted,
still cited.

**The `users` pin.** Even outside the ledger, `users` rows are permanent:

- `internal/scim/store.go:343-346` — the only delete verb refuses to delete, by
  anti-criterion P0-508-1.
- No `DELETE FROM users` exists in `internal/db/queries/` — the sole DML source
  for all DB access under sqlc.
- `action_plans.owner_id UUID NOT NULL REFERENCES users(id)` and
  `policy_acknowledgments`' composite FK would RESTRICT the delete anyway.
- `local_credentials` and `sessions` **do** declare `ON DELETE CASCADE` from
  `users` — so the cascade was designed, and then no code was ever written to
  trigger it.

### 5.2 Which design the code most naturally supports

**Tombstone (redact-in-place, retain the row).** Five code-level affordances
point there, and none point elsewhere:

1. **Actor fields are unreferenced plain strings.** Almost every `actor` /
   `*_by` / `authored_by` column is `TEXT` with no FK.
   `migrations/sql/20260511000030_decisions_audit.sql` states the design intent
   explicitly: "No FK to `decisions` because the audit trail must survive a
   future hard-delete." Overwriting these with a sentinel is a pure UPDATE with
   zero referential fallout.
2. **The tombstone vocabulary already exists in the schema.**
   `action_plan_audit_log.action_type` includes `'tombstoned'`
   (`migrations/sql/20260612070000_action_plans.sql`).
3. **The tombstone is already the project's documented disposal posture for this
   exact substrate.** `docs/governance/data-retention.md:154-160` names "Ledger
   tombstone (append-only-with-supersede)" as disposal method 5, and §4.5
   specifies the procedure — "The original record is never deleted — canvas
   invariant #3 forbids it. A tombstone record is appended…" — naming it as the
   posture for the evidence ledger and the unified audit log. Choosing anything
   else would put the product at odds with a merged governance document.
4. **The immutability block is role-scoped**, so a privileged, audited redaction
   path is implementable without relaxing anything for `atlas_app`.
5. **The schema's defaulted-empty-string pattern almost works, with one concrete
   trap.** `display_name TEXT NOT NULL DEFAULT ''`,
   `owner_user TEXT NOT NULL DEFAULT ''` and `authored_by TEXT NOT NULL DEFAULT ''`
   all accept `''`. But several tables carry non-empty CHECK constraints that
   `''` would violate — `decisions_audit_actor_nonempty`,
   `artifact_access_log_actor_nonempty`, `decision_audit_log_user_id_nonempty`,
   `user_roles_user_id_nonempty`, `sample_audit_log_actor_nonempty`,
   `csf_assessment_audit_actor_nonempty`, `aggregation_rule_audit_log_actor_nonempty`,
   `feature_flag_audit_log_actor_nonempty`, `imported_catalog_audit_log_actor_nonempty`,
   `group_role_audit_log_user_id_nonempty`, `iccd_actor_nonempty`,
   `scim_group_members_user_id_nonempty`. **The tombstone must therefore be a
   sentinel string, not an empty string.** This is a non-obvious, load-bearing
   implementation constraint that slice 504's design must carry.

**Why pseudonymisation is not the erasure answer (though it is the DSAR
answer).** Pseudonymisation requires a stable keyed mapping, and the schema has
no key-management surface for one — `internal/auth/keystore` is JWT-signing
specific. More decisively, **Recital 26 treats pseudonymised data as still
personal**, so it would not discharge Art. 17. It _is_ the right primitive for
the subject-correlation problem in slice 505 (a stable subject hash to join
across surfaces without materialising the identifier) — a different job.

**Why refuse-with-explanation cannot be the default, but must be a branch.**
Art. 17(3)(b) (legal obligation) and 17(3)(e) (establishment, exercise or
defence of legal claims) are genuine carve-outs, and for evidence inside a
frozen audit period a refusal is lawful and audit-defensible. But the carve-out
is per-record and time-bounded; as a blanket posture it would be unlawful. The
code already carries the exact predicate that scopes it correctly:
`audit_periods.frozen_at` / `frozen_by`. And
`docs/governance/data-retention.md` §6 already defines the legal-hold vocabulary
the branch would key off.

**Three ratified anchors settle this without novel argument** — all three were
missed by the first pass of this section and each is load-bearing for the ADR:

1. **Tombstone disposal is already pre-authorized.**
   `docs/adr/0012-append-only-evidence-ledger.md:60-64` — "(Disposal, when it
   comes, is a tombstone, not a mutation — see the data-retention policy; the
   invariant constrains it to tombstones-only.)" — and `:112-115` accepts
   "the data-retention policy is therefore tombstone-based rather than
   row-deleting." Invariant #2 does not need reinterpreting; it already says this.
2. **Field-level redaction provably cannot disturb a frozen period.**
   `docs/adr/0003-audit-period-freeze-hash-inputs.md:32-49` fixes `frozen_hash`
   over content-only inputs, binding evidence **by sorted id array**, not by
   payload content. Redacting a payload field therefore leaves `frozen_hash`
   byte-identical. Invariant #10 survives with no re-freeze and no re-signature.
3. **Deletion was already foreclosed at the DB layer.**
   `sample_evidence.evidence_record_id` and `sample_annotations.evidence_record_id`
   both carry `REFERENCES evidence_records(id) ON DELETE RESTRICT`
   (`20260511000010_audit_samples.sql:151-153`, `:176-178`). Any evidence row
   ever drawn into an auditor sample is undeletable regardless of role.

**The decision.** Per this slice's AC-3 and the OE-394 work-order — which
supersedes the slice doc's original "the agent should NOT pick one" note, because
downstream slices are blocked on a decision rather than a survey —
[ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md) **decides:
tombstone-by-default (field-level redaction-in-place plus an appended erasure
record), executed by a separately-credentialed `atlas_erase` role, with a
scoped per-record refuse-as-deferral branch.** See D3 in the decisions log for
why the survey-only instruction was overridden, and ADR-0020 §7 for the seven
mechanism amendments slice 504's spec needs.

---

## 6. Open questions: what this audit did and did not touch

| OQ                                             | Status after this audit                                                                                                                                                        |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **#5** — hosted offering shape                 | **NOT pre-decided** (P0-330-1). Surfaced in [§4C](#c-a-hosted-offering--does-not-exist-determination-deliberately-not-pre-decided) as a dependency of the 2028 re-evaluation.  |
| **#7** — privacy module sibling vs first-class | **NOT re-opened** (P0-330-2). The sibling resolution is treated as settled input. This audit dissents only on the _gating_ of the documentation half, not on the architecture. |
| **#10** — breach-notification workflow shape   | **STAYS OPEN** (P0-330-3 / AC-8). Design pressure recorded at PRIV-9; no shape proposed.                                                                                       |

---

## 7. Cross-reference with slice 329

Per **AC-6**: findings that overlap slice 329's GDPR coverage are noted with an
ownership recommendation, dedupe decided at follow-up-filing time.

| This audit                          | Slice 329 counterpart                                                                | Relationship                                                                                                                                                     | Ownership                                                                                                                                                                       |
| ----------------------------------- | ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **PRIV-4** (no personal-data map)   | ISO 27001 A.5.34 row — "no operator-side PII handling document"                      | **Direct dedupe.** Same gap, different altitude.                                                                                                                 | **330.** 329 identified it; 330 produces the inventory that closes it. Not filed twice.                                                                                         |
| **PRIV-3** (RoPA absent)            | GDPR row — "Art 30 ROPA … DEFERRED per OQ #10 (correct deferral; **not a Finding**)" | **Contested dedupe.** The two audits reach different verdicts because they ask different questions.                                                              | **330.** 329 asked "can the project pass an audit?"; 330 asks "can an operator deploy compliantly?" Recommend the maintainer accept 330's framing for the operator-facing half. |
| **PRIV-7** (`subject_module` drift) | Art. 25 row — "PARTIAL … slice 180 … pre-commitment only"                            | **Extends.** 329 recorded the foundation as present; 330 finds it has since drifted.                                                                             | **330.** New information.                                                                                                                                                       |
| **PRIV-8** (identity retention TTL) | H-4 data-retention policy, closed by slice 375; `data-retention.md` §3.1 Gap 1       | **Partial dedupe, distinct scope.** 375 covered the _project's own_ artifacts and explicitly excluded tenant data. PRIV-8 is the _product-side_ gap.             | **330.** Not covered by 375. Cross-reference noted so the maintainer sees they are adjacent, not identical.                                                                     |
| **PRIV-9** (breach notification)    | I-5 — "GDPR Art 33 correctly deferred per OQ #10 — not a Finding"                    | **Dedupe, agreeing.** Both Informational, both non-findings.                                                                                                     | **329.** 330 adds only design pressure; no new finding.                                                                                                                         |
| —                                   | I-6 — "Privacy module v0 correctly deferred per OQ #7"                               | 330 partially dissents: the _module_ deferral is correct, but PRIV-2/3/4 argue the _documentation and erasure-design_ work should not inherit the module's gate. | Surfaced to the maintainer as a scoping question, not a re-file.                                                                                                                |
| **PRIV-5** (cloud-LLM transfer)     | No counterpart — the LLM-routing slice postdates 329                                 | **New.** No dedupe.                                                                                                                                              | **330.**                                                                                                                                                                        |
| **PRIV-2** (erasure)                | No counterpart — 329 did not examine Art. 17                                         | **New.** No dedupe. The load-bearing finding.                                                                                                                    | **330.**                                                                                                                                                                        |

---

## 8. Follow-up fan-out

**Already owned — not re-filed** (P0-330-4: DSAR, erasure and RoPA are each
their own tracer-bullet and are not bundled):

| Finding | Existing slice                     | Status                                                                                       |
| ------- | ---------------------------------- | -------------------------------------------------------------------------------------------- |
| PRIV-2  | 504 — right-to-erasure (tombstone) | `not-ready`, gated. **Unblocked by ADR-0020** on the design half; still gated on privacy-v0. |
| PRIV-1  | 505 — DSAR export workflow         | `not-ready`, gated on privacy-v0.                                                            |
| PRIV-3  | 506 — RoPA product primitive       | `not-ready`, gated on privacy-v0.                                                            |
| PRIV-9  | 507 — breach-notification workflow | `not-ready`, gated on ADR-0017 + privacy-v0.                                                 |

**Newly filed** — all six are documentation or mechanical-migration work that
does **not** depend on the privacy-v0 greenlight, which is the point:

| #   | Finding         | Work                                                                                                                                                    |
| --- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F-1 | PRIV-2          | Correct `docs/governance/data-retention.md`'s inaccurate "per-record deletion" claim and state the honest current Art. 17 position                      |
| F-2 | PRIV-4 + PRIV-1 | Publish `docs/governance/personal-data-inventory.md` from §3 and link it from `docs/SELF_HOSTING.md`; it becomes the shared spec for slices 504 and 505 |
| F-3 | PRIV-3          | Author the project's own six-column RoPA as a governance document (distinct from slice 506's product primitive)                                         |
| F-4 | PRIV-5          | Document the Chapter V transfer analysis for the three cloud LLM providers; add a residency lever or an explicit EU-operator guard                      |
| F-5 | PRIV-7          | Resolve the `subject_module` drift — extend the column to the eleven post-180 tables, or narrow the pre-commitment in `CONTRIBUTING.md`                 |
| F-6 | PRIV-8          | Set retention windows for `sessions`, `oauth_token_exchanges`, `oauth_revoked_tokens`; resolve the dormant `geo_*` columns                              |

Low findings (PRIV-10, PRIV-11) are **report-only** for maintainer triage,
matching the slice-329 and slice-331 precedent.

---

## Disposition

| Slice AC                                         | Status                                                                                                                                                    |
| ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **AC-1** agent runs against the nine surfaces    | Done — [§1](#1-per-surface-verdicts).                                                                                                                     |
| **AC-2** decisions log with per-surface findings | Done — `docs/audit-log/330-privacy-gdpr-ccpa-audit-decisions.md`.                                                                                         |
| **AC-3** erasure design (load-bearing)           | Done and **ratified**, not merely proposed — [ADR-0020](../adr/0020-right-to-erasure-vs-append-only-ledger.md). See D3 for the survey-vs-decide override. |
| **AC-4** RoPA follow-up                          | Done — slice 506 exists (product primitive) + F-3 (project document).                                                                                     |
| **AC-5** DSAR follow-up                          | Done — slice 505 exists + F-2 (the shared surface registry it depends on).                                                                                |
| **AC-6** slice-329 cross-references              | Done — [§7](#7-cross-reference-with-slice-329).                                                                                                           |
| **AC-7** no code modified                        | Done — the diff is documentation only.                                                                                                                    |
| **AC-8** OQ #10 not pre-decided                  | Done — PRIV-9 records pressure only.                                                                                                                      |
| **AC-9** `pre-commit run --files` passes         | Run at PR time.                                                                                                                                           |
