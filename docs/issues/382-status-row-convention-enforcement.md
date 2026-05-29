# 382 — Enforce STATUS row convention: orchestrator-only edits + CI lint guard

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Three batches this session (slices 332, 365, 378) hit `docs/issues/_STATUS.md` merge conflicts because BOTH the orchestrator's claim-stake commit AND the engineer's PR commit edit the same row. The conflict surfaces only AFTER the orchestrator merges the claim-stake to main, making the engineer's PR DIRTY. Resolution requires manual conflict resolution (take engineer's version, since it includes spillover row additions) + force-push to the engineer's branch + CI re-run. ~5 min of orchestrator wall-clock per occurrence, plus the cognitive overhead of remembering the resolution rule.

**Root cause.** `Plans/prompts/04-per-slice-template.md` (the slice template engineers follow) contains a "flip slice doc status `ready` → `in-review`" step that targets `docs/issues/<NNN>-<slug>.md` (the slice's own doc). Recent engineers (slices 332/365/378) interpret this broadly and ALSO update the corresponding row in `_STATUS.md` — which is the orchestrator's domain. The template doesn't explicitly forbid this, so the drift is rational from the engineer's perspective.

**The convention going forward.** The orchestrator owns `_STATUS.md` entirely. Engineers only edit the slice's own doc and any in-scope code/docs. Claim-stake (`chore/status-batch-NNN-claim-stake`) adds the row; reconcile (`chore/status-batch-NNN-reconcile`) flips it to `merged`. Engineers do not touch `_STATUS.md` on their branches.

**The enforcement.** Three layers:

1. **Convention docs.** Update `Plans/prompts/04-per-slice-template.md` and `Plans/prompts/05-parallel-batch.md` to explicitly state engineer-side `_STATUS.md` edits are forbidden.
2. **CLAUDE.md mention.** One sentence in "Working norms" so the rule is discoverable from the load-bearing project doc.
3. **CI lint check.** Reject PRs from non-`chore/status-batch-*-{claim-stake,reconcile}` branches if `docs/issues/_STATUS.md` is modified. Catches future drift even if a future engineer reads the template differently OR if a `/idea-to-slice` invocation has a bug.

**Scope discipline.**

- DOES NOT change how engineers update their slice's own `docs/issues/<NNN>-<slug>.md` (they still flip `Status: ready` → `Status: in-review` there).
- DOES NOT remove the row from STATUS — orchestrator still adds it at claim-stake and flips to `merged` at reconcile.
- DOES NOT touch any engineer's already-merged work — purely forward-looking convention.
- DOES NOT change the orchestrator's responsibility (still owns claim-stake + reconcile + dedup).

**Trigger.** Recurring batch friction surfaced across slices 332, 365, 378 this session. The pattern is documented in batch 158/159/160 memories under "STATUS conflict resolution" + "engineer-vs-orchestrator STATUS race." User-confirmed file 2026-05-29 after session-stability analysis showed this is the single highest-leverage process fix available.

## Threat model

Process discipline + CI lint addition. STRIDE pass:

- **S (Spoofing):** CLEAN. No auth surface added or modified. CI lint runs in GitHub Actions context that already validates the actor's branch + commit.
- **T (Tampering):** The lint check could theoretically be bypassed by an attacker who could either (a) modify the workflow itself, or (b) name their malicious branch `chore/status-batch-NNN-claim-stake`. Both require write access to the repo, in which case `_STATUS.md` integrity is already compromised. CLEAN — defense-in-depth, not the only gate.
- **R (Repudiation):** CLEAN. No audit-log writes added; the existing CI run record + the orchestrator's reconcile commit are the audit trail.
- **I (Information disclosure):** CLEAN. The lint check just fails the PR; doesn't disclose anything not already in the diff.
- **D (Denial of service):** CLEAN. Lint check runs in <1s on a small grep + branch-name match.
- **E (Elevation of privilege):** CLEAN. No role check added or modified.

**Threat-model verdict:** CLEAN.

## Acceptance criteria

- [ ] **AC-1.** `Plans/prompts/04-per-slice-template.md` updated with explicit text: "DO NOT edit `docs/issues/_STATUS.md`. The orchestrator owns that file entirely via the claim-stake commit (adds row) + reconcile commit (flips to merged). Engineers only edit `docs/issues/<NNN>-<slug>.md` (the slice's own doc) and any in-scope code/docs."
- [ ] **AC-2.** `Plans/prompts/05-parallel-batch.md` updated similarly if/where it references STATUS row updates from engineer side. If no such references exist, document the absence in the decisions log.
- [ ] **AC-3.** `CLAUDE.md` "Working norms" section gains a one-line entry: "Engineers do not edit `docs/issues/_STATUS.md` — the orchestrator's claim-stake and reconcile commits are the only paths to the row."
- [ ] **AC-4.** New GitHub Actions job `_STATUS guard` that runs on every PR. Fails if `docs/issues/_STATUS.md` is in the PR diff AND the branch name does NOT match `^chore/status-batch-[0-9]+-(claim-stake|reconcile)(-.+)?$`. Mounted in `.github/workflows/ci.yml` or a new dedicated workflow — engineer's call recorded in D1.
- [ ] **AC-5.** The new job is wired into branch protection's required-checks list. Add to `.github/branch-protection.yml` (or equivalent — find the canonical config) so the lint blocks merge, not just shows a warning.
- [ ] **AC-6.** Lint job produces an actionable error message: "This PR modifies `docs/issues/_STATUS.md` but the branch `<branch>` is not a claim-stake or reconcile branch. STATUS rows are orchestrator-only — see `CLAUDE.md` 'Working norms' for the convention. If you need to flip a status row, ask the orchestrator to file the reconcile PR."
- [ ] **AC-7.** A synthetic-positive test: file a 1-commit branch named `engineer/test-status-guard` that modifies `_STATUS.md`, push, observe the lint failure. Document the verification in the decisions log. Then delete the synthetic branch.
- [ ] **AC-8.** A synthetic-negative test: existing reconcile-PR pattern (e.g. the most recent merged `chore/status-batch-NNN-reconcile` PR) still passes the lint. Document the verification.
- [ ] **AC-9.** Decisions log at `docs/audit-log/382-status-row-convention-enforcement-decisions.md` records: D1 lint job mount location (existing `ci.yml` vs new workflow); D2 error message wording finalization; D3 branch-name regex final form (handles both `chore/status-batch-NNN-claim-stake` and `chore/status-batch-NNN-reconcile-<suffix>` like slice 364's `chore/status-batch-160-reconcile-364`).
- [ ] **AC-10.** `pre-commit run --all-files` passes; `go mod tidy` clean (no Go changes expected, but verify per slice 377 lesson).

## Constitutional invariants honored

- **No change to data flow or persistence layer.** Pure process discipline + CI guard.
- **Audit trail unchanged.** Existing claim-stake/reconcile commit pair remains the canonical audit of every status row transition. CI guard adds an _additional_ audit (the PR check run record) without removing any existing one.

## Canvas references

- None directly. This slice is process discipline; the canvas describes product architecture.

## Dependencies

None. Pure config + docs slice.

## Anti-criteria (P0 — block merge)

- **P0-382-1.** Does NOT remove the engineer's ability to flip `Status: ready` → `Status: in-review` in the slice's OWN doc (`docs/issues/<NNN>-<slug>.md`). The slice doc IS the engineer's territory; only `_STATUS.md` is restricted.
- **P0-382-2.** Does NOT block reconcile-suffix branches like `chore/status-batch-160-reconcile-364` (slice 364's path). The regex MUST handle the `-<suffix>` form per slice 364's actual branch name.
- **P0-382-3.** Does NOT add the lint check as advisory-only — must hard-fail the PR to actually enforce the convention. Defer the choice (hard-fail vs advisory) per AC-1 of any future slice — this slice ships hard-fail per session-friction analysis.
- **P0-382-4.** Does NOT modify the `_STATUS.md` file itself in this PR (other than what the lint job's negative test exercises). The convention applies to THIS PR too — the slice doc lands via `docs/382-...` branch but the row is added by orchestrator's claim-stake (which is exempt). Slice 364 was the same pattern.
- **P0-382-5.** Does NOT introduce a new dependency (no `golangci-lint` plugin, no Go module, no npm package). The lint is a small Bash/Python check in the workflow.
- **P0-382-6.** Does NOT widen scope to other STATUS-style files (e.g. `docs/audit-log/...`, `CHANGELOG.md`). Just `docs/issues/_STATUS.md`.

## Skill mix (3-5)

- GitHub Actions workflow editing (the lint check)
- Bash / regex for branch-name matching
- Markdown editing for the convention docs
- Synthetic-test discipline (push + observe + clean up)

## Notes for the implementing agent

### Phase-2 grill output (from /idea-to-slice 2026-05-29)

- **Domain model:** "STATUS row convention" maps cleanly to `docs/issues/_STATUS.md`. No terminology drift.
- **Scope:** single coherent vertical (docs convention + CI guard).
- **Already-built check:** no prior slice addresses STATUS row enforcement. Slice 158 (slice 069 testing discipline) is the closest analog — it ratcheted four test surfaces; this slice ratchets one config surface.
- **Hidden finding:** the orchestrator's claim-stake branch sometimes uses a suffix (e.g. slice 364's `chore/status-batch-160-reconcile-364`). The regex in AC-4 MUST handle this — captured as P0-382-2 + D3.
- **Hidden finding:** the slice doc filing PR for this slice (the `docs/382-...` branch) ALSO modifies `_STATUS.md` (via the orchestrator's claim-stake pattern). The convention applies to docs-filing too — captured as P0-382-4.

### Phase-3 threat-model output

CLEAN. No new auth, no new endpoint, no new data flow. The lint check itself runs in GitHub Actions; can't be tampered without repo write access, in which case `_STATUS.md` integrity is already compromised (defense-in-depth).

### Implementation hints

- **Regex form** (proposed; engineer locks in D3): `^chore/status-batch-[0-9]+-(claim-stake|reconcile)(-[a-zA-Z0-9-]+)?$`. Handles `chore/status-batch-160-reconcile` AND `chore/status-batch-160-reconcile-364`.
- **GitHub Actions extraction.** Get the PR branch name via `${{ github.head_ref }}`. Get the changed files via `git diff --name-only origin/main...HEAD`.
- **Hard-fail mechanics.** `exit 1` in the bash step + the step name in branch protection's required-checks list.
- **Synthetic test artifacts** (AC-7, AC-8): use `gh workflow run` or a throwaway branch; clean up before merging this PR. The decisions log captures the test evidence (PR URL + observed lint output).

### Cross-references

- batch 158 memory `project_batch_158_closed.md` — "Conflict resolution surfaced the orchestrator-vs-engineer STATUS race in its severest form"
- batch 159 memory `project_batch_159_closed.md` — go-mod-tidy lesson (same engineer-side pre-push class)
- batch 160 (in flight) — slice 378 conflict resolved via take-engineer-side regex
- `Plans/prompts/04-per-slice-template.md` — convention spec being updated
- `Plans/prompts/05-parallel-batch.md` — workflow being updated if it references engineer-side STATUS edits
- `CLAUDE.md` "Working norms" — new sentence lands here

### Why this slice now

Session-stability analysis on 2026-05-29 surfaced this as the single highest-leverage process fix among the recurring failure modes. Three batches blocked per session × ~5min orchestrator wall-clock each = ~15min/session saved + the cognitive overhead of remembering the take-engineer-side resolution rule. CI guard makes the convention self-enforcing — no convention drift on future engineer interpretations.
