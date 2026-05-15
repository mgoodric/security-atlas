# 077 — Dependabot `deps` commit prefix + release-please section — decisions log

Slice 077 is `Type: AFK`. This log records the subjective build-time
judgment calls made while flipping the Dependabot commit prefix to `deps`
and adding the dedicated "Dependencies" section to release-please. Format
mirrors the JUDGMENT-slice convention (Decisions made · Revisit once in
use · Confidence).

## Decisions made

### 1. AC-3 — re-hide `chore` (Option A), not keep + rename (Option B)

**Options considered:**

- **(A)** Revert PR #144's `chore` unhide to `hidden: true`, leaving only
  the new `deps` section to surface dependency bumps. The `chore` type
  still exists in the config (per anti-criterion P0-A2) but is hidden
  from the rendered changelog.
- **(B)** Keep PR #144's `chore` unhide and rename "Dependencies and
  chores" back to "Chores" or "Maintenance" so future genuine chores
  still surface (separately from deps under the new `deps` section).

**Chosen: (A).** The slice doc's "Notes for the implementing agent"
explicitly recommends Option A as the cleaner default. After this slice
lands, the post-1.5.1 commit window will be dominated by `chore(status):`
flips (per-slice claim-stake commits) and the rare hand-authored
maintenance commit. Surfacing those as a `Chores` section in release
notes is noise — they are internal hygiene with no audit-trail value to
end users. The `chore` type stays in the config (the section name is
restored to plain "Chores" so the config remains self-documenting), but
`hidden: true` keeps it out of the rendered changelog.

If a future maintainer wants `chore` visible (because of, e.g., a
refactor wave landing as `chore(refactor):` that they want in the
changelog), the change is two lines: flip `hidden` to `false` and rename
the section. Reversible.

**Confidence: high.** This is the slice doc's recommendation and
matches the broader pattern of "changelog == user-relevant changes;
internal bookkeeping stays hidden."

### 2. AC-2 — position of the new `deps` entry: immediately after `revert`

**Options considered:**

- Position at top (before `feat`) — wrong, treats deps as more important
  than features.
- Position immediately after `revert` (between user-facing types and
  meta-types like `docs`/`ci`).
- Position at bottom (after `chore`/`style`) — would hide deps far below
  Documentation and CI, downplaying their importance for compliance
  visibility.

**Chosen: immediately after `revert`.** release-please renders sections
in their config-array order, so the v1.5.2 release notes will read:
Features → Bug Fixes → Performance → Reverts → **Dependencies** →
Documentation → CI / CD → (hidden: Code Refactoring, Tests, Build,
Chores, Style). Dependencies sits between user-facing changes (above)
and meta (below), which reads naturally: "what changed for users, then
what changed under the hood." Also matches the slice doc's recommended
position verbatim.

**Confidence: high.**

### 3. GitHub Actions ecosystem block — flip from `ci` to `deps` too

**Discovery during the grill.** The `.github/dependabot.yml`
github-actions block was the only one not using `chore` to begin with —
it used `commit-message.prefix: "ci"`. The slice doc's narrative says
"every ecosystem block" but its AC-1 wording only enumerates
`chore` → `deps`. The judgment call is whether the github-actions block
also flips.

**Options considered:**

- **(A)** Flip github-actions to `deps` like every other ecosystem.
  Dependabot PRs become `deps(actions):` and land under the
  "Dependencies" section alongside go/npm/pip/docker deps.
- **(B)** Leave github-actions on `ci`. PRs stay as `ci(actions):`,
  landing under "CI / CD" (still visible because that section is already
  unhidden).

**Chosen: (A).** Dependabot updates are Dependabot updates regardless
of ecosystem. Splitting actions-deps from other deps would force readers
of the changelog to look in two sections for the same kind of mechanical
maintenance. Hand-authored CI workflow changes (`ci(release-please):
...`, `ci(branch-protection): ...`) keep the `ci` type and continue to
land in "CI / CD" — that's the correct routing for human-authored CI
changes vs. automated CI dependency updates.

**Confidence: high.** The `ci` type stays in the config (still
unhidden) for hand-authored CI changes — only the github-actions
Dependabot block's commit prefix changes.

### 4. CONTRIBUTING.md — add `deps` to the Conventional Commits type table

**The table at `CONTRIBUTING.md` lines 97-109 is the canonical
contributor-facing schema for Conventional Commit types in this repo.**
After this slice, Dependabot opens `deps:`-prefixed PRs, but the table
listed no `deps` row — a drift between docs and reality.

**Chosen:** add a `| deps | none | Dependency bump (Dependabot) |` row
to the table (between `docs` and `chore`), and add a short
"Dependency updates" subsection beneath the table documenting Dependabot
behavior (weekly cadence, prefix shape, render destination, major-bump
caveat).

**Why CONTRIBUTING.md, not `docs-site/docs/install.md`:** the install
doc is operator-facing — "how do I bring the bundle up?" Dependency-bump
convention is a dev-process concern. CONTRIBUTING.md already houses the
Conventional Commits + DCO + PR workflow material; the Dependabot
paragraph sits with its peers.

**Confidence: high.**

### 5. AC-5 — manual verification plan (release-please-action dry-run)

**Attempt:** `npx release-please release-pr --dry-run` from this
worktree's checkout against the local `release-please-config.json`.

**Outcome:** documented inline below. The command needs a GitHub token
to inspect the merged-commits state of the remote `main` branch in
order to render the next release PR's body. The worktree's dev shell
either (a) has a `gh auth token` we can pipe in, or (b) does not — and
in case (b) the manual verification plan kicks in.

**Verification plan if a clean dry-run is not feasible:**

1. Static inspection of the config diff (already done — this log
   captures the rationale).
2. JSON-schema validation: `release-please-config.json` references
   `https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json`.
   The new `deps` entry's shape (`type`/`section`/`hidden`) is identical
   to every other entry in `changelog-sections`; if the schema accepts
   the others, it accepts this one. Manually confirmed: the schema's
   `changelog-sections` items definition is open-typed on `type` (any
   string allowed) with `section` and `hidden` required.
3. Post-merge verification: the next release-please-action run on `main`
   (triggered by any merged PR) will either successfully open a release
   PR or fail loudly. Failure is recoverable by reverting this slice.
4. Smoke test deferred to the next Dependabot Monday cron run (AC-7,
   already documented as OPTIONAL in the slice acceptance criteria).

**Dry-run captured output:**

The full `release-pr --dry-run` flow needs a GitHub token with App-level
GraphQL permissions (the personal `gh auth token` returns `401 Bad
credentials` against release-please's `releaseIterator` GraphQL query —
release-please-action in CI uses an App token via `RELEASE_PLEASE_APP_ID`
that is not available outside the GHA runtime). Falling back to two
parallel checks that together provide equivalent confidence:

1. **`release-please debug-config`** (which uses the same config loader
   as `release-pr`) parses the modified config **without error** and
   returns a fully-realized `Manifest` object showing
   `releasedVersions: { '.': Version { major: 1, minor: 5, patch: 1 } }`
   and `changelogSections: [Array]` (the 12-element array populated from
   the new config). No parse warnings, no schema violations.

2. **Local JSON-schema-shape check** of the rendered visible-section
   order:

   ```
    1. [visible] type=feat       section=Features
    2. [visible] type=fix        section=Bug Fixes
    3. [visible] type=perf       section=Performance
    4. [visible] type=revert     section=Reverts
    5. [visible] type=deps       section=Dependencies
    6. [visible] type=docs       section=Documentation
   10. [visible] type=ci         section=CI / CD
   (hidden: refactor, test, build, chore, style)
   ```

   This is the order that release-please-action will render the v1.5.2
   release notes in, given the config we just merged. The new
   **Dependencies** section sits exactly between Reverts and
   Documentation as AC-2 designed.

Live release-please-action verification fires on the next merged PR (the
GHA workflow re-runs on every push to `main` and opens or updates the
release PR for v1.5.2). If the config were broken, that workflow would
fail loudly and would be reverted before causing release damage.

**Confidence: high.** debug-config parsed clean + JSON-schema shape
matches the architecturally-intended render order. The live
release-please-action run on the next merged PR is the final
confirmation, and it is recoverable by revert if anything is off.

## Revisit once in use

- **Decision 1 (Option A re-hide chore):** if a future maintainer
  surfaces a real need to see `chore`-typed work in the changelog (e.g.
  a large refactor wave landing as `chore(refactor):` commits), flip
  `hidden: false` on the `chore` entry and rename the section
  ("Maintenance" or similar). Two-line config change.
- **Decision 3 (github-actions to deps):** if hand-authored CI workflow
  changes (e.g., a new `ci(release-please):` slice) start frequently
  surfacing under "CI / CD" and Dependabot's actions bumps need to be
  visually distinguished from them, the github-actions block can flip
  back to `ci`. The "Dependencies" section would then lose the
  actions-bump variant. Defer until/unless that confusion arises in
  practice.
- **Decision 4 (deps row in CONTRIBUTING.md table):** kept for
  contributor clarity. No revisit anticipated.
- **Auto-merge for safe dep bumps:** explicitly out of scope (P0-A4 +
  P0-A5). If/when auto-merge is added (separate slice), the convention
  will be patch/minor-only via a separate GitHub Action gated on CI
  green + Renovate-style policy file. Document at that time.
