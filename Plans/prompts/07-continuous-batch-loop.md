# 07 — Continuous Batch Loop

Wraps `05-parallel-batch.md` in `/loop` dynamic mode so batches run unattended until the queue empties or a guard fires.

## What this is

Prompt `05-parallel-batch.md` is the unit of work — one orchestration cycle that picks ≤ 3 conflict-safe slices, builds them in parallel worktrees, and squash-merges them to `main`. Historically you've re-run that prompt by hand every ~2 hours.

This file wraps that loop. Four guards decide whether each iteration runs at all; two amendments adapt `05` for unattended operation; spillover-as-slice is reinforced so out-of-scope finds become future iterations' input automatically.

The loop is **stateless across invocations** — state lives in `docs/issues/_STATUS.md` (source of truth for the queue) and one local audit trail (`~/.claude/MEMORY/LEARNING/REFLECTIONS/continuous-batch.jsonl`). Restart is just `/loop <prompt body>` again.

## When to use

- Heads-down on something else and want the backlog to drain in the background
- Overnight / weekend runs while the ready set is deep
- Sustained throughput where you'd otherwise be the human bottleneck on the report-back gate

## When NOT to use

- Right after a constitution change — let the next manual batch surface any new open questions before unattended runs
- Right before a release — manual oversight on the last 2-3 slices catches release-readiness issues
- When the ready set is < 3 — manual run is fine; the wrapper buys nothing
- When you don't want PRs piling up overnight (the open-PR guard caps this, but consider the maintainer-cost of a 5-PR review backlog tomorrow morning)

## Invocation

Paste the prompt body below verbatim into a `/loop` invocation (no interval — dynamic-mode self-pacing):

```
/loop <prompt-body-from-this-file>
```

## Prompt

```
You are running the continuous-batch loop for security-atlas. Read this entire
prompt every iteration — each iteration is a fresh agent session with no inherited
context. Working directory is /Users/gmoney/Development/security-atlas; if it's
not the cwd, cd there first.

═══════════════════════════════════════════════════════════════════════════════
ITERATION GUARDS — run all four FIRST, before any work. Each guard that fires
ends THIS iteration immediately AND does NOT call ScheduleWakeup, so the loop
terminates cleanly until the human restarts it.
═══════════════════════════════════════════════════════════════════════════════

GUARD-1 · QUEUE EMPTY
  Read docs/issues/_STATUS.md. Count rows whose status is `ready` AND whose
  deps are all `merged` (a slice with one not-yet-merged dep is not actually
  pickable, no matter what its own row says).
  If the count is 0:
    - Print: "GUARD-1 fired: queue empty — loop terminated"
    - Append-to-audit-trail (see PER-ITERATION STATE PERSISTENCE below)
    - Exit without ScheduleWakeup

GUARD-2 · OPEN-PR CEILING (human-authored only)
  Run:
    gh pr list --repo mgoodric/security-atlas --state open --base main \
      --json number,title,author --limit 50 \
      | jq '[ .[] | select(.author.login as $a |
          ["dependabot[bot]","renovate[bot]","app/dependabot","app/renovate"]
          | index($a) | not) ] | length'

  This counts only HUMAN-authored open PRs. Bot-authored upgrade PRs
  (Dependabot, Renovate) are deliberately excluded — the maintainer
  triages those on their own cadence; they should not block the
  continuous loop from making feature-slice progress.

  If the resulting count >= 5:
    - Print: "GUARD-2 fired: 5+ human-authored PRs in flight — pausing loop"
    - Append-to-audit-trail
    - Exit without ScheduleWakeup

  Rationale: don't pile up review/CI backlog faster than the merge queue
  clears, but don't conflate "I haven't gotten to those Dependabot PRs
  yet" with "review backlog from this loop".

  Edit the bot-exclude list if other bots ship PRs to your default
  branch (snyk-bot, release-please, github-actions[bot] for auto-
  generated changelog PRs, etc.). Keep human reviewers and AI Engineer
  subagents in the count — both consume the same review budget.

GUARD-3 · MAINTAINER STOP FILE
  Check for /Users/gmoney/Development/security-atlas/.STOP_LOOP
  If it exists:
    - Print: "GUARD-3 fired: .STOP_LOOP present — loop terminated"
    - Append-to-audit-trail
    - Exit without ScheduleWakeup
  This is the maintainer's clean-stop primitive. The file persists across
  iterations until they remove it; resume = `rm .STOP_LOOP` then `/loop ...`.

GUARD-4 · UNEXPECTED-COMMIT DRIFT
  In the main worktree, run:
    git fetch origin main
    drift=$(git log HEAD..origin/main --oneline | wc -l | tr -d ' ')
  If drift > 0 AND no batch from THIS iteration could have produced those
  commits (since we haven't done any work yet this iteration, drift > 0 always
  means external):
    - Print: "GUARD-4 fired: <drift> external commit(s) on origin/main — pausing for maintainer review"
    - Append-to-audit-trail
    - Exit without ScheduleWakeup
  Rationale: an external push (maintainer, hotfix bot, or another session)
  invalidates this iteration's view of the world. Resume after the human
  reconciles.

If ALL four guards pass, proceed to the WORK PHASE.

═══════════════════════════════════════════════════════════════════════════════
WORK PHASE — execute Plans/prompts/05-parallel-batch.md verbatim, with the
two amendments below.
═══════════════════════════════════════════════════════════════════════════════

Read Plans/prompts/05-parallel-batch.md fully. Execute its prompt section (the
content inside the triple-backtick block) end-to-end, INCLUDING all six steps:
selection → claim-stake PR → parallel Engineers → merge order → merge queue
→ final reconcile PR.

AMENDMENT 1 · SKIP THE REPORT-BACK GATE
  Step 1 of 05 says "Report-back format (WAIT for my approval before spawning
  anything)". For continuous-loop runs, do NOT wait for human approval.
  Instead:
    a) Print the report-back content to the console (selection list, conflict
       analysis, migration sequence allocations, open-questions check) — this
       is the audit trail of WHAT this iteration picked and WHY
    b) If the report-back surfaces ANY of the following, do NOT proceed —
       escalate (see "ESCALATION" below) and exit:
         - A picked slice has an unresolved open question in
           Plans/canvas/11-open-questions.md
         - The conflict-safe subset has size 0 (everything ready is
           pairwise-conflicting with at least one other ready slice)
         - The orchestrator's confidence in the file-conflict prediction
           for any pick is < 70% (use judgement; over-pick is more
           expensive than under-pick here)
    c) Otherwise: proceed directly to Step 2 of 05 (worktree setup + status-
       only claim-stake PR) without waiting.

  All other conflict-safety rules in 05 apply unchanged. Only the human-
  approval gate is removed; the discipline behind it is not.

AMENDMENT 2 · SPILLOVER-AS-SLICE (reinforces existing convention)
  Per-slice subagents (Plans/prompts/04-per-slice-template.md) already capture
  out-of-scope finds as slices. Make this explicit in EACH subagent's prompt
  for continuous-batch runs:

    "If during this slice an out-of-scope bug, scope expansion, or unrelated
    tech-debt finding emerges that you cannot land in this PR, you MUST:
      1. Compute the next available slice number: run `ls docs/issues/[0-9]*.md
         | sed -E 's|.*/([0-9]+).*|\\1|' | sort -n | tail -1` and add 1
      2. Write the finding as docs/issues/<NNN>-<kebab-slug>.md using the
         slice format (Plans/prompts/04-per-slice-template.md is the source
         of truth; slice 051 is the closest hotfix-shape exemplar)
      3. In its Narrative, cite the parent slice: 'Surfaced during slice
         <PARENT-NNN>, captured as follow-up per continuous-batch policy.'
      4. Set its initial status to `ready` if all its deps are already
         `merged`; otherwise `not-ready` with the unmet dep listed.
      5. DO NOT modify _INDEX.md — the existing parallel-automation batch
         process owns that registration; just the slice file is enough.
      6. DO NOT attempt to fix the spillover finding in this PR — stay in
         scope."

  Track every spillover slice this iteration produced for the audit trail.

═══════════════════════════════════════════════════════════════════════════════
ESCALATION — paths that pause the loop pending human input
═══════════════════════════════════════════════════════════════════════════════

The loop pauses (exits without ScheduleWakeup) on any of:

  E-1 · A picked slice has an unresolved open question (Amendment 1b)
  E-2 · No conflict-safe subset exists (Amendment 1b)
  E-3 · A CI failure surfaces a design decision rather than a mechanical fix
        (this is 05's documented "STOP and ask" hard rule; honor it)
  E-4 · The orchestrator fails to recover an Engineer subagent after 2
        resume attempts (per 05's failure-mode playbook)
  E-5 · Branch protection rejects a status PR for an unexpected reason
        (not a transient CI fail — a structural reason)

On any escalation:
  - Print: "ESCALATION E-<N>: <one-line description>"
  - Append-to-audit-trail with outcome="escalated_<E-N>"
  - Write the full escalation context (the picked slice's surface, the
    failure mode, the design question, etc.) to
    ~/.claude/MEMORY/STATE/continuous-batch-escalation.md (overwrite)
  - Exit without ScheduleWakeup

The human reads continuous-batch-escalation.md, resolves the issue (answers
the open question, fixes the design call, runs `rm .STOP_LOOP` if applicable),
and restarts the loop manually.

═══════════════════════════════════════════════════════════════════════════════
CONTINUATION — schedule the next iteration
═══════════════════════════════════════════════════════════════════════════════

If the WORK PHASE completed successfully (final reconcile PR merged in Step 6
of 05) AND no escalation fired AND no guard fired:

  Call ScheduleWakeup with:
    - delaySeconds: 300
        (5 min — lets CI on the just-merged final-reconcile PR fully drain
        before the next iteration's GUARD-2 reads `gh pr list`)
    - reason: "post-batch <NNN1,NNN2,NNN3> CI cooldown; next iteration to
      drain queue"
    - prompt: pass back the SAME full prompt body the user originally
      invoked. Do NOT use the <<autonomous-loop-dynamic>> sentinel — that's
      for the parameter-less autonomous loop; this is a user-driven loop
      with an explicit prompt.

═══════════════════════════════════════════════════════════════════════════════
PER-ITERATION STATE PERSISTENCE — audit trail
═══════════════════════════════════════════════════════════════════════════════

Every iteration — guard-fired, work-completed, or escalated — appends ONE
JSONL line to:
  ~/.claude/MEMORY/LEARNING/REFLECTIONS/continuous-batch.jsonl

Schema:
  {
    "ts": "<ISO-8601 UTC>",
    "outcome": "<merged | guard_1 | guard_2 | guard_3 | guard_4
                  | escalated_E1 | escalated_E2 | escalated_E3
                  | escalated_E4 | escalated_E5>",
    "slices_picked": [<NNN>, ...] or null,
    "prs_opened": [<gh-pr-number>, ...] or null,
    "prs_merged": [<gh-pr-number>, ...] or null,
    "spillover_slices_created": [<NNN>, ...] or null,
    "wall_clock_seconds": <int> or null,
    "next_iteration_scheduled": <bool>
  }

Also overwrite (single line):
  ~/.claude/MEMORY/STATE/continuous-batch-latest.txt

Format:
  <ISO-8601> · <outcome> · merged: <NNN1,NNN2,NNN3 | none> ·
  spillover: <NNN4,NNN5 | none>

The maintainer can `cat` continuous-batch-latest.txt for one-line status
without reading transcripts.

═══════════════════════════════════════════════════════════════════════════════
HARD RULES (continuous-loop-specific, supplement 05's)
═══════════════════════════════════════════════════════════════════════════════

- NEVER skip a guard. Guards exist because unattended runs need explicit
  exit conditions.
- NEVER call ScheduleWakeup on a guard-fired or escalation path. Loop
  termination MUST be explicit.
- NEVER reduce delaySeconds below 300 — under-5-minute waits churn the
  cache and risk CI race conditions on rebases.
- NEVER chain more than ONE batch from a single agent turn. Each iteration
  is a fresh ScheduleWakeup — that's the contract.
- NEVER modify _INDEX.md as part of spillover-slice creation. The parallel-
  automation batch process owns _INDEX.md registration (memory note: slices
  059-064 etc. are in docs/issues/ but not yet in _INDEX.md by design).
- NEVER edit Plans/prompts/05-parallel-batch.md from inside the loop —
  that's a meta-change requiring human review. Surface as escalation E-3
  if 05 itself seems to need updating.
```

## What to expect

- Each iteration: ~2-3hr wall-clock (≈ max slice estimate + ~12 min status-PR overhead)
- Audit trail: one JSONL line per iteration in `~/.claude/MEMORY/LEARNING/REFLECTIONS/continuous-batch.jsonl`
- One-line status: `~/.claude/MEMORY/STATE/continuous-batch-latest.txt` (overwritten each iteration)
- Escalation context (when it fires): `~/.claude/MEMORY/STATE/continuous-batch-escalation.md`
- Theoretical max throughput is bounded by GUARD-2 (5 open human-authored PRs) plus per-batch wall-clock (~2-3hr) — at three slices per iteration that's ~24-36 slices/day if you keep approving merges fast enough to stay under the open-PR ceiling
- Spillover slices accumulate in `docs/issues/`; future iterations pick them up automatically once their deps merge

## How to stop the loop mid-run

Three primitives, in order of preference:

1. `touch /Users/gmoney/Development/security-atlas/.STOP_LOOP` — GUARD-3 fires at the start of the next iteration. Most graceful (current iteration finishes, no abrupt interrupt).
2. Direct Ctrl-C on the `/loop` session if you're at the terminal.
3. Wait — the queue-empty guard (GUARD-1) hits when nothing's ready, the open-PR ceiling (GUARD-2) hits if you've stalled CI/review.

## How to resume

1. If you used `.STOP_LOOP`: `rm /Users/gmoney/Development/security-atlas/.STOP_LOOP`
2. If escalation E-3 (design call) fired: resolve the design question (update `Plans/canvas/11-open-questions.md` or the relevant slice), then re-run `/loop <prompt body>`
3. Otherwise: just re-run `/loop <prompt body>`. State is in `_STATUS.md` + the latest-state file; the loop is stateless across invocations.

## Tuning knobs

The ceilings + caps in the prompt body are picked from current scale:

| Knob                        | Default               | When to lower                                    | When to raise                                                                                                                                                         |
| --------------------------- | --------------------- | ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Open-PR ceiling (GUARD-2)   | 5 human-authored      | If review backlog is hurting                     | If you have parallel reviewers. Bot PRs (Dependabot/Renovate) are excluded by default — edit the jq filter in the prompt to include or exclude additional bot logins. |
| ScheduleWakeup delaySeconds | 300                   | Don't — < 300 races CI                           | Raise to 1200-1800 for "check back hourly" feel                                                                                                                       |
| Conflict-safe subset cap N  | 3 (inherited from 05) | If predictions are uncertain (per 05's guidance) | Don't — N=4+ is 05's documented anti-pattern                                                                                                                          |

Edit those numbers in the prompt body directly. The wrapper is yours to tune; `05-parallel-batch.md` should stay untouched (it's the unit of work — version it independently).

## Why a wrapper, not an edit to 05

`05-parallel-batch.md` codifies a single batch's quality discipline. Re-running it manually is the right interaction for high-judgement runs (constitution changes, pre-release sweeps). The continuous loop is an _additional_ mode on top, not a replacement. Keeping them separate means:

- The per-batch quality bar stays untouchable
- The loop's guards / amendments are visible and editable in one place
- `/loop` semantics (dynamic-mode `ScheduleWakeup`) compose with the work prompt rather than being entangled in it
- A future "weekly batch" or "release-gated batch" wrapper can sit alongside this one without disturbing 05
