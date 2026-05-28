# 334 — Test framework review decisions log

**Slice:** 334
**Type:** JUDGMENT
**Date:** 2026-05-27
**Companion report:** `docs/audits/334-test-framework-review.md`

The audit narrative lives in the companion report. This log is the
structured artifact AC-2 of the slice doc requires: per-framework
current-state · target-state · gap · remediation path. It also captures
the JUDGMENT decisions Claude made while conducting the audit.

---

## Per-framework current vs target

### Go unit

| Dimension              | Current state                                                                                          | Target state                                                                | Gap                          | Remediation path                                                      |
| ---------------------- | ------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------- | ---------------------------- | --------------------------------------------------------------------- |
| Parallelism discipline | `t.Parallel()` adoption by-author (zero in `eval/state_test.go`, every test in `eval/helpers_test.go`) | Pure-unit tests default to `t.Parallel()` unless a documented reason not to | Convention-drift             | Polish-round-1 sweep + a documented rule in `CONTRIBUTING.md`         |
| Coverage gate          | Monotonic ratchet across 110+ packages via `cmd/scripts/coverage-gate`                                 | Same, with the `excludes` list shrinking over time                          | Excludes growth (60 entries) | Audit `excludes` quarterly; retire entries with a coverage-lift slice |
| Assertion library      | stdlib `testing`                                                                                       | Same                                                                        | None                         | n/a — keep                                                            |
| Test layout            | Internal-package access for unit (`package eval`), external for integration (`package eval_test`)      | Documented convention                                                       | Unwritten convention         | Add to `CONTRIBUTING.md`                                              |
| Golden files           | None                                                                                                   | testdata/\*.golden for HTML / PDF / template-rendered output                | Pattern not adopted          | Adopt when a render target stabilizes; not urgent today               |

### Go integration

| Dimension               | Current state                                                                               | Target state                                                       | Gap                                          | Remediation path                                                          |
| ----------------------- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ | -------------------------------------------- | ------------------------------------------------------------------------- |
| Real-services binding   | Real Postgres + NATS + MinIO + `audit-rls.sh`                                               | Same                                                               | None                                         | n/a — keep                                                                |
| Package enrolment       | Hand-maintained 47-entry list in `ci.yml`                                                   | Discovery primitive (CI step grep's `//go:build integration` tags) | High-severity gap (I-1)                      | Slice 345                                                                 |
| CI yaml maintainability | 2488-line yaml with extensive in-line slice history                                         | Yaml is structural; history lives in `git log` / sidecar doc       | High-severity gap (I-2)                      | Slice 346                                                                 |
| `-p 1` serialization    | Justified by shared seed rows in `evidence_kind_schemas`, `scf_anchors`, `evidence_records` | Keep as-is; comment sharpened to distinguish shared-seed from RLS  | Comment-clarity nit                          | Captured in polish-round (slice 348)                                      |
| Test-helper duplication | Per-package `TestMain` re-implementation (~30 LOC × 20+ packages)                           | Shared `internal/testpgx/` helper                                  | Medium-severity sustainability concern (I-4) | Polish-round-1 covers the discovery; full helper extraction is a v3 slice |

### Frontend vitest

| Dimension           | Current state                                            | Target state                                                       | Gap                         | Remediation path                                  |
| ------------------- | -------------------------------------------------------- | ------------------------------------------------------------------ | --------------------------- | ------------------------------------------------- |
| Coverage ratchet    | None (informational upload only since slice 069)         | Per-file or per-directory ratchet via vitest `coverage.thresholds` | High-severity gap (V-1)     | Slice 347                                         |
| Test-path enrolment | Hand-maintained `include` array, 11 entries with escapes | Generic `**/*.test.ts` glob + exclude-list                         | Medium-severity drift (V-2) | Slice 348                                         |
| Mock duplication    | Every route test re-declares `NextResponse` mock         | `web/lib/test-utils/next-mocks.ts` shared                          | Medium-severity drift (V-3) | Slice 348                                         |
| Environment         | `node` (slice 069 P0-A3, no JSX)                         | Same until component-test surface grows                            | Tracking concern            | Re-evaluate when component-test count crosses ~20 |

### Frontend Playwright e2e

| Dimension              | Current state                                                                   | Target state                                                               | Gap                                          | Remediation path                                                        |
| ---------------------- | ------------------------------------------------------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------- | ----------------------------------------------------------------------- |
| Real-services binding  | Hybrid: real Next.js + mocked upstream atlas (57 `route.fulfill` calls)         | Strategy decision (cross-ref slice 333)                                    | Strategy-level ambiguity (P-1)               | Slice 333 owns the call; this audit observes the framework reality      |
| Page Object Model      | Not adopted; specs use testid-direct                                            | Strategy decision (cross-ref slice 333)                                    | Strategy-level ambiguity (P-2)               | Slice 333 owns the call                                                 |
| Parallelism            | `fullyParallel: false`, comment cites pre-slice-201 reasoning                   | Experiment with `fullyParallel: true` post-201                             | Medium-severity convention-drift (P-3)       | Polish-round-1 (slice 348) experiments; if green, lifts the flag        |
| `test.skip` discipline | 8 files carry quarantines; mix of legitimate env-gates and unresolved test-debt | Every `test.skip` references either an env-gate or an open spillover slice | Medium-severity sustainability concern (P-4) | Polish-round-1 triage pass                                              |
| Browser matrix         | chromium-only (slice 069 deferred firefox + webkit)                             | Re-evaluate post-v1                                                        | Low-severity tracking concern (P-5)          | Polish-round-1 captures the re-evaluation as a tracked item; no add yet |
| Trace / screenshot     | `retain-on-failure` configured                                                  | Same                                                                       | None                                         | n/a — keep                                                              |

---

## Decisions made

This is a JUDGMENT slice. The decisions Claude made during the audit
that warrant a recorded rationale:

### D1 — Spillover fan-out shape (4 slices, not 5)

**Options considered:**

- (a) One slice per finding (20 slices — explodes the backlog)
- (b) One slice per surface (4 slices, but bundles unrelated findings inside a surface)
- (c) Individual slices for high-severity + bundled polish-round for medium + low (chosen)
- (d) Single mega-slice "test framework polish 1" (violates AC-3)

**Chosen path: (c).** Each high-severity item is a tracer-bullet slice
(AC-3 compliant); all medium + low findings bundle into slice 348 (AC-4
allows this — "OR per-framework individual slices", and the polish round
is the simpler shape at this finding count).

**Confidence:** high. Matches the slice 337 audit's fan-out shape and
the slice doc's AC-3/AC-4 explicit allowance.

### D2 — Report location: `docs/audits/` (not `docs/audit-log/`)

**Options considered:**

- (a) Report at `docs/audit-log/334-test-framework-review.md` (matches slice doc AC-2)
- (b) Report at `docs/audits/334-test-framework-review.md` + decisions log at `docs/audit-log/334-test-framework-review-decisions.md` (chosen)

**Chosen path: (b).** Two reasons:

- The task instructions to the implementing agent explicitly request
  `docs/audits/334-test-framework-review.md`. The task instruction is
  the binding contract.
- The slice 337 audit (companion JUDGMENT slice from the same audit
  campaign) already established the two-file pattern: narrative report
  at `docs/audits/`, structured decisions log at `docs/audit-log/`.
  Following the established pattern keeps the audit campaign coherent.

The slice doc's AC-2 is satisfied by the decisions log (this file) which
holds the structured artifact AC-2 requires.

**Confidence:** high.

### D3 — `-p 1` recommendation: keep, do not relax

**Options considered:**

- (a) Keep `-p 1` as-is (chosen)
- (b) Relax to `-p 2` or `-p 4` for the integration job
- (c) Split job into Phase A (serial) + Phase B (parallel) immediately
- (d) Document the constraint, defer the relaxation decision

**Chosen path: (a) + the comment-sharpening from option (d).** The audit
narrative names the reason: the colliding rows live in shared catalog +
append-only ledger tables, not in tenant-scoped tables. Relaxing without
first partitioning the test packages would surface duplicate-key
violations. The v3 split path is documented in the audit report so the
relaxation is recoverable when wall-clock pressure shows up.

The slice doc P0-334-3 explicitly forbids removing `-p 1` without an
"explicit RLS-test-pattern preservation argument". The recommendation
honors that — and goes one step further by clarifying that `-p 1` is
NOT primarily about RLS at all.

**Confidence:** high.

### D4 — Severity assignment for V-1 (vitest coverage ratchet)

**Options considered:**

- (a) Medium — "informational ratchet was always the slice 069 plan"
- (b) High — "the ratchet is the load-bearing primitive that keeps coverage from regressing; absence is a class-of-bug" (chosen)

**Chosen path: (b).** Reasoning: the Go side's ratchet has retroactive
enrolment slices (279, 283, 284, 287, 288, 290, 293, 294, 295, 297, 310,
313, 315, 317, 318, 319, 320) which exist BECAUSE the ratchet caught the
gap. The TS side has no equivalent forcing function, so equivalent gaps
ship silently. "Slice 069 promised this and 250 slices later it hasn't
landed" is exactly the shape of high-severity sustainability concern.

**Confidence:** medium. A reasonable reader could call this medium on
the grounds that vitest coverage today is non-trivial (slice 069 + the
seed authoring across many slices); the absent ratchet is a velocity
risk, not an active regression. The high call is the more honest read of
"it was promised and the promise is open".

### D5 — Surface-by-surface vs theme-by-theme report shape

**Options considered:**

- (a) Surface-by-surface findings, then cross-framework themes (chosen)
- (b) Theme-by-theme, with the four surfaces as columns inside each theme
- (c) High-severity-first, then medium, then low, surfaces as a column

**Chosen path: (a).** The persona's "framework architecture" lens is
surface-native; reading the audit as a contributor inheriting one
surface (e.g. "what does the Playwright suite need?") is the dominant
read pattern. The cross-framework themes section catches the patterns
that span surfaces without forcing the report's primary structure
through them.

**Confidence:** high.

### D6 — Reporting `t.Parallel()` adoption as a Medium (not Low)

**Options considered:**

- (a) Low — "style nit, no real bug surface"
- (b) Medium — "by-author convention without an enforcing rule means it never converges" (chosen)

**Chosen path: (b).** Pure-unit tests without `t.Parallel()` make the
unit run sequential where it could be parallel. With ~1000 unit tests
across the codebase the wall-clock cost is real (estimated 30-90s per
CI run). More importantly, the convention is by-author; without a
written rule it never converges. Medium-severity is the right call.

**Confidence:** medium. A persona reader who values consistency above
wall-clock might rate it lower; a reader who values CI velocity above
consistency might rate it higher.

### D7 — Cross-reference policy

The slice doc's AC-5 requires cross-referencing slice 333; AC-6 requires
cross-referencing slice 069. Both honored. Additionally, P-1 and P-2
(strategy questions surfaced by the framework audit) are explicitly
flagged as "strategy-level call; framework audit observes, strategy
audit owns the call" rather than expanded into framework-level
recommendations. This honors P0-334-6 (do not cross into slice 333's
territory).

**Confidence:** high.

---

## Revisit once in use

When the spillover slices land and the polish round is applied, these
calls warrant re-evaluation:

1. **V-1 severity.** Once the vitest ratchet lands (slice 347), re-read
   the coverage data and reclassify. If the actual measured coverage is
   already at a healthy level (>70% across the vitest surface), the
   ratchet is preventative; if it's below 50%, V-1 was correctly high
   and the polish slices that follow are catching real debt.
2. **P-3 (`fullyParallel`) experiment outcome.** Polish-round-1 will
   experiment with `fullyParallel: true`. Either it halves CI wall-clock
   (the comment was outdated, lift the flag) or it surfaces a real race
   (document the new constraint, keep the flag). The audit cannot
   predict which.
3. **I-1 enrolment discovery primitive shape.** Slice 345 picks an
   implementation shape (shell-script grep vs Go meta-test vs CI step).
   Once landed, re-audit whether the enrolment retroactives stop. If a
   year later there's another batch of "enrol package X" slices, the
   primitive shape was wrong.
4. **The `excludes` list (U-2).** Quarterly audit. The right path is
   slow erosion: each quarter, retire 5-10 entries with a coverage-lift
   slice. If the list grows instead of shrinks, the gate is decorative.
5. **Theme 3 (test-fixture duplication).** Polish-round-1 will batch
   the cheap wins (`web/lib/test-utils/next-mocks.ts`, sqlstate
   constants). The Playwright route-mock factory is a strategy call
   (slice 333). If both halves of the polish round land and Playwright
   route-mocks are still 57 in-line declarations, the strategy call
   surfaced and someone owes a decision.
6. **Strengths re-validation.** Audit only-criticism distorts. Re-read
   the strengths section in 6 months. Specifically: is stdlib `testing`
   still the right call once mutation testing arrives (slice 333
   surface)? Is integration tier still binding against real services or
   has a mock crept in (Article IX regression)?

---

## Confidence summary

| Decision                                                  | Confidence |
| --------------------------------------------------------- | ---------- |
| D1 — Spillover fan-out (4 slices, not 5)                  | high       |
| D2 — Report at `docs/audits/`; log at `docs/audit-log/`   | high       |
| D3 — Keep `-p 1`; defer relaxation to v3 split-phase path | high       |
| D4 — V-1 (vitest ratchet) severity = high                 | medium     |
| D5 — Surface-by-surface report structure                  | high       |
| D6 — `t.Parallel()` adoption severity = medium            | medium     |
| D7 — Strategy-vs-framework cross-reference discipline     | high       |

`medium`-confidence decisions (D4, D6) are top of the revisit list.
