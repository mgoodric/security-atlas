# OpenGRC concept-borrow inventory

**Status:** complete · **Authored:** 2026-05-20 · **Resolves:** `Plans/canvas/11-open-questions.md` #2 (Shape B-deferred — process commitment + this inventory as the artifact)

**License posture:** OpenGRC is CC BY-NC-SA 4.0. This document captures **concepts and patterns only** — no code, schema, or text was copied. Every borrow is attributable; every concept-borrow that lands in security-atlas code should cite OpenGRC in the relevant commit message.

---

## TL;DR — load-bearing finding

OpenGRC is a solo-maintained, alpha-stage Laravel/Filament project with **essentially no third-party community footprint** (zero Hacker News hits, no Reddit threads, omitted from the major 2025 OSS-GRC roundup articles). The richest user-signal lives in the project's own GitHub issue tracker, which is unusually candid.

The single most important observation: **OpenGRC's data model is framework-keyed.** Controls belong to a single `Standard` via FK; the deduplication seam is the `Implementation` join. This is the exact pattern security-atlas's **constitutional invariant #1** ("one control, N framework satisfactions") was written to avoid. OpenGRC's own users hit the duplication wall in practice (issue #272: "import 109 ISO 27001 controls, then manually attach each one to a policy") — empirical evidence that the architectural choice has real operator cost.

Vocabulary is mostly safe to borrow with attribution. Architecture is the failure mode our invariants were written to prevent.

---

## OpenGRC snapshot

| Field             | Value                                                                                                    |
| ----------------- | -------------------------------------------------------------------------------------------------------- |
| Canonical repo    | <https://github.com/LeeMangold/OpenGRC>                                                                  |
| Docs              | <https://docs.opengrc.com>                                                                               |
| Primary stack     | PHP / Laravel / Filament admin framework                                                                 |
| Database          | MySQL/MariaDB (Laravel default; no Postgres-specific features)                                           |
| License           | CC BY-NC-SA 4.0 (relicensed 2025-04-14 from MIT); resale + hosting-for-customers prohibited              |
| Stars / Forks     | 119 / 60                                                                                                 |
| Contributors      | Lee Mangold 667 commits / next contributor 24 / Dependabot tail                                          |
| Releases          | Only `v0.1.0-alpha` (2024-10-19) and `v0.1.0-alpha-1` (2024-10-20); **no GA tag** in the 18 months since |
| Open issues / PRs | 8 / 2 (very small — could indicate small user base OR Discord-channeled feedback)                        |
| Activity grade    | Active solo cadence; last push 2026-05-06                                                                |
| Maintainer signal | Responsive on issues (close times in days, not months)                                                   |

**License consequence:** the hosting prohibition predictably shrinks the contributor pool to bug-fix tourists. The solo-maintainer dynamic is structural, not accidental.

---

## Research methodology

Two parallel research passes:

- **Community-sentiment pass** (Perplexity-backed) — surveyed Hacker News (Algolia API), Reddit (site-restricted searches), 2025/2026 OSS-GRC roundup articles, vendor blog posts, podcast transcripts.
- **Repo + product-surface pass** (Claude-backed) — read README, docs, migrations, Filament resources, open + closed issues, recent PRs.

**Limit of the investigation:** the community-sentiment pass returned **near-zero signal** outside the GitHub issue tracker. This is itself a finding — OpenGRC has not yet been adopted into the production stacks that generate migration stories, conference talks, or blog comparisons. The praise side of this inventory is consequently thinner than the pain side.

---

## BORROW — concepts worth lifting (with attribution)

| #       | Concept                                                                                | Why it's good                                                                                                                                                                                                            | Where it lands in security-atlas                                                                                                                                                                                                                                                                       |
| ------- | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **B1**  | **"Implementation" as a UX noun for the dedup seam**                                   | Operators speak it fluently in OpenGRC issues (#272 uses it correctly without explanation). The word bridges "control text" and "what we actually do" — a natural intermediate noun.                                     | UCF graph already provides this structurally. Consider the noun "Implementation" as a UX label for the `ControlActivity` / `evidence_kind` configuration surface in the operator dashboard, so operators see the same vocabulary they already use.                                                     |
| **B2**  | **Polymorphic `AuditItem` (`auditable_type` + `auditable_id`)**                        | One audit table holds mixed-type auditees (Control / Policy / Vendor / Risk) without N parallel tables. Clean schema, simple UI.                                                                                         | Read-model layer for the AuditPeriod / sample-population view in canvas §8. The ledger itself stays evidence-first; the read model can polymorphize for UI ergonomics.                                                                                                                                 |
| **B3**  | **"Bundle" as the catalog distribution unit (user-facing)**                            | A single zipped/JSON package that imports a framework + its controls. Lower cognitive load than asking operators to think about "OSCAL profile + catalog + component-def trio."                                          | Wrap our OSCAL ingest pipeline in a user-facing "Framework Pack" concept for the self-host UX. OSCAL stays the wire format; "Pack" is the noun the operator sees.                                                                                                                                      |
| **B4**  | **`test_plan` (or `evaluation_hint`) field on Implementation**                         | A free-text "how would an auditor test this" attached to the implementation itself, not just the control. Makes manual evidence self-documenting.                                                                        | Add `test_plan` / `evaluation_hint` to `ControlActivity` or `EvidenceRequirement` so manual evidence has a built-in instruction layer the operator can read at sample time.                                                                                                                            |
| **B5**  | **NDA-gated Trust Center document download flow**                                      | End-to-end shape: public landing → per-document gating → access-request approval → audit log. Concrete reference shape, not a stub.                                                                                      | Trust Center is deferred per canvas §1.6 (don't build vanity trust centers until v3). When we do build it, OpenGRC's NDA-gated download flow is a usable reference.                                                                                                                                    |
| **B6**  | **Vendor Portal = separate user class**                                                | `VendorUser` ≠ `User`. Distinct authentication realm against the same DB. Sidesteps tenant-isolation risk when external respondents need authenticated access.                                                           | When the questionnaire-response portal lands (deferred from slice 155 v1 tracer-bullet), model `ExternalRespondent` as a non-tenant principal authenticated via per-tenant magic link, not as a regular Member.                                                                                        |
| **B7**  | **"Surveyor" pattern — questionnaire auto-fill from prior responses + control corpus** | Read incoming vendor questionnaire → propose answers from existing Implementations + Policies + prior responses. Aligned conceptually with our AI-assist boundary IF we add the citations + human approval requirements. | Maps directly onto canvas §4.6 AI-assist surface. Borrow the _concept_; harden the boundary: every Surveyor-style suggestion in security-atlas MUST cite specific evidence IDs and pass the `human_approved=true` schema enforcement. Particularly relevant to slice 155's AnswerLibrary v2 follow-on. |
| **B8**  | **`DataRequest` / `DataRequestResponse` with a separable `code`**                      | Treat evidence requests as first-class addressable records, not just attachments on an audit item. OpenGRC's 2025-10-09 `audit_item_data_request` join lets one response satisfy many control samples.                   | Already aligns with our Evidence SDK. Adopt the affordance: one evidence response should be cite-able from many audit-item / sample contexts without duplication.                                                                                                                                      |
| **B9**  | **Activity log + in-app notifications as table-level concerns**                        | Spatie activity-log pattern applied uniformly across resources. Cheap to add, high audit-trail value.                                                                                                                    | Couple to our existing OTEL trace layer for an in-app feed. Covers SOC 2 CC7.x logging-of-administrative-actions evidence cheaply.                                                                                                                                                                     |
| **B10** | **In-line "repeater" UI for sub-entities instead of separate routes**                  | OpenGRC maintainer's framing in #57: edit sub-items in the parent form rather than dedicated CRUD pages. Reduces navigation depth and cognitive load.                                                                    | Apply to `ControlActivity` on `Control` and `EvidenceRequirement` on `ControlActivity` in the operator UI. The mockups already lean this way; OpenGRC's #57 reasoning validates the choice.                                                                                                            |
| **B11** | **Single "Programs" grouping above Audits**                                            | Programs aggregate audits over time (e.g., "SOC 2 Program" spanning the 2025 and 2026 audits). Mirrors how security leaders actually think and talk.                                                                     | Consider a `Program` aggregate above `AuditPeriod` in canvas §8. Captures the multi-year audit narrative that boards ask for ("how has our SOC 2 program evolved?").                                                                                                                                   |
| **B12** | **Asset `exposure` + `criticality` as bounded enums, not free-form tags**              | OpenGRC PR #243 adds bounded enums for asset risk-scoring inputs. Avoids the "everyone tags differently" trap.                                                                                                           | If asset management lands in v2, mirror this discipline: enumerate `exposure` and `criticality`; don't open-text them.                                                                                                                                                                                 |
| **B13** | **Maintainer-responsive issue triage as a public signal of project health**            | Lee Mangold closes install issues in days. The cadence itself is a trust signal for prospective adopters.                                                                                                                | Do the same and make the cadence visible — status page or weekly merged-PR digest. Already partially in place via slice 070 showboat walkthroughs.                                                                                                                                                     |

---

## LEAVE — patterns we explicitly do NOT copy

| #       | Pattern                                                           | Why we leave it (which decision / invariant it violates)                                                                                                                                                                                                                                                                         |
| ------- | ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **L1**  | **Framework-keyed Controls (`controls.standard_id` FK)**          | Violates **invariant #1** ("one control, N framework satisfactions"). The UCF graph + STRM edge model exists to prevent this duplication. Borrowing the data model would unmake security-atlas's core thesis. Empirical pain: OpenGRC issue #272 — operator imports 109 controls and has no way to bulk-attach them to a policy. |
| **L2**  | **Single-org / no-tenant deployment**                             | Violates **invariant #6** (RLS at the database layer). security-atlas's vCISO use case requires multi-tenant from day one. RLS retrofits on single-tenant codebases are notoriously broken; OpenGRC has no foundation to retrofit onto.                                                                                          |
| **L3**  | **No append-only evidence ledger; live entities mutate in place** | Violates **invariant #2** (ingestion + evaluation stages separated; append-only ledger between them). OpenGRC's `audit_items` join live `controls` and `implementations` by FK — a mid-audit edit silently changes the audit record. Audit-period freezing (invariant #10) is structurally absent.                               |
| **L4**  | **Undocumented AI-assist boundary**                               | Violates the **AI-assist boundary (hard)** in CLAUDE.md. OpenGRC's marketed "AI-Powered Audits" and "Surveyor" auto-answers do not (publicly) document human-approval requirements, evidence citations, or schema-level `human_approved` enforcement. Adopting their stance would be a regulatory liability.                     |
| **L5**  | **Default LLM = OpenAI cloud-only**                               | Violates our **"local Ollama default; cloud opt-in per tenant with visible banner"** posture (resolved 2026-05-13 at slice 050). OpenGRC supports OpenAI + DigitalOcean (PR #229) — both cloud. Data residency is a feature for the audit-firm buyer.                                                                            |
| **L6**  | **Bundle proprietary frameworks (ISO 27001, etc.) directly**      | Licensing risk. OpenGRC ships ISO 27001 as a bundle — a posture we have not green-lit. security-atlas defers SCF bundling pending legal review (canvas OQ #1 was resolved in favor of user-imports-the-spreadsheet) and explicitly defers CCM/CAIQ/SIG to ingest-customer-files-only.                                            |
| **L7**  | **Filament admin panel as the user-facing frontend**              | Conflicts with canvas §9 (Next.js 16 + shadcn/ui + Tailwind 4). Filament is excellent for admin CRUD, but our buyer-facing dashboard is a content-heavy server-component surface, not a CRUD console. Slice 005 already committed the stack.                                                                                     |
| **L8**  | **Closed/proprietary connector model (in-tree PHP only)**         | OpenGRC's "Connector" concept is aligned with ours, but if implementations land as in-tree PHP they create a closed-by-language ecosystem. Violates our **open-source-thesis anti-pattern** in canvas §1.6 ("closed proprietary connectors"). Our Evidence SDK is language-agnostic by design (gRPC + per-language SDKs).        |
| **L9**  | **Solo-maintainer + hosting-prohibition license model**           | The hosting prohibition causes the solo-maintainer dynamic (contributors won't invest if they can't monetize hosting). Our resolved license posture (Apache 2.0 per OQ #3) is the inverse.                                                                                                                                       |
| **L10** | **Marketing-language feature pages without docs behind them**     | OpenGRC's docs site has nav entries that 404 against a docs.opengrc.com `/grc-foundations/data-model/` path. A prospective adopter cannot read how the data model works. Our `Plans/` canvas is the inverse posture — ship the architecture in public.                                                                           |

---

## Process commitment going forward

Per the OQ #2 resolution (Shape B-deferred): when a UI-heavy slice picks up, the engineer (or maintainer pre-grill) does a **30-minute OpenGRC look-around for the specific surface** before drafting the mockup. The look-around updates this inventory with new borrows or leaves if surfaced. Slices to apply this to first:

| Slice                                           | OpenGRC surface to look at                                                                         |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| **155** (Questionnaire feature — tracer bullet) | Surveyor flow (questionnaire ingest + auto-fill from corpus); `audit_item_data_request` join shape |
| **027** (Auditor workspace)                     | Audit + AuditItem polymorphic flow; `DataRequest` / `DataRequestResponse` shape                    |
| Future Trust Center slice (v3)                  | NDA-gated download flow, public landing + access-request approval workflow                         |
| Future Vendor Portal slice                      | `VendorUser` separate-user-class pattern; vendor `risk_score` calibration                          |
| Future Programs slice                           | "Program → Audit" aggregation                                                                      |
| Future Asset module (v2)                        | `exposure` + `criticality` bounded enums                                                           |

---

## Citations

**Primary sources:**

- Repo metadata: <https://github.com/LeeMangold/OpenGRC>
- Releases (only two pre-release tags): <https://github.com/LeeMangold/OpenGRC/releases>
- Migrations (schema evidence): <https://github.com/LeeMangold/OpenGRC/tree/main/database/migrations>
- Filament resources (UI surface): <https://github.com/LeeMangold/OpenGRC/tree/main/app/Filament/Resources>
- Models: <https://github.com/LeeMangold/OpenGRC/tree/main/app/Models>
- License history + hosting prohibition: <https://opengrc.com/about>
- Docs site: <https://docs.opengrc.com>

**Cited issues (open + notable closed):**

- #272 — relationship-import gap (load-bearing pain): <https://github.com/LeeMangold/OpenGRC/issues/272>
- #271 — SQL ambiguous-column bug on policy-attach: <https://github.com/LeeMangold/OpenGRC/issues/271>
- #270 — hazards-catalog template request: <https://github.com/LeeMangold/OpenGRC/issues/270>
- #269 — risk-to-asset linkage request: <https://github.com/LeeMangold/OpenGRC/issues/269>
- #268 — Docker install on Windows: <https://github.com/LeeMangold/OpenGRC/issues/268>
- #266 — CSV import template rejection: <https://github.com/LeeMangold/OpenGRC/issues/266>
- #263 — Docker install on Mac (closed): <https://github.com/LeeMangold/OpenGRC/issues/263>
- #256 — Ubuntu native install (closed): <https://github.com/LeeMangold/OpenGRC/issues/256>
- #252 — Windows install (closed): <https://github.com/LeeMangold/OpenGRC/issues/252>
- #127 — Docker image NOT_PLANNED: <https://github.com/LeeMangold/OpenGRC/issues/127>
- #72 — Import via API/CSV; "Bundles" concept: <https://github.com/LeeMangold/OpenGRC/issues/72>
- #57 — Sub-controls; maintainer's in-line-repeater reasoning: <https://github.com/LeeMangold/OpenGRC/issues/57>
- #19 — S3 support (closed COMPLETED): <https://github.com/LeeMangold/OpenGRC/issues/19>
- #17 — Connectors concept: <https://github.com/LeeMangold/OpenGRC/issues/17>

**Cited PRs:**

- PR #257 — Docker first-run setup fix (community contribution): <https://github.com/LeeMangold/OpenGRC/pull/257>
- PR #246 — AI prompt variables: <https://github.com/LeeMangold/OpenGRC/pull/246>
- PR #243 — Asset exposure/criticality enums: <https://github.com/LeeMangold/OpenGRC/pull/243>
- PR #230 — DataManager import/export module: <https://github.com/LeeMangold/OpenGRC/pull/230>
- PR #229 — DigitalOcean AI provider: <https://github.com/LeeMangold/OpenGRC/pull/229>
- PR #228 — Policy download button: <https://github.com/LeeMangold/OpenGRC/pull/228>
- PR #227 — Checklists feature: <https://github.com/LeeMangold/OpenGRC/pull/227>

**Comparison landscape (sparse but cited):**

- Hacker News Algolia search (zero genuine OpenGRC hits): <https://hn.algolia.com/api/v1/search?query=OpenGRC>
- OSS GRC roundup omitting OpenGRC (Cetin, 2025): <https://medium.com/@GorkemCetin/open-source-grc-tools-you-should-be-using-in-2025-eramba-ciso-assistant-verifywise-649364e00206>
- OSS GRC roundup giving OpenGRC a one-sentence profile (Baserow, 2025): <https://medium.com/@baserow/top-open-source-grc-tools-2025-59aadaee4973>
- Eramba (the dominant OSS GRC default-pick): <https://www.eramba.org/>

---

## Strategic note

The single load-bearing insight: **OpenGRC has converged on the same connector-as-capability and Implementation-as-dedup-seam intuitions we have** — but its substrate (framework-keyed controls, single-tenant, no evidence ledger, undocumented AI boundary) is the exact failure mode our constitutional invariants were written to avoid.

That convergence-of-vocabulary + divergence-of-substrate is actually the wedge story for the v1 ICP comparison: "OpenGRC and security-atlas use similar nouns, but when you certify against a second framework you hit a wall in OpenGRC that doesn't exist in atlas." Operator-empathy on day one; structural correctness on day two.
