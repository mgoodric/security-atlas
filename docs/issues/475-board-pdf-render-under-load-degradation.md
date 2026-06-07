# 475 — Board/questionnaire PDF render must degrade to 503 (not 500/hang) under load

**Cluster:** Quality / Backend
**Estimate:** M (1–2d)
**Type:** JUDGMENT (degradation policy + deadline shape)

**Status:** `ready`

> Filed 2026-06-06 after the chromedp PDF integration tests
> (`TestPDF_ReturnsPDFOrServiceUnavailable` in `internal/api/board`,
> `TestExportPDF_SmokeTest` in `internal/api/questionnaires`) recurred as a
> flake across batches 193/195/196 and then **failed two consecutive reruns**
> blocking batch 197 (PRs #1026/#1027) under sustained concurrent CI load.
> Not a regression in those PRs (neither touches the PDF packages).

## Narrative

**Why.** The PDF endpoints render via headless chromium (chromedp). The board
test `TestPDF_ReturnsPDFOrServiceUnavailable` is written to accept EITHER a
200 (`%PDF-` bytes) OR a 503 (graceful degradation when chrome is
unavailable) — and fail on anything else. It has been failing at ~30.1s. That
means under heavy CI runner load the render neither completes (200) within the
deadline NOR degrades cleanly to 503 — the handler returns a non-graceful
result (500 / client-deadline) and the test's `default` branch fails. So the
real defect is in the **handler's degradation behavior under a slow/contended
chrome**, surfaced as a test flake that has now crossed into a hard blocker
when CI is saturated.

**What.** Make the PDF render path degrade DETERMINISTICALLY: when the
chromedp render exceeds a bounded render deadline (or chrome is slow/contended,
not just absent), the handler returns **503 Service Unavailable** (the
already-documented graceful path) rather than a 500 or a hung request. Pair
with a render deadline tuned for CI-contended headless chrome (the current
effective ~30s ceiling is too tight under parallel-shard load), and make the
two PDF integration tests assert the 200-or-503 contract against that
deterministic behavior so they stop flaking.

**Scope discipline.** The board + questionnaire PDF render handlers and their
two integration tests. Does NOT change the PDF _content_/template, does NOT
remove the chromedp dependency, does NOT touch the slice-340/341 chromedp
WSURLReadTimeout work (that fixed connection setup; this is render-time
degradation). Does NOT relax the tests to "accept any status."

## Threat model

STRIDE — verdict **has-mitigations** (availability-focused).

- **D — Denial of service / availability (core).** A slow/contended chrome
  must not hang the request or 500 — it degrades to 503 within a bounded
  deadline. Cap the render time + concurrent render count so a burst of PDF
  requests can't exhaust headless-chrome resources. _Mitigation = the slice._
- **S/T/R/I/E.** N/A — no auth/identity/tenant-data surface change; the render
  reads the already-authorized board brief for the caller's tenant (RLS
  unchanged). Audit/logging of render failures should stay (R).

## Acceptance criteria

- [ ] **AC-1.** When the headless-chrome render exceeds the (configurable)
      render deadline OR chrome is unavailable/contended, the PDF endpoint
      returns **503**, never a 500 or a request that hangs past the deadline.
- [ ] **AC-2.** The render deadline is bounded + tuned so a normal render
      completes well within it on a loaded CI runner (raise the effective
      ceiling above the ~30s that currently flakes; make it configurable).
- [ ] **AC-3.** A cap on concurrent renders (or a render queue with a bounded
      wait) prevents PDF-request bursts from exhausting chrome.
- [ ] **AC-4.** `TestPDF_ReturnsPDFOrServiceUnavailable` and
      `TestExportPDF_SmokeTest` assert the 200-or-503 contract deterministically
      and no longer flake under load (run them N× under simulated contention as
      part of the slice's proof, the slice-340 stress pattern).
- [ ] **AC-5.** A render-failure/degradation is logged (WARN) with enough
      context to distinguish "chrome absent" from "render deadline exceeded".
- [ ] **AC-6.** No change to PDF content/templates; existing 200-path assertion
      (`%PDF-` magic + `application/pdf`) still holds when chrome is healthy.

## Constitutional invariants honored

- **Fail-soft availability** — a degraded dependency (headless chrome) yields a
  clean 503, not a 500/hang (operator-honest degradation).
- **#6 RLS** — render reads the caller's tenant brief only; unchanged.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting (the PDF surface).

## Dependencies

- Builds on slice 340/341 (chromedp WSURLReadTimeout — connection setup). This
  slice is render-time degradation, complementary. No unmerged dep → `ready`.

## Anti-criteria (P0 — block merge)

- **P0-475-1.** Does NOT relax the tests to accept arbitrary statuses — the
  200-or-503 contract is the point.
- **P0-475-2.** Does NOT return 500 or hang on a slow/contended chrome.
- **P0-475-3.** Does NOT change PDF content/templates or remove chromedp.
- **P0-475-4.** Does NOT widen the render's auth/tenant surface.

## Notes for the implementing agent

- The flake signature: `--- FAIL: TestPDF_ReturnsPDFOrServiceUnavailable
(30.10s)` — exactly ~30s, i.e. a deadline is being hit and the handler is
  NOT returning the 503 the test would accept. Find where the render's
  effective ~30s ceiling lives (handler render ctx, chromedp exec allocator
  timeout, or the test HTTP client) and make the deadline-exceeded path return 503. `internal/api/board/integration_test.go:479` is the board test;
  `internal/api/questionnaires` has the export-PDF twin.
- This flake has cost reruns across 4 batches and blocked batch 197 — it is the
  highest-value test-infra fix in the current backlog.
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a chore/status branch, not this `docs/475` branch.
