# 245 — Risks mockup-stale: "N above appetite" subtitle has no v1 backend concept

**Cluster:** Documentation / Mockup hygiene
**Estimate:** 0.25d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (`/risks` page), captured as a
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/risks.html` line 111 carries the title-row subtitle:

> `47 risks · 3 above appetite`

Risk appetite is not a v1 concept. Specifically:

1. **Canvas §6** (`Plans/canvas/06-risk.md`) describes the residual
   risk pipeline — `Residual = inherent × (1 − control_effectiveness)`
   — and the risk register's link to control effectiveness. The
   section does NOT mention appetite, risk_appetite, or any tally of
   "risks above a tolerance band".
2. **Schema** (`migrations/`): grep returns zero matches for
   `appetite`, `risk_appetite`, `above_appetite`. The `risks` table
   carries `inherent_score`, `residual_score`, `treatment`,
   `treatment_owner` (slice 002 / slice 019) but no appetite column.
3. **Wire type** (`web/lib/api.ts` lines 1952-1973): the `Risk` type
   has no appetite field.
4. **API path** (`internal/risk/`): no `/v1/risks/appetite`,
   `/v1/risk_appetites`, or equivalent endpoint.

The mockup is referencing a concept that does not exist anywhere in
v1. The live `/risks` page omits the subtitle honestly — the
subtitle in `RisksPageInner` (`page.tsx` lines 353-360) reads only
"Flat list of all risks · for the org-tree view see Risk hierarchy".

The audit classifies this as **mockup-stale (category iv)** per the
slice-178 vocabulary. The fix is to walk the mockup back — not to
ship an appetite field.

Risk appetite IS a common GRC concept and would slot in alongside
the methodology choice (the maintainer-facing open question on which
methodology to default). A future v2+ slice could:

(a) add a per-category appetite band (or per-methodology appetite
scalar) as a tenant-configurable policy,
(b) compute the per-risk over-appetite predicate at residual-update
time,
(c) surface the tally in the title subtitle.

That work is **out of scope for this slice.** The smaller correct
path for now: update the mockup to remove the appetite phrase, and
file an explicit "v2 risk-appetite module" placeholder slice for
the maintainer to weigh against other v2 product scope when the
v2-cycle planning happens.

## Threat model

**Verdict.** **no-mitigations-needed.** Documentation edit + a
placeholder issue. No code change, no schema change, no auth
surface.

## Acceptance criteria

- **AC-1.** `Plans/mockups/risks.html` line 111 updated. The
  `47 risks · 3 above appetite` becomes `47 risks` (or matches
  whatever subtitle phrasing other list pages use — `controls.html`
  shows `82 controls · 6 frameworks in scope` which is a similar
  pattern; replicate the shape but with the count only since "above
  appetite" has no replacement).
- **AC-2.** A v2-placeholder slice file is filed at
  `docs/issues/<NNN>-risk-appetite-module-v2-placeholder.md` (next
  free number in the v2 spillover range, not 243-247). The
  placeholder records the appetite concept, the per-category vs
  per-methodology design tension, and notes the v2 prerequisite work
  (per-category appetite policy + over-appetite predicate +
  title-bar tally). It carries `Status: deferred` and explicitly
  cites this slice (245).
- **AC-3.** A line is appended to `Plans/canvas/11-open-questions.md`
  noting "risk appetite as a first-class field (v2+ decision; see
  slice <NNN-placeholder>)".
- **AC-4.** CHANGELOG entry: "Risks mockup: remove
  `above appetite` subtitle (mockup-stale); v2 appetite module
  filed (#245)".

## Constitutional invariants honored

- **Truth-telling chrome.** The mockup must not promise a feature
  that has no backing implementation. Walking the subtitle back
  resolves the gap honestly.
- **One change per slice.** This slice ships the documentation
  walk-back ONLY. The v2 module is filed as a placeholder, not
  built.

## Canvas references

- `Plans/canvas/06-risk.md` — residual risk pipeline (no appetite)
- `Plans/canvas/11-open-questions.md` — to be appended in AC-3

## Dependencies

- **#178** (UI honesty audit harness) — `in-progress`. This slice
  resolves a category-iv finding from the slice 204 fleet.

## Anti-criteria (P0 — block merge)

- **P0-245-1.** Does NOT add an `appetite` field to the schema,
  the wire type, or the API. That is the v2 placeholder slice's
  scope (and even then, only after maintainer prioritization).
- **P0-245-2.** Does NOT change the live `/risks` page subtitle.
  The page is honest today; the gap is in the mockup, not the page.
- **P0-245-3.** Does NOT delete the mockup line — replace it with a
  truthful subtitle in the same shape.
- **P0-245-4.** Does NOT default-prioritize the v2 placeholder
  ahead of other v2 work. The maintainer ranks v2 scope.

## Skill mix (3-5)

1. Mockup hygiene — HTML edit
2. Slice authorship — v2 placeholder + open-question append
3. Pattern-matching — replicate the controls-page subtitle shape
