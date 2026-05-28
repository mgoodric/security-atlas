# 346 — CI yaml: extract inline slice-history commentary

**Cluster:** infra
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

`.github/workflows/ci.yml` is 2488 lines. The `tests-integration` job
alone carries ~150 lines of inline slice-by-slice commentary
(`# Slice 279: extended to include...`, `# Slice 283: extended to...`,
etc.). The historical narrative documents why each package was enrolled
and what the coverage delta was, which is genuinely useful information —
but it lives in the wrong place. A new contributor reading the job to
understand its structure has to mentally filter ~70% of the content as
historical context.

Finding I-2 of slice 334's framework audit.

**The fix.** Move the slice-history commentary out of the yaml body. The
information stays accessible (and is already in `git log` + the slice
docs themselves), but the yaml becomes structural.

Shape options:

1. **Sidecar doc.** `docs/ci/integration-job-history.md` holds the
   narrative. `ci.yml` retains a single comment block pointing readers
   to it. Cheap; preserves the information; lets the yaml shrink.
2. **Inline collapse to one-liner.** Each `# Slice 279: extended to
include ...` line collapses to `# Enrolment history: see docs/ci/...`.
   Same effect, more aggressive.
3. **Delete and rely on `git log`.** Every yaml comment block has a git
   blame trail. `git log -p ci.yml` produces the same history.
   Aggressive; loses the narrative density that makes the current
   commentary readable.

Recommendation: option (1). Preserves the writing (the comments are
load-bearing for understanding the enrolment-retroactive pattern); moves
it out of the yaml body where it interrupts reading flow.

This is a JUDGMENT slice — the call between options (1), (2), (3) is
subjective. Claude makes the call in implementation and writes it down
in the decisions log; the slice ships when CI is green.

**Why now:** the yaml has crossed 2400 lines. The integration-job
section is the dominant contributor. Without intervention, every new
enrolment slice adds 8-15 more lines of inline commentary.

**Trigger:** Surfaced 2026-05-27 by slice 334 framework audit (finding I-2).

## Threat model

Infra slice with no auth / data / network surface. STRIDE pass: CLEAN
across all categories. The CI behavior must not change — the slice is
re-organizing comments, not workflow logic.

## Acceptance criteria

- [ ] **AC-1.** The `tests-integration` job in `ci.yml` is at least
      100 lines shorter after the slice (extracted commentary).
- [ ] **AC-2.** A sidecar doc (e.g.
      `docs/ci/integration-job-history.md`) holds the extracted
      commentary, fully attributable to the originating slice.
- [ ] **AC-3.** The yaml retains a header comment block in
      `tests-integration` pointing to the sidecar.
- [ ] **AC-4.** No workflow behavior change: the integration job runs
      identical packages with identical flags before and after this
      slice.
- [ ] **AC-5.** Decisions log written at
      `docs/audit-log/346-ci-yaml-history-extraction-decisions.md` per
      JUDGMENT slice convention.
- [ ] **AC-6.** `pre-commit run --all-files` passes.

## Constitutional invariants honored

None at risk — this is a documentation reorganization slice.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — CI / GitHub Actions

## Dependencies

- **#069** (testing discipline) — `merged`. The job whose comments are
  extracted.

## Anti-criteria (P0 — block merge)

- **P0-346-1.** Does NOT change any CI behavior (no flag changes, no
  package-list changes, no service-container changes).
- **P0-346-2.** Does NOT modify any test file.
- **P0-346-3.** Does NOT touch CLAUDE.md or canvas.
- **P0-346-4.** Does NOT bundle with slice 345 or 347.

## Skill mix

- yaml editing
- Markdown authoring (sidecar doc)
- Standard read/grep

## Notes for the implementing agent

The dominant commentary block is in `.github/workflows/ci.yml` lines
275-515 (the `tests-integration` job preamble + inline rationale). Read
that section first to inventory what's worth preserving in the sidecar.

The pattern is uniform: `# Slice NNN: extended to include X` followed by
a paragraph explaining the coverage delta. The sidecar can be a simple
chronological table — slice number, package added, before% → after%,
load-bearing pattern.
