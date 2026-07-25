# 517 — HIPAA covered-entity workflow (phase-3; deferred §10.3)

**Cluster:** Workflow
**Estimate:** XL (multi-slice epic — decompose before building)
**Type:** JUDGMENT + design (covered-entity program mechanics)
**Status:** `deferred` (canvas §10.3 phase-3; do NOT build before phase-3 is opened)

## Narrative

Slice 481 shipped the HIPAA Security Rule **catalog** (requirement nodes + STRM
edges to SCF anchors). This slice is the **covered-entity / business-associate
program workflow** that the canvas explicitly defers to phase 3 (canvas §10.1
"Deliberately deferred from MVP … HIPAA-specific covered-entity workflow"; §10.3
"HIPAA-specific covered-entity workflow primitives"). It is named here so the
deferral is tracked, NOT scheduled — it must not be built before phase 3 is
formally opened.

The covered-entity workflow is the program mechanics the catalog deliberately
does not model:

1. **Required-vs-addressable decision flow** — §164.306(d) lets a covered entity
   implement an _addressable_ implementation specification, document why it is
   not reasonable/appropriate, and implement an equivalent alternative. This is a
   per-spec decision workflow with a documented-rationale artifact — the first
   thread of the workflow, and the reason slice 481 kept R/A a YAML comment only
   (slice 481 decisions log D6). The structured R/A field belongs here.
2. **Business Associate Agreement (BAA) tracking** — §164.308(b)(1) /
   §164.314(a): tracking which business associates handle ePHI, their BAAs,
   renewal dates, and the satisfactory-assurance attestations.
3. **Breach risk-assessment** — §164.402 four-factor breach risk assessment and
   the §164.404–164.408 notification obligations.
4. **§164.308 administrative-safeguard process flows** — the program activities
   (security management process, periodic evaluation, sanction policy) as
   workflows, not just catalog nodes.

This is an epic; decompose into tracer-bullet slices when phase 3 opens.

## Threat model

This slice introduces genuinely new, high-sensitivity surface (unlike the
catalog slices) — it handles covered-entity program state about ePHI handling,
so the threat model is substantial and must be authored per-slice when this epic
is decomposed. Sketch:

- **S — Spoofing.** New authenticated workflow endpoints; must reuse the OAuth
  AS + RBAC/ABAC boundary; no new unauthenticated ingress.
- **T — Tampering.** BAA records, addressable-spec decisions, and breach
  assessments are tenant-scoped mutable state — must be RLS-enforced (invariant
  #6) and, for audit-binding artifacts, append-only / versioned with the
  AI-assist human-approval boundary intact.
- **R — Repudiation.** Every required-vs-addressable decision and breach
  assessment must be auditable (who decided, when, with what rationale) — this is
  a regulatory record.
- **I — Information disclosure.** This is the highest-weight category: breach
  assessments and BAA records reference ePHI-handling relationships. Tenant
  isolation (RLS) is mandatory; cross-tenant leakage is a regulatory breach in
  itself. The AI-assist boundary (no cross-tenant seeding) applies if any
  drafting assist lands.
- **D — Denial of service.** Workflow endpoints must carry the standard
  rate/size bounds.
- **E — Elevation of privilege.** Covered-entity workflow roles must compose with
  the existing RBAC model; no implicit privilege.

A full per-slice STRIDE is required before any building.

## Acceptance criteria

To be authored when phase 3 opens and the epic is decomposed. High level:

- [ ] Required-vs-addressable decision flow with documented-rationale artifact
      (structured R/A field added to the HIPAA catalog rows; addressable-spec
      alternative-implementation capture).
- [ ] BAA tracking (records, renewal, satisfactory-assurance attestations),
      RLS-enforced and tenant-scoped.
- [ ] Breach risk-assessment (§164.402 four-factor) + notification workflow.
- [ ] FrameworkScope ePHI-environment example (pairs with slice 518).

## Dependencies

- **#481** (HIPAA catalog) — merged; this workflow consumes the catalog.
- Canvas §10.3 phase-3 — must be formally opened first.
- Auth substrate (OAuth AS, RBAC/ABAC, RLS) — in place.

## Anti-criteria (P0)

- Do NOT build before phase 3 is formally opened (canvas §10.3 deferral).
- Must NOT publish any audit-binding artifact without one-click human approval
  (AI-assist boundary — constitutional).
- Must enforce tenant isolation at the DB layer (RLS, invariant #6); a
  cross-tenant ePHI-relationship leak is a regulatory breach.
