# Slice 347 — vitest coverage ratchet — decisions log

**Slice type:** `AFK` (the ACs are mechanically verifiable: ratchet is wired, fails red on regression, monotonic ↑, tests + lift land in the same PR going forward). This log is not a JUDGMENT-slice sign-off gate — it records the canvas-interpretation judgment calls the slice surfaced so future iteration is tractable. None of these blocked merge.

## Background

Slice 334's framework audit finding V-1 (HIGH) named the TS-side coverage-ratchet gap. The Go side enforces a monotonic per-package floor via `cmd/scripts/coverage-gate` + `cmd/scripts/coverage-thresholds.json` (slice 069 + the 17-slice retroactive enrolment trail). The TS side measures coverage (vitest's `coverage-summary.json` is uploaded as a CI artifact) but nothing consumes the measurement — there is no ratchet, no floor, no regression-fail.

Slice 069's deferred follow-up "raise the bar" promise had been open for ~250 slices. This slice closes both threads.

## Decisions made

### D1 — Option 1 (per-file thresholds in `vitest.config.ts`) selected over option 2 (custom gate script) and option 3 (autoUpdate)

The slice doc presented three implementation paths:

1. Per-file thresholds in `vitest.config.ts` — hand-curated map; vitest's built-in `coverage.thresholds` enforces.
2. Standalone `cmd/scripts/coverage-gate-ts/` — Go-shape mirror; gate validates the `coverage-summary.json` artifact post-vitest-run.
3. vitest `coverage.thresholds.autoUpdate` — vitest writes the floor back into the config on green.

Chose **option 1**.

**For:**

- Matches the Go-side **mental model** (one floor per file/package, hand-curated, lifted in the same PR as the tests that hit the new bar). The Go side's `coverage-thresholds.json` is the canonical shape; option 1 mirrors that shape exactly. A reader who has seen the Go ratchet knows how to read the TS ratchet.
- **No new infra.** vitest already collects coverage and supports `coverage.thresholds` natively. The existing `Frontend · vitest` CI job already runs `npm run test:coverage`; with the thresholds populated, the job fails red on regression — no workflow change needed.
- **Per-file granularity** is the natural unit for vitest. Go's per-package granularity is the natural unit for `go test ./...` (the test runner reports per-package). vitest's `coverage-summary.json` reports per-file. Per-file is also more honest — a directory-level floor masks per-file regressions inside the directory (a 100% file dropping to 80% can be hidden by a 60%→80% sibling).

**Against (rejected):**

- Option 2 adds a Go binary (or shell script) that reads the JSON and re-implements `vitest`'s threshold-check logic. That's duplicate code for no benefit — the built-in already handles the edge cases (relative-path resolution, micromatch globbing, per-metric threshold maps). The Go-side gate exists because the Go test runner does NOT have a built-in threshold-check that fails CI; vitest does.
- Option 3 (`autoUpdate`) writes the new measured value back into the config on every green run. That defeats the **monotonic ratchet contract** — the floor would silently track the measurement, which is the opposite of a ratchet (a ratchet enforces a one-way movement; auto-update is a two-way tracker). It would also surprise reviewers by editing committed files mid-build. Deferred to a follow-up slice as a possible enhancement (D3 below).

### D2 — Floor methodology: `max(0, floor(measured - 2pp))` matching slice 069

Each floor in `web/coverage-thresholds.json` is `max(0, floor(measured - 2pp))`. Identical to the Go-side rule (`cmd/scripts/coverage-thresholds.json` `$methodology` key).

**Rationale.** The 2-pp band absorbs measurement noise from minor refactors that re-shuffle which branches the test actually executes. Without the band, an innocuous refactor (e.g., changing an `if/else` to a ternary) could swap two equivalent paths and drop the measured count by one branch out of N — enough to dip 1 pp below the floor in a small file.

The `floor(...)` (not `round(...)` or `ceil(...)`) means we always under-state, never over-state, the floor. `Math.floor(99.9) = 99` vs `Math.round(99.9) = 100` — under-stating is the safer monotonic-ratchet choice (a future measurement of 99 would still pass).

`max(0, ...)` covers the edge where `measured < 2` (a file with 1% measured coverage; floor would be -1 → 0). Slice 347 doesn't hit this edge — no file in the baseline has 1% — but the guard is cheap and matches slice 069.

### D3 — `autoUpdate` consideration deferred

`coverage.thresholds.autoUpdate` (option 3 from the slice doc) is deferred to a future enhancement slice. Slice 347 ships with `autoUpdate` NOT set (default: false).

**Why defer, not reject.** `autoUpdate` would lower toil — when a contributor's PR raises measured coverage, vitest could automatically lift the floor without a hand-edit. But:

- The first round of ratchet hygiene needs to be **hand-curated and reviewable**. A reviewer reading slice 347 needs to be able to look at `web/coverage-thresholds.json` and see exactly which numbers were seeded. `autoUpdate` introduces noise in that diff — the first auto-update PR would mix the slice's seed numbers with subsequent measurement bumps.
- `autoUpdate` writes the config file on every green build. That's a write into a committed file from a test runner, which complicates the CI mental model. A contributor would need to remember to commit the updated config after running tests locally.
- The slice 069 Go-side ratchet does NOT auto-update. Symmetry argues for the TS-side ratchet to also stay hand-curated until both sides decide together that auto-update is the right move.

The deferral is documented; a follow-up slice can revisit once the first round of ratchet hygiene proves stable.

### D4 — Why not break out per-package (like Go) — per-file is the natural unit for vitest

The Go-side ratchet is per-Go-package because `go test ./...` reports per-package; that's the granularity the test runner emits. The TS-side runner (vitest) emits **per-file** coverage via `coverage-summary.json`. We follow the runner's natural unit.

Per-directory (e.g., `lib/**`, `app/api/**`) would aggregate across all files in the directory — a single high-coverage outlier could mask a sibling regression. The Go-side ratchet doesn't have this problem because Go packages already correspond to a single directory of files all measured together; for TS, a directory holds many independently-covered files, so per-directory is strictly less precise than per-file.

The slice doc's recommendation hinted at per-directory ("Per-directory matches the Go-side mental model"), but on inspection the cost was clear: 107 per-file rows is manageable in JSON, 13 KB total, diff-friendly, and lifts are local (one file at a time). Per-directory would have been 6-10 rows, easier to read, but would have hidden per-file regressions.

### D5 — 0% files intentionally omitted; ratchet starts at truth

71 files in the coverage `include` set (out of 178 total) measured 0/0/0/0 at the slice 347 baseline — no tests reach them yet. They are intentionally **omitted** from `web/coverage-thresholds.json`.

**Rationale.** Including them with `floor=0` would be a no-op (any coverage measurement passes ≥ 0). Including them with `floor>0` would violate P0-347-3 (do not seed floors above the measured value). The honest representation is omission; the rule is that when a future slice adds the first test for an omitted file, that slice also adds the file to `coverage-thresholds.json` with floor = `floor(measured - 2pp)`.

This is documented in the JSON header (`$omitted_zero_pct_count`, `$omitted_zero_pct_rationale`) so a future maintainer reading the file sees the omission is intentional and procedural, not an oversight.

### D6 — JSON sidecar (`web/coverage-thresholds.json`) over inline TS map

The threshold data could have lived inline inside `web/vitest.config.ts` as a TypeScript object literal. Chose a sibling JSON file for three reasons:

- **Mirrors the Go-side shape.** `cmd/scripts/coverage-thresholds.json` is the canonical Go-side file; the TS counterpart is `web/coverage-thresholds.json`. Same shape, same metadata keys (`$comment`, `$methodology`, `$how_to_raise`, `$how_to_extend`). A reader who has seen the Go file knows immediately how the TS file works.
- **Numerical diff hygiene.** Ratchet lifts touch one number at a time. A JSON diff is cleaner than a TS-object diff (no trailing commas, no quote inconsistencies, no type-cast noise). Reviewers see exactly what number moved.
- **Tool-friendliness.** Future scripts (jq queries, regen tools, audit reports) can read the JSON directly without parsing TS. The Go side benefits from this same property today.

The vitest config reads the JSON via `JSON.parse(readFileSync(...))` at config-load time. The cost is negligible (config loads once per test run).

### D7 — Tests that exist but weren't yet floor-enforced get enforced going forward

The 107 entries in `coverage-thresholds.json` cover every file that has measurable non-zero coverage today. They are now ratchet-enforced — a future PR that drops any of them below the seeded floor fails CI red.

This includes files where the seeded floor is high (e.g., `app/(authed)/audits/filters.ts` at 98/98/98/98). The cost is that any refactor of these files must keep coverage. The benefit is that we lock in the discipline that ALREADY EXISTS — the tests are there, the coverage is there, and now a regression is observable.

## Anti-criteria compliance check

- **P0-347-1** (no floor lowered): no floors lowered. The threshold map is greenfield (slice 347 is the first to seed values); P0-347-1 becomes load-bearing for FUTURE slices.
- **P0-347-2** (no production code modified): zero production code touched. Changes are: `web/vitest.config.ts` (test config), `web/coverage-thresholds.json` (new file), `web/testing.md` (docs), `docs/audit-log/347-vitest-coverage-ratchet-decisions.md` (this log).
- **P0-347-3** (no floor seeded above measured): all floors derived from measured values via `max(0, floor(measured - 2pp))`. 0% files omitted (not seeded > 0).
- **P0-347-4** (not bundled with 345/346): scope is the vitest ratchet alone.
- **P0-347-5** (CLAUDE.md and canvas untouched): zero edits to either. `CLAUDE.md`'s "Testing discipline (four enforced surfaces)" block already documents that "no coverage gate yet" for vitest; that line will be updated in a future docs-only slice once this ratchet has settled.

## Spillover

None. Future-direction items already named in this log: D3 (`autoUpdate` enhancement) — file when first round of hygiene proves stable.
