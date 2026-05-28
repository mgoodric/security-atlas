# Slice 341 — chromedp `WSURLReadTimeout(60s)` fan-out — decisions log

**Parent slice:** 340 (`docs/audit-log/340-chromedp-flake-decisions.md`)
**Branch:** `quality/341-chromedp-wsurlreadtimeout-fanout`
**Type:** JUDGMENT (per `Plans/prompts/04-per-slice-template.md` slice types)
**Date:** 2026-05-27

This slice is a mechanical fan-out of slice 340's chromedp watchdog fix
to the four other PDF renderers that share the same chromedp
ExecAllocator pattern. It records only the build-time judgment calls;
the diagnostic record lives in slice 340's log.

---

## D1 — Copy the constant vs extract a shared helper

**Decision:** Copy the constant per package; do NOT extract a shared
helper, file, or package.

**Mechanics:** `const chromedpWSURLReadTimeout = 60 * time.Second` is
declared in:

1. `internal/policy/pdf/render.go` (slice 340 — pre-existing on main)
2. `internal/board/pdf.go` (this slice — `package board`)
3. `internal/questionnaire/pdf.go` (this slice — `package questionnaire`)
4. `internal/audit/walkthrough/export.go` (this slice — `package walkthrough`)

`internal/board/pack_pdf.go` shares `package board` with `pdf.go`, so it
references the in-package declaration from `pdf.go` rather than
re-declaring (a per-file re-declaration would be a Go compile error).
The slice doc's "copy in 4 places" framing was per-file; the
package-level invariant collapses two of those into a single
declaration. A comment in `pack_pdf.go` near the import block names
where the constant lives so a future reader doesn't grep-fail.

**Net declarations after this slice:** 4 packages × 1 declaration each
= 4 declarations (the same arithmetic as the slice doc's "5 renderers
total" framing, just expressed at package granularity).

**Rationale:**

- P0-341-3 of the slice doc explicitly forbids a shared helper package
  for this slice. The reasoning chain is:
  - The constant is a single primitive value (`60 * time.Second`).
  - The surrounding context — which chromedp flags belong, what the
    outer-context timeout should be — varies per package.
  - A shared `pkg/chromedphelpers` package would couple four otherwise
    unrelated subsystems (`policy`, `board`, `questionnaire`,
    `walkthrough`) for the benefit of deduplicating four lines of code.
  - The Go-idiomatic move when the unit of sharing is a single value
    with explanatory documentation is "duplicate the value with the
    explanation". Premature abstraction is more debt than the
    duplication it eliminates.
- The doc comment on each declaration is identical and cites slice
  340, slice 341, and the chromedp version, so a future reader at any
  of the four declarations gets the same context.

**Extraction trigger (deliberate):** If a sixth chromedp-based renderer
lands in this codebase, extract a `pkg/chromedphelpers` package
(or similar) at that point. The threshold is "the cost of the next copy
exceeds the cost of the abstraction", and at four copies we are
comfortably below that threshold. Slice 341 itself is the precedent: a
mechanical fan-out at five sites cost ~20 minutes of wall-clock time,
which is a strong signal the current factoring is honest.

---

## D2 — No test changes (no `t.Skip` to remove)

**Decision:** Ship without touching any test file in the four packages.

**Verification of premise:** `git grep -l 't.Skip.*chromedp' internal/board/
internal/questionnaire/ internal/audit/walkthrough/` from the worktree
root returns empty. None of the four packages has a currently-skipped
chromedp integration test, which means:

- AC-2 (no regression in any of the four packages' existing
  integration tests) is the test bar.
- This slice is purely **preventive**. Slice 340 was the canary that
  flaked first; the other four haven't flaked **yet** because their
  cumulative-load ordering on the CI runner has so far landed them
  under the 20.0s wsURLReadTimeout boundary by 40-50ms of luck. The
  same Harden-Runner audit-mode latency exposure applies to all of
  them.
- If a future cumulative-load shift moves any of these four past the
  20.0s boundary before this slice merges, that test would fail like
  slice 340's `TestRender_ProducesRealPDF` did. The fix in this slice
  is intentionally the same shape so the diagnostic precedent (slice
  340's log) is the diagnostic precedent for whichever sibling flakes
  next.

**Net:** zero test files modified by this slice.

---

## D3 — `DefaultTimeout` / `PDFTimeout` bumps deferred

**Decision:** Do NOT raise the per-package `PDFTimeout` constant in any
of the four packages in this slice. Slice 340 raised
`internal/policy/pdf.DefaultTimeout` from 30s to 90s for its own
package because the chromedp render that flaked there had outer-context
headroom pressure. The four target files in this slice do not have
evidence of that pressure today:

| File                                   | Existing constant    | Current value | Decision |
| -------------------------------------- | -------------------- | ------------- | -------- |
| `internal/board/pdf.go`                | `PDFTimeout`         | 30s           | Keep     |
| `internal/board/pack_pdf.go`           | (none at file scope) | n/a           | n/a      |
| `internal/questionnaire/pdf.go`        | `PDFTimeout`         | 30s           | Keep     |
| `internal/audit/walkthrough/export.go` | `PDFTimeout`         | 45s           | Keep     |

**Rationale:**

- The slice doc explicitly tags the `DefaultTimeout` bump as
  **optional** ("Optionally raise package-level DefaultTimeout /
  render-budget constants").
- The load-bearing fix in slice 340 is the **inner**
  `WSURLReadTimeout(60s)`, which is the chromedp-internal watchdog
  that fires regardless of the outer context's deadline. Raising the
  outer timeout without raising the inner watchdog does nothing; the
  reverse is the actual fix.
- The four current values (30s, 30s, 45s) all sit comfortably above
  the 22-23s tail-of-distribution wall-clock observed in slice 340's
  diagnostic. The outer-context envelope is not tight in any of
  these packages today.
- Bumping the constant in four packages preemptively is churn for no
  current benefit. If a sibling renderer starts failing the outer
  context deadline after this fix lands, a focused follow-on slice
  can lift the relevant `PDFTimeout` with a one-line change and a
  citation to whichever failed run surfaces the need.

**Reversal trigger (deliberate):** If any of the four packages produces
a CI failure where the inner `WSURLReadTimeout(60s)` succeeds but the
test's outer-context deadline (`ctx, _ := context.WithTimeout(..., 30 *
time.Second)`) fires, file a follow-on slice to bump that package's
`PDFTimeout` to 90s. The trigger is "outer-context deadline observed in
a real failure", not "speculative parity with slice 340".

---

## Verification

- `go build ./internal/board/... ./internal/questionnaire/...
./internal/audit/walkthrough/...` — clean.
- `git grep -n "chromedp.WSURLReadTimeout" internal/` — produces 5
  hits (1 in `internal/policy/pdf/render.go`, 4 in this slice's
  targets), matching the expected fan-out shape.
- `git grep -l 't.Skip.*chromedp' internal/` — empty (no test file
  touched, no regression surface).

## Follow-ups

- None expected. If a follow-on slice surfaces (e.g., the `PDFTimeout`
  reversal trigger fires, or a sixth chromedp renderer arrives and
  triggers the D1 extraction threshold), it will be filed under
  Amendment 2 spillover protocol.
