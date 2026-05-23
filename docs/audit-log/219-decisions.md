# 219 — UI honesty: board-pack header Author cell hardcoded to em-dash

**Slice:** `docs/issues/219-ui-honesty-board-pack-author-cell-hardcoded.md`
**Type:** AFK (decisions log captured at orchestrator request — not a JUDGMENT slice)
**Branch:** `frontend/219-board-pack-author-cell`
**Parent:** slice 204 (UI parity audit fleet, board-pack page audit)

## Decisions made

### D1 — Choose Option A (drop the Author cell) over Option B (record + render)

- **Chosen.** Option A — drop `<MetaCell label="Author" value="—" />` from
  `web/components/board-pack/pack-header.tsx` and ship a 3-cell meta strip
  (Period end / Generated / Approver). No backend change.
- **Considered.** Option B — add an `author` column to the board-pack table
  populated from the JWT subject of the `/v1/board-packs` POST, surface it
  in the pack JSON, render it in the cell. ~1.5d with a migration and a
  wire-shape extension.
- **Rationale.** The slice spec defaults to Option A. The em-dash placeholder
  reads as "data is missing" when the field is actually not modeled — a
  small UI-honesty gap (CLAUDE.md anti-pattern: data-bound surfaces that lie).
  Option B is the right shape if/when a maintainer decides authorship is
  audit-trail content worth recording; it can be filed as a follow-on
  slice. Option A is the cheapest, lowest-risk path and respects the v1
  binary success test (no fabricated audit content).
- **Confidence.** high. Spec defaulted to A; B was explicitly listed as a
  follow-on candidate.

### D2 — Extract meta labels to a pure-logic `.ts` module for testability

- **Chosen.** Create `web/components/board-pack/pack-header-meta.ts`
  exporting `PACK_HEADER_META_LABELS = ["Period end", "Generated",
"Approver"] as const`. Import the constant into `pack-header.tsx` and
  reference each cell's label as `PACK_HEADER_META_LABELS[i]`. Cover the
  constant with `pack-header-meta.test.ts`.
- **Considered.** Inline string literals in `pack-header.tsx` (no extracted
  module). Cheapest in lines of code but leaves the component shape
  untestable — `web/vitest.config.ts` is node-env / no-JSX so the
  component itself cannot be rendered to assert "Author isn't in the
  strip".
- **Considered.** Add `@testing-library/react` and render the component.
  Rejected — slice 069 explicitly carved out this dependency
  (P0-A3 in slice 069 anti-criteria, codified at vitest.config.ts:7-9).
  Re-introducing it for a single 3-line component refactor would be a
  large precedent shift for a small honesty fix.
- **Considered.** Add a Playwright spec for board-pack-detail asserting
  no Author label is rendered. Rejected as out-of-scope for this slice —
  no existing board-pack-detail Playwright spec exists, and creating one
  would mean writing seed fixtures, auth, navigation, and waiters for a
  page the audit harness already partially covers. The unit test on the
  extracted constant is sufficient for the AC-3 intent (regression-
  guarding "Author not present"). If a follow-up slice spins up a
  detail-page Playwright spec for other reasons, the no-Author assertion
  can be added there cheaply.
- **Rationale.** The slice 222 precedent (posture-coverage-caption.ts +
  posture-coverage-caption.test.ts) is the established pattern in this
  module: extract pure logic into `.ts`, unit-test the constant, leave
  the JSX wiring trivial. Re-using it keeps the test surface consistent
  and avoids opening a vitest-config conversation for a non-load-bearing
  change.
- **Confidence.** high. Mirrors a precedent set three slices earlier.

### D3 — Diverge from the mockup (Author cell is in `Plans/mockups/board-pack.html`)

- **Chosen.** Do NOT update `Plans/mockups/board-pack.html` line 69 in this
  slice. The mockup still shows a 4-cell strip with `Sam Rivera (CISO)`
  in the Author cell. Code intentionally diverges from the design
  artifact. A code comment in `pack-header.tsx` documents the divergence.
- **Considered.** Bundle a mockup update (drop the Author cell from §00)
  with the code change. Rejected — mockup edits live in their own slice
  type (cf. slice 220, which was a pure-mockup slice for the §03 chart
  alignment). Mixing chrome scope here would inflate the PR.
- **Rationale.** "Honesty > parity" — the design artifact can drift one
  step ahead of code, and the next mockup-alignment slice (whoever files
  it) can decide whether to re-add Author to the mockup (Option B path)
  or strip it (catching up to Option A). The code comment in
  `pack-header.tsx` flags the divergence for that future reader.
- **Confidence.** medium. Some readers might prefer a paired mockup edit;
  the modular split is the established slicing discipline.

### D4 — Anti-criteria scan: confirm none of the P0s are at risk

- **Chosen.** Verified before merge:
  - **P0-219-1** (no fake author data) — Option A removes the cell
    entirely; no fabricated value is introduced. ✓
  - **P0-219-2** (no backend / migration touch) — Code change is
    confined to `web/components/board-pack/pack-header.tsx`,
    `web/components/board-pack/pack-header-meta.ts`,
    `web/components/board-pack/pack-header-meta.test.ts`,
    `docs/audit-log/219-decisions.md`. No backend, schema, or
    migration files touched. ✓
  - **P0-219-3** (Approver cell preserved) — The Approver MetaCell is
    retained verbatim, including the `publishedBy` data binding and the
    `muted={!publishedBy}` styling. ✓
- **Anti-criteria from prompt** (no `_STATUS.md` / `CHANGELOG.md` touch,
  no unrelated MetaCells touched) — verified by the `pre-commit` /
  `git diff` review.
- **Confidence.** high.

## Revisit once in use

- **R1.** If a future slice models authorship on the board-pack record
  (Option B path), `pack-header-meta.ts` can grow back to a 4-tuple and
  `pack-header.tsx` can re-add the cell with a data binding (NOT a
  hardcoded em-dash). The slice 219 anti-pattern guard is the
  `pack-header-meta.test.ts` regression assertion "does not contain
  Author"; that test would be the explicit gate that future author flips.
- **R2.** Mockup update slice — if a maintainer wants the mockup to
  catch up to the live component (drop Author from §00), file that as
  a small mockup slice. The pattern is slice 220.

## Files touched

- `web/components/board-pack/pack-header.tsx` — drop Author MetaCell;
  switch grid from `md:grid-cols-4` to `md:grid-cols-3`; import labels
  from new pure-logic module; refresh the file header comment.
- `web/components/board-pack/pack-header-meta.ts` — new module; export
  the 3-tuple constant.
- `web/components/board-pack/pack-header-meta.test.ts` — new unit test;
  asserts exact list + no-Author regression guard + length=3.
- `docs/audit-log/219-decisions.md` — this file.

## Anti-criteria honored

- **P0-219-1.** No fabricated author data introduced. The cell is removed
  entirely; the only remaining identity field is `Approver`, which is
  data-backed (`pack.published_by`).
- **P0-219-2.** No backend modification: code change confined to web/.
  No migration, no API schema change, no `pack.go` touch.
- **P0-219-3.** `Approver` MetaCell is preserved verbatim, including the
  `pending` fallback rendering and the muted-color styling.

## Constitutional invariants honored

- Anti-pattern rejection (canvas §1.6 / CLAUDE.md anti-patterns):
  "data-bound surfaces that lie" — em-dash promised an Author the system
  cannot produce. Closed.
- AI-assist boundary (CLAUDE.md): not touched — board narratives still
  publish only with one-click human approval; the meta strip is
  deterministic.
- Frontend testing discipline (slice 069): vitest is node-env / no-JSX;
  this slice respects the rule by extracting a pure-logic module rather
  than introducing component rendering.
