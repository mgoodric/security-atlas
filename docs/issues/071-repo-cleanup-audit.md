# 071 — Repo cleanup audit + in-place updates

**Cluster:** Infra
**Estimate:** 2-3d
**Type:** JUDGMENT

## Narrative

Run a structured audit across the entire repository, ensure every doc is up to date with current reality, and surface (but do NOT execute) any deletion candidates. The repo has accumulated a year of fast-burndown v1 work and there are known surfaces — stale `.md` references to merged-then-renamed primitives, leftover worktree directories on disk, dead test fixtures from earlier slices, dropped `_INDEX.md` rows, outdated tech-stack tables, etc. — that haven't been pruned because the v1 burn-down prioritized landing slices over hygiene.

**Constraint (load-bearing — do NOT violate):** this slice **DOES NOT DELETE FILES.** Deletions are a separate decision from "is this file stale?". Stale files might still be valuable as historical record (decisions logs, batch drift sections, old slice docs); the deletion call is the maintainer's, not the engineer's. The slice's deliverables are therefore (a) an audit report identifying every survey category with findings, (b) in-place updates to docs that are factually stale, and (c) **a follow-on slice** authored by this slice's engineer that itemizes deletion candidates with justifications, status `not-ready`, awaiting explicit maintainer approval before any deletion executes.

Survey categories the audit covers, end-to-end (each is one section of the audit report at `docs/audits/2026-Q2-repo-cleanup.md`):

1. **`Plans/canvas/` tech-stack accuracy** — every row in `Plans/canvas/09-tech-stack.md` is cross-referenced against actual current usage. Identify drift (versions, replaced libraries, abandoned choices), update in-place.
2. **`Plans/canvas/11-open-questions.md` resolution status** — every item marked unresolved is checked against current `main`. Items resolved during the v1 burn-down get the `RESOLVED YYYY-MM-DD` blockquote treatment (matching the format of items 4, 13, 19, 20).
3. **`docs/issues/_INDEX.md` vs `docs/issues/*.md`** — every slice file is checked against the index; missing rows (memory note: slices 059-064 are deliberately not in the index per the parallel-batch convention) are reconciled with an explicit policy paragraph in the index header.
4. **`docs/issues/_STATUS.md` historical drift sections** — the drift sections accumulate. Each one is verified against the merge actually performed (every commit SHA, every PR number, every slice list). Drift discovered is corrected in-place.
5. **`README.md`** — every claim, every link, every dependency reference, every command. Specifically: every `just` recipe mentioned is verified to exist; every doc link is verified to resolve; the badge row is verified against current CI job names; the screenshots from slice 057 are verified to render under both light and dark `<picture>` variants.
6. **`CONTRIBUTING.md`** — same treatment as README. Every workflow described is verified executable. The pre-commit, slice-template, and ship-gate references are verified pointing at extant skill paths.
7. **`docs-site/docs/*.md`** — the five core pages from slice 058. Every code block, every link, every claim verified. Surfaces that drifted (e.g., a CLI command rename) get an in-place fix.
8. **`docs/RELEASE_READINESS.md` and other top-level `docs/*.md`** — same treatment.
9. **`docs/adr/000N-*.md` records** — verified against current implementation: did the implementation honor the ADR? Did a later slice tacitly reverse it? Drift gets a "Status" update in the ADR (Honored / Superseded by slice NNN / Partially honored — see §X).
10. **`docs/audit-log/*-decisions.md` JUDGMENT-slice logs** — verified that every "Revisit once in use" item is either (a) still open and worth tracking, or (b) genuinely revisited and resolvable. Resolvable items get a `RESOLVED YYYY-MM-DD` note.
11. **`web/e2e/*.spec.ts` preamble comments** — preambles written before slice 069 wired Playwright claim `Playwright is not installed`. The post-slice-069 reality is the runner is installed; the AC-5 PARTIAL is the seed harness. Update preambles in-place to reflect current state.
12. **`fixtures/**`/`web/e2e/fixtures.ts`/`internal/db/integration_test.go` fixtures\*\* — every fixture file is checked against its consumer. Unreferenced fixtures (no consumer in any test, in any spec, in any walkthrough) are recorded as deletion candidates.
13. **`internal/**`dead code** —`go vet`+`staticcheck`(if available) +`unused`analyzer surface dead exports, dead functions, dead struct fields. Each finding is investigated for "is this unused because the consumer hasn't been built yet" (KEEP — annotate with`// TODO(slice-NNN): consumed by upcoming feature`) vs "this is genuinely orphan" (deletion candidate).
14. **`web/` dead code** — `tsc --noEmit` + `eslint` + an unused-export scan via `knip` (if cleanly installable as a `web/devDependency` — judgment call, log it) surface dead UI surface. Same KEEP-vs-CANDIDATE classification.
15. **Stale git worktrees on disk** — `_STATUS.md`'s "Stale worktrees still on disk" line lists ~45 directories under `../security-atlas-NNN/`. None are on `main`'s branches anymore; all are confirmed-safe to `git worktree remove`. This is a deletion candidate (filesystem, not in-repo, but in the engineer's working environment) — recorded in the audit and in the follow-on deletion slice.
16. **Top-level config drift** — `go.work` vs current Go module layout; `pyproject.toml` vs current Python toolchain (uv lockfile presence, ruff config); `package.json` workspace roots vs actual subtrees; `.gitignore` entries that no longer protect against anything; `.prettierignore` entries (note: slice-69-era added `CHANGELOG.md` here) that should stay or go.

**Concrete in-place updates the engineer MAY make freely** (no separate slice needed):

- Fixing broken doc links
- Updating version numbers in tech-stack tables to current
- Adding `RESOLVED YYYY-MM-DD` blockquotes to canvas open-questions and decisions-log "Revisit once in use" items
- Rewriting `web/e2e/*.spec.ts` preamble comments to reflect post-slice-069 reality
- Adding ADR "Status:" headers
- Annotating dead-but-intentional code with `// TODO(slice-NNN)` comments
- Fixing typos, prettier-driftable formatting, link-rot in code comments

**Concrete actions the engineer MAY NOT take** (these go in the follow-on deletion slice):

- Removing any file from the repo
- Deleting any function, exported type, or test fixture (even if unused)
- Removing any entry from `_INDEX.md`
- Removing any worktree from disk
- Deleting any branch (local or remote)
- Removing any historical drift section from `_STATUS.md` (even if redundant)

The follow-on slice (auto-numbered as the next available NNN after this one merges, per the spillover convention) carries every deletion candidate with: file path, evidence it's unused (grep results, build verification, runtime trace), what was deleted in it would break (anti-evidence — what consumers, even latent), proposed action (delete / archive-to-tag / leave). Maintainer reviews + approves before that slice is implemented.

## Acceptance criteria

- [ ] AC-1: `docs/audits/2026-Q2-repo-cleanup.md` exists with sections 1–16 from the narrative, each section beginning with a brief "Method" paragraph (how the survey was done — which tools, which commands, which scope) and ending with a "Findings" table (Item / Status: Up-to-date | Updated-in-place | Deletion candidate / Note).
- [ ] AC-2: Every in-place update committed in this PR is cross-referenced from the audit report's "Findings" table by its file path + commit SHA reference.
- [ ] AC-3: A follow-on slice file exists at `docs/issues/NNN-repo-cleanup-deletions.md` (where NNN is computed via `ls docs/issues/[0-9]*.md | sed -E 's|.*/([0-9]+).*|\1|' | sort -n | tail -1` + 1). It uses the per-slice template, is **status `not-ready`** in `_STATUS.md` with the dep `071 (this slice) merged + maintainer approval`, and contains a per-category deletion candidate list with the four columns specified in the narrative.
- [ ] AC-4: `Plans/canvas/09-tech-stack.md` row-by-row verification table appears in the audit report. Any version drift identified is corrected in-place in the canvas (with a tiny "(verified 2026-MM-DD)" annotation in the canvas section that has stable structure for review).
- [ ] AC-5: `Plans/canvas/11-open-questions.md` is updated in-place: any items resolved during v1 get a `RESOLVED YYYY-MM-DD` blockquote citing the resolving slice (format matches items 4, 13, 19, 20).
- [ ] AC-6: `docs/issues/_INDEX.md` either gets the missing slice rows added (per current convention, depending on what the engineer's grill concludes) OR gets a clear "## Index policy" paragraph documenting why slices 059-064+ are deliberately absent. The engineer makes the call (this is the judgment call); records it in the decisions log.
- [ ] AC-7: `README.md` link-rot check: every link resolves (`curl -fsSL --head` for absolute URLs, `test -e` for relative). Every `just` recipe mentioned exists in the justfile. Every CI badge image URL resolves. Findings table in the audit; in-place fixes in this PR.
- [ ] AC-8: `web/e2e/*.spec.ts` preamble comments are updated to reflect post-slice-069 reality (Playwright IS installed; the AC-5 PARTIAL is the seed-data harness, not the runner). One commit covers the five specs.
- [ ] AC-9: `docs/adr/*.md` Status headers added — each ADR gets a one-line "Status: Honored / Superseded by slice NNN / Partial — see §X" header just below the title.
- [ ] AC-10: `docs/audit-log/*-decisions.md` JUDGMENT-slice "Revisit once in use" items: each one is checked for resolvability; resolved items get a `RESOLVED YYYY-MM-DD` note inline; unresolved items are left untouched. Findings table in the audit.
- [ ] AC-11: A `decisions log` for this slice at `docs/audit-log/071-repo-cleanup-decisions.md` (JUDGMENT-slice protocol from `Plans/prompts/04-per-slice-template.md`) records the major calls — particularly the `_INDEX.md` policy decision (AC-6) and any borderline "in-place fix vs deletion candidate" classifications.
- [ ] AC-12: `_STATUS.md` is updated with a drift section recording the in-place updates (which files changed, which audit categories they correspond to) AND adding the follow-on deletion slice as `not-ready` (dep: 071 merged + maintainer approval, where "maintainer approval" is captured in a comment on the deletion slice's PR).
- [ ] AC-13: Pre-commit clean. CI green. No CI gate is added by this slice (audit is a one-shot, not a recurring check).

## Constitutional invariants honored

- **Working norms — Style** (CLAUDE.md "Ask before destructive operations"): this slice is _structured to refuse destructive operations_ by design — deletions are a separate slice, approved out-of-band, executed only after explicit go-ahead.
- **Working norms — Ask before destructive operations**: the audit-report-plus-deletion-slice split IS the implementation of this norm for repo cleanup.
- **AI-assist boundary**: nothing in this slice is AI-generated content masquerading as authoritative; the audit is verifiable evidence (`grep`, `curl -fsSL`, `go build`, `tsc`) and the in-place updates are mechanical (link fixes, version updates, status annotations).

## Canvas references

- `Plans/canvas/09-tech-stack.md` — primary surface audited (AC-4)
- `Plans/canvas/11-open-questions.md` — resolution status surface (AC-5)
- `Plans/prompts/04-per-slice-template.md` "Slice types" — JUDGMENT-slice protocol the deletion follow-on inherits
- `CLAUDE.md` "Executing actions with care" — the "ask before destructive operations" norm this slice operationalizes

## Dependencies

- All currently-merged v1 slices (the audit operates on the v1 final state)
- No new feature deps — pure hygiene

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT delete any file. Period. Detected at PR review time by `git diff --diff-filter=D main..HEAD` returning empty. If the engineer thinks something should be deleted, it goes in the follow-on slice — never in this PR.
- **P0-A2**: Does NOT modify production code (`cmd/`, `internal/`, `web/app/`, `web/components/`, `web/lib/`) except to add `// TODO(slice-NNN)` comments annotating dead-but-intentional code. Anything more invasive is a separate slice — production code changes inside a hygiene PR confound review.
- **P0-A3**: Does NOT add a recurring CI gate ("must run audit every PR" or similar). The audit is a one-shot snapshot; future audits are scheduled by the maintainer.
- **P0-A4**: Does NOT remove historical drift sections from `_STATUS.md`. They are the audit trail of how the v1 backlog actually got merged; collapsing them now loses the record.
- **P0-A5**: Does NOT rename files. Renames cause cascading link-rot that this audit is trying to FIX, not introduce. If a file has a misleading name, the rename goes in the follow-on slice.
- **P0-A6**: Does NOT modify `Plans/prompts/05-parallel-batch.md` or `Plans/prompts/04-per-slice-template.md`. Those are the loop's spec — meta-changes go through a separate human-reviewed slice. Per `Plans/prompts/07-continuous-batch-loop.md` HARD RULES.
- **P0-A7**: Does NOT batch this slice in parallel with another slice that touches docs heavily. Co-batching with slice 070 (walkthroughs), slice 058-follow-ups, or any docs-heavy work invites merge conflicts on the very files this slice is normalizing. Solo-by-design.

## Skill mix (3–5)

- `engineering-advanced-skills:codebase-onboarding` (the audit IS a structured re-onboarding; same skill, applied to ourselves)
- `simplify` (the audit report and the in-place updates should be ruthlessly compressed; reports that read like reports nobody reads)
- `security-review` (the audit may incidentally surface secrets, real names, vendor token prefixes in fixtures or comments — the same constraint as slice 050 sanitization)
- `engineering-advanced-skills:runbook-generator` (the follow-on deletion slice is a runbook — each candidate row is a step with evidence + anti-evidence + proposed action)

## Notes for the implementing agent

- This slice is **solo-by-design** in the sense of P0-A7 — do not let the orchestrator batch it with another docs-touching slice. The conflict surface is the entire `docs/`, `Plans/`, and top-level config tree.
- Time-box each survey category. If category N expands beyond ~4 hours of work, file it as a spillover slice ("audit category N — needs deep dive") and continue. Don't let any single category derail the whole audit.
- The follow-on deletion slice doc is THE deliverable that translates audit into action. Spend time on it. Each row needs (a) path, (b) evidence-it-is-unused (specific grep / build / runtime trace), (c) anti-evidence (what would break, even latently), (d) proposed action. The maintainer will read this slice line-by-line before approving; quality matters here more than in the audit report.
- For the `_INDEX.md` policy call (AC-6): the memory note "slices 059-064 etc. are in `docs/issues/` but not yet in `_INDEX.md` by design" is what you'll have to either codify (write the policy paragraph) or revisit (add the rows). Both are defensible. Pick one, record why, move on — don't iterate.
- The `staticcheck` / `unused` Go analyzers may not be in the repo's default linter mix (`golangci-lint` config varies by repo). If they aren't, install them as a dev dependency for this audit and document the find. Mention in the decisions log.
