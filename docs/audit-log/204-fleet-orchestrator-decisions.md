# Slice 204 — Fleet orchestrator decisions log

Decisions made by the orchestrator-engineer (not by the per-page Agents) while dispatching + collecting the slice 204 audit fleet on 2026-05-23.

## D1 — Fleet concurrency

**Decision**: 4 in-flight agents per wave, 3 waves total (4 + 4 + 3 = 11 pages).

**Rationale**: matches slice 204 AC-9's "max 4 in-flight" cap. Three waves rather than two waves of 6 because:

- The Agent tool's background invocation pattern blocks completion notification to the orchestrator's session; staging 4 at a time keeps the completion-notification stream readable.
- Anthropic API rate-limit / overload risk is non-trivial at 11 concurrent subagents.
- Each agent runs ~5-10 min wall-clock; 3 waves of 4 = ~30 min serialized worst-case, acceptable.

**No deviation from the cap occurred.** Every wave completed all 4 (or 3 in wave 3) agents successfully on first try; no respawns.

## D2 — Reduced-scrutiny mockup pages

**Decision**: `/` (index) and `/questionnaires` received explicit "lighter-scope" prompts — agents instructed to file 1-3 findings (not 5) and not strain to fill the budget.

**Rationale**:

- `index.html` is a mockup-internal navigation page (lists the other mockups). There's no equivalent live "product" page; the live `/` simply redirects. The audit scope collapses to redirect-honesty + dead-link integrity + mockup-coverage gaps.
- `questionnaire.html` corresponds to a feature whose frontend doesn't ship yet — slice 155 backend merged but its frontend was deferred to a never-filed "slice 156". The primary finding is the gap itself; secondary findings (mockup-stale items) are bonuses.

**Outcome**: both agents respected the cap (index used 2/5 spillover slots; questionnaire used 2/5). No "found 5 findings just because budget allowed" gaming.

## D3 — De-duplication strategy

**Decision**: agents instructed to read existing slice 154 + slice 178 spillovers + dedup against them BEFORE filing. No post-fleet dedup pass by the orchestrator.

**Rationale**: each agent has full repo context and can dedup against existing slices cheaply via `grep -l "parent.*<NNN>"` and `git log --all --oneline -- docs/issues/`. Centralizing dedup at the orchestrator would require collecting all 49 spillover specs + comparing pairwise, which is a much heavier post-processing step.

**Outcome**: settings agent (PR #524) explicitly skipped 11 slice-154 spillovers and documented the verification — F1/F2/F4 verified live, F3/F5/F7/F10 trusted from merged PR #338, F6→162, F8→163, F11→164-171 chain. Risks agent dedupped against slice 185 (row-click) without re-filing. Controls agent did not re-file slice 100's already-merged sidebar-drop concern. Cross-agent dedup happened correctly at the cross-cutting "top-bar chrome" boundary — 6 separate findings (#213, #223, #228/#229, #235, #243, #257) each cite the cross-cut nature and the aggregate report flags this as a candidate umbrella slice.

The pattern that did NOT dedup well: cross-cutting patterns like "disabled button without tooltip" appear 5 times in the catalog. The orchestrator's aggregate report (`204-aggregate.md`) flags this for maintainer attention rather than retroactively merging spillovers.

## D4 — Slice 178 vs slice 204 boundary

**Decision**: slice 204 is the **judgment-level parity pass**, distinct from slice 178's **heuristic-rule pass**. The two coexist; agents were instructed to use slice 178's `makeReadOnly(page)` + `web/e2e-audit/` patterns for read-only enforcement but to NOT re-run slice 178's heuristic engine.

**Rationale per slice 204 spec**:

> Re-running slice 178's harness — that harness ran already (8 findings, 6+2 split). This slice is the human-judgement parity pass, not a heuristic-rule pass.

**Outcome**: agents focused on judgment-level findings (does the live page match the mockup's _intent_, not just its DOM?). Categories (iii) data-bound-honesty and (iv) mockup-stale are inherently judgment-class; (i) layout and (ii) broken-interaction overlap with slice 178's heuristic class but with more semantic nuance.

## D5 — CI-delta scan

**Decision**: every per-page PR ran the standard CI delta on its own; the orchestrator's aggregate PR (this PR) ran the same delta on the aggregate + decisions log + CHANGELOG addition.

**Outcome to date** (at the time of writing, before this aggregate PR opens):

- All 11 per-page PRs opened successfully
- All 11 hit one prettier auto-format amend cycle (consistent expected behavior — markdown tables get realigned)
- Zero CodeQL findings (no code changes; pure docs)
- Zero pre-commit hook failures after the amend cycle
- Zero AC-7 (secret scrub) violations — all 11 reports verified clean of Bearer/cookie/JWT values

## D6 — Target choice (atlas-edge vs local docker-compose)

**Decision**: atlas-edge live deployment, not local docker-compose.

**Rationale**: per slice 204 AC-8, audit runs against the well-known dev-seed dataset (no real-tenant content). atlas-edge satisfies this — it's seeded with the bootstrap user only (admin@example.com), the slice-211 role grants, and the slice-006 SCF catalog (50 SOC 2 controls). No real tenant content; no privacy concern.

**Tradeoff vs local docker-compose**:

- atlas-edge: faster (no compose bring-up); real network stack; admin JWT minted once + reused across all 11 agents
- local docker-compose: slower; requires `bash deploy/docker/test-self-host-bundle.sh` setup; isolated per-agent state

The maintainer (Matt) authorized this in the dispatch conversation; the choice is recorded here for future auditors who may inherit this pattern.

## D7 — Authentication via long-lived admin JWT

**Decision**: pre-minted a single admin JWT via `/auth/local/login` (slice 209) on the orchestrator's machine; passed the same JWT to all 11 agents via `/tmp/atlas-edge-admin-jwt`.

**Rationale**: simpler than per-agent sign-in (would have required each agent to handle the credential dance). The JWT has a 1h TTL (slice 209 D2); the entire fleet completed within that window so no re-mint was needed.

**Anti-criteria honored**: P0-A5 (no Bearer tokens / cookies in committed artifacts) — verified by post-merge grep across all 11 PRs.

**Cleanup**: post-merge, `/tmp/atlas-edge-admin-jwt` is rotated by the next sign-in cycle (the JWT is short-lived; the bootstrap password's value is what matters and lives in 1Password).

## D8 — Slice number range allocation

**Decision**: 5-slot ranges per page (213-217, 218-222, ..., 263-267) — 55 slots reserved for 49 actual findings.

**Allocation table**:

| Page           | Slots reserved  | Slots used  |
| -------------- | --------------- | ----------- |
| audits         | 213-217         | 5/5         |
| board-pack     | 218-222         | 5/5         |
| controls       | 223-227         | 5/5         |
| dashboard      | 228-232         | 5/5         |
| evidence       | 233-237         | 5/5         |
| policies       | 238-242         | 5/5         |
| risks          | 243-247         | 5/5         |
| settings       | 248-252         | 5/5         |
| controls/{id}  | 253-257         | 5/5         |
| / (index)      | 258-262         | 2/5         |
| questionnaires | 263-267         | 2/5         |
| **TOTAL**      | **55 reserved** | **49 used** |

The 6 unused slots (260, 261, 262, 265, 266, 267) are simply unallocated — they can be claimed by future spillovers without risk of collision since the audit's range is bounded.

## D9 — Per-PR shape vs single aggregate PR

**Decision**: 11 individual per-page PRs (plus this aggregate PR), per the maintainer's explicit preference at dispatch time. The original slice 204 spec language ("open ONE PR containing: the aggregate, the decisions log, all per-page reports, and all spillover slice docs") was overridden by the maintainer's "I would prefer individual PRs" request.

**Rationale for the maintainer's choice**: smaller per-PR review surface; easier to triage which audit results land vs which get deferred; allows mockup-stale findings to merge ahead of code-fix findings without entangling them.

**Tradeoff accepted**: 12 PRs (11 audit + 1 aggregate) is more review backlog than 1 mega-PR. Acceptable per the slice's explicit "spillover-as-slice" discipline.

## D10 — CI-delta scan (aggregate PR)

To be filled in by the orchestrator after this aggregate PR's CI completes.

---

**Provenance**: this log captures decisions made by the slice-204 orchestrator-engineer during fleet dispatch + collection. Per-page Agents wrote their own per-page audit logs at `docs/audit-log/204-page-audit-<page>.md` (each merged with its respective audit PR). The aggregate report at `docs/audit-log/204-aggregate.md` is the master cross-reference.
