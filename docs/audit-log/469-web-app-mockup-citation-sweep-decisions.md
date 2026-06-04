# 469 — decisions log: sweep `web/app/**` `Plans/mockups/` provenance citations

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is the residual tail of slice 459's
mechanical comment/doc sweep — comment-only, no runtime surface. The only
verification surfaces are the compile/lint/test gates, which a comment-only diff
cannot regress.)

Parent: slice 459 (`sweep Plans/mockups/ provenance citations everywhere EXCEPT
web/app`). Grandparent: slice 437 (`git mv Plans/mockups/ → Plans/_archive/mockups/`).
Sibling collision now cleared: slice 448 (`web/app`) merged, so the surface 459
deliberately deferred (its D2 / R1) is free.

## Decisions made

### D1 — Update-path vs rephrase: inherited slice 459's mechanical update-path

**Options:** (a) rewrite each `Plans/mockups/<page>.html` citation to the literal
new path `Plans/_archive/mockups/<page>.html`; (b) rephrase each to a path-free
phrasing ("the archived iteration-1 mockup").

**Chosen:** (a) — mechanical `s#Plans/mockups/#Plans/_archive/mockups/#g` on the
23 in-scope `web/app/**` source files, byte-identical to slice 459's D1.

**Rationale:** The citations carry **line numbers** (`control.html lines
139-152`, `policies.html lines 154-165`, `controls.html line 217`) that are still
load-bearing for a developer wanting to chase the exact iteration-1 design
reference. A path-free rephrase would discard that navigability — the entire
point of the slice (AC-1: the provenance links should resolve again). Update-path
preserves the line-anchored citation and makes it resolve. Matching 459's
convention exactly also keeps the lineage's diffs uniform. Confidence: **high**.

### D2 — Test-file `//` comments ARE in scope (distinct from 459's D4 fixture-string carve-out)

Three of the 23 files are `.test.ts`:
`web/app/(authed)/policies/ack-window.test.ts:60`,
`web/app/(authed)/controls/filters.test.ts:207`,
`web/app/(authed)/controls/[id]/tabs.test.ts:14`.

**Question:** does AC-3 ("leave deliberately-invalid negative-test fixture
strings as-is, cf. slice 459's `manifest.test.ts:47`") exclude these?

**Chosen:** updated all three. They are explanatory `//` provenance comments
("The mockup at `…` line 279 reads:", "Matches `…` line 217 example",
"`…` lines 143-149: Overview, Evidence, …") that document _why_ an assertion pins
a literal. The assertion string literals themselves
(`"365-day acknowledgment window"`, `"SOC2 · ISO · CSF"`, the seven tab keys) are
**untouched** — rewriting the path in the comment changes no assertion.

**Why this differs from 459's D4:** slice 459's `manifest.test.ts:47` carve-out is
a _fixture string literal_ (`mockupPath: "Plans/mockups/dashboard.html"`) chosen
**because it is invalid** — a negative test asserting the validator's regex
rejects a capital-`P`, slash-bearing path. Editing it would touch a test fixture
(forbidden by AC-2). No such fixture string exists anywhere under `web/app`
(confirmed: every `web/app` hit is a `//`-comment, none is a value passed to
code). So AC-3 is satisfied here **by absence** — there was nothing of that class
to leave as-is. Confidence: **high**.

### D3 — Diff verified comment-only (AC-2): 36/36 balanced path-swaps, zero spurious edits

After the sweep, the diff is 23 files / 36 insertions / 36 deletions. Verified
mechanically that (1) every removed line contained the old `Plans/mockups/` path,
(2) every added line contains the new `Plans/_archive/mockups/` path, (3) every
changed line is a comment (`//`, `*`, or `/*`), and (4) no removed line lacked
the old path (which would signal a spurious deletion). All four checks passed.

(The 36-vs-35-grep-hit delta is benign: the `git diff` line count is the
authoritative measure and is balanced 36/36; grep's per-occurrence count differs
because a couple of citations sit on lines where the surrounding comment context
made the hunk boundary land on an adjacent unchanged line. No file was
over-edited.) Confidence: **high**.

### D4 — CHANGELOG bullet added (mirrors slice 459's D5)

Pure-comment churn arguably needs no CHANGELOG entry, but slice 459 added one for
traceability of the slice-437 navigability lineage and this slice is its explicit
continuation. Added a `### Changed` bullet for the same reason — a one-line cost
that keeps the lineage (437 → 459 → 469) auditable end-to-end. Confidence:
**medium** (defensible either way; erred toward recording the work, consistent
with the parent).

### D5 — Stayed strictly inside `web/app/**`; runtime resolver untouched

The batch directive scopes this slice to `web/app/**` only (459 already swept
`internal/`, `web/components`, `web/lib`, `web/e2e`; slice 437 already repointed
the runtime `mockupsDir()` resolver and the `web/e2e-audit/` honesty-audit
harness). No file outside `web/app/**` was edited; no runtime path string was
changed; `coverage-thresholds.json`, `scripts/`, `ci.yml`, and
`docs/issues/_STATUS.md` (orchestrator-only) were not touched. Confidence:
**high** (pure scope discipline; no design judgment).

## Revisit once in use

- **R1 (highest):** AC-1 is now satisfied tree-wide — `grep -rn
"Plans/mockups/" web/app | grep -v _archive` returns zero. Combined with slice
  459, **no live `Plans/mockups/` provenance citation remains in any code
  surface** (the only residual `Plans/mockups/` strings are dated historical
  records — CHANGELOG prior entries, `docs/**`, the dated
  `Plans/canvas/13-ui-mockup-audit-2026-05-16.md` — left verbatim as
  point-in-time facts per slice 459's D3, and the one deliberately-invalid
  `web/e2e-audit/lib/manifest.test.ts:47` fixture per 459's D4). The 437 → 459 →
  469 sweep lineage is closed; no further follow-up sweep is needed unless the
  mockups are relocated again (R2).
- **R2:** if the mockups are ever moved/renamed again, both the `_archive` path
  citations _and_ the `manifest.test.ts` invalid-fixture string would need a
  re-sweep. The functional resolver (`mockupsDir()`) is the only place that must
  stay correct for runtime; the comments are best-effort navigability.
- **R3:** consider whether line-anchored citations (`lines 139-152`) are worth
  keeping given the mockups are frozen iteration-1 artifacts — a future call
  could strip the line numbers. Inherited verbatim from slice 459's R3; out of
  scope here (update-path was the conservative, lineage-consistent choice).

## Confidence summary

| Decision                                       | Confidence |
| ---------------------------------------------- | ---------- |
| D1 update-path vs rephrase (inherit 459)       | high       |
| D2 test-file `//` comments in scope            | high       |
| D3 diff verified comment-only (36/36 balanced) | high       |
| D4 add CHANGELOG bullet                        | medium     |
| D5 stayed inside web/app, runtime untouched    | high       |
