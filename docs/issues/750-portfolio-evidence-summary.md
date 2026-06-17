# 750 — Portfolio / multi-control evidence summary

**Cluster:** AI-assist
**Estimate:** L (3-5d)
**Type:** JUDGMENT (the cross-control rollup shape + the corpus-bounding across
many controls + the dashboard mount)
**Status:** `ready` (depends on #502 merged)

> Surfaced during slice 502, captured as follow-up per continuous-batch policy.
> Slice 502 shipped evidence-summarization v0 for **one control, one summary**
> (502 "Scope discipline") and named a cross-control / portfolio summary as an
> explicit follow-on (502 decisions-log "Revisit once in use" #2). This is that
> follow-on.

## Narrative

**Why.** Slice 502 answers "what does the evidence for THIS control show". The
solo security leader (the v1 persona) also asks the portfolio question — "what
does my evidence show across this **framework** / this **scope cell** / my whole
**program** right now". Today that means reading per-control summaries one by
one. The platform holds the cross-control evidence deterministically; it does
not summarize at the portfolio level.

**What.** A portfolio variant of the 502 surface: for a **set of controls**
(selected by framework, scope cell, or control-family filter), assemble a bounded
cross-control evidence rollup (a small, capped number of cited excerpts PER
control, then a capped number of controls), feed it to the slice-499 per-tenant
inference client, and render a non-binding, cited summary — "what your evidence
collectively shows across these N controls" — on a **dashboard** surface, with
citations resolving to specific control + evidence IDs.

**Scope discipline.** Mirror 502's constitutional contract entirely
(validate-every-citation-then-suppress, no fabricated coverage, cross-tenant
proven absent, no approve/publish/export, never persisted, local-default
routing, current live evidence only). The new work is the **cross-control corpus
bounding** (this is the hard JUDGMENT call — how many controls, how many records
per control, how to keep the prompt bounded AND the summary meaningful at
portfolio scale) and the **dashboard mount**. It does NOT ship a period-scoped
portfolio summary (that is the intersection of #749 + this, a further follow-on).

## Threat model

Same STRIDE family as 502, with two sharpened concerns:

**T — fabrication at scale.** A portfolio summary spanning many controls has more
surface area to assert unsupported coverage ("all 40 controls are covered" when
the rollup only shows passing evidence for 30).
_Mitigation/AC:_ the same strict citation gate, applied to the larger
citable-id set; every coverage claim must cite specific control/evidence IDs;
numeric claims about counts ("30 of 40 controls have fresh evidence") should be
verified against the deterministic rollup (the slice-440 numeric-claim-check
pattern) — a fabricated portfolio statistic auto-suppresses.

**D — corpus blow-up.** N controls × M records is the unbounded-corpus risk
squared.
_Mitigation/AC:_ a two-level bound — cap records-per-control AND cap
controls-per-summary; state both bounds honestly in the UI ("summarizing the K
most-relevant of N controls").

## Acceptance criteria

- [ ] **AC-1.** For a filtered control set, a two-level bounded cross-control
      evidence rollup is assembled under RLS (cap per-control records AND cap
      controls).
- [ ] **AC-2.** Strict citation gate (no fabricated coverage), applied to the
      cross-control citable-id set.
- [ ] **AC-3.** Numeric portfolio claims verified against the deterministic
      rollup (slice-440 pattern) — fabricated counts suppress.
- [ ] **AC-4.** Cross-tenant isolation proven across the whole control set.
- [ ] **AC-5.** Dashboard mount; non-binding, no approve/publish/export, never
      persisted, current live evidence only, both bounds labeled.
- [ ] **AC-6.** Decisions log (the cross-control bounding is the headline
      JUDGMENT call) + changelog.

## Anti-criteria (P0 — block merge)

- **P0-750-1.** Inherits ALL 502 anti-criteria.
- **P0-750-2.** Does NOT feed an unbounded N×M corpus — two-level bound,
  honestly labeled.
- **P0-750-3.** Does NOT fabricate portfolio-level coverage or counts (numeric
  verification).

## Dependencies

- **#502 (merged)** — the per-control surface this generalizes.
- Reuses the slice-499 per-tenant inference client and the slice-440
  numeric-claim-verification pattern.
- Control-set filtering reuses the existing scope / framework / control-family
  query paths.

## Notes

Heavier than #749 (a genuinely new rollup shape + dashboard surface + numeric
verification at portfolio scale), hence L. The cross-control corpus-bounding is
the load-bearing JUDGMENT call — get it wrong and the summary either blows the
token budget or says nothing useful.
