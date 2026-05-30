# ADR 0009 — ui-honesty harness promotion path

**Status:** Proposed — PLAN ONLY. This ADR scopes the staged promotion of the `e2e-audit/` ui-honesty harness from informational to merge-blocking. It does NOT promote anything; the harness stays informational until a future slice executes a stage.

**Date:** 2026-05-29

**Resolves (scopes):** slice 333 finding **Q-10** (`docs/audits/333-qa-strategy-gap-analysis.md`) — the strongest e2e surface (`web/e2e-audit/`, real services, no mocks) is informational-only, while the merged gate (`frontend-playwright`) runs the hybrid-mocked suite. Folds in finding **Q-18** (the `e2e/` and `e2e-audit/` fixtures are independent and should converge).

**Implements-the-scope-through:** `docs/issues/353-qa-strategy-tactical-round-1.md` (sub-theme E). Slice 353 ships this plan; future slices execute the stages.

**Slot note:** Slots 0001–0008 occupied (0003 pre-existing double collision). Next free slot is 0009.

---

## Context

There are two Playwright surfaces:

| Surface          | CI job                | Upstream atlas API                               | Gate status            |
| ---------------- | --------------------- | ------------------------------------------------ | ---------------------- |
| `web/e2e/`       | `frontend-playwright` | **mocked** (57 `route.fulfill`)                  | **merge-blocking**     |
| `web/e2e-audit/` | `frontend-ui-honesty` | **real** (production-build stack, real services) | **informational only** |

The asymmetry is the finding: the project has a true e2e tier
(`e2e-audit/`, exercises the real BFF→atlas wire) and a hybrid-mocked tier
(`e2e/`), and the **mocked one is the gate**. The real one — which would
catch BFF↔atlas wire drift, production-build-only failures, and
multi-tenant flow regressions the mocks paper over — does not block merge.

`e2e-audit/` is not promoted today for a real reason: a real-services
production-build suite is inherently more flaky than a mocked one (bring-up
races, timing, real network). Promoting it wholesale would import that flake
into the merge gate. The path below promotes it WITHOUT importing the flake,
by promoting only the assertions that have proven stable.

## Decision (scoped — plan only)

Promote the ui-honesty harness in four stages. Each stage is a future slice;
this ADR fixes the staging so the executing slices do not re-litigate the
direction.

### Stage (a) — 30-day stability audit

Before promoting anything, measure. For 30 days, record per-assertion
pass/fail across `frontend-ui-honesty` CI runs (the harness already emits a
JSON report — `e2e-audit/reports/ui-honesty-*.json`; the
[flake-counter](../flake-budget.md) infrastructure can aggregate it). Output:
a stability ranking of each `ui-honesty.spec.ts` assertion (stable = passes
on every clean-main run for 30 days; volatile = any non-code-change failure).
This stage is pure measurement — no gate change. It reuses the slice-352
flake dashboard plumbing rather than inventing a new one.

### Stage (b) — promote stable assertions to a merged sub-gate

Split the ui-honesty suite into two projects: a `ui-honesty-stable` project
(the assertions that passed stage (a)'s 30-day bar) wired as a NEW
merge-blocking CI job, and a `ui-honesty-volatile` project (the rest) that
stays informational. The merged sub-gate starts SMALL — only the assertions
with proven stability — so promotion never raises the gate's flake rate
above the [flake budget](../flake-budget.md)'s Playwright target (<1%).

### Stage (c) — keep volatile assertions informational

The assertions that did not clear stage (a) stay on the informational
`frontend-ui-honesty` job. They are not deleted — they are signal, just not
merge-blocking signal. As each volatile assertion is hardened (its
flakiness root-caused, slice 340 pattern), a follow-up slice migrates it
from the volatile project to the stable sub-gate. The boundary moves
monotonically toward "more stable, more blocking."

### Stage (d) — migrate assertion logic from mocked `e2e/` to real `e2e-audit/`

The end state: assertions that today live in mocked `e2e/` specs migrate to
real-services `e2e-audit/` specs, over N slices, as the stable sub-gate
grows. The mocked suite shrinks to the flows that genuinely need fast mocked
iteration (or is retired where the real-services version is stable). This is
the multi-slice tail; it is explicitly NOT scheduled here — it is the
direction, executed opportunistically as stable coverage accretes.

### Fixture convergence (folds in Q-18)

`web/e2e/fixtures.ts` and `web/e2e-audit/fixtures.ts` are independent today
(separate JWT-acquisition flows). When stage (b) lands a merged ui-honesty
sub-gate, those two fixtures should converge onto a single JWT-minting flow
(the slice-201 → slice-389 multi-tenant test-issue-jwt path is the natural
shared substrate). The convergence is a step WITHIN stage (b)'s execution
slice, not a separate track — promoting the harness to merge-blocking is the
forcing function that makes two fixtures untenable.

## Anti-criteria (this ADR / slice 353)

- **Does NOT** change any CI job's gate status — `frontend-ui-honesty` stays
  informational (slice 353 P0-2).
- **Does NOT** touch `.github/workflows/ci.yml` (batch-166 reservation;
  any CI change is a future stage's slice).
- **Does NOT** split or modify `ui-honesty.spec.ts`.
- **Does NOT** converge the fixtures yet (stage-(b) work).

## Consequences

- The "promote vs. keep-mocked-forever" strategic call (slice 333 Theme 1)
  is decided: **promote, staged, stability-gated** — but the bet is hedged,
  because promotion only ever admits proven-stable assertions, so the merge
  gate's flake rate cannot regress.
- This path COMPOSES with ADR 0007 (contract-test tier): the contract tier
  pins the wire SHAPE cheaply; the promoted ui-honesty assertions pin real
  BEHAVIOR through the production stack. They are complementary, not
  alternatives (slice 333 framed them as either/or; the resolution is both,
  each at its own cost tier).

## Follow-on slices

File stage-(a) first (30-day stability audit; pure measurement, reuses the
flake-counter plumbing). Stages (b)–(d) follow as the stability data
warrants. Each stage is its own slice with its own AC set.
