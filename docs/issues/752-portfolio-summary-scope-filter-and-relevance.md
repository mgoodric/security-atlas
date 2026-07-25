# 752 — Portfolio evidence summary: scope-cell filter + richer relevance ranking

**Cluster:** AI-assist
**Estimate:** M (2-3d)
**Type:** JUDGMENT (the scope-intersection control resolution + the relevance rule)
**Status:** `not-ready` (the scope-cell filter leg depends on the FrameworkScope
ownership/intersection UX; the relevance-ranking leg is ready against #750)

> Surfaced during slice 750, captured as follow-up per continuous-batch policy.
> Slice 750 shipped the portfolio / multi-control evidence summary with a
> two-level bounded cross-control corpus, filtered by **control-family** or
> **framework version** (or whole-program). It deliberately deferred two
> JUDGMENT-heavy extensions (750 decisions-log "Revisit once in use" #1 + #2).
> This is that follow-on.

## Narrative

**Why.** Slice 750's portfolio summary answers "what does my evidence show across
this framework / this control-family / my whole program". Two refinements were
out of proportion for 750's core (the cross-control rollup shape) and were
deferred:

1. **Scope-cell filter.** AC-1 of #750 named "by framework, scope cell, OR
   control-family"; #750 shipped the OR's family + framework legs. The scope-cell
   leg (`effective_scope(control) = applicability_expr ∩ framework_scope.predicate`,
   canvas invariant #4/#5) is heavier graph/DSL work — it needs the
   multidimensional scope intersection, not a column filter — and is the natural
   third filter dimension when the PCI/HIPAA scope-ownership UX lands.

2. **Richer relevance ranking.** #750's "most-relevant of N controls" is currently
   "most-recent within a deterministic (`bundle_id`) order" — a stable but blunt
   subset selection. When a framework matches far more than the
   `MaxControlsPerSummary` (12) cap, a coverage-gap-weighted or freshness-weighted
   ranking would make the bounded subset materially more useful (surface the
   controls the operator most needs to see, not the first 12 by bundle id).

**What.** Extend the slice-750 `PortfolioStore` / `PortfolioFilter` with:

- A **scope-cell filter mode**: resolve the in-scope control set via the existing
  scope / framework-scope intersection paths (reuse, do not invent), bounded by the
  same two-level cap. Honestly labeled in the prompt + UI as the active filter.
- A **relevance rule** for the controls-per-summary selection: replace the pure
  `bundle_id` ordering with a documented relevance score (candidate inputs:
  freshness staleness, coverage gaps, recent drift). The rule is the JUDGMENT call;
  it must stay DETERMINISTIC (same inputs => same subset) so the summary is
  reproducible and the "K of N" honesty holds.

**Scope discipline.** Inherits ALL slice-750 (and therefore slice-502) constitutional
contract verbatim: two-level bound, strict citation gate over the cross-control
citable-id set, numeric-claim verification, cross-tenant isolation across the whole
set, non-binding / read-only / never-persisted, local-default routing, current live
evidence only. The new work is ONLY the scope-cell control resolution and the
relevance rule. It does NOT ship the period-scoped portfolio summary (#749 ∩ #750
— a separate further follow-on, see Notes).

## Acceptance criteria

- [ ] **AC-1.** A scope-cell filter resolves the in-scope control set via the
      existing scope / framework-scope intersection (reused, not reinvented),
      under RLS, bounded by the slice-750 two-level cap.
- [ ] **AC-2.** The controls-per-summary subset is selected by a documented,
      DETERMINISTIC relevance rule (not pure bundle-id order); the rule is recorded
      in the decisions log.
- [ ] **AC-3.** Both bounds + the active filter mode (incl. scope) are labeled
      honestly in the UI + prompt.
- [ ] **AC-4.** Cross-tenant isolation proven for the scope-filtered set.
- [ ] **AC-5.** All slice-750 / slice-502 inherited anti-criteria hold.
- [ ] **AC-6.** Decisions log (the scope-intersection resolution + the relevance
      rule are the headline JUDGMENT calls) + changelog.

## Anti-criteria (P0 — block merge)

- **P0-752-1.** Inherits ALL slice-750 + slice-502 anti-criteria.
- **P0-752-2.** The relevance rule is DETERMINISTIC (same inputs => same subset);
  no nondeterministic / model-driven control selection.
- **P0-752-3.** Scope resolution reuses the existing scope / framework-scope
  paths; does NOT introduce a parallel scope mechanism.

## Dependencies

- **#750 (this lands first)** — the portfolio surface this extends.
- Scope-cell leg depends on the FrameworkScope ownership/intersection UX
  (the open decision "FrameworkScope ownership workflow UX" in CLAUDE.md). Until
  that is settled the scope leg is `not-ready`; the relevance-ranking leg could
  ship independently against #750 if split.

## Notes

The period-scoped portfolio summary (a frozen-audit-period cross-control rollup =
the intersection of #749 and #750) is a SEPARATE further follow-on, not part of
this slice. If/when demanded, file it independently; it composes #749's
frozen-population reader with #750's two-level cross-control bound.
