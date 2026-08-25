---
phase: climbing
progress: 0
---

# Security Atlas

An open-source, self-hostable GRC platform. Initiative ISA — every epic under
`isa/` inherits the Constraints below and cannot violate them.

The repo already carries its design intent in three places: `CLAUDE.md`
(constitutional principles), `Plans/` (the canvas), and `docs/adr/` (21 ratified
decisions). This file does not restate them. It states what must be **true of
the whole product**, and names what would show each claim false.

## Problem

A solo security leader at a 50–150-person startup runs the entire program
alone: risk register, board reporting, SOC 2, vendor reviews, policies,
exceptions. The tools that exist force a bad trade. SaaS GRC (Vanta, Drata)
hosts your evidence on someone else's infrastructure and prices per framework,
which is exactly the diligence question your own customers will ask you about.
Enterprise GRC (OneTrust, Archer) is licensed and implemented rather than
adopted. Both duplicate controls per framework, so adding ISO to an existing
SOC 2 program means maintaining the same control twice and letting the copies
drift.

## Vision

One control graph, many frameworks, evidence you own. A program run from a
single source of truth where adding a framework is a mapping exercise rather
than a duplication exercise, where the evidence record is append-only so any
past date can be reconstructed, and where the whole thing runs on your own
infrastructure with no paid tier.

## Out of Scope

These are documented failures of existing tools, rejected deliberately.

- Policy template libraries as a feature. Five high-signal templates, not fifty
  placeholders.
- Proprietary collector agents on endpoints. osquery, Fleet, read-only APIs.
- Vanity trust centers, until customers actively demand one.
- "Continuous monitoring" that is 24-hour polling wearing a different name.
  Event-driven where the API allows, and the interval named honestly where not.
- Closed connectors. They defeat the thesis.
- Being a multi-tenant SaaS. Tenancy exists so one operator can separate
  environments, not so we can host other people's evidence.

## Principles

1. **A control is one thing that many frameworks ask for.** Frameworks are
   views over a control graph, never containers that own their own copies.
   Every duplication is future drift.
2. **What was observed and what it means are different stages.** Ingestion
   records; evaluation interprets. A bug in the second must never be able to
   corrupt the first.
3. **The record is append-only, so the past stays reconstructible.** A GRC tool
   whose history can be edited is worth less than a spreadsheet, because the
   spreadsheet does not claim otherwise.
4. **Say what is not covered.** An honest gap is the product. Coverage that
   quietly absorbs an unevidenced control is the failure mode the whole
   category is known for.
5. **AI suggests, humans commit.** Nothing audit-binding leaves the system on a
   model's say-so, and every suggestion carries a citation to the evidence or
   policy it rests on.
6. **Self-hostable means actually self-hostable.** No paid tier, no phone-home,
   no capability that only works if you buy something.

## Constraints

Inherited by every epic. These are the constitutional principles from
`CLAUDE.md`, restated as constraints so an epic can cite them by id. `CLAUDE.md`
remains the authority; if the two disagree, `CLAUDE.md` wins and this file is
the defect.

- C1 · One control, N framework satisfactions. The UCF is a graph with
  STRM-typed edges through SCF anchors. Controls are never duplicated per
  framework.
- C2 · Ingestion and evaluation are separate stages with an append-only
  evidence ledger between them. Evaluation never writes source-of-truth
  evidence.
- C3 · The Evidence SDK exposes one canonical inbound API. The platform-side
  wire surface is always push, whatever the connector does source-side.
- C4 · Scope is multidimensional, not a tree. Cells are tuples over
  (BU × env × geo × cloud × data_class × product).
- C5 · FrameworkScope intersects with control applicability.
  `effective_scope(control, framework) = applicability_expr ∩ framework_scope`.
- C6 · Tenant isolation is enforced at the database layer via RLS on every
  tenant-scoped table, and denies on missing context. Not in application code.
- C7 · SCF is the canonical catalog. Mappings go requirement → SCF anchor,
  never requirement → requirement.
- C8 · OSCAL is the wire format, not the daily data model.
- C9 · Manual evidence is first-class and renders the same surface as
  automated. Lifecycle, ownership and freshness apply equally.
- C10 · Audit-period freezing. A frozen period draws samples only from evidence
  with `observed_at ≤ frozen_at`; live state continues independently.
- C11 · No audit-binding artifact is published without one-click human
  approval. `ai_assisted=true` cannot carry `human_approved=true` without
  `human_approver` set, enforced in schema via the shared
  `ai_assist_human_approver_guard`.
- C13 · These need a human before they merge: `migrations/*`, `isa/*`,
  `.github/workflows/*`, `deploy/*`, `controls/*`, and `internal/auth/*`.
  This repo is PUBLIC, so an unreviewed merge is a published one. A migration
  runs once against real evidence, a workflow runs with the repo's
  credentials, `controls/` is the catalog every framework view is computed
  from, and `internal/auth/` is the tenant boundary C6 says lives in the
  database rather than in application code. A fire may propose changes to any
  of them; merge-on-green does not apply.
- C12 · Local inference is the default. Cloud LLM routing is opt-in per tenant
  and visibly indicated.

## Goal

The v1 success test, unchanged from `CLAUDE.md`: the solo security leader runs
their next SOC 2 audit out of security-atlas, generates the next board pack
from it, and does not reach for Vanta or a spreadsheet to fill a gap.

## Claims

Initiative-level only. Anything true of one feature belongs in that feature's
epic, not here.

- [ ] ISC-1: A real SOC 2 audit is run end to end out of the platform without
      reaching for another tool to fill a gap. **manual · experiential** —
      the claim is about a whole audit cycle with a real auditor, and no
      fixture reproduces that. Falsifier: any point in the cycle where the
      operator exports to a spreadsheet to do the actual work.
- [ ] ISC-2: No control in the catalog is duplicated across frameworks.
      Falsifier: two control rows resolving to the same SCF anchor with
      overlapping framework satisfactions. This is C1 stated as something
      queryable, and it is the invariant most likely to erode quietly as
      frameworks are added.
- [ ] ISC-3: Any past date is reconstructible from the evidence ledger.
      Falsifier: a replay at time T that disagrees with what the platform
      reported at T. Ledger corruption is the failure that would make every
      other claim unfalsifiable, so this one is load-bearing.
- [ ] ISC-4: No audit-binding artifact reaches a customer or auditor without a
      recorded human approver. Falsifier: any published artifact whose row
      carries `ai_assisted=true` and `human_approved=true` with a null
      approver, or any publish path that bypasses the guard entirely.
- [ ] ISC-5: A stranger can self-host it from the public repo without buying
      anything. **manual · external** — the falsifier is another person's
      machine, which this repo cannot reach. Falsifier: any required
      capability behind a paid dependency, a phone-home, or a credential only
      the author has.

## Test Strategy

Naming a probe that does not exist yet is deliberate. The engine treats an
unrunnable probe as a third value, not a pass, so these claims report
`unverifiable` rather than closing on silence. Leaving them untabled would
have marked them `manual` instead, which would be a false statement about
ISC-2, ISC-3 and ISC-4 — all three are machine-checkable, they are simply
unchecked today.

| isc   | tier    | type | check                                                             | threshold | tool                                       |
| ----- | ------- | ---- | ----------------------------------------------------------------- | --------- | ------------------------------------------ |
| ISC-2 | service | sql  | no SCF anchor carries two controls with overlapping satisfactions | 0         | `bash isa/probes/no-duplicate-controls.sh` |
| ISC-3 | service | bash | replay at T equals what was reported at T                         | 0         | `bash isa/probes/ledger-replay.sh`         |
| ISC-4 | service | sql  | no published artifact has ai_assisted without a human_approver    | 0         | `bash isa/probes/human-approver-guard.sh`  |

ISC-1 and ISC-5 carry no row on purpose. Both are manual with a stated reason
in the claim itself: ISC-1 is experiential (a whole audit cycle with a real
auditor) and ISC-5 is external (another person's machine). Neither becomes
automatable by wanting it to.

## Anti-claims

- A1: The platform does not claim a control is covered. It claims what
  evidence exists, how fresh it is, and what that leaves uncovered. Coverage is
  the auditor's conclusion, not the tool's.
- A2: The platform does not promise less work. It promises the work is done
  once rather than once per framework.

## Not yet specified

- Whether ISC-2's falsifier can be a probe against a seeded catalog, or needs
  the real SCF import to be meaningful.
- What "audit-binding artifact" enumerates to. C11 is enforced per-surface
  today; ISC-4 asserts it globally and cannot close until the set is named.
- How this initiative's epics relate to the 69 v1 slices already merged. The
  slices are history; the epics are current state. The mapping is unwritten.

## Decisions

Ratified decisions live in `docs/adr/` and are not restated here. As of
2026-08-21 there are 21, ADR-0001 through ADR-0021. The ones that most directly
bound the claims above:

| ADR  | Bears on                                                     |
| ---- | ------------------------------------------------------------ |
| 0012 | append-only evidence ledger (C2, ISC-3)                      |
| 0013 | UCF graph, one control N satisfactions (C1, ISC-2)           |
| 0014 | multidimensional scope, FrameworkScope intersection (C4, C5) |
| 0011 | RLS tenant isolation (C6)                                    |
| 0003 | audit-period freeze hash inputs (C10)                        |
| 0006 | board-narrative AI assist (C11)                              |
| 0020 | right to erasure vs the append-only ledger (C2, ISC-3)       |

ADR-0020 is the one to read first when a claim here feels too absolute:
append-only and erasure genuinely conflict, and that ADR is where the conflict
was resolved rather than wished away.

## Language

- **Control** — one requirement the program satisfies, once, regardless of how
  many frameworks ask for it.
- **Satisfaction** — an edge from a control to a framework requirement through
  an SCF anchor. Frameworks have satisfactions; they do not have controls.
- **Evidence** — an observation recorded with an `observed_at`. It is never
  edited, only superseded.
- **Scope cell** — a tuple over the six scope dimensions. Not a node in a tree.
- **FrameworkScope** — the predicate naming what a given framework covers. PCI
  CDE, HIPAA covered systems and the SOC 2 system are three different sets.
- **Audit period** — a window that can be frozen. Once frozen, its sample
  population stops moving.
- **Audit-binding artifact** — anything that leaves the system and is relied on
  by an auditor or customer. The enumeration is unwritten; see Not yet
  specified.

## Changelog

### 2026-08-21 · initiative ISA written

Written from the repo's own documents rather than from a description of the
product: `CLAUDE.md` supplied the constraints, `docs/adr/` the decisions, and
the v1 success test the goal. No claim here is new intent.

Deliberately five claims, not fifty. The 45 open Plane OEs for this repo were
NOT converted into claims. A Plane OE is a task; a claim is a statement about
the ideal state, and converting one into the other reproduces the queue this
system exists to replace. Work that matters re-derives itself as an unclosed
claim under an epic. Work that does not re-derive was not worth carrying.
