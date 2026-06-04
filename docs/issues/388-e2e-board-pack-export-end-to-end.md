# 388 — Board-pack export end-to-end Playwright spec

**Cluster:** Quality / e2e
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 351, captured per continuous-batch policy.

Board-pack export is a v1 binary success-test flow (CLAUDE.md: "generate
the next board pack from it"). Slice 333 Q-9 and slice 351's coverage
matrix (flow #12, P1) both flag it as only half-covered:
`web/e2e/board-pack-detail.spec.ts` exercises the UI render of a board
pack but NOT the generate → render-PDF → download chain end-to-end.

Slice 351 was scoped to P0 spec-fill + quarantine triage (anti-criterion
P0-1 explicitly defers P1/P2 specs to follow-ups). This is that
follow-up.

## What

Author `web/e2e/board-pack-export-e2e.spec.ts` covering the export chain:
generate a board pack → render the PDF (the chromedp-backed renderer,
cf. slices 340/341) → assert the download fires with a `.pdf` filename.
Follow the established `route.fulfill` mock convention (P0-4 of slice 351) unless a real-stack assertion is required for the PDF render — if
the chromedp render path cannot be mocked meaningfully, scope the spec
to the BFF generate + download-trigger boundary and note the PDF-bytes
assertion as a separate real-stack concern.

## Scope discipline

- DOES NOT touch the board-narrative AI-assist surface (constitutional;
  out of scope).
- DOES NOT re-implement `board-pack-detail.spec.ts` — this is additive.

## Acceptance criteria

- [ ] AC-1: `web/e2e/board-pack-export-e2e.spec.ts` exists and runs in
      the merged Playwright gate.
- [ ] AC-2: covers generate → render → download with a `.pdf` filename
      assertion.
- [ ] AC-3: deterministic (auto-wait / `waitForResponse`; no sleeps).

## Dependencies

- #340, #341 — merged. The chromedp PDF-render path.
- #351 — the audit that filed this.

## Cross-references

- Slice 351 coverage matrix
  (`docs/audits/351-e2e-critical-flow-coverage-matrix.md`) — flow #12.
