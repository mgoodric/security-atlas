# Slice 477 — walkthrough PDF render degradation: decisions log

**Type:** JUDGMENT · **Parent:** slice 475 · **Cluster:** Quality/Backend

Slice 477 fans the slice-475 PDF-render degradation fix out to the THIRD
headless-chrome renderer (walkthrough export) that slice 475 deliberately left
untouched. The hard design work (the shared `internal/pdfrender.Limiter`, the
error taxonomy, the 503-mapping pattern) was done by 475; this slice is route +
map + test. All calls below are minor — reuse-as-is — per the spawn brief.

## Decisions

### D1 — Reuse the shared `pdfrender.Default()` limiter as-is (no fork)

`walkthrough.RenderPDF` now wraps the chromedp work in
`pdfrender.Default().Do(ctx, fn)`, identical to `board.RenderPDF` /
`questionnaire.RenderPDF`. The chromedp body was extracted verbatim into an
unexported `renderWalkthroughBytes(ctx, htmlDoc)`. Routing through the SAME
process-wide limiter keeps the concurrency cap a true TOTAL across all three
chrome renderers (slice 475 decision D1) — a walkthrough burst can no longer
spawn chrome outside the cap. No second limiter, no new tunables, no new
dependency.

- **Confidence:** High. Byte-for-byte the board pattern; the limiter is already
  on main and exercised by the board/questionnaire integration suites.

### D2 — Drop the handler's `context.WithTimeout(r.Context(), PDFTimeout)` wrapper

The Export handler previously imposed its own 45s deadline before calling
`RenderPDF`. The limiter now owns the bounded render deadline
(`pdfrender.Default().RenderTimeout()`, default 90s, env-tunable), so a second
handler-level deadline would (a) double-bound the render and (b) re-introduce a
raw `context.DeadlineExceeded` that the limiter's classification is designed to
absorb. The handler now passes `r.Context()` straight through, mirroring
`internal/api/board/handlers.go`. `walkthrough.PDFTimeout` is kept as a
deprecated alias for `pdfrender.DefaultRenderTimeout` so any external reference
still compiles (mirrors `board.PDFTimeout`).

- **Confidence:** High. Same alias treatment board uses; no caller reads it on
  the hot path.

### D3 — 503-mapping helper mirrors board/questionnaires one-for-one

Added `internal/api/walkthroughs/pdf_degradation.go` with `pdfDegradation` /
`logPDFDegradation` / `pdfDegradationReason`, mapping all three modes
(`walkthrough.ErrChromeUnavailable` / `pdfrender.ErrRenderDeadline` /
`pdfrender.ErrQueueSaturated`) to 503 with a mode-distinguishing WARN. A genuine
bug still returns `ok=false` and falls through to `httperr.WriteInternal` (500).
Messages match the questionnaire wording style ("retry shortly").

- **Confidence:** High. Structural copy of the two sibling helpers.

### D4 — HTTP-level integration test in `internal/api/walkthroughs` (new test package)

The deadline→503 proof is a handler-level assertion, so the test lives where the
mapping lives. The `internal/api/walkthroughs` package had no test file before,
so this slice adds the board-style harness (`testServer` over the assembled
`api.New` router, `freshTenant`, a control + walkthrough seed via the real
RLS-enforced store) plus four tests mirroring slice 475:
`TestPDF_ReturnsPDFOrServiceUnavailable` (baseline 200-or-503),
`TestPDF_RenderDeadlineDegradesTo503` (1ns deadline → 503, the load-bearing
proof), `TestPDF_QueueSaturationDegradesTo503` (1-slot fail-fast → 503), and
`TestPDF_StressNoNonGraceful` (12× under a tight cap, every response graceful).

- **Confidence:** High. All four pass locally against a freshly-migrated
  Postgres (see Proof).

### D5 — Enrol the new tagged package in the integration shard manifest

Adding a `//go:build integration` file to `internal/api/walkthroughs` makes the
slice-345 enrolment guard and the slice-417 shard-coverage guard require the
package be listed. The source of truth for the package list is
`scripts/integration-shards.txt` (ci.yml reads it via `run-integration-shard.sh`
and carries no inline list), so the in-bounds fix is one line in the manifest:
`B2  ./internal/api/walkthroughs/...` (B2 = audit-workflow family, alongside
`internal/audit/walkthrough` and `internal/api/board`). **ci.yml itself is
untouched** (sibling 473 owns it); the manifest edit is the correct enrolment
surface. Both guards re-run green (90 tagged == 90 enrolled; union == tagged;
disjoint).

- **Confidence:** High. Both guard scripts pass locally (see Proof).

## Detection-tier classification

- `detection_tier_actual`: `none` — no NEW bug surfaced during the slice; this
  is the proactive fan-out of a defect slice 475 already diagnosed and fixed in
  two sibling packages. The walkthrough variant was identified by inspection,
  not by a failing test/incident (the endpoint is exercised less under CI load,
  per the slice spec, so it had not flaked).
- `detection_tier_target`: `integration` — had the latent 500/hang ever
  manifested, the new `TestPDF_RenderDeadlineDegradesTo503` is the tier that now
  catches it. The proof test fails (500, not 503) against the pre-slice handler.

## Revisit

- **Coverage floor (deferred — do NOT lift this batch).** `internal/api/walkthroughs`
  gains its first test file in this slice. Sibling 495 owns
  `coverage-thresholds.json` this batch, so this slice does NOT add/lift a
  walkthrough package floor. The tests are additive (no threshold row touched).
  If the project later wants a Go-unit floor for the walkthrough HTTP handlers
  (the pure-Go `pdfDegradation` switch is a natural candidate for a fast
  `helpers_test.go`, per the slice-353 pure-Go-pre-DB convention), that is a
  tiny follow-up slice — filed as a spillover, not done here.
- The deadline default (90s) and concurrency cap (2) are the shared limiter's
  tunables; any change is a `pdfrender` decision, not a walkthrough one.

## Constitutional check

No conflict. This is render-degradation hardening of an existing read endpoint:
no change to the AI-assist boundary, RLS/tenancy, evidence ledger, OSCAL wire
format, or walkthrough PDF content/templates. The chromedp dependency and the
slice 340/341 `WSURLReadTimeout` watchdog fix are untouched.
