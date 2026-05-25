# Slice 279 — Coverage audit + targeted lift · Decisions log

> Slice 279 is a JUDGMENT slice — Claude makes the subjective build-time
> calls and records them here per `Plans/prompts/04-per-slice-template.md`.
> The slice merges on CI green; the maintainer iterates post-merge from
> the revisit list below.

## Decisions made

### D1 — 5-package selection (final)

**Options considered:**

- **(a) Provisional 5 from slice spec:** `internal/decision`, `internal/risk`,
  `internal/board`, `internal/frameworkscope`, `internal/eval`.
- **(b) Substitute `internal/scope` (35.3% merged, 266 stmts) for
  `internal/board`** since board's 23.7%-merged starting point is too far
  from 70% to clear in one slice.
- **(c) Substitute `internal/api/controls` (26.3% merged, 559 stmts) for
  `internal/board`** — large HTTP-handler surface with more
  unit-testable code.

**Chosen: (a)** — keep the spec's provisional 5. Rationale:

1. The audit measurement at `docs/coverage-audit-2026-05.md` confirms the
   provisional 5 ARE the highest-leverage `unit-add` packages by the
   slice's stated criteria (gap × business criticality ÷ statement count).
2. `internal/board` is large but the publish-gate logic + HTML renderer
   are unit-testable; the chromedp PDF render is the only DB/IO surface
   that has to stay integration. Substituting away from board would punt
   the actual board-pack quality work that v1 success depends on.
3. The slice spec explicitly anticipates this case: "If integration-merge
   moves any of these above 70%, swap in the next-highest-leverage
   `unit-add` package from the audit" — but the rule is conditional on
   merge-clearing 70%, NOT on the test-writing effort being heavy. Board
   stays in the target set.

**Confidence:** high.

### D1a — Acknowledged partial lift for `internal/board` and `internal/eval`

After 4+ hours of test-writing, the lift outcomes are:

| Package                   | Before | After (merged) | Floor (after) | At 70%?           |
| ------------------------- | ------ | -------------- | ------------- | ----------------- |
| `internal/decision`       | 67.8%  | 72.1%          | 70            | yes               |
| `internal/frameworkscope` | 21.8%  | 77.8%          | 75            | yes               |
| `internal/risk`           | 36.1%  | 73.4%          | 71            | yes               |
| `internal/risk/aggrule`   | 19.1%  | 74.6%          | 72            | yes (bonus)       |
| `internal/eval`           | 31.4%  | 67.2%          | 65            | NO (2.8pp short)  |
| `internal/board`          | 23.7%  | 33.1%          | 31            | NO (36.9pp short) |

The two non-clearing packages:

- **`internal/eval`** — 67.2% merged is 2.8pp short of the 70% bar. The
  remaining uncovered surface is `consumer.go` (NATS subscriber), which
  requires NATS infrastructure to test. Per the slice spec's anti-pattern
  list, mocking out integration tests is forbidden. The pure-Go helpers
  (`IsNotFound`, `IsBadScopePredicate`, `FreshnessMaxAge`,
  `NewScheduler`, `NewEngineFactory`) ARE now covered by
  `internal/eval/helpers_test.go`. Floor raised to 65 — a real ratchet
  from 14. Further lift goes to spillover slice 281.

- **`internal/board`** — 33.1% merged is 36.9pp short of 70%. The
  uncovered surface is dominated by `pack_store.go` (DB CRUD, 563
  lines), `store.go` (DB CRUD, 289 lines), and `pack_pdf.go`'s chromedp
  render path. All of those need integration tests, which the slice's
  scope doesn't include (board has zero integration_test.go files
  today). The pure HTML builder + `Section.EffectiveText` +
  `StoredPack.IsPublished` + `writeOptIntRow` + `allSectionsApproved`
  - `buildPackHTML` + each section's data renderer ARE now covered by
    `internal/board/pack_pdf_html_test.go`. Floor raised to 31 — a real
    ratchet from 20. Further lift goes to spillover slice 282 (which
    files the board integration_test scaffold as a separate slice
    rather than bundling it here).

This is consistent with slice 069's ratchet contract: lift floors only
to where the tests actually reach. Honoring the contract honestly is
the constitutional invariant — claiming a 70% lift where the tests
don't reach 70% would be the worse outcome.

**Confidence:** medium-high. The pivot from "5 packages at 70%" to "5
packages substantively lifted, 3 cleared 70%, 2 floor-bumped to measured
minus 2pp + spillover" is a faithful read of the ratchet contract. A
maintainer revisiting this slice should ratify the partial lifts or, if
the v1 success bar truly requires board at 70%, fund the board
integration_test scaffold as the next slice.

### D2 — Integration-merge tool: `gocovmerge`

**Options considered:**

- **(a) `gocovmerge`** (github.com/wadey/gocovmerge) — the standard
  Go-ecosystem tool. Pinned 2016 release but functionally complete.
- **(b) `gocov` + JSON intermediate** — heavier, dual-format pipeline.
- **(c) Hand-rolled in-process merge inside coverage-gate** — no extra
  binary, but reimplements semantics.

**Chosen: (a) for the CI workflow, (c) ALSO for the coverage-gate
in-process path.** Rationale:

1. CI uses `gocovmerge` because it's the upstream-blessed tool; pinned
   at `b5bfa59ec0adc420475f97f89b58045c721d761c` (the canonical SHA per
   github.com/wadey/gocovmerge) so the merge is reproducible.
2. coverage-gate ALSO grew an in-process merge (`-extra-profile=` flag)
   so a local invocation does not need the separate binary. The
   in-process implementation mirrors gocovmerge's `set` mode semantics
   (union the line specs; a line is covered if ANY profile hit it). The
   in-process path is exercised by every `go run
./cmd/scripts/coverage-gate -profile=unit.cov
-extra-profile=integration.cov` invocation; the CI path uses
   `gocovmerge` + the gate against the single merged file. Both paths
   produce identical exit codes for the same input pair.
3. Pinning gocovmerge at a SHA avoids supply-chain risk from a future
   tag bump.

**Confidence:** high.

### D3 — `internal/frameworkscope` integration tests fix (pre-existing bug)

When extending the CI integration test list to include
`./internal/frameworkscope/...`, two tests failed:
`TestHTTP_EffectiveScope` and `TestHTTP_EffectiveScope_NoActivatedFrameworkScope`.

**Root cause:** migration `20260511000009_control_bundle.sql` added
`bundle_id` as NOT NULL on the `controls` table. The
`seedScopeAndControl` test helper in `integration_test.go` predates that
migration and does not provide `bundle_id` in its INSERT. The tests
have been broken since `bundle_id` shipped — they would have surfaced
on the first CI run that included this package's integration tests, but
the package was never enrolled in the integration job (per slice 279
audit, this is a CI gap).

**Fix:** added `bundle_id` = `legacy_<uuid>` to the INSERT, matching the
backfill pattern the migration itself uses for pre-existing rows. The
helper now provides a deterministic bundle_id per test. This is a
load-bearing fix for the slice 279 outcome — without it, adding
`./internal/frameworkscope/...` to CI would break the integration job.

**Confidence:** high. The fix is mechanical and matches the migration's
own backfill semantics.

### D4 — CI runtime impact of the merged-profile run

The new merged-profile gate adds three steps to the
`tests-integration` job: download the unit-coverage artifact (~2s on
GitHub-hosted runners), install gocovmerge (~5s, cached), and run the
merge + gate (~3s). Total: ~10s on top of the existing ~5min
integration test run. Acceptable per slice 069's CI-budget.

Tested locally: the merge of a 36k-line unit profile + a 105k-line
integration profile completes in <1s on an M1.

**Confidence:** high.

### D5 — Spillover slice slot allocation

Spillover slices 281-302 are allocated for the long-tail `unit-add`
packages from the audit. The audit doc names each package + its
spillover slot. Slot 281 is `internal/eval` (the lift target that
didn't clear 70% in this slice). Slot 282 is the board
integration_test scaffold. The remaining spillovers (283-302) cover
the 20 audit-flagged `unit-add` and `exempt-leaning` packages.

The spillover slices have status `not-ready` because they depend on
slice 279 itself merging first (the CI infrastructure for merged-mode
coverage lives in this slice). After 279 merges, the orchestrator can
flip them to `ready`.

**Confidence:** high.

### D6 — Tiered-floor block as DOCUMENTATION not ENFORCEMENT

The new `$tier_recommendations` block in
`cmd/scripts/coverage-thresholds.json` is GUIDANCE only — the gate at
`cmd/scripts/coverage-gate` does not read it. The `thresholds` map
remains the only enforced surface. P0-279-6 codifies this; the block
shapes future per-package floor decisions but never blocks a merge.

**Confidence:** high.

## Revisit once in use

The list below is the iteration backlog the maintainer should review
once the coverage gate has been operating against the merged profile
for a few weeks:

1. **`internal/eval` final 2.8pp gap.** The remaining uncovered surface
   in eval is the NATS consumer; cleanly testing it needs an in-test
   NATS embed (slice 015's pattern) or a refactor that splits the
   consumer into a pure-logic decode step + a thin NATS-bound IO
   layer. Spillover slice 281 owns this.

2. **`internal/board` integration_test scaffold.** Board is the biggest
   gap (33.1% merged vs 70% target). Spillover slice 282 files the
   integration_test.go scaffold; once it lands, board's merged % will
   move materially (analogous to how slice 279 moved frameworkscope
   from 21.8% → 77.8% by enrolling its integration tests).

3. **Audit doc accuracy on connector cmd packages.** The audit lists
   `connectors/aws/cmd/aws-connector` etc. as `exempt-leaning`. If a
   future slice adds non-trivial cobra-bound logic to one of these,
   the audit flagging should flip from "exempt" to "unit-add" and the
   floor should ratchet. Re-audit when the next connector lands.

4. **Tiered guidance: API handlers tier currently at 70% target.**
   Several API-handler packages (`internal/api/controls`,
   `internal/api/controldetail`, `internal/api/oscalexport`) sit at
   ~25-40% merged. The tier target is 70% — these are spillover
   slices' targets. Re-evaluate the 70% bar for API-handler packages
   once a handful of spillovers land; if they show 70% is too
   aggressive for HTTP middleware-heavy code, soften the tier target
   to 60% in a follow-up.

5. **Mode mismatch corner.** The coverage-gate accepts profiles where
   primary and extra disagree on covermode (taking the primary's mode);
   the in-process merge then proceeds. In practice this happens when a
   developer runs `go test -cover ./...` (default `set`) against the
   integration profile (`atomic` in CI). Consider rejecting the
   mismatch loudly in a future slice — currently the gate just warns
   to stderr.

6. **gocovmerge SHA pin.** Slice 279 pins
   `wadey/gocovmerge@b5bfa59ec0adc420475f97f89b58045c721d761c`. If the
   action repo rotates or the SHA becomes unreachable, switch the CI
   step to the in-process `-extra-profile=` invocation (no external
   download required). This is a maintainer-facing fallback, not a
   functional change.

## Confidence summary

| Decision | Confidence  |
| -------- | ----------- |
| D1       | high        |
| D1a      | medium-high |
| D2       | high        |
| D3       | high        |
| D4       | high        |
| D5       | high        |
| D6       | high        |

Top of the revisit list: D1a (the partial board + eval lift). The
maintainer should ratify or fund the followup integration_test scaffold
for board.
