# 077 — Dependabot `deps` commit prefix + dedicated release-please section

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK

## Narrative

PR #144 (`chore(release-please): surface chore and ci commits in changelog`) is a short-term fix to make the 1.5.1 release notes include the 7 dependency bumps that just merged. It works by unhiding the generic `chore` type and renaming its section to "Dependencies and chores." This is correct for 1.5.1 because the post-1.5.0 commit window happened to be all-deps + all-docs — no `chore(status):` noise to worry about.

The long-term shape is cleaner: give dependency bumps their own `deps` Conventional Commit type, separate from `chore`. The release-please changelog then has a clean **Dependencies** section that doesn't intermingle with other `chore(*)` work (status reconciles, prettier-only commits, release-please's own commits, etc.).

**The two-line change:**

1. `.github/dependabot.yml` — change `commit-message.prefix` from `"chore"` to `"deps"` on every ecosystem block (currently three: `gomod`, `npm`, `github-actions`). `prefix-development` (which Dependabot uses for `devDependencies`) should ALSO be `"deps"` — but Dependabot reads it for dev deps only when the ecosystem distinguishes (npm does; gomod doesn't). Keep `include: "scope"` so commits read `deps(deps):` / `deps(deps-dev):` / `deps(actions):`.
2. `release-please-config.json` — add `{"type": "deps", "section": "Dependencies", "hidden": false}` to `changelog-sections`. **Keep** the PR #144 `chore` unhiding (or revert it — engineer's judgment call, recorded in decisions log; the post-1.5.1 commit window will determine whether `chore` noise is a real concern or theoretical).

**Why this is its own slice (not part of #144):**

- PR #144 needs to land NOW to fix the 1.5.1 release notes. Dependabot config changes don't help 1.5.1 because the 7 already-merged dep commits have immutable `chore(deps):` / `ci(deps):` prefixes.
- The Dependabot change is a config refactor with no urgency.
- Co-batching with #144 would (a) couple a release-fix to a config-cleanup with different urgency profiles, (b) delay the 1.5.1 release notes fix while we debate the cleaner shape, and (c) put more files in a PR that release-please will read and base its next release on.

**A wrinkle to handle in this slice's grill:**

If PR #144 lands with `chore` unhidden, the `chore` section is named "Dependencies and chores" — which is no longer accurate after this slice lands (deps will go to the new "Dependencies" section under `deps`; `chore` will only have non-dep chores). The engineer should EITHER:

- **Option A**: revert PR #144's `chore` unhide (re-hide `chore`, leaving only the new `deps` section to surface dep bumps cleanly). This is the cleanest end state. Note: any FUTURE `chore(status):` or `chore(prettier):` commits won't appear in the changelog — which is what we want (they're internal hygiene).
- **Option B**: keep PR #144's `chore` unhide AND rename the section back to "Chores" or "Maintenance" (so future genuine chores still surface, separately from deps). This produces more verbose changelogs but preserves an audit trail of all non-feature work.

The engineer makes the call as part of this slice's grill, records it in the decisions log, and ships.

## Acceptance criteria

- [ ] AC-1: `.github/dependabot.yml` updated: every ecosystem block's `commit-message.prefix` changes from `"chore"` to `"deps"`. `commit-message.prefix-development` (where set) also changes to `"deps"`. `include: "scope"` stays. Verified by `grep prefix .github/dependabot.yml` returning only `"deps"` values.
- [ ] AC-2: `release-please-config.json` `changelog-sections` array gains `{"type": "deps", "section": "Dependencies", "hidden": false}` immediately after `revert` (so the section ordering in release notes reads: Features, Bug Fixes, Performance, Reverts, **Dependencies**, Documentation, ...). The exact insertion position is the engineer's judgment call; record in decisions log.
- [ ] AC-3: The `chore` section is **either** (a) re-hidden by reverting PR #144's change to `hidden: true`, **OR** (b) kept unhidden with the section name renamed from "Dependencies and chores" back to "Chores" or "Maintenance". The engineer picks A or B per the narrative wrinkle, with the rationale captured in the decisions log.
- [ ] AC-4: `docs-site/docs/install.md` or `CONTRIBUTING.md` (engineer picks the right home) gets a brief "Dependency updates" paragraph documenting the convention: Dependabot opens `deps(deps):` and `deps(deps-dev):` PRs weekly Mondays; the changelog surfaces them under "Dependencies"; safe minor/patch bumps auto-merge after CI green; majors are investigated individually.
- [ ] AC-5: A short test: open an in-PR test commit with `deps(test): example` shape AND a `chore(test): example` shape against the new config (or just inspect the diff against `release-please`'s output schema documentation). Verify the `deps` commit lands in the "Dependencies" section and the `chore` commit lands per the AC-3 chosen direction. This is informal — does NOT require CI gating, but the engineer should manually confirm via a one-off `release-please-action` dry-run command in the decisions log.
- [ ] AC-6: A `decisions log` for this slice at `docs/audit-log/077-dependabot-deps-commit-prefix-decisions.md` records: (1) Option A vs B from AC-3 and why, (2) the section-ordering choice in AC-2 (release-please renders sections in their config order), (3) anything that surfaced during the manual `release-please-action` dry-run.
- [ ] AC-7: After merge, the **next** Dependabot PR that opens carries the new `deps:` prefix. Verified by waiting for the next weekly Dependabot run (Monday) OR by manually triggering Dependabot via the GitHub UI on a known stale dep. This verification step is OPTIONAL for merging the slice — the AC is "the config is correct"; the verification is post-merge sanity.
- [ ] AC-8: `CHANGELOG.md` doesn't need touching by this slice — release-please regenerates the rendering on the next release tag. The first "Dependencies" section will appear in the v1.5.2 (or v1.6.0) release notes naturally.
- [ ] AC-9: Pre-commit clean. CI green. No production code touched.

## Constitutional invariants honored

- **Working norms — Cite sources** (CLAUDE.md): the slice's design choices cite PR #144 verbatim (the future-consideration paragraph that originated this slice) for full audit trail.
- **AI-assist boundary**: nothing here is AI-generated content; this is mechanical config + a release-notes routing decision.

## Canvas references

- _(none — this is build-process hygiene, not architectural; canvas doesn't speak to changelog generation conventions)_

## Dependencies

- **#144** (release-please-surface-deps PR, **not yet merged** at the time this slice is written; the slice spec assumes it merges normally) — the chore-unhiding short-term fix this slice supersedes
- **050** (public release readiness + release automation) — established the release-please-as-source-of-truth convention this slice extends

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT change the `commit-message.prefix` to anything other than `"deps"`. The Conventional Commits ecosystem doesn't have a single-canonical "dep bump" type, but `deps:` is a commonly-used convention (see how `release-please` itself documents it). Don't invent a third option.
- **P0-A2**: Does NOT remove or rename `chore` outright. The `chore` type still has legitimate uses (status reconciles, prettier reformats, release-please's own commits); whether it's hidden or visible is the AC-3 decision, but it doesn't disappear from the config.
- **P0-A3**: Does NOT touch already-merged commits to rename their prefixes (`git filter-branch` / `git rebase -i`). Released commits are immutable. The 7 dep bumps from v1.5.1 keep their `chore(deps):` / `ci(deps):` prefixes forever; the changelog rendering for v1.5.1 is whatever PR #144 produced; future releases get the cleaner shape.
- **P0-A4**: Does NOT auto-merge ANY Dependabot PR as a side effect of this slice's merge. The Dependabot prefix change is purely cosmetic-from-release-please's perspective; the PR-merge logic (whether auto-merge is enabled, what's required for it) is a separate concern (currently no auto-merge is configured; that's a slice-N decision if/when desired).
- **P0-A5**: Does NOT add an auto-merge GitHub Action for Dependabot. If the maintainer wants auto-merge for patch/minor dep bumps, it's a separate slice with its own risk assessment.

## Skill mix (3–5)

- Dependabot configuration syntax (the file is small; the trick is verifying `prefix-development` semantics across ecosystems)
- release-please configuration (the `changelog-sections` schema; section ordering in rendered output; what counts as a "released" type vs hidden)
- `simplify` (the docs paragraph in AC-4 should be 3-5 lines, not a tutorial)
- `security-review` (verifying that nothing in the change can affect the `RELEASE_PLEASE_APP_ID` token-minting path or branch-protection bypass — should be zero touch on those; surface-level check)

## Notes for the implementing agent

- **Run release-please locally to verify before merging.** `npx release-please-action --token=... --command=release-pr --dry-run --config-file=release-please-config.json` (or equivalent invocation; consult release-please-action's docs for the exact dry-run flag) renders what the next release PR's body would look like. If the dry-run output shows the `deps` section forming correctly with mocked commits, you're good. If it doesn't, fix the config before opening the slice's PR.
- The AC-3 choice (Option A vs B) is genuinely judgment-level. Option A is cleaner; Option B is more auditable. Default to A unless you encounter a reason to surface non-dep chores during the grill.
- The next Dependabot weekly run is the verification (AC-7). It's OPTIONAL because waiting on a weekly cron blocks the slice unnecessarily; ship the config + verify after the fact.
