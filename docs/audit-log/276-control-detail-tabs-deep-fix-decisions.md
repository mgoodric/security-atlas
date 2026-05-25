# Slice 276 — control-detail-tabs.spec.ts deep-fix decisions

## Context

Slice 275 attempted to fix the racy `web/e2e/control-detail-tabs.spec.ts`
AC-1 / AC-2 / AC-8 (both) / AC-9 + AC-3-AC-7 cases by adding a
`gotoControlDetail` helper that gates the first assertion on the
`/coverage` network response, with a 30s backstop. The fix did not
work. Slice 275 ended by quarantining 7 tests via `test.skip()` and
filing this slice to own the deep investigation.

This document captures the actual root cause, the empirical evidence
that disproves slice 275's diagnosis, and the fix.

## D-276-1 — Actual root cause

**The page is crashing on render, not running slowly.** The
production component
`web/components/control/ucf-mini-viz.tsx:122` does
`req.title.slice(0, 34)`. The slice 254 e2e spec's mocked `/coverage`
response payload provides a `requirement_text` field on each
requirement row but does NOT provide `title`. With `req.title ===
undefined`, the `.slice()` call throws a `TypeError`. The React
render tree above `<UcfMiniViz>` (the Overview panel and the
Mappings panel both mount it) crashes; Next.js's page-level error
boundary catches the throw and replaces the page body with the
generic "This page couldn't load" fallback. The tablist, every tab
panel, and every assertable testid the spec asserts on never
render.

Evidence (downloaded from the slice 275 failing CI run's playwright
artifact, run id `26358836105`):

| Artifact                                          | Observation                                                                                                                                                                                                                  |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `test-failed-1.png` + `error-context.md` snapshot | Shows the "This page couldn't load" Next.js error-boundary fallback with Reload + Back buttons. NOT the `coverageQ.isLoading` Skeleton.                                                                                      |
| `0-trace.network`                                 | Every `/api/controls/*` request returned HTTP 200. The `page.route` mocks WERE intercepting. Slice 275's update note ("`page.route` mock for /coverage is NOT intercepting the request") was empirically false.              |
| `0-trace.trace` page event                        | `BrowserContext pageError`: `TypeError: Cannot read properties of undefined (reading 'slice')` from `Array.map` at `http://localhost:3000/_next/static/chunks/0bxlcl~ol-y3u.js:1:14906`. Stack pinpoints `ucf-mini-viz.tsx`. |

Cross-reference: `CoverageRequirement` type
(`web/lib/api.ts:1078-1094`) declares as REQUIRED:
`edge_id`, `requirement_id`, `code`, `title`, `framework_slug`,
`framework_name`, `framework_version`, `framework_version_id`,
`framework_version_status`, `relationship_type`, `strength`,
`coverage`, `source_attribution`. The pre-fix mock provided only 8
of those 13 required fields; `title` was among the 5 missing.

## D-276-2 — Why AC-8 refresh appeared to pass in the slice 275 CI run

The slice 275 failing CI run shows `AC-8: refresh on a tab-deep-
linked URL lands on that tab` PASSING in 1.3s while every other
test timed out at 30s. This was a clue, not a flake.

AC-8 refresh deep-links to `?tab=policies`. The page's
`ControlDetailPageInner` reads `activeTab` from the URL
(`?tab=policies` → `"policies"`), and the JSX conditionally
renders panels: `{activeTab === "overview" ? <OverviewPanel/> :
null}` and `{activeTab === "policies" ? <PoliciesPanel/> : null}`.
With `activeTab === "policies"`, the Overview panel never
mounts on first paint, so `<UcfMiniViz>` (which lives inside
OverviewPanel AND MappingsPanel) never instantiates, the
`req.title.slice()` call is never made, no crash, the tablist
and `policies-tab-panel` render, the test asserts and passes.

Every other test in the file lands on Overview (default tab) or
clicks Mappings (which also mounts `<UcfMiniViz>`) — those tests
trip the crash and fail.

This is the discriminating experiment slice 275 could have run but
didn't: "if hypothesis #1 (mount sequence too slow) is true, any
first-render assertion should fail; if hypothesis #4 (crash inside
a tab-specific component) is true, a deep-link to a tab that
bypasses that component should pass." AC-8 refresh was already the
natural experiment and the result was visible in the CI logs.

## D-276-3 — The 4 spec hypotheses re-verdicted

The slice 276 spec listed 4 hypotheses. Verdicts post-investigation:

| #   | Spec hypothesis                                                                                                  | Verdict                                                                                                                                                                                                                     |
| --- | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| H1  | Bearer cookie missing in mock request                                                                            | FALSE. Trace shows the `atlas_jwt` cookie present on every request; auth path is fine; the page mounts.                                                                                                                     |
| H2  | Request URL mismatch (BFF at a different path / query params differ)                                             | FALSE. Trace shows the browser fetched the exact URL the mock pattern targets and the mock returned the documented JSON.                                                                                                    |
| H3  | Race between route registration and navigation                                                                   | FALSE. The mocks intercepted; the page received the mocked payload. Slice 275 already disproved this in code-reading.                                                                                                       |
| H4  | `enabled: Boolean(id)` Suspense race                                                                             | FALSE. `coverageQ` resolves; `coverageQ.data` is populated; the page advances past the Skeleton; the tablist renders for one render-cycle before `<UcfMiniViz>` throws and the error boundary takes the whole subtree down. |
| —   | **Actual root cause: mocked payload missing the `title` field that the production `UcfMiniViz` component reads** | TRUE. Fixed by aligning the mock with the `CoverageRequirement` type contract.                                                                                                                                              |

## D-276-4 — Fix shape

E2E-only. No production code touched (slice 276 P0-276-2).

Edits in `web/e2e/control-detail-tabs.spec.ts`:

1. The `coverage` mock's `requirements[]` rows now include every
   field declared required by `CoverageRequirement`: `edge_id`,
   `code`, `title`, `framework_slug`, `framework_version_status`,
   `source_attribution`. Values are deterministic and realistic.
2. The `state` mock row now uses `scope_cell_id` (not the invented
   `scope_cell`), `result` (not `computed_state`), and adds
   `evaluated_at`, `freshness_class`, `evidence_count_in_window`,
   `trigger` — every required field on `ControlStateEntry`.
3. The `effectiveness` mock now provides `window_start` + `window_end`
   ISO timestamps (dropping the invented `window_days` integer) —
   matches the `ControlEffectiveness` type.
4. The `effective-scope` mock now provides a populated
   `effective_scope` cell array and `framework_scope_id` — matches
   the `EffectiveScopeResponse` type. It now ALSO dispatches per
   `framework_version` query parameter: the page issues one
   `/effective-scope?framework_version=<fv>` call per distinct
   `framework_version_id` in the coverage requirements (two here:
   SOC 2 + ISO 27001). The page's tab-count math sums
   `effective_scope_count` across all per-framework responses
   (`scopeCellSum` in `page.tsx`). The slice 254 mock returned a
   fixed `effective_scope_count: 12` per call which would have
   summed to 24 (AC-2 expects "12"). The design flaw was never
   observed because the page crashed earlier — the missing-`title`
   bug masked it. Slice 276 fixes both: 6 cells per framework × 2
   frameworks = 12, matching the AC-2 assertion.
5. The `risks` mock now provides `inherent_score` and shapes
   `residual_score` as the canvas-§2.2 `{likelihood, impact}` JSONB
   blob — matches `ControlLinkedRisk`.
6. All seven `test.skip()` markers are removed; the tests run.
7. The spec's preamble comment is rewritten to capture the real
   diagnosis so the next reader doesn't re-discover slice 275's
   dead-end mount-sequence hypothesis.

The `gotoControlDetail` helper from slice 275 is RETAINED. It's the
correct pattern for any page whose tablist is gated on a load-
bearing useQuery, the 30s backstop is harmless once the page
renders in <1s (which it now does), and removing it would lose
the README's documented "Gating the FIRST visibility assertion on a
network round-trip" pattern. Slice 275 was right about the shape;
it was wrong about THIS spec's failure mode.

## D-276-5 — Why not defensive-code in production

Hardening `ucf-mini-viz.tsx` (e.g. `(req.title ?? "").slice(0, 34)`)
was considered and rejected:

1. The production type contract IS the source of truth.
   `CoverageRequirement` declares `title: string` (required). The
   backend honors it. A consumer that special-cases the type-contract
   violation invites mocks (and bugs) to ship invalid payloads with
   no signal.
2. The page already has type-driven assertions everywhere else
   (`req.framework_name`, `req.code`, etc. in template strings —
   undefined becomes the string "undefined", which is sloppy but
   not a crash). Adding nullish-coalescing JUST on `.slice` callers
   would be inconsistent and would mask the real issue: the e2e
   mock was lying.
3. The slice 276 anti-criterion P0-276-2 explicitly forbids
   modifying production code unless the root cause is in production
   code. The root cause is in the mock. Fix the mock.

If a future PRD-level decision wants graceful-degradation on missing
optional fields, that's a separate slice (and would need to walk
every component that touches a required field).

## D-276-6 — Lesson generalisation

The class of bug surfaced here is: **mocked payloads must
satisfy the producer-side type contract, not just the fields the
test reads.** This is because the page consumes more of the type
than the test asserts on, and a missing required field becomes a
production-code crash that's hidden inside the spec's own scaffold.

`web/e2e/README.md` gains a "Mock payload schema-conformance"
subsection under the existing "Timing-sensitive assertions" block.
The pattern recommended:

- When mocking a fetch via `page.route`, ALWAYS resolve the
  `route.fulfill({body})` payload against the
  TypeScript type the consumer reads (e.g. `CoverageRequirement`
  for `/api/controls/{id}/coverage`).
- Treat a missing required field as an immediate spec bug — not
  a "the test reads X but not Y, so Y is optional" judgment call.
- When a Playwright test fails with a timeout on a `toBeVisible`
  AND the screenshot shows a generic error page (`"This page
couldn't load"`), the FIRST step is to read the trace's
  `pageError` events. A network timeout pattern can mask a
  render-tree crash.

## Anti-criteria honoured

- **P0-276-1** (does NOT delete the tests; un-skip + pass): all 7
  `test.skip()` markers removed; the 7 tests run; assertions are
  unchanged.
- **P0-276-2** (does NOT modify production page code): no edits
  to `web/app/(authed)/controls/[id]/page.tsx`, `ucf-mini-viz.tsx`,
  `coverage-table.tsx`, `freshness-clock.tsx`, or any other
  production-side file. Only `web/e2e/control-detail-tabs.spec.ts`,
  `web/e2e/README.md`, this audit-log, and `CHANGELOG.md` change.
- **`docs/issues/_STATUS.md`**: NOT modified.

## Files touched

- `web/e2e/control-detail-tabs.spec.ts` — preamble rewrite + mock
  payload schema-conformance + un-skip all seven tests.
- `web/e2e/README.md` — "Mock payload schema-conformance"
  subsection under "Timing-sensitive assertions"; honest
  correction to the slice 275 subsection's diagnosis.
- `docs/audit-log/276-control-detail-tabs-deep-fix-decisions.md` —
  this file.
- `CHANGELOG.md` — `### Fixed` bullet for slice 276.
