# Slice 701a — merge-gate `needs:` completeness + promotion to required · decisions log

**Type:** JUDGMENT · **Approach:** implement (close the gaps, derive the assertions, promote) · **Date:** 2026-07-25

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

> Two latent defects surfaced while building this slice, both in the slice-631
> merge-gate itself, and both found by reading the job rather than by any test:
> (1) a failed or cancelled `changes` job left `CODE` empty, which fell through
> the `!= 'true'` branch and PASSED the gate — the path-filter oracle failing
> opened the gate instead of closing it; (2) on a docs-only PR the gate
> short-circuited before looking at the three unconditional legs, so a RED
> `precommit` / `actions-pin-check` / `openapi-drift-check` was invisible to the
> aggregator. Neither is reachable by any existing tier — a CI gate's own
> decision logic has no unit, integration or e2e surface in this repo — so
> `manual_review` is both where they were caught and where they should have
> been. D6 records the "make it testable" follow-up.

---

## Why this slice exists

Slice 701 wants to delete the ~21 `-stub` jobs and make `CI · merge-gate` the
single required status check. That is only safe when the gate is _complete_:
every name branch protection requires must be represented in the gate's
`needs:`. Slice 693's audit found five omissions and flagged 701 as the one
recommendation in the whole pipeline-efficiency sweep with real merge-safety
risk.

701a is the safety groundwork. It closes the completeness gap, removes the
list-sync hazard structurally, and promotes the gate — and deliberately deletes
NOTHING. The stub collapse is a separate child slice that must not begin until
this one has soaked.

---

## AC-1 — the contexts-vs-needs diff

Enumerated from the files, not from the slice-693 audit's summary. Sources:
`.github/branch-protection.json` → `required_status_checks.contexts` (18 names
before this slice) and `.github/workflows/ci.yml` → `merge-gate.needs` (10
entries before this slice). Job-id ↔ display-name mapping read off each job's
`name:` key.

### Every required context, and the job that backs it

| #   | `required_status_checks.contexts` name              | backing job                                    | in `needs:` BEFORE | in `needs:` AFTER           |
| --- | --------------------------------------------------- | ---------------------------------------------- | ------------------ | --------------------------- |
| 1   | `Go · build + test`                                 | `build-go` (+`build-go-stub`)                  | yes                | yes                         |
| 2   | `Go · integration (Postgres RLS)`                   | `tests-integration` (+ stub)                   | yes                | yes                         |
| 3   | `Go · lint`                                         | `lint-go` (+ stub)                             | yes                | yes                         |
| 4   | `Go · sqlc generate diff`                           | `sqlc-drift` (+ stub)                          | yes                | yes                         |
| 5   | `Proto · lint + generate diff`                      | `proto` (+ stub)                               | yes                | yes                         |
| 6   | `Frontend · install + build`                        | `build-frontend` (+ stub)                      | yes                | yes                         |
| 7   | `Frontend · Playwright e2e`                         | `frontend-playwright` (+ stub)                 | yes                | yes                         |
| 8   | `Frontend · Playwright e2e (prod-build standalone)` | `frontend-playwright-prod-build` (+ stub)      | **NO**             | **yes (added)**             |
| 9   | `Frontend · vitest`                                 | `frontend-vitest` (+ stub)                     | **NO**             | **yes (added)**             |
| 10  | `Helm chart · lint + template`                      | `helm-lint` (+ stub)                           | **NO**             | **yes (added)**             |
| 11  | `Python · ruff`                                     | `lint-python` (+ stub)                         | yes                | yes                         |
| 12  | `pre-commit · all hooks`                            | `precommit` (unconditional, no stub)           | **NO**             | **yes (added)**             |
| 13  | `actions-pin-check`                                 | `actions-pin-check` (unconditional, no stub)   | **NO**             | **yes (added)**             |
| 14  | `openapi-drift-check`                               | `openapi-drift-check` (unconditional, no stub) | **NO**             | **yes (added)**             |
| 15  | `Analyze (go)`                                      | `analyze` in `.github/workflows/codeql.yml`    | n/a                | **n/a — out of reach (D2)** |
| 16  | `Analyze (javascript-typescript)`                   | `analyze` in `.github/workflows/codeql.yml`    | n/a                | **n/a — out of reach (D2)** |
| 17  | `GitGuardian Security Checks`                       | GitHub App — no workflow job                   | n/a                | **n/a — out of reach (D2)** |
| 18  | `DCO`                                               | GitHub App — no workflow job                   | n/a                | **n/a — out of reach (D2)** |

**Result: 14 of the 18 required contexts are backed by a `ci.yml` job, and all
14 are now in `needs:`. The remaining 4 are structurally unrepresentable — see
D2. None of the 18 is a brick: all 18 report on live branch protection today.**

### Jobs added to `needs:` with no required context of their own

Slice 701's AC-1 names five jobs explicitly. Two of the five (`frontend-vitest`,
and by extension the prod-build Playwright leg) turned out to be genuine
contexts-driven gaps; the other three are real legs that back no required
context. They are added anyway, because the point of the gate is to be the
single thing the stub collapse can safely stand on:

| job                       | display name                            | why it is in `needs:`                                 |
| ------------------------- | --------------------------------------- | ----------------------------------------------------- |
| `tests-integration-shard` | `Go · integration (shard A/B1/…)`       | already present. Matrix aggregate — the slice-474 leg |
| `oscal-bridge`            | `OSCAL bridge · Python (ruff + pytest)` | slice 701 AC-1 mandate                                |
| `fuzz`                    | `Go · fuzz (bounded)`                   | slice 701 AC-1 mandate                                |
| `frontend-lint`           | `Frontend · lint`                       | slice 701 AC-1 mandate                                |
| `govulncheck`             | `Go · govulncheck`                      | slice 701 AC-1 mandate                                |

### The audit's list vs. what the files actually say (AC-1, re-verified)

Slice 693's audit listed the omissions as `oscal-bridge`, `fuzz`,
`frontend-vitest`, `frontend-lint`, `govulncheck`. Re-deriving from the files
rather than trusting that list changes the picture materially:

- **The audit's list is not the contexts-vs-needs diff.** Only one of its five
  entries (`frontend-vitest`) is a required context. `oscal-bridge`, `fuzz`,
  `frontend-lint` and `govulncheck` back no name in `contexts` at all — they are
  legitimate coverage gaps for the stub-collapse goal, but they are not the
  slice-474 masking hole.
- **The audit MISSED five real contexts-driven omissions:**
  `frontend-playwright-prod-build`, `helm-lint`, `precommit`,
  `actions-pin-check`, `openapi-drift-check`. Three of those five
  (`helm-lint`, `frontend-playwright-prod-build`, `frontend-vitest`) were
  promoted to required by slice 419 only the day before, on 2026-07-24 — the
  audit's `needs:` comparison predates that promotion. The other two
  (`precommit`, `actions-pin-check` / `openapi-drift-check`) are unconditional
  guards that have been required since slices 128 and 140.

This is the concrete reason AC-1 says "re-verify rather than copy": had the
stubs been collapsed against the audit's list, `Helm chart · lint + template`,
`Frontend · Playwright e2e (prod-build standalone)`, `pre-commit · all hooks`,
`actions-pin-check` and `openapi-drift-check` would all have lost their
enforcement path — reopening the slice-474 hole on five checks while appearing
to close it on four.

### Jobs deliberately LEFT OUT of `needs:`

| job                                                                                                                            | why not                                                                                                                                                                                                                                                                                                                                       |
| ------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `test-self-host-bundle`                                                                                                        | Slice 419 explicitly RETAINED it advisory, for two written reasons (matrix-expanded context names; three Docker-Hub-incident reds in the soak window). Adding it here would promote it by the back door and undo that decision. See `$retain_advisory_from_slice_419`.                                                                        |
| `frontend-ui-honesty`                                                                                                          | `continue-on-error: true`, name literally ends `(advisory)`. A `continue-on-error` job always reports `success`, so asserting it would be vacuous _and_ misleading.                                                                                                                                                                           |
| `phantom-deps`, `assertion-density`, `branch-protection-drift-validate`                                                        | Same — `continue-on-error: true`, informational-job convention (slices 109/120/127).                                                                                                                                                                                                                                                          |
| `npm-audit`, `trivy-image`, `schema-removal-age`, `cache-path-guard`, `integration-enrolment-check`, `coverage-excludes-check` | Informational / narrower-filter guards, none required today. Promoting them is a decision with its own soak, not a side effect of this slice. `schema-removal-age` in particular is gated on `changes.outputs.schemas`, a NARROWER filter — it is `skipped` on an ordinary code PR and would brick the gate (see the `needs:` block comment). |
| `build-atlas`                                                                                                                  | Covered transitively: it is a `needs:` of `frontend-playwright` and `frontend-playwright-prod-build`, so a failed `build-atlas` makes both of those `skipped` on a code PR, which the gate already blocks on.                                                                                                                                 |

---

## D1 — Assertion list derived from `toJSON(needs)` (AC-2)

**Decision: derive, don't enumerate.**

Before this slice there were three lists that had to agree by hand:

1. `required_status_checks.contexts` in `.github/branch-protection.json`
2. `merge-gate.needs` in `.github/workflows/ci.yml`
3. the nine `require "…" "$R_…"` calls inside the merge-gate step

List 3 is now gone. The step reads `NEEDS_JSON: ${{ toJSON(needs) }}` and
iterates `to_entries[] | "\(.key)\t\(.value.result)"`. Adding a job to `needs:`
is now _sufficient_ to have it asserted; there is no second place to forget.

The failure mode this removes is not hypothetical for this repo — it is exactly
the shape of the bug being fixed: a job sitting in a list that nobody checks.
Before the refactor, `needs:` and the `require()` calls happened to agree only
because both were nine-ish entries maintained by the same hand on the same day.

**Residual hand-maintained surface, and why it is safe.** The step keeps a
`display()` case statement mapping job id → branch-protection context name, for
readable logs. It is cosmetic: an unmapped job falls through to `*)` and prints
its own id, so a stale entry costs a less-pretty log line and can never cause a
missed assertion. The assertion set comes from `NEEDS_JSON` alone.

**Two guards on the derivation itself.** A derived list can fail by deriving
nothing, which would read as "all green":

- `leg_count < 2` → hard fail. An empty or garbled `needs` context cannot pass.
- `needs.changes.result != 'success'` → hard fail (D3).

**List 1 vs list 2 is still hand-maintained**, and cannot be otherwise: branch
protection lives outside the workflow file, and four of its names come from
outside `ci.yml` entirely (D2). That gap is what the table in AC-1 above exists
to close by inspection, and what the slice-701 child will shrink by pruning
`contexts` down to `CI · merge-gate` + the four out-of-reach names + the
unconditional guards.

---

## D2 — Four required contexts cannot be in `needs:` at all

`needs:` in GitHub Actions is **workflow-scoped**. It can only name jobs in the
same workflow file. That puts four of the 18 required contexts permanently out
of the aggregator's reach:

| context                           | producer                                                             | why unreachable         |
| --------------------------------- | -------------------------------------------------------------------- | ----------------------- |
| `Analyze (go)`                    | `.github/workflows/codeql.yml`, job `analyze`, matrix `language: go` | different workflow file |
| `Analyze (javascript-typescript)` | same job, matrix `language: javascript-typescript`                   | different workflow file |
| `GitGuardian Security Checks`     | GitHub App                                                           | no workflow job exists  |
| `DCO`                             | GitHub App                                                           | no workflow job exists  |

**This is NOT the "required context with no corresponding job" brick the OE's
blocked-condition describes.** All four have producers and all four report
today — verified against live branch protection
(`gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks`
returns all 18 names, and the CodeQL matrix at `codeql.yml:29-33` is exactly
`go` + `javascript-typescript`). They are reachable by branch protection, just
not by `needs:`.

**Consequence for the child slice, stated here so it is not rediscovered:**
`CI · merge-gate` can never be the _sole_ required check. The minimum viable
required set after the stub collapse is `CI · merge-gate` + these four. All four
are unconditional security/provenance surfaces — CodeQL and GitGuardian are
called out in `ci.yml`'s own header comment as deliberately never path-filtered
— so keeping them required is aligned with slice 701's anti-criterion "does NOT
remove the unconditional security guards from required checks", not a
concession.

---

## D3 — `changes` is now asserted, closing a masking hole

The old gate read `CODE: ${{ needs.changes.outputs.code }}` and branched:

```bash
if [ "${CODE:-}" != "true" ]; then exit 0; fi
```

If the `changes` job itself **failed or was cancelled**, `needs.changes.outputs.code`
is the empty string. Empty `!= "true"`, so the gate took the docs-only branch and
**exited 0** — on a PR whose code-vs-docs classification was never established,
and where every downstream leg had been skipped for the same reason.

That is the slice-474 shape wearing a different hat: a leg that did not run,
read as a pass.

The new gate asserts `needs.changes.result == 'success'` **before** consulting
`CODE`, in both worlds, and fails closed otherwise. `changes` is unconditional
(no `if:`), so this cannot cost a legitimate PR anything.

This is a _tightening_. It does not relax the merge bar in any direction, which
the OE's boundaries forbid.

---

## D4 — Skip-tolerance is per-result, not per-branch (second hole closed)

The old gate short-circuited on `code != 'true'` and looked at nothing. The new
one always walks every leg, and forgives exactly one thing:

| leg result                              | `code == 'true'`               | `code != 'true'`                                   |
| --------------------------------------- | ------------------------------ | -------------------------------------------------- |
| `success`                               | OK                             | OK                                                 |
| `skipped`                               | **BLOCK** (the slice-474 hole) | OK — the slice-061 stub-twin posts the named check |
| `failure` / `cancelled` / anything else | **BLOCK**                      | **BLOCK**                                          |

`skipped` is the only forgiven non-success result, and only when there are no
Go-affecting changes. That is the same rule slice 631 wrote, expressed once
instead of as a branch.

The behavioural delta is confined to docs-only PRs and the three unconditional
legs (`precommit`, `actions-pin-check`, `openapi-drift-check`). Those have no
`if:`, so they never report `skipped`; previously the gate exited before
looking at them, so a RED one left the gate GREEN. Now it reddens the gate.

**This cannot brick a docs-only PR that would otherwise have merged**: all three
are already independently required contexts, so a PR with any of them RED was
already blocked. The change makes the aggregator honest about it, nothing more.

Path-filtered legs are safe to add because every one of them carries the
_identical_ guard `if: needs.changes.outputs.code == 'true'` — verified job by
job. A leg on a narrower filter would be `skipped` on an ordinary code PR and
would brick the gate; the `needs:` block now carries a comment saying so, and
`schema-removal-age` (gated on `changes.outputs.schemas`) is the concrete
example that stays out.

---

## D5 — Promote and complete in ONE change, prune in another

Three orderings were available:

| order                                                                  | verdict                                                                                                                                                            |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| promote first, fix `needs:` later                                      | **rejected.** An incomplete gate that is _required_ is worse than one that is advisory: it reads as a single trustworthy signal while silently omitting five legs. |
| fix `needs:` first, promote later                                      | rejected. Leaves the fixed gate advisory for another cycle for no gain — the completeness fix is precisely what makes promotion safe, so they belong together.     |
| **fix + promote together; prune `contexts` + delete stubs separately** | **chosen.**                                                                                                                                                        |

Pruning `contexts` down to `CI · merge-gate` (slice 701 AC-4) and deleting the
21 stubs (AC-5) stay out of this PR. Doing all four at once would swap an
enforced 18-name list for an unsoaked 1-name list in a single step, with no
intermediate state in which the new gate has demonstrated itself. The
intermediate state this slice creates — 19 required names, of which
`CI · merge-gate` is a _redundant_ 19th that overlaps 14 of the other 18 — is
deliberately belt-and-braces. If the promoted gate misbehaves, the 18 original
names are still enforcing underneath it, and reverting is one `git revert` plus
one `scripts/apply-branch-protection.sh` run.

**Cost accepted:** the four legs added under the slice-701 mandate
(`oscal-bridge`, `fuzz`, `frontend-lint`, `govulncheck`) become _effectively
required_ the moment `CI · merge-gate` is required, without having gone through
the slice-419 soak ritual. `govulncheck` in particular can go RED for reasons
unrelated to the PR (a newly-disclosed CVE in a dependency). That is the OE's
explicit instruction and it is the correct bar for a gate the stub collapse will
stand on — but it is a real new availability surface on the merge path, and it
is flagged here and in the PR body as a maintainer-review point rather than
buried.

---

## D6 — What is NOT tested, and the follow-up

The gate's decision logic has no automated test surface in this repo. It is
bash inside a `run:` block, exercised only by real CI runs, and the two holes
D3 and D4 closed were both found by reading it.

The proof runs below are the evidence for this slice. The durable fix — extract
the decision logic to `scripts/merge-gate-eval.sh` (reading `NEEDS_JSON` /
`CODE` from the environment exactly as it does now) with a
`scripts/merge-gate-eval_test.sh` table test, following the existing
`scripts/check-branch-protection-drift_test.sh` precedent — is deliberately out
of scope here: this PR changes branch-protection semantics and slice 701's
anti-criteria say that lands in isolation. Filed as a child OE.

---

## Proof 1 — fail-closed on a code PR (AC / OE step 5)

A real leg was forced to fail on this PR's branch, and `CI · merge-gate` went
RED as a result.

- **Forced failure:** commit `PROOF1_SHA` adds a deliberately-failing step to the
  `PROOF1_JOB` job in `ci.yml`.
- **Run (gate RED):** `PROOF1_RUN_URL`
- **merge-gate conclusion:** `PROOF1_CONCLUSION`
- **Revert:** commit `PROOF1_REVERT_SHA`
- **Run after revert (gate GREEN):** `PROOF1_GREEN_RUN_URL`

`PROOF1_LOG_EXCERPT`

## Proof 2 — docs-only PR, real legs skipped, gate GREEN (AC / OE step 6)

**A live docs-only PR carrying this change is structurally impossible, and that
is a property of the change, not a shortcut.** Any PR that contains the new
merge-gate necessarily edits `.github/workflows/**`, which is in the `changes`
job's `code` filter (`ci.yml`, the `filters:` block). So every PR that _has_ the
new gate is a code PR by construction, and `code == 'false'` cannot be observed
against it. Note also that `ci.yml` triggers on `push` only for `branches:
[main]`, so a docs-only follow-up commit pushed to this branch does not produce
a run either.

The docs-only path is therefore proved two ways, and closed by a third:

**(a) Deterministic execution of the shipped script.** The `run:` block was
extracted verbatim from `.github/workflows/ci.yml` (parsed out of the YAML, not
retyped) and executed against synthesized `toJSON(needs)` payloads matching what
GitHub produces. Eight scenarios, all as designed:

| scenario                              | `changes`             | legs                                                  | expected | actual                    |
| ------------------------------------- | --------------------- | ----------------------------------------------------- | -------- | ------------------------- |
| code PR, all green                    | success, `code=true`  | 19 × success                                          | exit 0   | exit 0, 20 legs asserted  |
| code PR, `govulncheck` RED            | success, `code=true`  | 1 × failure                                           | exit 1   | exit 1                    |
| code PR, shard `skipped`              | success, `code=true`  | 1 × skipped                                           | exit 1   | exit 1                    |
| code PR, `frontend-vitest` RED        | success, `code=true`  | 1 × failure                                           | exit 1   | exit 1                    |
| **docs-only PR**                      | success, `code=false` | 16 path-filtered × skipped, 3 unconditional × success | exit 0   | **exit 0**                |
| docs-only PR, `actions-pin-check` RED | success, `code=false` | 16 × skipped, 1 × failure                             | exit 1   | exit 1 (D4)               |
| `changes` job failed                  | failure               | all skipped                                           | exit 1   | exit 1 (D3)               |
| empty `needs` context                 | —                     | —                                                     | exit 1   | exit 1 (derivation guard) |

The docs-only row asserts exactly the AC-6/AC-7 semantics: 16 real legs
`skipped`, gate PASSES.

**(b) The live fail-closed run above** exercises the same loop, the same
`display()` map and the same jq derivation on a real runner. The only branch
Proof 1 does not reach is the `skipped`-tolerance arm, which (a) covers.

**(c) Closed by soak, before anything is deleted.** The first docs-only PR
opened after this merges will exercise the arm live. That observation is a
**hard precondition on the child slice** that deletes the stubs — the child must
not start until a docs-only PR has resolved `CI · merge-gate` green from the
skipped-legs path, which is the same soak slice 701 already demands. Filed as a
child OE so it is tracked rather than assumed.

---

## Branch-protection apply (OE step 7)

`.github/branch-protection.json` gains `CI · merge-gate` to
`required_status_checks.contexts` (18 → 19 names). Nothing was removed.

- `DRY_RUN=1 bash scripts/apply-branch-protection.sh` → payload validated
- `bash scripts/apply-branch-protection.sh` → `APPLY_RESULT`
- post-apply convergence (`scripts/check-branch-protection-drift.sh`) →
  `DRIFT_RESULT`

Reversal, if the promoted gate misbehaves: `git revert` this file's change and
re-run `scripts/apply-branch-protection.sh`. The PUT is a full-replace and
idempotent.

---

## Anti-criteria check

| slice 701 / OE-370 boundary                                            | held?                                                                                                 |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| No `-stub` job deleted                                                 | yes — `git diff` touches no `*-stub:` block; all 21 remain                                            |
| Does not change the merge bar's strictness downward                    | yes — every change is a tightening (D3, D4) or an addition (14+5 legs); no assertion was relaxed      |
| Does not remove the unconditional security guards from required checks | yes — nothing removed from `contexts`; D2 records that CodeQL + GitGuardian + DCO must _stay_         |
| Does not bundle another slice                                          | yes — the diff is `ci.yml`'s `merge-gate` job, `branch-protection.json`'s contexts list, and this log |
| PR left OPEN for maintainer review, not merged                         | yes                                                                                                   |
