# Slice 346 — CI yaml history extraction — decisions log

**Slice type:** `JUDGMENT` (the call between options 1/2/3 is subjective —
this log records the call and the live-behavior-vs-history line drawn
during inventory). The ACs are mechanically verifiable; this log is the
audit-binding decisions trail per JUDGMENT-slice convention.

**Date:** 2026-05-28
**Branch:** `infra/346-ci-yaml-history-extraction`
**Primary artifact:** `docs/ci/integration-job-history.md` (sidecar)
**Triggered by:** slice 334 framework audit finding I-2.

## Background

`.github/workflows/ci.yml` had grown to 2488 lines. The
`tests-integration` job (lines 191-653, 463 lines total) carried 19
inline `# Slice NNN: extended to include ...` commentary blocks (lines
309-514, 206 lines of pure enrolment history). A new contributor reading
the job to understand its structure had to mentally filter ~45% of the
job's content as historical context.

The information is genuinely valuable — it documents the
"enrolment-as-the-move" pattern from the v2 round-3 coverage-audit
campaign — but it lived in the wrong place.

## Decisions made

### D1 — Option (1) sidecar selected over option (2) inline-collapse and option (3) delete

The slice doc presented three shapes:

1. **Sidecar doc.** `docs/ci/integration-job-history.md` holds the
   narrative; ci.yml retains a single comment block pointing to it.
2. **Inline collapse to one-liner.** Each `# Slice 279: extended to
include ...` line collapses to `# Enrolment history: see docs/ci/...`.
3. **Delete and rely on `git log`.** `git log -p ci.yml` produces the
   same history.

Chose **option (1)**, matching the slice doc's recommendation.

**For:**

- **Preserves the writing.** The slice doc explicitly named this — the
  comments are load-bearing for understanding the enrolment-retroactive
  pattern, not just the facts. A reader who sees only "Slice 279 —
  enrolled frameworkscope, risk, risk/aggrule" loses the prose that
  explains WHY each enrolment was a coverage lift without writing new
  tests. Option (3) loses this entirely; option (2) loses it within the
  yaml but technically preserves git history (still requires `git log`
  spelunking).
- **Discoverable.** A new contributor reads the yaml top-to-bottom and
  sees a single pointer comment leading to the sidecar. Option (3)
  requires them to know git-blame exists and to run it; option (2)
  requires them to know which slice to look up.
- **Versionable.** The sidecar is a real markdown file that future
  enrolment slices can extend with the same prose discipline (a new
  table row + a new `### Slice NNN` section). The pattern stays uniform.
- **Cheap.** One new file; one yaml comment block edit. No CI workflow
  change, no behavior change, no test change.

**Against (rejected):**

- Option (2) inline-collapse achieves the same line-shrink but loses
  the prose density that makes the original commentary readable. The
  19 blocks describe a uniform pattern; reducing each to one line
  leaves no place to learn the pattern at all.
- Option (3) delete-and-rely-on-git is the most aggressive, and the
  cheapest in terms of files. But git-blame is a tool of last resort
  for documentation. A new contributor reading the yaml has no breadcrumb
  to know the history exists at all. The pattern would have to be
  re-derived by every reader who needs it.

### D2 — Live-behavior comments stay in the yaml; pure-history comments move

The inventory rule applied during the edit: **a comment moves to the
sidecar if and only if it documents a one-time historical decision that
no longer informs how to read the job's live behavior**. Comments that
document live-behavior — why a step exists in its current shape — stay.

Comments **retained in the yaml** (live-behavior documentation, line
numbers as of post-edit state):

| Lines    | Comment                                                                | Reason it stays                                                                                                                                |
| -------- | ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| ~210-218 | Slice 036 MinIO + Slice 015 NATS `services:`-vs-`docker run` rationale | Explains why `Start MinIO` / `Start NATS JetStream` workflow steps exist instead of `services:` entries — load-bearing for the job's structure |
| ~221-230 | Env block per-variable annotations                                     | Documents what each env variable is for                                                                                                        |
| ~294-298 | Slice 033 RLS audit constitutional-invariant rationale                 | Documents why the audit-rls.sh step exists at all (invariant 6 enforcement)                                                                    |
| ~300-308 | `-p 1` race rationale + local reproducer command                       | The one-line flag is opaque without the prose; reproducer is operator-facing                                                                   |
| ~374-382 | Slice 279 `Download unit coverage artifact` + merged-gate architecture | Explains the unit-vs-merged gate distinction (canonical coverage check)                                                                        |
| ~410-419 | Slice 297 `Migration round-trip TRUNCATE` rationale                    | Explains why TRUNCATE happens before down-migration walk; load-bearing for the round-trip's correctness                                        |

Comments **moved to the sidecar** (pure enrolment history):

The 19 `# Slice NNN: extended to include ...` blocks previously at
lines 309-514. Each block describes "this slice added these packages
to the package list below, and the merged coverage lifted from X% to
Y%". None of the 19 documented live behavior of the step itself — the
package list inside `go test ...` IS the live behavior; the
commentary was the chronological narrative for how each package got
on that list. Sidecar preserves verbatim.

### D3 — Sidecar shape: prose intro + chronological table + per-slice verbatim sections

The sidecar (`docs/ci/integration-job-history.md`) is organized as:

1. **Prose intro** — frames the enrolment-retroactive pattern and the
   live-vs-history split, with a pointer back to this decisions log.
2. **At-a-glance chronology table** — slice · packages enrolled ·
   coverage delta · pattern. Lets a reader navigate without reading
   all 19 blocks.
3. **Per-slice verbatim sections** — each of the 19 `### Slice NNN`
   sections carries the original prose. The writing is preserved
   because (per D1) the writing is load-bearing for the pattern.
4. **"How to extend"** — the contributor-facing instruction for what
   future enrolment slices should add (a table row + a new section)
   and what they should NOT add (an inline comment in the yaml).

Considered and rejected: pure table without verbatim sections (lossy
on prose); pure verbatim sections without table (hard to navigate);
appendix at the end of `docs/ci/PATH_FILTERING.md` (couples two
unrelated topics).

### D4 — Verification gate: yq round-trip diff

Per the slice doc and project lesson "verify before claim", the
load-bearing AC-4 (no workflow behavior change) was verified
mechanically before commit:

```bash
yq eval '.jobs.tests-integration' .github/workflows/ci.yml -o=json > /tmp/before.json
# ... apply edit ...
yq eval '.jobs.tests-integration' .github/workflows/ci.yml -o=json > /tmp/after.json
diff /tmp/before.json /tmp/after.json
```

`yq` strips comments by default; an empty diff between the JSON shapes
of the job before and after the edit is the canonical proof that no
field, no flag, no service-container, no env-var, no package-list
entry changed. Diff was empty on first attempt. Recorded here so a
future reviewer can re-run the same check if the slice ever needs
post-merge re-verification.

## Line-count receipts

- `.github/workflows/ci.yml` total: **2488 → 2286 lines** (−202 lines)
- `tests-integration` job span: **lines 191-653 (463 lines) → lines 191-451 (261 lines)** (−202 lines)
- AC-1 floor: ≥100 lines shrink; achieved 202. Floor cleared 2.02×.

## Constitutional notes

This slice is a documentation reorganization. No invariant at risk; no
behavior change. The threat-model surface is CI/secrets — verified
clean: no new secrets, no new outbound calls, no new permissions, no
new actions versions. STRIDE pass: CLEAN.

## Spillover

None. The 19 slice blocks are all enrolment slices; the slice doc
asked for a one-time reorganization and the inventory found no
additional cleanup the same PR could justify (per P0-346-4, no
bundling with adjacent slices).
