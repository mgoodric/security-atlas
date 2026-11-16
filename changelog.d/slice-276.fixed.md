- **e2e:** slice 276 — `web/e2e/control-detail-tabs.spec.ts` AC-1 / AC-2
  / AC-8 (both variants) / AC-8 garbage / AC-9 + AC-3-AC-7 un-skipped
  and passing. Slice 275 quarantined seven tests via `test.skip()`
  after a `gotoControlDetail` helper + 30s timeout fix failed to
  resolve the 30s-timeout symptom. Slice 276 pulled the failing CI
  run's playwright trace + error-context and found the actual root
  cause: the `/coverage` mock payload's requirement rows provided a
  `requirement_text` field but NOT `title` (and several other fields
  the production `CoverageRequirement` type declares as required).
  The production `<UcfMiniViz>` component (which the Overview AND
  Mappings panels mount) calls `req.title.slice(0, 34)` directly;
  with `req.title === undefined` the `.slice()` throws a `TypeError`;
  Next.js's page-level error boundary catches it and replaces the
  page body with the generic `"This page couldn't load"` fallback;
  every assertable testid disappears. The screenshot snapshot
  initially looked like a navigation failure; the trace's `pageError`
  event surfaced the real cause. Why slice 275's `gotoControlDetail`
  helper looked plausible: the AC-8 refresh test deep-links to
  `?tab=policies` (Overview panel never mounts → no
  `<UcfMiniViz>` → no crash) and passed in 1.3s, while every other
  test landed on Overview / clicked into Mappings and tripped the
  crash. The discriminating experiment was already in the failing
  CI log. Fix: align the seven `route.fulfill` mock payloads with
  their producer-side TypeScript type contracts
  (`CoverageRequirement`, `ControlStateEntry`, `ControlEffectiveness`,
  `EffectiveScopeResponse`, `ControlLinkedRisk`); un-skip the seven
  tests. E2E-only — no production code touched (P0-276-2).
  `web/e2e/README.md` gains a `### Mock payload schema-conformance`
  subsection under the existing timing-sensitive-assertions block;
  two debugging heuristics generalised (read `pageError` events
  before assuming a timeout means slow; annotate mock payloads with
  the producer type so the compiler catches missing fields). Slice
  275's `gotoControlDetail` helper is retained — it's the right
  pattern for any page with a load-bearing initial useQuery, and the
  30s backstop is harmless when the page renders in <1s. Full
  diagnosis + 4-hypothesis verdict in
  `docs/audit-log/276-control-detail-tabs-deep-fix-decisions.md`.
