# 320 — Coverage lift — `internal/demoseed` to 70%+

**Cluster:** Quality
**Estimate:** 2-3d (522 statements; data-heavy package)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` measured `internal/demoseed` at **4.4% merged coverage**, below the 70% aspirational target.

`internal/demoseed` is the demo seed dataset (slice 205) — pre-populated controls, risks, evidence, framework scopes for the self-host demo bundle. 522 statements; data-heavy (lots of struct literals + insert helpers).

**Disposition:** `unit-add` (likely lower-priority than the other spillovers)

**Notes:** Data-heavy packages are awkward to test — testing struct literals doesn't add value. The right shape is likely:

- Unit tests for the seed-runner orchestrator (Seed() / SeedTenant() entry points)
- Integration test for "fresh DB → demo seed → expected row counts per table"
- Treat the literal data as data (verify it parses correctly via a single unit test; don't test each literal)

Lower priority because demo-mode isn't on the production path; degradation in coverage doesn't hurt customer-facing reliability. But the audit doc still wants 70% — closeable with a focused test session.

## What ships in this slice

1. **Decide the test strategy** — orchestrator unit tests + integration test for end-to-end seed run; do NOT exhaustively test each struct literal.
2. **New unit tests** for the orchestrator + helpers.
3. **Integration test** for end-to-end seed (if not already present).
4. **Floor lift in `cmd/scripts/coverage-thresholds.json`** — add the new entry at `floor(merged_measured - 2pp)`.

If the package is genuinely all-data and the orchestrator surface is too thin to reach 70%, consider:

- Moving the package to `excludes` with a tier doctrine entry: "demo seed packages are data-only and exempt".
- OR refactoring the package so the data lives in a `.json` / `.sql` file loaded at boot, leaving a thin Go orchestrator that's easily testable to 70%+.

Document the call in the slice's decisions log.

## Acceptance criteria

- [ ] **AC-1.** `internal/demoseed` either (a) reaches ≥ 70% merged coverage, OR (b) is added to `excludes` with a tier-doctrine entry per the per-package judgment.
- [ ] **AC-2.** Each test exercises real branches with real assertions (no testing struct literals for the sake of testing).
- [ ] **AC-3.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` reflects the new floor OR the excludes addition.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.
- Slice 205 (demo seed dataset) — `merged`.

## Anti-criteria (P0 — block merge)

- **P0-320-1.** Does NOT raise the `internal/demoseed` floor without writing the unit tests.
- **P0-320-2.** Does NOT lower any existing floor.
- **P0-320-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-320-4.** Does NOT write vacuous tests of struct-literal values — the audit doc considers that a P0-279-7 violation by analogy.

## Notes for the implementing agent

This is the lowest-priority spillover from slice 312's round-3 audit. If the round-3 spillover queue gets long, deprioritize 320 in favor of the auth-substrate-v2 lifts (314 / 315) and admin-handler lift (313). The 4.4% baseline is honest — demo-mode coverage failing tomorrow doesn't hurt customers.

The honest answer might be "move to excludes with a documented rationale" — make that call explicitly in the slice's decisions log.
