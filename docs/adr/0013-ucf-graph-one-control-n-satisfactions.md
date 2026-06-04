# ADR 0013 — The UCF as a graph: one control, N framework satisfactions

**Status:** Accepted — **retrospective** record of a founding invariant (CLAUDE.md
architecture invariant #1). The decision was made and shipped long before this
ADR; this record reconstructs the trade-off context and the rejected
alternative after the fact. It does NOT re-open the question.

**Date:** 2026-06-04

**Records:** CLAUDE.md architecture invariant **#1** ("One control, N framework
satisfactions. The UCF is a graph with STRM-typed edges through SCF anchors.
Never duplicate controls per framework."). Closely bound to invariant #7 ("SCF
is the canonical control catalog. Mappings go requirement → SCF anchor, never
requirement → requirement directly.").

**Canvas:** [`Plans/canvas/03-ucf.md`](../../Plans/canvas/03-ucf.md) §3, with the
fully-worked graph model (diagrams, traversal queries, versioning, storage, and
the "one MFA evidence record satisfies six frameworks" walkthrough) in
[`Plans/UCF_GRAPH_MODEL.md`](../../Plans/UCF_GRAPH_MODEL.md).

**Implementation reference (cited, not restated):**
[`internal/ucf/`](../../internal/ucf/) (graph queries) and
[`internal/catalog/`](../../internal/catalog/) (SCF + framework versioning).

---

## Context

A GRC program runs against many frameworks at once — SOC 2, ISO 27001, NIST
CSF, PCI DSS, HIPAA, GDPR — and the same underlying control (say, "MFA is
enforced for administrative access") satisfies a requirement in each of them.
The central modeling question is how to represent that many-to-many reality
without it decaying every time a framework revises.

The dominant tool shape (canvas §3.1) maintains framework crosswalks as flat
pairwise tables: `(control_in_framework_A, control_in_framework_B)`. This has
two failure modes the product is built to avoid:

1. **It decays under versioning.** Every framework revision forces re-authoring
   of every pairwise table that touches it. An ISO 27001:2013 → :2022 update
   ripples into every crosswalk ISO participates in.
2. **It silently rounds N:M to 1:1.** Pairwise crosswalks lose the true
   cardinality of mappings; a requirement that maps to three anchors at
   different strengths becomes a single best-guess pair.

The naive way to make "this control satisfies five frameworks" easy is to
duplicate the control five times, once per framework. That is the specific
anti-pattern this invariant forbids — it multiplies maintenance by the number
of frameworks and guarantees the copies drift.

## Decision

**Model the Unified Control Framework as a directed, labeled graph anchored on
the Secure Controls Framework, with STRM-typed edges from framework
requirements to SCF anchors — never directly between framework requirements,
and never by duplicating a control per framework** (canvas §3).

- **Nodes:** framework requirements and SCF anchors. SCF (~1,400 controls,
  crosswalked to 200+ frameworks) is the canonical catalog and the hub every
  mapping passes through (invariant #7).
- **Edges:** STRM-typed mappings (per [NIST IR 8477](https://csrc.nist.gov/pubs/ir/8477/final))
  between a framework requirement and an SCF anchor, carrying a relationship
  type and a coverage _strength_. Framework-to-framework relationships are
  _derived_ by traversing through the shared SCF anchors, never stored
  directly.
- **One control, N satisfactions:** because all framework relationships route
  through SCF anchors, a single control mapped to an anchor automatically
  satisfies every framework requirement whose own edge reaches that anchor. One
  piece of evidence covering `SCF:IAC-22` at strength 1.0 makes
  `ISO27001:A.9.4.2 → SCF:IAC-22` (strength 0.8) covered at 0.8 — computed,
  not hand-maintained, and the UI surfaces the residual gap.

Querying framework coverage is a graph traversal (recursive CTEs over the
edge table — PostgreSQL is first-class for this; canvas tech stack), not a
join across per-framework duplicates.

**Why the SCF-anchor hub matters under versioning:** an ISO 27001:2013 → :2022
update changes only the edges from ISO requirements to SCF anchors. The SCF
graph itself and every _other_ framework's edges are untouched (canvas §3.1).
The hub-and-spoke shape localizes the blast radius of any single framework
revision to that framework's own edges.

## Consequences

**Positive:**

- A control is authored and maintained once; its satisfaction of N frameworks
  is derived. No per-framework copies to drift.
- Framework crosswalks stay coherent under revision — a framework update
  touches only that framework's edges to SCF, not a quadratic web of pairwise
  tables.
- True N:M cardinality and per-edge coverage strength are preserved, so the
  platform can compute _coverage strength per requirement_ and surface partial
  coverage honestly rather than rounding to "mapped / not mapped."
- One evidence record can satisfy many requirements automatically (the worked
  "one MFA record, six frameworks" example in `UCF_GRAPH_MODEL.md`).

**Negative / accepted trade-offs:**

- **Graph traversal is more complex than a flat join.** Coverage queries are
  recursive CTEs over the edge table rather than simple lookups. Accepted:
  Postgres recursive CTEs are first-class, and the complexity buys coherence
  under versioning that flat tables cannot offer.
- **Everything routes through SCF.** A requirement with no good SCF anchor
  needs an anchor (or an honest "unmapped") rather than a direct edge to a
  sibling requirement. This is a constraint, not a convenience — it is what
  keeps the graph from degenerating back into pairwise crosswalks.
- **Mapping lineage is itself versioned.** Mappings (`requirement → SCF`) are
  pinned to a `FrameworkVersion` _and_ an `SCF release`; the mapping table
  carries its own version lineage. More bookkeeping than a static crosswalk,
  and it is the bookkeeping that makes point-in-time framework coverage
  answerable.

## Alternatives considered (rejected — recorded retrospectively)

- **Per-framework duplicated controls (one control copy per framework it
  satisfies).** Rejected. It multiplies authoring and maintenance by the number
  of frameworks, guarantees the copies drift, and destroys the "one evidence
  record satisfies N frameworks" property — each duplicate would need its own
  evidence wiring. This is the exact anti-pattern invariant #1 names ("Never
  duplicate controls per framework") and the canvas anti-pattern list rejects
  ("Per-framework duplicated controls").
- **Direct requirement → requirement crosswalk tables (the Vanta-shaped flat
  pairwise model).** Rejected. It decays with every framework revision and
  silently rounds N:M to 1:1 (canvas §3.1). Routing every mapping through SCF
  anchors (invariant #7) localizes revision blast radius and preserves
  cardinality.
- **A non-SCF or bespoke internal catalog as the hub.** Rejected. SCF is an
  existing, maintained, broadly-crosswalked standard (~1,400 controls → 200+
  frameworks via NIST IR 8477 STRM); inventing a private hub would mean
  re-deriving that crosswalk corpus and owning its maintenance forever.
  Standing on SCF (invariant #7) is the leverage the whole graph model
  depends on.

## Related decisions

- Binds tightly to invariant #7 (SCF is the canonical catalog; mappings go
  requirement → SCF anchor) — that invariant is the "through SCF anchors"
  half of this one.
- Composes with **ADR-0014** (multidimensional scope + FrameworkScope
  intersection): coverage is computed over the graph _and_ over the
  effective-scope cell set; the graph answers "which requirements does this
  control reach," scope answers "in which cells does its evidence count."
- Composes with **ADR-0012** (append-only ledger): one evidence record reaching
  N requirements is the read-side payoff of the immutable ledger feeding the
  evaluation stage.
