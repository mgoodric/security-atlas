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

- Triaging Dependabot PRs that have been open more than ~24h (the cooldown window — gives upstream supply-chain attacks time to be discovered before you pull)
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

  PR=auto      — pick the OLDEST open Dependabot PR that:
                   - was created > 24h ago (supply-chain cooldown window)
                   - has no "/loop dep-review" comment from us yet
                   - has CI status green OR pending (skip CI-failing PRs
                     — those need maintainer attention before we burn
                     cycles analyzing them)
                 After completing, call ScheduleWakeup({delaySeconds: 300,
                 prompt: <same /loop prompt body>}) IF another eligible
                 PR remains. Otherwise exit.

To list eligible PRs in `auto` mode:
  gh pr list --repo mgoodric/security-atlas --state open --base main \
    --search "author:app/dependabot author:dependabot[bot] -comment:/loop dep-review" \
    --json number,createdAt,statusCheckRollup \
    --limit 30 \
  | jq '[.[] | select(
      (.createdAt | fromdateiso8601) < (now - 86400)
      and (.statusCheckRollup[] | select(.conclusion == "FAILURE")) == null
    )] | sort_by(.createdAt) | .[0].number'

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
STEP 7 — Optional action (only on LOW with --auto-merge enabled)
═══════════════════════════════════════════════════════════════════════════════

If the user invoked with PR=auto AND configured AUTO_MERGE_LOW=true
(an environment variable they set before invoking /loop), AND the risk
classification is LOW AND CI is green:

    gh pr review <PR> --approve --body "auto-approved by /loop dep-review (LOW risk)"
    gh pr merge <PR> --squash --auto

Otherwise: post the comment and leave the merge decision to the maintainer.

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
"comment_url": "<url>"
}

This is the audit trail; it's also what the maintainer queries to
periodically calibrate the prompt (if a "LOW" risk classification later
caused a regression, the JSONL has the evidence to backfill that into
the prompt's heuristics).

═══════════════════════════════════════════════════════════════════════════════
HARD RULES (dep-review-specific)
═══════════════════════════════════════════════════════════════════════════════

- NEVER auto-merge on MEDIUM or HIGH risk, even if the user configured
  AUTO_MERGE_LOW=true. AUTO_MERGE_LOW means "LOW risk + green CI + no
  HIT-BREAKING anywhere"; HIGH and MEDIUM are explicit human-eyes paths.
- NEVER push commits to the Dependabot branch from this prompt. The
  prompt READS the PR and COMMENTS on it. Adding code to a Dependabot
  PR is a separate, manual maintainer action.
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
| Cooldown age (24h before a PR becomes eligible) | 24h | Lower to 1h if a security-only PR needs faster turnaround; raise to 72h if the supply-chain attack landscape has gotten worse and you want more soak time |
| Commit-log cap (100 commits scanned) | 100 | Raise if you're routinely hitting it on first-major-version bumps for fast-moving libs; lower if API rate-limit pressure |
| Test-suite filter on STEP 5 | basename-based | Switch to a Go/npm/pip module-path filter if the basename heuristic produces too many false matches |
| AUTO_MERGE_LOW env var | unset (default off) | Set to `true` ONLY after you've watched the prompt's LOW classifications for a few weeks and they match your own judgment. The cost of a wrong auto-merge is far higher than the cost of one extra click |
| Risk thresholds (LOW/MEDIUM/HIGH boundaries) | per STEP 4c | If you find HIGH is too noisy (every major bump hits HIT-NEUTRAL → HIGH even when innocuous), tune 4c's MAJOR-bump rule. Keep changes anchored in observed outcomes from the JSONL audit trail, not intuition |

## What this prompt deliberately does NOT do

- **Doesn't generate code changes.** Analysis only. The maintainer (or a future slice's agent) decides whether to add tests, write a migration shim, or just merge.
- **Doesn't replace fossabot, Aikido, Snyk, or GitHub's native Dependabot-→-AI-agent feature.** This is a personal-workflow tool, not a product. It uses the same primitives those products use (AST + changelog + call-site grep) but stays narrow and tunable rather than aiming for product-level coverage.
- **Doesn't enforce a policy** ("never merge axios majors", "always pin lockfile to peer-dep range"). Policy is the maintainer's call; this prompt informs that call.
- **Doesn't handle indirect (transitive) dependency bumps with no direct lockfile change.** Those are rare and the prompt would correctly identify "no direct call sites" → LOW; useful enough.

## Provenance

Surfaced 2026-05-15 in the deploy-walkthrough session at `~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/` and traced back to the earlier conversation on AI-driven dependency upgrade analysis (2026-05-13). The conclusion from that conversation was that the genuinely-defensible wedge is *delta-scoped predictive test generation* — and this prompt is the personal-workflow instantiation of that idea, narrowed to security-atlas's specific dependency set. See `~/.claude/MEMORY/WORK/20260513-105215_dependency-upgrade-ai-landscape/` for the landscape scan that informs the design choices here (why not visual-regression, why not multi-package grouped PRs in v1, why CHANGELOG-first then commit-log-fallback).
```
