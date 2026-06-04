// Slice 222 — methodology caption text for the board-pack posture
// section.
//
// Why this lives in its own pure-TS file (slice 183 precedent): the
// web/vitest config disallows component rendering (slice 069 / 130),
// so the caption string is split out of the .tsx component into a
// pure module that a node-env vitest can import without touching JSX.
// The component imports the constant and renders it; the unit test
// pins the constant to the constitutional source.
//
// Source of truth: Plans/_archive/mockups/board-pack.html §01 footer
// (lines 146–148). Any drift between this constant and the mockup is
// a bug — keep them aligned.

export const POSTURE_COVERAGE_CAPTION =
  "Coverage definition: weighted SCF-anchored evidence pass rate intersected with each framework's scope predicate, over the period. Methodology unchanged from prior quarter.";
