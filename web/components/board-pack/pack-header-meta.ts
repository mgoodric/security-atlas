// Slice 219 — board-pack header meta-strip label list.
//
// Extracted as pure-logic per the slice 222 (posture-coverage-caption.ts)
// precedent: web/vitest.config.ts is node-env / no-JSX, so component shape
// is asserted by unit-testing the constant the component renders from.
//
// The Author cell was removed in slice 219 (em-dash hardcode was a UI-
// honesty gap — the backend board-pack record has no author field). If a
// future slice models authorship on the pack record and wants to surface
// it, add the label back here (and re-grade the grid back to 4 columns
// in pack-header.tsx). Anti-pattern guard: do NOT re-add "Author" with a
// hardcoded em-dash placeholder.

export const PACK_HEADER_META_LABELS = [
  "Period end",
  "Generated",
  "Approver",
] as const;

export type PackHeaderMetaLabel = (typeof PACK_HEADER_META_LABELS)[number];
