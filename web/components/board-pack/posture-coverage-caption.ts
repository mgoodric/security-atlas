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
// Source of truth (#2056): the dashboard's per-framework posture query
// (internal/db/queries/dashboard.sql FrameworkPosture), which the board
// pack reads verbatim. The archived mockup's wording ("weighted
// SCF-anchored evidence pass rate") described a formula the platform
// never computed; keep this caption aligned with the query instead.

export const POSTURE_COVERAGE_CAPTION =
  "Coverage definition: the share of each framework's in-scope requirements (those its activated framework scope does not exclude) with at least one control on a mapped SCF anchor. The same figure the dashboard shows; the trend and baseline are over the last 90 days.";
