# Slice 179 — `schema-removal-age` CI check — decisions log

> JUDGMENT-type slice. Per `Plans/prompts/04-per-slice-template.md`, the engineer
> makes the subjective build-time calls and records them here; the maintainer
> iterates post-deployment if any decision proves wrong. None of these calls
> touches the constitutional product-runtime AI-assist boundary.

The slice doc (`docs/issues/179-schema-removal-age-ci-check.md`) is the
source of truth for what was built. This log records the subjective
build-time decisions that the spec deliberately left open.

## D1 — Implementation language: bash (chosen) vs Go script

**Picked:** Bash script at `scripts/check-schema-removal-age.sh`, with a
companion `_test.sh` smoke harness.

**Alternatives considered:**

| Option                    | For                                                                                                                                                                                                                                 | Against                                                                                                                                                                                                                         |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Bash                      | Matches `scripts/check-branch-protection-drift.sh` precedent (slice 127 / 158); zero Go-toolchain cost in the CI step; the work is a thin `git log` wrapper + arithmetic; test harness is a sibling `_test.sh` (slice 127 pattern). | Date arithmetic in pure bash is non-portable across macOS BSD vs GNU; needs a python3 sidecar.                                                                                                                                  |
| Go (under `cmd/scripts/`) | Matches `cmd/scripts/coverage-check` and `cmd/scripts/coverage-gate` precedents; native time arithmetic; could share types with the schema-registry package.                                                                        | The check has no schema-registry coupling — it reads only git output. Adding a Go module + tests + module-graph entry for ~50 lines of arithmetic is overkill. The CI step would need `setup-go` to build, lengthening runtime. |

**Rationale:** The work is git-log parsing + arithmetic + an env-flag
guard. Bash is the minimum-viable surface and matches the
`scripts/check-*` shape that already enforces other invariants in this
repo (branch-protection drift, openapi drift, etc.). The portability
edge case (BSD `date` vs GNU `date`) is solved by a one-shot
`python3 -c` call — python3 is available on every supported developer
machine and is pre-installed on `ubuntu-latest`.

**Confidence:** High. The script is small enough that a future
maintainer can rewrite it in Go inside a single PR if any of bash 3.2,
python3, or the test harness becomes a friction point.

## D2 — Script location: `scripts/` (chosen) vs `cmd/scripts/`

**Picked:** `scripts/check-schema-removal-age.sh`.

**Rationale:** `cmd/scripts/` is the Go-binary surface
(`coverage-check/main.go`, `coverage-gate/main.go`). Shell utilities
live in `scripts/` (15+ siblings, including
`check-branch-protection-drift.sh`, `check-openapi-drift.sh`,
`audit-rls.sh`). Splitting bash and Go by directory matches the
existing convention; no value in mixing.

**Confidence:** High.

## D3 — Date arithmetic: `python3 -c` (chosen) vs `date -d` vs pure bash

**Picked:** `python3 -c` block invoked once per removed file with
positional args.

**Alternatives considered:**

- **`date -d`** — GNU coreutils form. Doesn't work on macOS BSD `date`
  without `coreutils` brew install. The script is intended to run
  identically on the contributor's macOS laptop and on
  `ubuntu-latest`, so coupling to GNU `date` would split the contract.
- **Pure-bash arithmetic** — parse ISO-8601 by hand. Verbose,
  error-prone, no timezone-offset handling.
- **`python3 -c`** — cross-platform, stdlib only, handles offsets via
  `datetime.fromisoformat` (after the `Z → +00:00` replace that pre-
  3.11 Python requires). Trivially correct.

**Initial implementation note:** the first cut of the script used a
heredoc form (`python3 - <<'PY'`). Bash 3.2 (the macOS default shell)
mis-parses heredocs nested inside `$(...)` command substitution when
the body or the surrounding string contains parentheses. The current
form (`python3 -c '...'`) is the portable alternative.

**Receipts:** `scripts/check-schema-removal-age.sh::age_days` block
comment documents the bash-3.2 gotcha.

**Confidence:** High.

## D4 — Override surface: env var on script (chosen) vs label-check in script

**Picked:** Script reads `SCHEMA_REMOVAL_OVERRIDE` from its environment.
The CI job is the label-aware surface — it calls
`gh pr view --json labels`, sets the env var to `1` when
`[deprecation-override]` is present, and propagates that to the
script's invocation.

**Alternatives considered:**

- **Script calls `gh pr view` itself** — couples the unit-of-work to
  the GitHub-Actions environment. Hard to unit-test (would need to
  mock `gh`). Worse separation of concerns.
- **CI hard-codes the label name in the script call** — same shape
  but louder; the env-var form keeps the script a pure function of
  (file list, override flag).

**Rationale:** Clean separation: the script is pure; the CI job
introspects PR metadata. Same pattern as slice 127's
`check-branch-protection-drift.sh` (`ATLAS_FIXTURE_*` env overrides
for test isolation).

**P0-179-2 honored:** the override label name is hard-coded ONLY in
the CI workflow (`.github/workflows/ci.yml::schema-removal-age::Resolve
override label`) via a jq exact-match (`map(select(. == "[deprecation-override]"))`).
The script knows nothing about labels — it only knows `OVERRIDE=1` or
not.

**Confidence:** High.

## D5 — Test-fixture trust root: real ephemeral git repo (chosen) vs mocked git output

**Picked:** Real ephemeral git repo (`mktemp -d` + `git init` +
commits with fake `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE` env values).

**Alternatives considered:**

- **Mock `git log`** — fake the script's git-output dependency via a
  shim on `PATH`. Lighter-weight but doesn't test the actual command
  invocation; a `--diff-filter=A` typo would slip through.
- **Real git repo with fake dates** — heavier setup but tests the
  exact bytestream the script sees in production.

**Rationale:** The trust root of this slice is `git log` on `main`
(P0-179-1). Faking the trust root in tests would let real-world
syntax bugs in the `git log` invocation pass green. Real-git-with-
faked-dates exercises the production path end-to-end. Test harness
runs in ~0.3 s locally — the overhead is irrelevant.

**Receipts:** `scripts/check-schema-removal-age_test.sh` 27
assertions across 8 cases (a-h: all-pass, one-violation, override,
zero-input, unknown-path, stdin, exact-90-day boundary, multi-
violation).

**Confidence:** High.

## D6 — Not a required check (slice doc P0-179-4)

**Picked:** Job is NOT added to `.github/branch-protection.json`
required-checks. Runs only when the PR touches the schema-registry
tree.

**Why this is structural, not just deferred:**

- The first schema-deletion PR will be the live test of the check. We
  want to see it pass once against a real delete before promoting it
  to a required check, otherwise the first community contributor who
  trips a false-positive would be blocked on a CI bug.
- The deprecation window is a 90-day-cadence event — adding a CI
  check to required-checks before it has fired against a real PR
  carries a higher-than-usual unknown-unknowns risk.
- Promotion to required-checks is a 5-line follow-up PR once the
  check has proven its shape. Slice 179 doesn't need to bundle that
  promotion.

**Confidence:** High. The slice doc explicitly says don't promote in
this slice; D6 just records the rationale.

## D7 — Path-filter gate: new `schemas:` filter output (chosen) vs `code:` reuse

**Picked:** Extend the existing `changes` job in
`.github/workflows/ci.yml` with a new `schemas:` filter output that
matches ONLY `internal/api/schemaregistry/schemas/**`. The new job
gates on `needs.changes.outputs.schemas == 'true'`.

**Alternative:** Gate the new job on the existing `code:` output,
which already includes `internal/**`. Would have worked but means
the schema-removal-age check runs (and posts a redundant
"no removals" pass-line) for every code PR. Adding a narrow filter
keeps the check noise-free on the 90%+ of PRs that don't touch
schemas, and removes any need for a stub-sibling because the job
simply doesn't run on irrelevant PRs.

**Rationale:** The check is NOT a required check (D6); we don't need
the same-name stub pattern that the required-checks jobs use. A
narrow `if:` is sufficient.

**Confidence:** High.

## D8 — Failure error-message shape

**Picked:** Exact slice-AC-3 phrasing:
`<path> introduced <date>, age <N> days, must be >= 90 days; remaining: <90-N> days. Override with [deprecation-override] label + audit-log entry.`

**Why exact match matters:** The phrasing names the file, the
recorded introduction date, the computed age, the floor, the
remaining days, AND the escape hatch — five pieces of information in
one line. A contributor reading the CI failure should be able to
decide their next action without opening any other tab.

**Confidence:** High; copied verbatim from the slice spec.

## D9 — Fail-closed on "file absent from main" (P0-179-6)

**Picked:** When `git log --diff-filter=A` returns nothing for a
removed path, the script exits 2 (misuse) with a clear message rather
than exit 0 (no commit found = age 0 = floor violation = fail) or
exit 1 (treat as floor violation).

**Reasoning:**

- Exit 0 would be wrong (silently passing).
- Exit 1 (floor violation) is misleading — the script can't actually
  compute an age, so claiming the file is "too young" lies about the
  diagnostic.
- Exit 2 (misuse) is correct: the script is being asked to evaluate
  something it cannot evaluate. The error message names two likely
  causes (file created+deleted in the same PR, or shallow checkout)
  and tells the maintainer how to fix each.

The "created+deleted in the same PR" case is genuinely a no-op for
the 90-day rule — the file never reached main, so the clock never
started — but the safest call is still to surface the condition so
the maintainer can confirm rather than silently bless it. The CI job
fetches `main` with `fetch-depth: 0`, which eliminates the shallow-
checkout case in production.

**Confidence:** Medium-high. If real PRs trip this edge frequently
(e.g., a churn-y schema is created and deleted in the same PR), the
fail-closed behavior is too strict — we'd amend the script to drop
paths absent from main with a "skipped: never reached main" line
instead. The current behavior is the conservative starting point; the
maintainer reviews the first failure and decides.

## D10a — AC-11 (CI integration test): documented as out-of-band

The spec allows "or document why this is hard to test in CI itself."
We take that path.

**Why:** Testing the CI integration requires either (a) a synthetic PR
that removes a known-young schema, asserts the job fails, then a
companion PR that removes a known-old schema and asserts the job
passes — both running against real GitHub Actions in a real branch
context, OR (b) a meta-CI harness that boots `act` (the local GitHub
Actions runner) on every PR. Both are disproportionate to the slice.

**What we do instead:**

1. The script test harness (`scripts/check-schema-removal-age_test.sh`,
   27 assertions across 8 cases) covers the worker's contract end-
   to-end against a real git repository, including stdin form, exact
   boundary, multi-violation, and the "unknown path" edge.
2. The CI integration's behavior — `git diff --diff-filter=D ... |
bash scripts/check-schema-removal-age.sh` — is the same byte-
   stream the harness exercises in case (f). The YAML wires up the
   inputs identically.
3. The first real schema-deletion PR on this repo is the live
   integration smoke test. Since slice 179 is structurally a
   no-op until the first deletion lands (currently zero schemas
   are eligible to be removed under the 90-day floor — they're all
   < 90 days old), the maintainer reviewing that first PR sees the
   check fire and validates the integration in production.
4. Slice 179 itself is the first PR that touches the schema-registry
   tree (via the new `README.md`) so the new `schemas: true` filter
   output fires on this very PR. The CI job runs (the path filter
   matches) but finds zero D-filtered files (we only added a file)
   and exits 0 quietly. That IS the smoke test the spec asked for —
   it proves the workflow YAML is shape-valid, the filter triggers,
   the script is reachable, and the no-removals path works against
   real `origin/main`. The first removal PR is the rest.

**Receipts:** This PR's CI run shows `Schema · removal-age (90-day
floor)` running, the diff step reporting `count=0`, and the script
output `no removed schema files supplied`.

## D10 — 90-day boundary semantics: inclusive (chosen)

**Picked:** A file introduced exactly 90 full days before the check
runs PASSES. The comparison is `age_days < 90` for failure, so
`age_days == 90` is eligible.

**Rationale:** The slice doc says "must be >= 90 days" (AC-3); the
inclusive boundary matches the spec phrasing. The test harness has a
dedicated boundary case (g) that pins this.

**Confidence:** High.

---

## Out-of-scope findings (no spillover filed)

None. The slice was self-contained.

## Receipts (file paths only)

- `scripts/check-schema-removal-age.sh` — the worker.
- `scripts/check-schema-removal-age_test.sh` — 27-assertion smoke harness.
- `.github/workflows/ci.yml::schema-removal-age` + `changes::schemas` filter — CI integration.
- `internal/api/schemaregistry/schemas/README.md` — operator-facing block.
- `schemas/README.md` — breadcrumb update.
- `CHANGELOG.md::[Unreleased]::Added` — slice 179 entry.
