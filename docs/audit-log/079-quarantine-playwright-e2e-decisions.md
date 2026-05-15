# 079 — Quarantine `Frontend · Playwright e2e` until the seed-data harness lands — decisions log

Slice 079 is `Type: AFK`. This log records the subjective build-time
judgment calls made while quarantining the `frontend-playwright` job
introduced by slice 069. Format mirrors the JUDGMENT-slice convention
(Decisions made · Revisit once in use · Confidence).

## Decisions made

### 1. AC-1 — Path A (`continue-on-error: true`), not Path B (path-filter) or Path C (remove the job)

**Options considered:**

- **(A)** Add `continue-on-error: true` at job level on the
  `frontend-playwright` job. The job still runs on every PR, but its
  red conclusion does not poison the PR's status checks. New failure
  modes (a step before the test step flaking, a spec-author typo
  crashing the runner, an upstream Playwright regression) surface as
  warning annotations.
- **(B)** Path-filter the job to PRs that touch `web/e2e/**` only via a
  `paths-filter` shim (or a job-level `if:` against a dorny output).
  Job runs only when someone edits the specs. ~0 runs/day at current
  cadence.
- **(C)** Remove the `frontend-playwright` job from `ci.yml` entirely
  until the seed-harness slice (082) lands. Cleanest but loses signal
  on any non-seed-data regression.

**Chosen: (A).** The slice doc's "Notes for the implementing agent"
explicitly recommends Path A as the default. Three reinforcing reasons:

1. **Preserves signal.** A spec-author typo that crashes Playwright
   bootstrap, an upstream chromium image regression, or a NATS / MinIO
   service-container break is exactly the kind of failure we still want
   to see — they have nothing to do with the seed-data gap. Path B and
   Path C both blind us to those. Path A surfaces them as warning
   annotations on every PR.
2. **Smallest viable change.** One `continue-on-error: true` line plus
   the inline-comment trail (slice 079 anti-criterion P0-A4). The
   `ci.yml` `frontend-playwright` job otherwise stays bit-for-bit
   identical to what slice 069 shipped. Reversible in one line when
   slice 082 lands and flips the field back to `false` (slice 082 AC-4).
3. **Honest about state.** The job has never passed; promoting it to a
   `paths-ignore` shape (Path B) implies "we'll re-light it when
   relevant" which is a forward-looking commitment we cannot honour
   until the harness exists. Path A is honest: "this is known-failing,
   here is the trail."

**Revisit once in use:**

- If the warning-annotation noise becomes its own ignore-fatigue
  problem (engineers stop reading the annotations), reconsider Path B
  as an interim while harness work is in flight.
- If a NEW failure mode lands and is not caught for >2 weeks, the
  warning-annotation model is failing its load-bearing purpose; revisit.

**Confidence: high.** Slice doc default; matches the broader pattern of
keeping non-required checks visible-but-non-blocking until they have a
green track record (slice 065 `Self-host bundle · end-to-end`, slice 038
`Helm chart · lint + template`, slice 069 `Frontend · vitest` all ship
non-required first).

### 2. AC-2 — `continue-on-error: true` at job level, not step level

**Options considered:**

- **(i)** Add `continue-on-error: true` only on the `Run Playwright
tests` step (line ~792 of `ci.yml`, the actual `npx playwright test`
  invocation).
- **(ii)** Add `continue-on-error: true` at job level so EVERY step of
  the `frontend-playwright` job is allowed to fail without poisoning
  the PR's conclusion.

**Chosen: (ii) — job level.** The `frontend-playwright` job has 13
steps, only 1 of which is the test invocation. The other 12 (postgres
service, MinIO docker-run, NATS docker-run, role bootstrap, forward
migrations, atlas binary build, web `npm install`, web `npm run build`,
Playwright chromium install, atlas server boot, web server boot, the
post-failure artifact upload, the post-failure log dump) all have
plausible non-seed-data failure modes — image pull flakes, transient
network, npm-registry hiccups, port collisions. Step-level
`continue-on-error` on only the test step would let those step failures
poison the PR, defeating the slice's purpose.

The job's `Upload Playwright report on failure` and `Dump server logs
on failure` steps both gate on `if: failure()` — these continue to fire
when an earlier step within the job fails, because `failure()` evaluates
the job-step state, not the job's externally-reported conclusion.
`continue-on-error: true` at job level only affects how the job's
conclusion is reported up to the workflow / PR status; intra-job
`if: failure()` gating is preserved (verified against GitHub Actions
docs on the `continue-on-error` field).

**Confidence: high.** Mechanically determined by the job's step count
and the field's scoping semantics.

### 3. Inline-comment scope

The comment block above `continue-on-error:` cites three pieces of
context:

1. Slice number (**079**) — per anti-criterion P0-A4 (no silent
   disable).
2. Slice 069 AC-5 PARTIAL + the merging-PR link (gh#132) — per slice
   doc AC-2.
3. The follow-on slice number (**082**) — so a future reader who
   finds the comment can navigate to the slice that removes the
   quarantine.

A fourth thing considered and dropped: the failure-count statistic
(52/62 today). It is true at the moment this slice lands and ages
poorly — by the time someone reads the comment in 6 months it would
either be wrong or stale. The decisions log keeps the statistic; the
inline comment keeps only the pointers.

**Confidence: high.**

### 4. Spec count discrepancy — slice doc says "five un-shimmed specs"; repo has 7

The slice doc reads "the five un-shimmed specs (`web/e2e/*.spec.ts`)".
At the time this slice runs the directory contains seven `.spec.ts`
files:

| Spec file                  | Slice | Seed-data needed?    |
| -------------------------- | ----- | -------------------- |
| `admin-bootstrap.spec.ts`  | 060   | yes (failing)        |
| `audit-workspace.spec.ts`  | 048   | yes (failing)        |
| `control-detail.spec.ts`   | 041   | yes (failing)        |
| `dashboard.spec.ts`        | 040   | yes (failing)        |
| `risk-hierarchy.spec.ts`   | 056   | yes (failing)        |
| `first-time-login.spec.ts` | 073   | no (route-mocked)    |
| `version-footer.spec.ts`   | 072   | no (unauth `/login`) |

The slice doc's "five un-shimmed" claim is consistent with reality: the
two route-mocked specs (slice 072 + 073) were authored under the same
slice-069 shim convention and use `page.route()` to mock the platform's
HTTP surface or assert on the unauthenticated `/login` route only.
They pass without seed data. The five listed above are the failing
specs that the harness slice (082) addresses.

This is recorded here so a future reader of the harness slice does not
think they need to re-cover the route-mocked specs.

**Confidence: high.** Verified by reading the file headers of the seven
spec files directly.

### 5. CONTRIBUTING.md placement — Test infrastructure subsection before AI-assist boundary

This batch (30) also includes slice 081 which edits CONTRIBUTING.md.
The two edits are in different sections of the file:

- **079 (this slice):** adds a "Test infrastructure" subsection placed
  between the existing "Refreshing the README screenshots" section
  (which ends at line 192 in the unmodified file) and the existing
  "AI-assist boundary" section (which starts at line 194).
- **081:** adds a "Local CI parity" subsection near the existing
  `pre-commit run --all-files` mention (line 142 area).

The two insertion points are ~50 lines apart. Rebase resolution is
keep-both-safe.

**Confidence: high.** Verified by reading the current CONTRIBUTING.md
and the batch-30 disjoint-surfaces summary in `_STATUS.md` row 16.

### 6. No CHANGELOG.md edit in this PR

Per slice 077's pattern (audit-log entry 1) and the project's
release-please convention: `CHANGELOG.md` is generated from
Conventional-Commit messages on the next release tag. The `feat(infra):`
type on this slice's squash-merge commit causes release-please to surface
it under "Features → infra" in v1.5.2's release notes. No manual
CHANGELOG.md edit is correct.

This was confirmed by inspecting `CHANGELOG.md` at the time of this
slice — every existing entry corresponds 1:1 to a merged PR with a
generated section header. No manually-authored entries.

**Confidence: high.**

## Post-merge follow-ups

1. Slice 082 (`Playwright e2e seed-data harness`) — filed in this PR
   with status `not-ready`. When a maintainer staffs it, AC-4 of slice
   082 flips Path A back to fail-on-red.
2. Watch the next 5–10 PRs after this slice merges. If the Playwright
   warning-annotation surfaces a NEW failure mode (anything not
   "expected element not visible"-shaped), confirm the warning model
   is doing its load-bearing job.
3. If slice 082 is not picked up within 30 days, file a noise-budget
   audit slice to revisit Path A vs Path B vs Path C with fresh data.
