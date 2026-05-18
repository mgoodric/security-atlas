# 128 — SHA-pin every GitHub Action across all workflows (+ CI guard to prevent regression)

**Cluster:** Infra (CI security)
**Estimate:** 1-2d
**Type:** AFK

## Narrative

Filed 2026-05-18 via `/idea-to-slice` from a StepSecurity Harden-Runner dashboard security recommendation. The dashboard at `https://app.stepsecurity.io/github/mgoodric/security-atlas` flagged that most actions in our 6 workflow files are tag-pinned (e.g. `actions/checkout@v6`) rather than commit-SHA-pinned, leaving us exposed to the well-known **tag-jacking** supply-chain attack class: an attacker who compromises an action's git push permissions can move a floating tag like `v6` to point at malicious code, and every consumer pinned to `@v6` silently picks it up on the next CI run.

Slice 117 (StepSecurity Harden-Runner audit mode, merged 2026-05-18) already established the SHA-pin convention for ONE action — `step-security/harden-runner@ab7a9404c0f3da075243ca237b5fac12c98deaa5 # v2.19.3`. This slice extends that discipline to **every** action in every workflow.

Audit (2026-05-18): 22 unique non-harden-runner `uses:` lines across 6 workflows (`ci.yml`, `codeql.yml`, `container-publish.yml`, `docs-publish.yml`, `release-please.yml`, `release.yml`) — roughly 30-40 total occurrences once you count actions used by multiple jobs. Each line gets resolved via `gh api repos/<owner>/<repo>/git/refs/tags/<tag>` then replaced inline with `@<40-char-sha> # <tag>`.

The slice ships **two surfaces**: (1) the one-time pinning sweep, and (2) a CI guard that fails the build if any future PR introduces a non-SHA-pinned action. Without the guard, the discipline drifts the moment someone copy-pastes a `uses: org/repo@v2` from a tutorial.

Out of scope (file separate if needed): action version bumps (this slice pins TO THE CURRENT TAG'S SHA, whatever version it is today — slice 084's `goreleaser-action v6→v7` migration stays separately responsible), drift detection for other repo-config files (slice 127 covers `.github/branch-protection.json`), action-allowlist policies (StepSecurity Harden-Runner block-mode in slice 118 covers the runtime side).

## Threat model

| STRIDE                                   | Threat                                                                                                                                                                                                                                                                                                                                                           | Mitigation                                                                                                                                                                                                                                                                                                                                           |
| ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing                           | n/a — no auth surface added                                                                                                                                                                                                                                                                                                                                      | n/a                                                                                                                                                                                                                                                                                                                                                  |
| **T** Tampering (HIGH — the whole point) | **Tag-jacking**: attacker with push access to an action's repo (or a compromised maintainer) moves a floating tag (`v6`) to point at malicious code. Every consumer pinned `@v6` picks it up silently on next CI run. The malicious code runs in our CI runner with our secrets in scope (GITHUB_TOKEN, anything exposed via env, anything the workflow injects) | **AC-1 through AC-6 ARE the mitigation.** SHA pins are immutable; an attacker can't retroactively change what `@<sha>` resolves to. Combined with slice 117's Harden-Runner audit-mode egress logging, any malicious egress from a compromised action that we haven't repinned to its compromised SHA would be visible in the StepSecurity dashboard |
| **R** Repudiation                        | n/a — workflow files are public; no operation needs an audit trail                                                                                                                                                                                                                                                                                               | n/a                                                                                                                                                                                                                                                                                                                                                  |
| **I** Information disclosure             | The pinned SHAs are public in the file — but the tag→SHA mapping is already public via `gh api`. No new disclosure                                                                                                                                                                                                                                               | n/a                                                                                                                                                                                                                                                                                                                                                  |
| **D** Denial of service                  | If an action's repo deletes a SHA we depend on (force-push history rewrite), our CI breaks. Low likelihood for established orgs (`actions`, `docker`, `github`, `googleapis`); higher for solo-maintainer actions. Mitigation: prefer actions from established orgs; for solo-maintainer actions, fork if the action is load-bearing                             | **AC-7 documents the failure mode** + recommends a per-action source-org review when a new action is added                                                                                                                                                                                                                                           |
| **E** Elevation of privilege (HIGH)      | A tag-jacked action with our CI's GITHUB_TOKEN can push to our repo, modify branch protection, exfiltrate secrets via OTEL/etc. Pre-slice-128, EVERY action in every workflow is a potential privilege-escalation vector                                                                                                                                         | **AC-1 + slice 117 Harden-Runner + slice 127 branch-protection drift-detect together close the loop**: SHA pins prevent the tag-jack, Harden-Runner audits the egress, branch-protection drift-detect catches if a compromised action tries to weaken governance                                                                                     |

**Threat-model verdict:** HAS-MITIGATIONS. The slice's value IS the mitigation. AC-2's CI guard is what prevents regression — without it, the discipline degrades the first time a new contributor adds a `@v2`-style line.

## Acceptance criteria

### Pinning sweep (one-time)

- [ ] AC-1: Every `uses: <action>@<ref>` line in `.github/workflows/*.yml` uses a 40-character commit SHA as `<ref>`, with a `# <tag-name>` comment for human readability. The slice-117 convention is the exemplar: `uses: step-security/harden-runner@ab7a9404c0f3da075243ca237b5fac12c98deaa5 # v2.19.3`. EXCEPTION: actions already SHA-pinned (slice 117's harden-runner) are left untouched.
- [ ] AC-2: Helper script at `scripts/check-action-pins.sh` walks all workflow files, extracts `uses:` lines, asserts each `<ref>` matches `^[0-9a-f]{40}$`, exits non-zero on any violation. Reusable locally + in CI.
- [ ] AC-3: For each action that gets pinned, the SHA chosen is whatever `gh api repos/<owner>/<repo>/git/refs/tags/<tag>` resolves to TODAY (no version bump). If the tag isn't a direct SHA (some orgs use annotated tags) follow the `.object.sha` chain to the commit.
- [ ] AC-4: Reverse-lookup verification: a separate one-off step (script or manual) confirms each chosen SHA is reachable from the action's default branch (i.e. not a SHA on a fork or a deleted branch) — protects against pinning to an unreachable commit.

### CI guard

- [ ] AC-5: New job `actions-pin-check` added to `.github/workflows/ci.yml`. Runs `scripts/check-action-pins.sh` on every PR + on main pushes. Exits non-zero (and FAILS the build) if any `uses:` line is tag-pinned instead of SHA-pinned. UNLIKE most CI hygiene jobs in this repo (slices 069/089/109/120), this one is **blocking** because regressing the pin discipline silently re-opens the supply-chain attack surface.
- [ ] AC-6: The new job follows the slice-117 harden-runner-first-step convention. Path-filter stub-twin per slice 061 ONLY if the job is added to `.github/branch-protection.json` required-checks (AC-9 below); otherwise no stub-twin needed (the job runs unconditionally and either passes or fails).

### Dependabot compatibility

- [ ] AC-7: Verify `.github/dependabot.yml` has `package-ecosystem: github-actions` configured. Dependabot understands the `# <tag>` comment convention and updates SHA-pinned actions correctly (it re-resolves the new tag's SHA + updates both the SHA and the comment). If absent, add it. Document the verification in the decisions log D1.
- [ ] AC-8: After the sweep merges, the next Dependabot run on a GitHub Action should propose a SHA-bump PR (not a tag-bump PR). Confirm by watching the next bot-PR within 1 week of merge; document the observed PR# in the decisions log D2.

### Branch protection

- [ ] AC-9: Add `actions-pin-check` to `.github/branch-protection.json` required-checks. Coordinate with slice 127 (branch-protection drift fix) — if 127 ships first, add to its list and let the drift-detect verify; if 128 ships first, slice 127's apply step picks this up. Whichever order, both checks end up in the live config.

### Docs

- [ ] AC-10: `CONTRIBUTING.md` gets a "Adding a GitHub Action" subsection (3-5 sentences): how to look up the SHA for a new action (`gh api repos/<owner>/<repo>/git/refs/tags/<tag> --jq .object.sha`), the `@<sha> # <tag>` format, the local-repro for `actions-pin-check`. Point to slice 117 D2 + this slice's decisions log for the historical context.
- [ ] AC-11: Decisions log at `docs/audit-log/128-sha-pin-all-github-actions-decisions.md`. Required entries:
  - **D1**: Per-action SHA chosen at sweep time (action name → tag → SHA, table format) — gives future contributors a clean reference for "what SHA did we land on?"
  - **D2**: Dependabot post-merge verification (AC-8 result)
  - **D3**: Coordination outcome with slice 127 (AC-9 — which slice landed first, how the branch-protection list converged)
  - **D4**: Any actions where the tag→SHA lookup was non-trivial (annotated tags, redirects, forks) — surfaces edge cases for future reference

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — pinning is mechanical; the CI guard is small (~20 lines). No refactor, no new abstractions.
- **Canvas anti-patterns (§1.6)** — "Closed proprietary connectors (defeats the OSS thesis)" applies in spirit: tag-jacked actions are a similar supply-chain failure, and SHA pinning is the mitigation. Aligns with the project's overall "security-by-default in CI" stance from slices 069/079/082/117.
- **Tech-stack table (CLAUDE.md)** — existing entry "CI/CD · GitHub Actions" is unchanged; this slice hardens the existing layer.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (CI/CD line — informational)
- `Plans/canvas/01-vision.md` §1.6 anti-patterns (supply-chain hygiene mindset)
- Slice 117 D2 (the original SHA-pin precedent for harden-runner — the convention this slice extends)
- Slice 127 (branch-protection drift fix — coordinate on the required-checks update)
- Slice 069/089/109/120 (informational CI job pattern — the SHAPE this slice's CI guard follows, but BLOCKING instead of informational)
- Slice 084 (cosign v3 + goreleaser-action v7 — explicitly NOT bundled into this slice; coordinate the SHA-pin to the CURRENT version)

## Dependencies

- None blocking. Slice 117 (Harden-Runner) merged ✓ — establishes the convention. Slice 127 (branch-protection drift fix) is in flight — coordinate at PR-time (whichever ships first, the other rebases).

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT bump any action's version. Pin TO THE CURRENT TAG'S SHA. Version bumps are Dependabot's job. The sweep is "snapshot what's there today, lock it down."
- **P0-A2**: Does NOT skip slice 117's already-pinned harden-runner line. Verify it's still pinned correctly post-sweep; leave it alone.
- **P0-A3**: Does NOT introduce a tool dependency (`pin-github-action` CLI, etc.) without justifying in the decisions log. The default approach is `gh api` + bash. A community tool is acceptable IF (a) it's actively maintained, (b) the engineer audits its source before use, (c) the dependency is bounded (i.e. used by `scripts/check-action-pins.sh` only, not by the workflows themselves).
- **P0-A4**: Does NOT make `actions-pin-check` a `continue-on-error: true` informational job. The discipline must be blocking — slice 117 + the CI guard together are the supply-chain mitigation; an informational job allows the regression we're preventing.
- **P0-A5**: Does NOT use vendor-prefixed test fixture tokens.
- **P0-A6**: Does NOT change any action's input parameters / options during the pin. Pure pin; behavior unchanged.
- **P0-A7**: Does NOT pin to a SHA that's not reachable from the action's default branch (AC-4). Pinning to a fork-SHA or an unreachable commit defeats the audit.
- **P0-A8**: Does NOT silently skip any workflow file. The audit script (AC-2) is the source-of-truth — if it greps a workflow and finds no `uses:` lines, that's fine; if it finds tag-pinned lines, those MUST be pinned.

## Skill mix

- `gh api` for tag→SHA resolution
- Bash scripting (audit + CI guard) — slice 120's `scripts/audit-deps.sh` is the closest reference for the shape
- GitHub Actions workflow YAML editing (mechanical sweep across 6 files)
- Slice 117 D2 awareness (harden-runner SHA-pin convention)
- Coordination with slice 127 (branch-protection drift) at PR-merge time

## Notes for the implementing agent

- **The sweep is mechanical but tedious.** ~22 unique actions × 1-3 occurrences each. Use a script:

  ```bash
  for action in $(grep -hE "^\s+uses: " .github/workflows/*.yml | grep -v step-security/harden-runner | sed -E 's|.*uses: ([^@]+)@([^ ]+).*|\1 \2|' | sort -u); do
    repo=$(echo "$action" | cut -d' ' -f1)
    tag=$(echo "$action" | cut -d' ' -f2)
    sha=$(gh api "repos/$repo/git/refs/tags/$tag" --jq '.object.sha' 2>/dev/null || echo "TAG-NOT-FOUND-FOR-$repo@$tag")
    echo "$repo@$tag → $sha"
  done
  ```

  Some actions use sub-paths (`github/codeql-action/init` — the repo is `github/codeql-action`, the action is the `/init` directory). The script needs to handle that — strip path segments before lookup.

- **For annotated tags** (some orgs use them — e.g. `actions/checkout` historically): `.object.sha` from the tag ref may be the TAG object's SHA, not the commit's. Chase the chain: `gh api repos/X/git/tags/<tag-sha> --jq '.object.sha'` to get to the commit. The audit script should detect this and dereference automatically.

- **codeql-action quirk**: `github/codeql-action/init@v4` and `github/codeql-action/analyze@v4` and `github/codeql-action/autobuild@v4` all share the same repo SHA (they're sub-paths of one action). Pin all three to the same SHA from `repos/github/codeql-action/git/refs/tags/v4`.

- **The CI guard MUST run on the SAME workflow file change set, not just on `**/_.yml`** — otherwise a PR could add a tag-pinned action to a NEW workflow file and the guard would miss it. The audit script should walk `.github/workflows/_.yml` glob (which covers any new file dropped in).

- **Coordination with slice 127 (branch-protection drift fix, PR #272):** if slice 127 lands first, this slice's AC-9 (add `actions-pin-check` to required-checks) becomes a one-line edit to `.github/branch-protection.json` PLUS the apply step from slice 127's docs. If THIS slice lands first, slice 127 picks up the new required-check naturally during its drift reconcile.

- **The PR will touch all 6 workflow files.** Expect prettier/pre-commit to want to format. Run `pre-commit run --all-files` BEFORE pushing to avoid the fixup-amend dance.

- **Why blocking, not informational:** Slices 069, 089, 109, 120 use the `continue-on-error: true` informational-CI-job pattern because they're surfacing findings the maintainer chooses to act on (coverage drops, vuln scans, phantom deps). Slice 128 is different: the discipline of "all actions SHA-pinned" must hold continuously to mitigate the supply-chain threat. An informational job allows the regression. The CI guard MUST fail the build; AC-9 puts it in required-checks.

## Out-of-scope (would be separate slices)

- **129 (`not-ready`)**: drift-check for other security-relevant repo-config files (`.github/dependabot.yml`, `.github/CODEOWNERS`, repo-level settings exposed via `gh api repos/.../settings`). Slice 127 covers branch-protection; the broader pattern is "any declarative config that has a live counterpart needs drift-detect." File when the second instance surfaces.
- Action version bumps (Dependabot handles via the SHA-update flow per AC-7/AC-8)
- Fork-our-own-copy of any solo-maintainer load-bearing action (AC-7's failure-mode note is the trigger; only file if a specific action becomes a concern)
- StepSecurity Harden-Runner block-mode promotion (slice 118 — already filed, gated on 14-day soak)
