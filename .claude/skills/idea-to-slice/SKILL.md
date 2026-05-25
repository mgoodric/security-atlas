---
name: idea-to-slice
description: Turn a small product idea into a tracer-bullet vertical slice (one cohesive slice doc at docs/issues/NNN-slug.md) following security-atlas's slice convention, with explicit security threat-model analysis as a required design phase. USE WHEN turn idea into slice, draft slice, create slice from idea, slice generator, tracer bullet slice from idea, add slice to backlog, write me a slice for, capture this as a slice.
---

# /idea-to-slice — Idea → tracer-bullet vertical slice (with threat model)

Codified pipeline for turning a small product idea into a full slice doc that conforms to `Plans/prompts/04-per-slice-template.md`, with a mandatory security-analysis phase folded into the design flow. Output: one `docs/issues/<NNN>-<slug>.md`, one feature branch, one DCO-signed commit, one PR.

## When to use

- User has a small product idea ("compliance calendar", "Dependabot review prompt", "/settings page", "control-cadence events on the calendar") and wants it materialized as a properly-formatted slice
- Spillover from another slice's grill — a finding that's out of scope for the current slice but should become a future slice
- Bug report that warrants more than a hotfix — needs a vertical slice with proper ACs

## When NOT to use

- For obvious bug fixes (just write the fix; if it's substantial, file a slice manually with one AC)
- For tracking-only items (file as a GitHub issue directly, not a slice)
- For ideas that need clarification first — use `AskUserQuestion` to refine the idea BEFORE invoking this skill
- For meta-process changes (CLAUDE.md edits, prompt-file edits, CI workflow changes) — those go through normal PR flow, not the slice convention

## Prerequisites + skill composition

This skill is a thin orchestrator that composes existing skills. Check what's installed before invoking; missing skills cause the workflow to fall back to less precise alternatives or fail loudly with the install command.

### Required (must be present)

- PAI built-ins: `Read`, `Write`, `Edit`, `Bash`, `Glob`, `Grep`, `AskUserQuestion`, `WebFetch` (all standard Claude Code)
- `Plans/prompts/04-per-slice-template.md` (slice format spec — the skill's output conforms to this)
- `Plans/prompts/05-parallel-batch.md` (workflow context for how slices get picked up)
- `docs/issues/_STATUS.md` (read-only — for next-slot computation + dep status)

### Preferred (slot directly into specific phases)

| Skill             | Source                                                                                              | Phase                                                                               | Fallback if not installed                                                                                                                                                          |
| ----------------- | --------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `grill-with-docs` | mattpocock/skills (typically `~/.agents/skills/grill-with-docs/`; symlink into `~/.claude/skills/`) | Phase 2 — domain-model grill                                                        | Skill emits a structured grill-prompt and asks the user to paste back the grill output, OR falls back to inline self-grilling using the FirstPrinciples mode of PAI Thinking skill |
| `Security` (PAI)  | `~/.claude/skills/Security/`                                                                        | Phase 3 — threat model                                                              | Skill performs an inline STRIDE-style threat enumeration using its own checklist below (less polished but functionally equivalent for slice-design purposes)                       |
| `to-issues`       | mattpocock/skills                                                                                   | Phase 6 alt-path — if the slice exceeds 1.5d and warrants splitting into sub-issues | Skipped; the single-slice output is the deliverable                                                                                                                                |

### Optional (would improve fidelity if installed; not installed by default per your skill inventory)

- **`to-prd` (mattpocock)** — Would replace Phase 4 draft logic with a more structured PRD-first generation. Install:
  ```
  npx claude-code-templates@latest -y --skills "mattpocock/to-prd"
  ln -s ~/.agents/skills/to-prd ~/.claude/skills/to-prd
  ```
- **`grill-me` (mattpocock)** — Would replace Phase 5 pressure-test with a Socratic grilling pass that surfaces hidden assumptions. Install same pattern as above.

### Not installed AND should not block

- If `grill-with-docs` is missing, this skill DOES NOT auto-install it. Instead it emits a one-line note: "Phase 2 fidelity reduced — install mattpocock/grill-with-docs for proper domain-model grilling." Then it falls back to inline self-grilling.
- Same fallback discipline for `Security`, `to-prd`, `grill-me`.

The fallbacks are intentionally lossy — running this skill with everything installed produces higher-quality slices than running it bare. Call out the gap explicitly to the user so they can decide whether to install the missing skill or accept the reduced fidelity.

## Workflow

### Phase 0 — Normalize the idea

Take the idea description from the slash-command argument. If the description is fewer than 5 words OR omits any of (cluster | rough scope | what triggered the idea), invoke `AskUserQuestion` with 1-3 questions:

- **Cluster**: Frontend / Backend / Infra / Quality / Security / Multi-tenancy / etc. — pick from the cluster list in `Plans/prompts/04-per-slice-template.md` or in observed clusters across `docs/issues/*.md`
- **Rough scope**: 0.5d (single function + test) / 1-2d (one new package or page) / 3d+ (multi-package, schema change, multi-surface)
- **Trigger**: Bug surfaced → spillover slice / Roadmap item → planned slice / User research → product slice / Operator pain → ergonomics slice

If the idea is clear (5+ words, clear cluster + scope inferable), skip the questions and proceed.

### Phase 1 — Context recovery

Read in parallel (one message, multiple Read/Glob/Grep calls):

1. `CLAUDE.md` — constitutional invariants
2. Most recent 3 slices in `docs/issues/[0-9]*.md` (by mtime) — for current convention + voice
3. `Plans/prompts/04-per-slice-template.md` — slice format spec
4. `docs/issues/_STATUS.md` — for next slot computation + which deps are merged
5. `Plans/canvas/*.md` — search for the idea's domain keyword (`rg -l "<keyword>" Plans/canvas/`)
6. `internal/api/` directory listing — look for the package(s) the idea touches
7. `web/app/` route listing — look for related pages/mockups
8. `migrations/sql/` — most recent 5 migrations for current schema state

Compute:

- **Next slice slot**: `ls docs/issues/[0-9]*.md | sed -E 's|.*/([0-9]+).*|\\1|' | sort -n | tail -1 | awk '{print $1+1}'` then verify no open PR is using that slot via `gh pr list --search "in:title slice <NNN>"`
- **Closest existing slices** (by cluster + topical keyword): for the slice's anti-criteria + dependencies sections
- **Open questions check**: search `Plans/canvas/11-open-questions.md` for the idea's keyword — if a hit, surface that to the user BEFORE proceeding (the slice may need to wait on a design decision)

### Phase 2 — Grill (preferred: `grill-with-docs`)

If `grill-with-docs` is installed:

> Invoke `/grill-with-docs` against the idea + the context gathered in Phase 1. The grill should produce:
>
> - Terminology that's wrong vs the existing domain model
> - Scope that drifts (the idea as stated may include scope that belongs in a different slice)
> - Constitutional invariant violations (idea that contradicts CLAUDE.md or an existing ADR)
> - Spillover candidates (parts of the idea that should be split off into separate slices)

If `grill-with-docs` is NOT installed:

> Self-grill using this checklist:
>
> 1. **Domain model**: Does the idea use terminology consistent with `internal/api/<package>/` exports + existing slices? Flag every term that diverges; propose the canonical name.
> 2. **Scope creep**: Does the idea bundle 2+ tracer-bullet vertical surfaces (e.g. "a calendar page AND a settings page")? Split into separate slices.
> 3. **Constitutional invariants**: Does the idea touch RLS, auth, tenancy, audit-log writes, secrets handling? Each touch is a constraint surface that needs explicit anti-criteria in the slice.
> 4. **Already-built check**: Does an existing slice already do this? `rg -l "<keyword>" docs/issues/` — if yes, the new slice is either redundant or an extension; clarify with the user.

Emit grill output as a structured block in the slice's "Notes for the implementing agent" section so the implementing engineer sees the design decisions that shaped the slice's scope.

### Phase 3 — Security analysis / threat model (REQUIRED)

This phase is **mandatory** for every slice — even ones that don't obviously touch security. The point is that a security analysis as part of the _design_ phase catches threats before they're baked into ACs, instead of in security-review during merge.

If `Security` skill is available:

> Invoke `Security` skill in threat-model mode against the idea. Provide:
>
> - The idea description
> - The data flow inferred from Phase 1 (which packages produce data, which consume it, what crosses the tenant boundary)
> - The auth boundary (which endpoints are authenticated, which are not, which require admin role)

If `Security` skill is not available OR for a faster inline pass, run a STRIDE-style enumeration:

**S — Spoofing** (authentication threats)

- Does the slice add new authenticated endpoints? List them.
- Does the slice add unauthenticated endpoints? (calendar.ics token, /health, etc.) — Justify each one.
- Could a forged identity (stolen cookie, replayed bearer, token-collision) reach the new surface?

**T — Tampering** (integrity threats)

- Does the slice accept user input that becomes part of a query, file path, command, or URL? List each input + the validation that gates it.
- Does the slice modify data that other slices read? What's the contention model?

**R — Repudiation** (audit-log threats)

- Does the slice perform any operation that should leave an audit trail?
- Are existing audit-log writes (decision_audit_log, evidence_audit_log, etc.) covered for the new surface?

**I — Information disclosure** (confidentiality threats)

- What tenant-scoped data does the slice expose? Is RLS enforced on every read path?
- Does the slice cache or log data in a way that could leak across tenant boundaries?
- Does the slice's output (JSON, ICS, logs) leak more than the minimum needed (e.g. exposing internal IDs, debug strings)?

**D — Denial of service** (availability threats)

- Does the slice accept unbounded inputs (large query windows, paginated reads without caps, unbounded file uploads)?
- Could a malicious or buggy caller cause resource exhaustion (DB query plan blowup, memory growth, infinite recursion)?

**E — Elevation of privilege** (authorization threats)

- Does the slice introduce a new role check? Is it enforced consistently across every endpoint that touches the new surface?
- Could a non-admin caller reach an admin-only operation through the new code path?
- Does the slice cross the `atlas_app` / `atlas_migrate` / `atlas_service_account` role boundary? (See `internal/db/integration_test.go` for the canonical role model.)

For each STRIDE category, emit:

- **Identified threats**: 1-3 sentences per threat
- **Mitigations**: how the slice's design (or existing infrastructure) handles each
- **Anti-criteria** to add to the slice: the explicit "DOES NOT" guards that prevent the threat from being introduced during implementation

The threat-model output becomes a new section in the slice doc titled `## Threat model` (similar to slice 051's existing threat-model section). The mitigations become explicit ACs; the anti-criteria are added to the P0 list.

### Phase 4 — Draft slice (preferred: `to-prd`; fallback: inline)

If `to-prd` is installed:

> Invoke `to-prd` with the idea + Phase 1 context + Phase 2 grill output + Phase 3 threat model. Map its PRD output to the security-atlas slice format (the per-slice-template at `Plans/prompts/04-per-slice-template.md`).

If `to-prd` is not installed (default):

> Draft the slice inline following the per-slice-template structure verbatim. Required sections in order:
>
> 1. `# <NNN> — <Title>` (title is action-oriented, < 80 chars)
> 2. `**Cluster:** ... **Estimate:** ... **Type:** AFK | HITL | JUDGMENT`
> 3. `## Narrative` — 2-4 paragraphs covering WHY (what's broken or missing today), WHAT (the deliverable shape), and SCOPE DISCIPLINE (what's deliberately out)
> 4. `## Threat model` — output from Phase 3 (NEW vs the existing template; required for every slice generated by this skill)
> 5. `## Acceptance criteria` — atomic ACs grouped by surface (backend / frontend / tests / docs). Each AC is binary-testable. Pass the Splitting Test from the per-slice-template.
> 6. `## Constitutional invariants honored` — name the invariants from CLAUDE.md the slice respects
> 7. `## Canvas references` — pointers into `Plans/canvas/*.md`
> 8. `## Dependencies` — `#NNN (merged|in flight)` per dep slice
> 9. `## Anti-criteria (P0 — block merge)` — explicit "does NOT" guards, INCLUDING the security ones from Phase 3
> 10. `## Skill mix (3-5)` — the skills the implementing engineer needs
> 11. `## Notes for the implementing agent` — context the per-slice template's grill won't surface (design decisions, threat-model context, references to MEMORY/WORK if surfaced from a deploy session, etc.)

The AC count should be:

- 0.5d slices: 5-10 ACs
- 1-2d slices: 10-20 ACs
- 3d+ slices: 20-30 ACs

If the count drifts above 30, the slice is too big — split before continuing.

### Phase 5 — Pressure-test (preferred: `grill-me`; fallback: PAI Thinking Red Team mode)

If `grill-me` is installed:

> Invoke `/grill-me` on the drafted slice. The output should be a list of hidden assumptions, fragile ACs, missing edge cases, or scope-creep risks. Apply each finding back to the slice draft.

If `grill-me` is not installed:

> Invoke `Skill("Thinking")` with Red Team mode against the slice draft. Ask it to find:
>
> - ACs that hide compound requirements (Splitting Test failures)
> - Anti-criteria that are too broad ("do not break anything" is meaningless — be specific)
> - Constitutional invariants the slice claims to honor but doesn't enforce
> - Missing test ACs (every code-changing AC needs a corresponding test AC)
> - Threat-model gaps (re-run STRIDE checklist as a verification pass)

Apply Red Team findings back to the draft. Iterate at most twice (first round = obvious fixes; second round = subtle ones); a third round usually means the slice is wrong-shaped and needs Phase 0 / Phase 1 revisit.

### Phase 6 — Write + register + commit + PR

1. Verify next slice slot is still free: `ls docs/issues/[0-9]*.md` plus `gh pr list --search "<NNN>"` — automation may have filled it during Phases 2-5.
2. If slot is taken: increment + retry. Don't silently overwrite.
3. Switch to `main`, pull, branch as `docs/<NNN>-<kebab-slug>` (e.g. `docs/094-compliance-calendar`).
4. Write `docs/issues/<NNN>-<slug>.md`.
5. **Register the slice in `docs/issues/_STATUS.md` in the SAME commit** — this is load-bearing for the continuous-batch loop's GUARD-1 (which reads the canonical Status table, not the slice files). Two edits required:
   a. Add a canonical row under the `## Status table` section. Place it in numeric order (immediately after the row whose number is `<NNN> - 1`). Template:

   ```
   | <NNN> | <Title — copy from the slice's `# <NNN> — <Title>` line, may truncate parentheticals to fit> | `ready` (or `not-ready` if any dep is unmerged) | — | — | — | — | <one-line notes: cluster · type · estimate · short rationale or provenance reference> |
   ```

   **Status value (hard rule):** the new row's status MUST be `ready` (spec landed; implementation pending) — NOT `merged`. `merged` is reserved for slices whose **implementation has shipped**, not for slices whose **spec PR has merged**. The two are different events: the spec PR `/idea-to-slice` produces is design work; the implementation lands later via a separate `feat:` PR opened by the continuous-batch loop or a maintainer. Use `not-ready` only when at least one dependency is still unmerged AND the dependency is technical (a slice the implementation imports from), not editorial (e.g., "I want to write the audit doc first" is not a not-ready blocker).

   Historical incident (2026-05-25): a housekeeping PR conflated "spec PR for slice 277 merged" with "slice 277 implementation merged" and set the canonical row to `merged`. The loop's GUARD-1 then skipped slice 277 entirely (counts only `ready` rows). Diagnosed + corrected via a status-only follow-up PR. Avoid by keeping the spec/impl distinction load-bearing: spec landing = `ready`; implementation landing = `merged`.

   b. Add a small drift block ABOVE the most recent existing drift block. Template:

   ```
   ## Drift detected — YYYY-MM-DD (slice <NNN> filed via /idea-to-slice)

   <one-sentence summary: what surface, what cluster, what user-confirmed scope>.

   | Row | Transition | Evidence |
   | --- | --- | --- |
   | <NNN> | (new row) → `ready` | spec at `docs/issues/<NNN>-<slug>.md` · PR pending |
   ```

   c. Update the file's top-of-file `**Last reconciled:** YYYY-MM-DD (...)` marker to reflect the addition.

6. Run `pre-commit run --files docs/issues/<NNN>-<slug>.md docs/issues/_STATUS.md` to catch prettier reformat. Re-stage if hook modified either file.
7. Commit with DCO sign-off (`git commit -s`) using a Conventional Commit subject like `docs(issues): add slice <NNN> — <title>`. Body summarizes scope + key anti-criteria + provenance ("surfaced YYYY-MM-DD during X"). The slice file AND the `_STATUS.md` edits go in **one commit** — they're one logical unit ("this slice exists + here's the row to track it").
8. Push with `-u` (`git push -u origin docs/<NNN>-<slug>`).
9. Open PR with `gh pr create --base main --head docs/<NNN>-<slug>` and a body that mirrors the commit body plus the threat-model summary.
10. Update the PR body with the canonical row's PR number once `gh pr create` returns it (so the `_STATUS.md` row's notes can cite `gh#<N>`). Optional polish: amend the commit with the resolved PR number in the row.
11. Return the PR URL to the user.

**Why the `_STATUS.md` registration step is mandatory** (Phase 6.5 in spirit, integrated into Phase 6 as step 5):
The continuous-batch loop (`Plans/prompts/07-continuous-batch-loop.md`) reads `_STATUS.md` as its single source of truth for pickable work via GUARD-1. A slice file with no canonical row is invisible to the loop — it cannot be picked, cannot be dispatched, cannot be reconciled. Historical sessions (slices 273, 274, 276, 277, 278, 279) all hit this gap and required follow-up reconcile PRs to register their rows retroactively — net cost ~3-5 min churn per slice. Codifying the registration in the same commit eliminates the gap permanently.

**Branch-switch defense:** the security-atlas parallel-batch automation frequently switches the checked-out branch mid-session. After every git operation, re-verify `git branch --show-current` matches the intended branch. If not: stash if dirty, re-switch, cherry-pick the commit onto the right branch, force-push with explicit `--force-with-lease=<branch>:<expected-sha>`. (Session 2026-05-15 hit this pattern three times; the recovery dance is now standard.)

### Phase 7 — Spillover capture

If Phase 2 (grill) or Phase 3 (threat model) surfaced findings that warrant their own slices (rather than being scope-creep on the primary slice), file each as an additional slice via the same skill on subsequent slots. Note the parent-slice reference in each spillover slice's narrative ("Surfaced during slice <NNN> design via /idea-to-slice").

Spillover discipline matches `Plans/prompts/07-continuous-batch-loop.md` Amendment 2: do NOT bolt spillover findings onto the primary slice — they get their own slot.

## Output contract

Per invocation:

- **Primary deliverable**: one `docs/issues/<NNN>-<slug>.md` + a `_STATUS.md` row registration (same commit) + one branch + one DCO-signed commit + one PR URL
- **Secondary deliverables** (only if surfaced): N spillover slice PRs, each labeled with parent-slice reference; each ALSO carrying its own `_STATUS.md` row registration
- **Console summary**: 5-line summary including PR URL, AC count, threat-model verdict (CLEAN / has-mitigations / HOLD-pending-review), the canonical row's status (`ready` / `not-ready`), and any "skill not installed" call-outs

## Hard rules

- **NEVER** skip Phase 3 (threat model). Even "obviously safe" slices (UI tweaks, doc updates) get the STRIDE pass — the cost is 60 seconds; the benefit is that a security regression that emerges months later has a design-time audit trail.
- **NEVER** write a slice without first checking `docs/issues/_STATUS.md` for an open question that blocks the idea. Surface the open question to the user instead of guessing.
- **NEVER** silently install a missing dependency skill. Call it out to the user with the install command and let them decide whether to install or accept the fallback.
- **NEVER** write more than ONE primary slice per invocation. If the idea genuinely needs N slices, file the first one + N-1 spillover stubs; do NOT bundle into one mega-slice.
- **NEVER** commit a slice file directly to `main`. The PR is the gate; branch protection enforces it anyway, but the skill should not even try.
- **NEVER** ship the slice file without the matching `_STATUS.md` row registration in the same commit. A slice with no canonical row is invisible to the continuous-batch loop's GUARD-1 — it cannot be picked. The skill's Phase 6 step 5 is mandatory; skipping it forces a retroactive reconcile PR (~3-5 min churn) and risks the row being forgotten entirely.
- **NEVER** set the new canonical row's status to `merged` just because the spec PR merged. `merged` is reserved for slices whose **implementation** has shipped; the spec PR landing is the start of the slice's life, not the end. Spec landing = `ready`. Implementation landing (a later, separate PR) = `merged`. Conflating the two hides ready work from GUARD-1.
- **NEVER** use vendor-prefixed test fixture tokens in any test reference within the slice — neutral `test-*` only (per slice 05's documented convention).
- **NEVER** auto-merge the resulting PR. The slice is design work; the maintainer reviews it before it enters the batch queue.

## Anti-patterns (don't do these)

- **One-shot mega-slice from a one-line idea.** If the user says "build the whole reporting subsystem", invoke `AskUserQuestion` to decompose. Don't generate a 50-AC slice that nobody will pick up.
- **Threat-model rubber-stamp.** If Phase 3 returns "no threats identified" for a slice that introduces a new authenticated endpoint, the analysis was lazy. Re-run with explicit STRIDE per category.
- **Cargo-cult slice numbering.** Always recompute the slot at Phase 6 — the slot you computed in Phase 1 may have been claimed by parallel automation in the intervening minutes.
- **Skipping the Phase 2 grill on "obvious" slices.** Even obvious slices benefit from terminology alignment — half the per-slice grill stalls in the existing batch workflow come from slices whose terminology drifted at design time.

## Provenance

Generated 2026-05-15 from Matt's request to consolidate the manual idea→slice pipeline that produced slices 091, 092, 093, 094 and prompts 07, 08 during the security-atlas Unraid-deploy session. Composes with the existing prompt files (`05-parallel-batch.md` picks the resulting slice up; `07-continuous-batch-loop.md` drives the loop). Threat-model phase added per maintainer request to embed security analysis in the design phase rather than discovering threats at security-review time.

The composition pattern (use mattpocock skills where available, fall back to PAI when not) is intentional — pocock skills are tightly-scoped and excellent at their narrow job; PAI skills are broader and provide reasonable fallbacks. Adding new mattpocock skills (`to-prd`, `grill-me`) to the installed set strictly improves slice fidelity; deferring those installs degrades gracefully.
