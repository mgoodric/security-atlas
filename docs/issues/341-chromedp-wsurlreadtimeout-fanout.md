# 341 — Apply chromedp WSURLReadTimeout fix to remaining four PDF renderers

**Cluster:** Quality / Infra
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** 340 (chromedp `TestRender_ProducesRealPDF` flake — spillover)

## Narrative

Slice 340 diagnosed the chromedp 20-second `wsURLReadTimeout` flake in
`internal/policy/pdf/render.go` and applied `WSURLReadTimeout(60s)` as
the fix. Four other renderers in the codebase use the same chromedp
ExecAllocator pattern with the same Chrome flag set and the same
exposure to the watchdog:

| File                                       | Slice it shipped in |
| ------------------------------------------ | ------------------- |
| `internal/board/pdf.go:72`                 | slice 031           |
| `internal/board/pack_pdf.go:58`            | slice 032           |
| `internal/questionnaire/pdf.go:86`         | slice 155           |
| `internal/audit/walkthrough/export.go:141` | slice 027           |

All four currently ship without `chromedp.WSURLReadTimeout(...)`. They
haven't flaked in CI yet because their integration tests either run
shorter cumulative load on the runner or hit a faster-cold-start case
than `internal/policy/pdf` did. The flake is structurally identical
in all five renderers — slice 340 was the canary.

## What ships in this slice

1. Add `chromedp.WSURLReadTimeout(chromedpWSURLReadTimeout)` to each
   of the four other renderers' `NewExecAllocator` options.
2. Define a shared constant (either copy from `internal/policy/pdf` or
   extract a tiny helper — see D1 below).
3. Optionally raise the relevant package-level `DefaultTimeout` /
   render-budget constants from 30s to 90s for parity with slice 340's
   policy/pdf fix.
4. No test changes required (no integration tests are currently
   skipped for any of these four — yet).

## Acceptance criteria

- [ ] **AC-1.** All four renderers include
      `chromedp.WSURLReadTimeout(60 * time.Second)` in their
      ExecAllocator options.
- [ ] **AC-2.** No regression in any of the four packages' existing
      integration tests.
- [ ] **AC-3.** Decisions log captures the trade-off between "copy the
      constant five times" vs "extract a shared helper" — D1 below
      pre-suggests copy-for-now.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md):** preventive — no test is
  currently skipped, but the same flake would skip these next if the
  CI cumulative load shifts.
- **Anti-criterion: don't dodge chromedp.** This slice doesn't change
  the renderer; only the watchdog timer.

## Dependencies

- Slice 340 (parent) must merge first so the diagnostic precedent is
  on `main` before the fan-out lands.

## Anti-criteria (P0 — block merge)

- **P0-341-1.** Does NOT change PDF output bytes for any renderer.
- **P0-341-2.** Does NOT add a new dependency.
- **P0-341-3.** Does NOT extract a shared package solely to dedupe
  the constant — that's premature abstraction; copying the
  `chromedpWSURLReadTimeout = 60 * time.Second` constant in four
  places is fine until the codebase grows a sixth renderer.

## Notes for the implementing agent

**D1 pre-suggestion — copy or extract?** The natural impulse is to
extract a `pkg/chromedphelpers` (or similar) shared helper. Don't.
The constant is a single value; the surrounding context (which
flags belong, what timeout the outer HTTP request needs) varies per
package. Copying `const chromedpWSURLReadTimeout = 60 * time.Second`
in four places with an identical doc comment is honest. If a sixth
renderer shows up, that's the time to extract.

**Fastest path:**

```bash
for f in internal/board/pdf.go internal/board/pack_pdf.go \
         internal/questionnaire/pdf.go \
         internal/audit/walkthrough/export.go; do
  # Apply the same one-line option addition + constant copy as slice 340
done
```
