# security-atlas — Architecture Canvas

**Status:** Pre-implementation ideation. No code yet.
**Audience:** Senior GRC engineers, CISOs, security platform architects.
**Date:** 2026-05-10

---

## Executive Summary

`security-atlas` is an open-source, self-hostable, replacement-grade GRC platform that treats a security program as a graph rather than a spreadsheet. The primary v1 user is **the solo security leader at a 50–150-person security-product startup who runs the entire program — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — alone, and whose customers will diligence the diligence tool itself.** Self-hosting your own GRC platform becomes a trust differentiator; owning the tool eliminates the Year-2 renewal cliff that drives practitioner pain in Vanta/Drata.

The spine is the [Secure Controls Framework](https://securecontrolsframework.com/) — ~1,400 controls already crosswalked to 200+ authority documents using NIST IR 8477 Set Theory Relationship Mapping (STRM) — and the wire format is [NIST OSCAL](https://pages.nist.gov/OSCAL/). Where Vanta and Drata commit you to a vendor-controlled evidence collector, security-atlas commits to a hybrid event-driven + query-driven evidence pipeline that composes existing OSS scanners (Prowler, Steampipe, Cloud Custodian, OPA, Cartography, osquery) rather than rebuilding them. Manual controls and the messy reality of partial coverage and exceptions are first-class. Scope is multidimensional (business unit × environment × geography × cloud × data class), not a single hierarchy. Risk is derived from controls, not joined to them.

The v1 success test is binary: does our primary user run their next SOC 2 audit out of security-atlas, generate their next board pack from it, and not reach for Vanta or a Google Sheet to fill a gap? If yes, v1 is done.

---

## Three load-bearing decisions

1. **The UCF is a directed labeled graph with STRM-typed edges through SCF anchors** — not flat per-framework crosswalk tables that decay. Worked out in [`UCF_GRAPH_MODEL.md`](./UCF_GRAPH_MODEL.md).
2. **Evidence ingestion and control evaluation are separated stages** with an append-only ledger between them, enabling point-in-time audit replay and audit-period freezing. The Evidence SDK exposes the ledger via two profiles (connector pull/subscribe, pusher push). Worked out in [`EVIDENCE_SDK.md`](./EVIDENCE_SDK.md).
3. **Board reporting is a v1 feature** with a templated narrative auto-drafted from real metrics and human-approved before publish. Security questionnaires (CAIQ, SIG, HECVAT, customer bespoke) plug into the same UCF graph — questions map to SCF anchors so one approved answer pre-populates equivalent questions across questionnaires.

---

## Read the canvas

The canvas is split into focused sections. Each is a single concept, ingestible in 5–15 minutes. Read in order on first pass; jump in by topic thereafter.

| # | Section | What's in it |
|---|---|---|
| 01 | [Vision and Positioning](./canvas/01-vision.md) | Product thesis · Why not Vanta/Drata/OpenGRC · Non-goals · Personas · Replacement-grade acceptance criteria · Anti-patterns we reject |
| 02 | [Domain Primitives](./canvas/02-primitives.md) | Six entities — Control, Risk, Evidence, Scope, Framework/Version, Policy — with full field tables and an ER diagram |
| 03 | [The Unified Control Framework](./canvas/03-ucf.md) | Graph not spreadsheet · STRM cardinality · Versioning · OSCAL · SCF as canonical catalog. Deep dive: [`UCF_GRAPH_MODEL.md`](./UCF_GRAPH_MODEL.md) |
| 04 | [Evidence Engine](./canvas/04-evidence-engine.md) | Evidence SDK (connector + pusher profiles) · v1 connector roster · Ingestion/evaluation separation · Control-as-code · Manual evidence · Security questionnaires (CAIQ/SIG/HECVAT). Deep dive: [`EVIDENCE_SDK.md`](./EVIDENCE_SDK.md) |
| 05 | [Scopes and Multitenancy](./canvas/05-scopes.md) | Scope dimensions · Per-cell evaluation · Inheritance/override · Postgres RLS · **FrameworkScope** (per-framework subset of cells and controls) |
| 06 | [Risk Register Linkage](./canvas/06-risk.md) | Treatment statuses · Residual risk derivation · Exception/waiver workflow with auto-expiry |
| 07 | [Metrics and Posture](./canvas/07-metrics.md) | KPIs · Leading vs lagging · Aggregation across scopes · Benchmarks · **Board reporting (first-class)** |
| 08 | [Audit Workflow](./canvas/08-audit-workflow.md) | Auditor role · OSCAL SSP/POA&M export · Sample-pull primitives · **Audit-period freezing** · Audit Hub collaboration |
| 09 | [Architecture and Tech Stack](./canvas/09-tech-stack.md) | Postgres + S3 · Go core + Python connector SDK · NATS JetStream · Plugin surfaces · OIDC + OPA |
| 10 | [Roadmap and Sequencing](./canvas/10-roadmap.md) | MVP (solo operator, one framework, real audit, board-ready) · Phase 2 (mapping engine) · Phase 3 (audit ecosystem) |
| 11 | [Open Questions Deferred](./canvas/11-open-questions.md) | 19 decisions the canvas does not resolve — licensing, governance, AI boundaries, UX workflows |
|  · | [Sources](./canvas/sources.md) | All cited references |

---

## Companion documents

- [`UCF_GRAPH_MODEL.md`](./UCF_GRAPH_MODEL.md) — graph diagrams, worked example (one MFA evidence record satisfying SOC 2 + ISO + NIST CSF + PCI + HIPAA + GDPR), STRM semantics, versioning math, storage rationale
- [`EVIDENCE_SDK.md`](./EVIDENCE_SDK.md) — full SDK contract, push profile, schema registry, middleware patterns, security threat model

## Mockups

UI mockups under [`mockups/`](./mockups/) — open [`mockups/index.html`](./mockups/index.html) in a browser:

- Program dashboard · Control detail view (with the UCF graph rendered) · Board pack preview · Questionnaire response with AI-assist

HTML + Tailwind via CDN, no build step. Iterating before committing to a frontend stack.

---

[Repository root](../) · [GitHub](https://github.com/mgoodric/security-atlas)
