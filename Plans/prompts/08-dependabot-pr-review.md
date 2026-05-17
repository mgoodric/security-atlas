# 08 — Dependabot PR Review (per-PR upgrade-safety analyzer)

Codified procedure for an AI agent to triage a single Dependabot PR: identify the upgrade delta, fetch the upstream history relevant to that delta, find consumer call sites in security-atlas, cross-reference upstream changes against those call sites, and post a structured PR comment with a risk rating + recommended action (auto-merge / merge after review / hold for tests).

Specific to this repo. Lives in `Plans/prompts/` alongside `05-parallel-batch.md` and `07-continuous-batch-loop.md`. Tunable knobs near the bottom.

## What this is

Dependabot raises a PR per dependency upgrade. The default flow is "CI runs your existing tests; if green, merge." That misses two classes of regression:

1. **Behavior changes inside an exported symbol your code uses.** Same function signature, different behavior — e.g. `axios@1.0` changed how response interceptors fire on 4xx. Existing tests cover the call site but not the new behavior path.
2. **Indirect breakage via transitive dependency churn.** A bump pulls in a new transitive that changes a shared type definition. Type-check is green; runtime explodes.

This prompt asks an AI agent to read the upstream's CHANGELOG, release notes, and the actual commit log between the two version tags; identify the symbols touched; cross-reference against the security-atlas codebase's call sites; and decide:

- **LOW risk**: patch bump, no exported-API changes, security-only fixes. CI green → auto-merge OK.
- **MEDIUM risk**: minor bump with API additions but no removals, or behavior changes in exported symbols the codebase does NOT use. CI green → merge after a maintainer eyeballs the posted analysis.
- **HIGH risk**: major bump, or any breaking change in exported symbols the codebase uses. CI alone is insufficient → write additional regression tests covering the specific delta points, run them, then escalate to maintainer.

The output is a PR comment using a structured Markdown template (see below). The comment is the audit trail; the recommended action is the actionable signal; the optional new regression tests are the safety net for HIGH-risk merges.

## When to use

- Triaging open Dependabot PRs against the GitHub Advisory DB + call-site cross-reference, with auto-merge on LOW risk
- The continuous-batch loop is paused and you want to use the available cycles for upgrade hygiene instead of feature slices
- A specific Dependabot PR has a CHANGELOG entry that mentions "breaking" or "behavior change" and you want a structured assessment before deciding

## When NOT to use

- For non-Dependabot PRs — this prompt's analysis is keyed off the Dependabot PR shape (single `package + old_version → new_version`). Renovate's grouped PRs work; arbitrary feature PRs don't.
- For patches to indirect transitive dependencies that don't touch a direct dep version (this kind of PR is rare; the prompt would correctly identify no consumer call sites and return LOW, but you're spending tokens for no real signal)
- When the Dependabot PR is for an ecosystem the prompt doesn't have a heuristic for (Java/Maven, Ruby/Bundler, Rust/Cargo etc. — this version covers npm, pip, go-modules; extend before using on others)

## Invocation

Single PR:

```
/loop <prompt-body-from-this-file with PR=<NNN>>
```

OR batch (recommended) — iterate over all open Dependabot PRs above the cooldown age:

```
/loop <prompt-body-from-this-file with PR=auto>
```

The `auto` mode iterates one PR per iteration (FIFO oldest-first), pauses for ScheduleWakeup-after-each, and self-terminates when no eligible Dependabot PR remains.

## Prompt

````
You are running the dependabot-pr-review procedure for security-atlas.
Working directory is /Users/gmoney/Development/security-atlas; cd there
first if not already.

Read this entire prompt every iteration — each iteration is a fresh
agent session with no inherited context.

═══════════════════════════════════════════════════════════════════════════════
INPUT MODE
═══════════════════════════════════════════════════════════════════════════════

The invoking user specifies one of:

  PR=<NNN>     — review this exact PR. After completing, do NOT call
                 ScheduleWakeup; this is a one-shot run.

  PR=auto      — pick the OLDEST open Dependabot PR that has no
                 "/loop dep-review" comment from us yet. CI-failing
                 and BEHIND PRs are NOT skipped at selection time —
                 PREFLIGHT (below) handles them (most are out-of-date
                 branches; rebase then re-enter the loop). Supply-chain
                 hygiene is signal-based (STEP 1.5 advisory check),
                 not clock-based — calendar cooldowns ship false
                 confidence; GitHub Advisory DB is the real defense.
                 After completing, call ScheduleWakeup({delaySeconds: 300,
                 prompt: <same /loop prompt body>}) IF another eligible
                 PR remains. Otherwise exit.

To list eligible PRs in `auto` mode:
  gh pr list --repo mgoodric/security-atlas --state open --base main \
    --search "author:app/dependabot author:dependabot[bot] -comment:/loop dep-review" \
    --json number,createdAt \
    --limit 30 \
  | jq '[.[]] | sort_by(.createdAt) | .[0].number'

═══════════════════════════════════════════════════════════════════════════════
PREFLIGHT — branch freshness + merge state (runs before STEP 1)
═══════════════════════════════════════════════════════════════════════════════

Most Dependabot CI failures on a repo with active development are
out-of-date branches, not real test failures from the upgrade. Diagnose
+ trigger a rebase before spending tokens on the analysis.

  P1. Read merge state and failing-check class:
        gh pr view <PR> --repo mgoodric/security-atlas \
          --json mergeStateStatus,statusCheckRollup,headRefOid

      mergeStateStatus values:
        - CLEAN     — ready to merge; proceed to STEP 1
        - UNSTABLE  — non-required checks failing OR has non-blocking
                      failures; proceed to STEP 1 (real analysis still
                      worth doing; informational fails are noise)
        - BEHIND    — branch is behind base; rebase needed (see P2)
        - UNKNOWN   — GitHub hasn't recomputed mergeability since last
                      base movement; treat as BEHIND. Posting
                      `@dependabot rebase` both triggers a recompute AND
                      fixes the underlying staleness if any (see P2)
        - DIRTY     — merge conflict; cannot auto-resolve (see P3)
        - BLOCKED   — required reviews or status checks not satisfied;
                      treat as CLEAN-or-UNSTABLE for analysis purposes
                      (the analysis comment IS the review signal)
        - HAS_HOOKS — pre-merge hook failure; treat as DIRTY

  P2. If BEHIND or UNKNOWN:
        a) Check whether we already asked Dependabot to rebase in the
           last hour:
             gh pr view <PR> --repo mgoodric/security-atlas --json comments \
               | jq '[.comments[] | select(
                   (.body | startswith("@dependabot rebase"))
                   and ((.createdAt | fromdateiso8601) > (now - 3600))
                 )] | length'
        b) If count > 0: a rebase is in flight — Dependabot hasn't
           force-pushed the new HEAD yet. Skip this PR for this
           iteration; the next loop tick will re-evaluate.
        c) Otherwise: post `@dependabot rebase` and skip this PR for
           this iteration:
             gh pr comment <PR> --repo mgoodric/security-atlas \
               --body "@dependabot rebase"
           Note in the audit JSONL: outcome="preflight_rebase_requested"
           (no comment.md posted yet — that comes after re-analysis on
           the rebased branch).
        d) Move to next eligible PR (or exit if none).

  P3. If DIRTY:
        Merge conflicts cannot be auto-resolved from this prompt.
        Post a one-line comment flagging the state + skip:
          gh pr comment <PR> --repo mgoodric/security-atlas \
            --body "## /loop dep-review · SKIPPED · DIRTY

This PR has merge conflicts with main. Dependabot cannot auto-rebase
through conflicts; maintainer attention required. Re-run \`/loop\` against
this PR after the conflict is resolved."
        Note in JSONL: outcome="preflight_skipped_dirty".
        Move to next eligible PR.

  P4. If CLEAN, UNSTABLE, or BLOCKED: proceed to STEP 1 below.

PREFLIGHT counts as one "completion" for `auto`-mode ScheduleWakeup
purposes (an iteration that ends in P2 or P3 still schedules the next
iteration).

═══════════════════════════════════════════════════════════════════════════════
STEP 1 — Identify the upgrade delta
═══════════════════════════════════════════════════════════════════════════════

For the chosen PR:

  1a. Fetch metadata:
        gh pr view <PR> --json title,body,files,headRefName

  1b. Parse from the title (Dependabot convention) or body:
        - package name (e.g. "axios", "@types/node", "github.com/lib/pq")
        - ecosystem (npm | pip | go-modules — derive from headRefName
          like "dependabot/npm_and_yarn/axios-1.7.5" or
          "dependabot/go_modules/github.com/lib/pq-1.10.9")
        - old_version, new_version (semver strings)

  1c. Categorize the bump from the version delta:
        - PATCH:    z bumped, x.y unchanged
        - MINOR:    y bumped, x unchanged
        - MAJOR:    x bumped (or pre-1.0 minor bumped — semver caveat)

  1d. Identify the upstream repo URL from the package manifest:
        - npm: `npm view <pkg> repository.url`  (or fetch
          https://registry.npmjs.org/<pkg> and read the JSON)
        - pip: `pip show <pkg>` + Project-URL field (or fetch
          https://pypi.org/pypi/<pkg>/json)
        - go-modules: derive from the import path (github.com/owner/repo
          is the repo URL directly)

  Output to console:
    UPGRADE: <pkg> <old_version> → <new_version> [<bump-class>]
    UPSTREAM: <repo-url>
    ECOSYSTEM: <npm|pip|go-modules>

═══════════════════════════════════════════════════════════════════════════════
STEP 1.5 — GitHub Advisory DB check (supply-chain hygiene, signal-based)
═══════════════════════════════════════════════════════════════════════════════

Replaces the earlier 24h calendar-cooldown approach with an evidence-
based check: if the target version is implicated in a published
advisory (CVE / GHSA / npm-security-advisory / OSV), HOLD the PR
regardless of CHANGELOG analysis. Calendar time was theater; the
Advisory DB is the actual signal.

  1.5a. Map our ecosystem string to GitHub Advisory DB's ecosystem
        parameter:
          - npm        → npm
          - pip        → pip
          - go-modules → go

  1.5b. Query the global Advisory DB for advisories affecting the
        target version:
          gh api "/advisories?ecosystem=<eco>&affects=<pkg>@<new_version>" \
            > /tmp/dep-review-<PR>/advisories.json
          jq 'length' /tmp/dep-review-<PR>/advisories.json

        The `affects` parameter does range-aware matching — a query for
        `axios@1.7.5` will return advisories with vulnerable ranges
        like `>= 1.0.0, < 1.15.1` (which 1.7.5 satisfies).

  1.5c. If `length > 0`:
          - Extract per-advisory: ghsa_id, summary, severity (low /
            medium / high / critical), and the matching vulnerable
            version range
          - Post a HOLD comment immediately (do NOT proceed to STEP 2):

            ```markdown
            ## /loop dep-review · HOLD · advisory implicates target version

            **Upgrade:** `<pkg>` `<old_version>` → `<new_version>` (<bump-class> bump · <ecosystem>)

            The GitHub Advisory DB returns <N> advisory/advisories whose
            vulnerable version range includes the target version `<new_version>`:

            | GHSA | Severity | Summary | Range |
            |---|---|---|---|
            | [<GHSA-ID>](https://github.com/advisories/<GHSA-ID>) | <severity> | <summary> | `<range>` |

            ### Recommendation

            **hold pending advisory resolution**

            Reasoning: Pulling `<new_version>` would land us in a known-
            vulnerable version range. Wait for Dependabot to surface a
            fixed version, OR (if upstream has shipped a fix that
            Dependabot has not yet picked up) close this PR + retrigger
            Dependabot to open against the patched version.

            ---
            *Analyzed by `/loop dep-review` against Plans/prompts/08-dependabot-pr-review.md. Reproduce locally: `gh api '/advisories?ecosystem=<eco>&affects=<pkg>@<new_version>'`.*
            ```

          - Append JSONL entry with `outcome: "advisory_hold"`,
            `risk: "HIGH"`, `auto_merged: false`. Include the GHSA IDs
            in a new `advisory_ghsa_ids` field.
          - Exit (do NOT proceed to STEP 2 — analysis of CHANGELOG /
            call-sites is pointless when the version itself is
            embargoed).

  1.5d. If `length == 0`: log the clean check + proceed to STEP 2.
        Cache the clean result in the JSONL entry for the eventual
        STEP-6 comment (no need to display it; the absence of an
        advisory section in the comment means clean).

  Output to console:
    ADVISORY: <N> advisories matched (HOLD if >0; proceed if 0)

═══════════════════════════════════════════════════════════════════════════════
STEP 2 — Fetch upstream history for the delta
═══════════════════════════════════════════════════════════════════════════════

  2a. Find the upstream tags matching old_version + new_version. Tag
      conventions vary — try in order:
        - "v<version>" (most common)
        - "<version>" (bare)
        - "release-<version>" (rare)
      Confirm via: gh api repos/<owner>/<repo>/git/refs/tags/<tag>

      If a tag isn't found for either version, the upstream may not tag
      pre-releases or may use a different scheme. Note the gap in the
      output and fall back to date-range comparison using the dates from
      the registry (npm/pypi/pkg.go.dev).

  2b. Pull the upstream CHANGELOG between the two versions. Try in order:
        - GitHub Releases body for the new_version tag:
            gh api repos/<owner>/<repo>/releases/tags/<new_tag> --jq .body
        - CHANGELOG.md / CHANGES.md / HISTORY.md at the new_version tag:
            gh api repos/<owner>/<repo>/contents/CHANGELOG.md?ref=<new_tag>
            --jq '.content' | base64 -d | sed -n "/^##.*<old_version>/,/^##.*[0-9]\\./p"
        - Inline release notes from the registry (npm changelog field,
          PyPI Release notes section)

      Save the CHANGELOG slice (the chunk covering old → new) to a local
      scratch file under /tmp/dep-review-<PR>/changelog.md.

  2c. Pull the upstream commit log between the two tags (cap at 100
      commits — most version bumps are < 50 commits; if more, fall back
      to CHANGELOG-only and note the cap in the output):
        gh api repos/<owner>/<repo>/compare/<old_tag>...<new_tag> \
          --jq '.commits[] | {sha: .sha[0:8], msg: .commit.message}' \
        > /tmp/dep-review-<PR>/commits.json

      If the diff is too large for the API (GitHub caps at 250 commits
      per /compare call), fall back to CHANGELOG-only.

  2d. Scan the CHANGELOG + commit messages for breaking-change markers:
        - "BREAKING CHANGE:" or "BREAKING:" anywhere in a commit message
        - Conventional Commit "<type>!:" prefix (e.g. "feat!:", "fix!:")
        - "removed", "deprecated", "renamed", "moved", "no longer
          supported" in CHANGELOG bullets
        - For PATCH bumps: any of the above is a red flag (patches
          shouldn't break — if they do, treat as effective MAJOR)
        - For MAJOR bumps: the absence of these markers is suspicious
          (maintainers don't bump major for fun — usually means the
          marker is in a release-notes blog post outside the repo;
          flag for manual review)

  Output to console (terse):
    CHANGELOG: <N> entries, <M> flagged as breaking
    COMMITS:   <N> commits scanned, <M> flagged as breaking

═══════════════════════════════════════════════════════════════════════════════
STEP 3 — Find call sites in security-atlas
═══════════════════════════════════════════════════════════════════════════════

  3a. Locate the imports. For each ecosystem:
        - npm: `grep -rn "from ['\"]<pkg>" web/ --include="*.ts" --include="*.tsx" \
                && grep -rn "require\\(['\"]<pkg>" web/ --include="*.js"`
        - pip: `grep -rn "^import <pkg>\\|^from <pkg>" oscal-bridge/ --include="*.py"`
        - go-modules: `grep -rn "\"<import-path>\"" --include="*.go"`

      Save the file:line list to /tmp/dep-review-<PR>/imports.txt.

  3b. For each importing file, extract the exact symbols used:
        - npm: named imports from `import { a, b, c } from "pkg"`, plus
          default-import usages (`import x from "pkg"; x.method()` →
          symbol "method")
        - pip: dotted-attribute access on the import name
        - go-modules: identifier-after-package usage
          (`pq.RegisterErrorClass(...)` → symbol "RegisterErrorClass")

      Save the symbol set to /tmp/dep-review-<PR>/symbols.txt.

  3c. Output:
        CALL SITES: <N> files importing <pkg>
        SYMBOLS USED: <comma-separated list, or "(default import, no
                       extracted symbols)" for whole-package usage>

  If <N> = 0, this is "imported in lockfile but not in source"
  (transitive-only) — emit risk LOW and skip to STEP 5 with a comment
  noting the absence of direct call sites.

═══════════════════════════════════════════════════════════════════════════════
STEP 4 — Cross-reference upstream changes vs. call sites
═══════════════════════════════════════════════════════════════════════════════

  For each symbol in /tmp/dep-review-<PR>/symbols.txt:

  4a. Search the CHANGELOG + commit messages for that symbol name.
      Use ripgrep with word-boundary matching:
        rg -w "<symbol>" /tmp/dep-review-<PR>/changelog.md \
                          /tmp/dep-review-<PR>/commits.json

  4b. Classify each match:
        - HIT-BREAKING: symbol appears in a flagged-as-breaking entry
        - HIT-NEUTRAL:  symbol appears but not in a breaking entry
                        (e.g. a bugfix that improves behavior)
        - NO-HIT:       symbol does not appear

  4c. Compute the per-PR risk based on the worst-case symbol classification:
        - Any HIT-BREAKING → HIGH risk
        - Any HIT-NEUTRAL on a MAJOR bump → MEDIUM risk
                                            (upstream may have changed
                                             behavior without flagging
                                             it in the entry that
                                             mentions the symbol)
        - All NO-HIT on a PATCH or MINOR bump → LOW risk
        - All NO-HIT on a MAJOR bump → MEDIUM risk
                                       (upstream definitely changed
                                        something material; absence of
                                        symbol-name mention just means
                                        the breaking change is in a
                                        symbol the codebase doesn't use)

  Output to console:
    SYMBOL CROSS-REF:
      <symbol1>: HIT-BREAKING in <commit-sha> "<commit-subject>"
      <symbol2>: NO-HIT
      ...
    RISK: <LOW | MEDIUM | HIGH>

═══════════════════════════════════════════════════════════════════════════════
STEP 5 — Test coverage check (HIGH risk only — skip for LOW + MEDIUM)
═══════════════════════════════════════════════════════════════════════════════

  Only run this step if STEP 4 returned HIGH.

  5a. For each importing file with a HIT-BREAKING symbol, find sibling
      test files:
        - npm: `find . -name "<basename>.test.<ts|tsx|js>" -o \
                       -name "<basename>.spec.<ts|tsx|js>"`
        - pip: `find . -name "test_<basename>.py"`
        - go-modules: `find . -name "<basename>_test.go"`

  5b. For each importing file MISSING a sibling test, draft a minimal
      reproducer test that:
        - Imports the symbol from the upgraded package
        - Exercises the specific call shape the consumer code uses
        - Asserts the expected behavior under the OLD documented contract
        - When run against the new package version, will reveal whether
          the contract still holds

      Save these as /tmp/dep-review-<PR>/proposed-tests/<file>_dep_review_test.<ext>
      (do NOT commit them to the codebase from this prompt — they're
      analysis artifacts the maintainer reviews before any
      add-tests-to-this-PR decision).

  5c. Run the existing test suite filtered to call-site-relevant tests:
        - npm:   `npm test -- --testPathPattern="<import-basename>"`
        - pip:   `pytest -k "<import-basename>"`
        - go-modules: `go test ./... -run "<symbol-pattern>"`

      Capture pass/fail to /tmp/dep-review-<PR>/test-output.txt.

═══════════════════════════════════════════════════════════════════════════════
STEP 6 — Post the structured PR comment
═══════════════════════════════════════════════════════════════════════════════

  Post via `gh pr comment <PR> --body-file /tmp/dep-review-<PR>/comment.md`
  using this template:

  ```markdown
  ## /loop dep-review · <RISK>

  **Upgrade:** `<pkg>` `<old_version>` → `<new_version>` (<bump-class> bump · <ecosystem>)
  **Upstream:** [<repo-url>](<repo-url>)
  **Diff scope:** <N> commits scanned · <M> CHANGELOG entries · <K> flagged-as-breaking

  ### Call sites in this repo

  <N> files import `<pkg>`. Symbols used:
  - `<symbol1>` — referenced in <count> files
  - `<symbol2>` — referenced in <count> files
  ...

  ### Cross-reference

  | Symbol | Upstream mention | Verdict |
  |---|---|---|
  | `<sym1>` | [<sha>](<repo-url>/commit/<sha>) "<subject>" | 🔴 HIT-BREAKING |
  | `<sym2>` | (no mention) | ⚪ NO-HIT |

  ### Recommendation

  **<one of: auto-merge OK | merge after maintainer review | hold pending tests>**

  Reasoning: <2-3 sentences explaining the risk classification using the
  specific symbols + commits identified above.>

  ### Additional notes
  <Optional. Test coverage gaps. Suggested follow-up slice if a HIT-BREAKING needs codebase changes beyond the dep bump. Anything the maintainer should know.>

  ---
  *Analyzed by `/loop dep-review` against Plans/prompts/08-dependabot-pr-review.md. Reproduce locally: `gh pr checkout <PR> && /loop ...`.*
````

═══════════════════════════════════════════════════════════════════════════════
STEP 7 — Auto-merge on LOW (default ON; opt-out via AUTO_MERGE_LOW=false)
═══════════════════════════════════════════════════════════════════════════════

If ALL of:

- risk classification from STEP 4 is LOW
- no HIT-BREAKING anywhere in the cross-reference table
- CI is green OR all failing checks are non-required-checks
  informational jobs (verify via `gh pr view <PR>
--json statusCheckRollup` — any FAILURE on a check listed in
  `.github/branch-protection.json` required-checks blocks auto-merge)
- AUTO_MERGE_LOW is NOT explicitly set to the literal string "false"
  (env var unset OR set to anything other than "false" → auto-merge
  proceeds)

then run:

    gh pr review <PR> --approve --body "auto-approved by /loop dep-review (LOW risk)"
    gh pr merge <PR> --squash --auto

The `--auto` flag means GitHub merges as soon as required checks pass —
safe even if some informational checks are still running. The maintainer
sees the analysis comment + approval + auto-merge-armed in their inbox
and can cancel before checks clear if anything looks off.

Set `auto_merged: true` in the JSONL entry when this branch runs.

If risk is MEDIUM or HIGH, OR if AUTO_MERGE_LOW is explicitly "false",
OR if a required check is failing: post the comment and leave the merge
decision to the maintainer. Set `auto_merged: false` in JSONL.

═══════════════════════════════════════════════════════════════════════════════
PER-PR STATE PERSISTENCE
═══════════════════════════════════════════════════════════════════════════════

After each PR is processed, append one JSONL line to:
~/.claude/MEMORY/LEARNING/REFLECTIONS/dep-review.jsonl

Schema:
{
"ts": "<ISO-8601 UTC>",
"pr": <NNN>,
"package": "<pkg>",
"ecosystem": "<npm|pip|go-modules>",
"old_version": "<x.y.z>",
"new_version": "<x.y.z>",
"bump_class": "<patch|minor|major>",
"risk": "<LOW|MEDIUM|HIGH>",
"call_sites_count": <N>,
"symbols_used": [<list>],
"hit_breaking_count": <N>,
"hit_neutral_count": <N>,
"auto_merged": <bool>,
"comment_url": "<url>",
"outcome": "<analyzed | advisory_hold | preflight_rebase_requested | preflight_rebase_in_flight | preflight_skipped_dirty>",
"advisory_ghsa_ids": [<list of GHSA-IDs if outcome=advisory_hold; else empty>]
}

For preflight-only outcomes (P2, P3), the analysis fields
(package/ecosystem/old_version/etc.) may be partially populated or null
— the `outcome` field is the load-bearing signal. Always record the PR
number and timestamp.

This is the audit trail; it's also what the maintainer queries to
periodically calibrate the prompt (if a "LOW" risk classification later
caused a regression, the JSONL has the evidence to backfill that into
the prompt's heuristics).

═══════════════════════════════════════════════════════════════════════════════
HARD RULES (dep-review-specific)
═══════════════════════════════════════════════════════════════════════════════

- NEVER auto-merge on MEDIUM or HIGH risk, even if AUTO_MERGE_LOW is
  unset (= default-on). The auto-merge default applies ONLY to LOW
  risk with no HIT-BREAKING and no failing required-checks; HIGH and
  MEDIUM are explicit human-eyes paths regardless of env config.
- NEVER push commits to the Dependabot branch from this prompt. The
  prompt READS the PR, COMMENTS on it, and may post `@dependabot rebase`
  to ask Dependabot itself to update the branch (Dependabot owns the
  rebase commit; we don't). Adding new code to a Dependabot PR (test
  drafts, migration shims, etc.) is a separate, manual maintainer
  action.
- NEVER skip STEP 2c (commit log scan) for MAJOR bumps. CHANGELOG-only
  analysis on a major version bump is insufficient signal; if the
  /compare API returns too many commits to scan, surface that as a
  HOLD recommendation, not as an "I'll just trust the CHANGELOG" pass.
- NEVER include the analysis comment if STEP 1 fails to parse the
  upgrade delta (e.g. multi-package grouped PR). Instead, post a
  one-line comment noting the prompt doesn't yet handle grouped PRs +
  exit. Don't attempt partial analysis.
- NEVER use vendor-prefixed test fixture tokens in any proposed test
  drafted in STEP 5 — neutral `test-*` only.
- NEVER attempt this prompt on a non-Dependabot PR (verify
  `gh pr view <PR> --json author --jq '.author.login'` is one of
  "dependabot[bot]" / "app/dependabot" / "renovate[bot]" /
  "app/renovate" first).

```

## Tunable knobs

| Knob | Default | When to adjust |
|---|---|---|
| Commit-log cap (100 commits scanned) | 100 | Raise if you're routinely hitting it on first-major-version bumps for fast-moving libs; lower if API rate-limit pressure |
| Test-suite filter on STEP 5 | basename-based | Switch to a Go/npm/pip module-path filter if the basename heuristic produces too many false matches |
| AUTO_MERGE_LOW env var | unset (default ON) | Set to the literal string `false` to disable the LOW-risk auto-merge default and require a maintainer click on every PR. Reasonable opt-out during prompt-calibration windows (the first weeks after a major prompt change) or if the JSONL audit trail shows the LOW classifier surfaced a regression. Default-on is the calibrated steady state |
| PREFLIGHT rebase cooldown (1h between `@dependabot rebase` requests) | 1h | Lower to 15m if Dependabot is responding fast and you want tighter loops; raise to 6h if Dependabot's rebase queue is backed up and we're issuing duplicates that confuse the audit trail |
| Risk thresholds (LOW/MEDIUM/HIGH boundaries) | per STEP 4c | If you find HIGH is too noisy (every major bump hits HIT-NEUTRAL → HIGH even when innocuous), tune 4c's MAJOR-bump rule. Keep changes anchored in observed outcomes from the JSONL audit trail, not intuition |

## What this prompt deliberately does NOT do

- **Doesn't generate code changes.** Analysis only. The maintainer (or a future slice's agent) decides whether to add tests, write a migration shim, or just merge.
- **Doesn't replace fossabot, Aikido, Snyk, or GitHub's native Dependabot-→-AI-agent feature.** This is a personal-workflow tool, not a product. It uses the same primitives those products use (AST + changelog + call-site grep) but stays narrow and tunable rather than aiming for product-level coverage.
- **Doesn't enforce a policy** ("never merge axios majors", "always pin lockfile to peer-dep range"). Policy is the maintainer's call; this prompt informs that call.
- **Doesn't handle indirect (transitive) dependency bumps with no direct lockfile change.** Those are rare and the prompt would correctly identify "no direct call sites" → LOW; useful enough.

## Provenance

Surfaced 2026-05-15 in the deploy-walkthrough session at `~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/` and traced back to the earlier conversation on AI-driven dependency upgrade analysis (2026-05-13). The conclusion from that conversation was that the genuinely-defensible wedge is *delta-scoped predictive test generation* — and this prompt is the personal-workflow instantiation of that idea, narrowed to security-atlas's specific dependency set. See `~/.claude/MEMORY/WORK/20260513-105215_dependency-upgrade-ai-landscape/` for the landscape scan that informs the design choices here (why not visual-regression, why not multi-package grouped PRs in v1, why CHANGELOG-first then commit-log-fallback).
```
