# 127 — Branch-protection drift fix + recurring drift-detect CI job

**Cluster:** Infra (CI hardening / governance)
**Estimate:** 1d
**Type:** JUDGMENT

## Narrative

Filed 2026-05-18 via `/idea-to-slice` after discovery during that day's cascade-unblock session. `.github/branch-protection.json` (the declarative source-of-truth file established by slice 050 AC-11) and the live GitHub branch-protection config on `main` have silently drifted apart.

**The file** lists 12 `required_status_checks.contexts` including `Frontend · Playwright e2e` and `Frontend · vitest`. **The live config** (via `gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks`) shows 10 contexts, missing both Playwright and vitest.

The drift caused real harm: during the 2026-05-17/18 session, four PRs (#234, #259, #262, #264) sat held for hours on a "Frontend · Playwright e2e is failing" rationale that turned out to be a phantom blocker — the check wasn't actually enforced live. Multiple iteration cycles were burned diagnosing a non-existent constraint before someone read the live API output and noticed the discrepancy.

This slice ships:

1. **Reconcile** the file ↔ live drift in a maintainer-chosen direction. JUDGMENT call between (a) edit the file to match live (acknowledge that Playwright + vitest are not gates today), OR (b) re-apply the file to the GitHub API to restore Playwright + vitest as gates now that slices 119 (port-3000 fix) + 122 (api_keys idempotency) + 123 (4 unmasked specs) have cleared the underlying flakes. Maintainer's lean per the surfacing conversation is **option (b) — restore enforcement** because the original reason for the silent relaxation no longer applies.

2. **Drift-detect CI job** that fails forward — same shape as slices 069 (verification suite), 089 (npm audit), 109 (sqlc generate diff), 120 (phantom-deps audit). Job runs on every PR + on main pushes, diffs the file's sorted contexts list against the live API's, posts a sticky PR comment with the diff if drift is found, and fails non-blockingly (`continue-on-error: true`, NOT in branch-protection.json's required-checks list — informational only). The job ITSELF must not be required, otherwise we create a chicken-and-egg.

3. **Apply ritual documentation** in `CONTRIBUTING.md` / `docs/audit-log/050-*.md`: when, why, and how to run `gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection --input .github/branch-protection.json`. Document the failure-mode-of-omission: if drift accumulates silently, security controls degrade in slow motion.

Out of scope for this slice (file separate follow-ups if needed):

- Drift detection for other GitHub-config files (`.github/dependabot.yml`, `.github/CODEOWNERS`, etc.)
- Automatic drift remediation (drift-detect only REPORTS; does not auto-apply)
- Audit-log-of-config-changes (who pushed what to live, when) — that's its own slice

## Threat model

| STRIDE                              | Threat                                                                                                                                                                                                                                                                                    | Mitigation                                                                                                                                                                                                                                                                                                                       |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing                      | n/a — no auth surface added                                                                                                                                                                                                                                                               | n/a                                                                                                                                                                                                                                                                                                                              |
| **T** Tampering (HIGH)              | A bad-actor PR modifies `.github/branch-protection.json` to weaken enforcement (e.g., removes a security check). Without drift detection the file change merges but the live API stays unchanged — but on the next legitimate `gh api -X PUT` application, the weakened file becomes live | **AC-3 (drift-detect CI job) IS the mitigation.** Posts a visible comment on every PR that touches `.github/branch-protection.json` showing the file-vs-live diff. Combined with `CODEOWNERS` review on `.github/**` (already in place per slice 050) the bad-actor flow needs both the file edit AND maintainer review approval |
| **R** Repudiation                   | A maintainer relaxes live branch-protection via `gh api` without updating the file (e.g., to admin-merge through a flake). The file's "source of truth" claim becomes a lie                                                                                                               | **AC-3 also covers this direction** — drift-detect fires whenever file ≠ live regardless of which side moved. The PR comment makes the un-tracked relaxation visible at the next PR-touch of any related file                                                                                                                    |
| **I** Information disclosure        | The file IS public in the repo. The live API output is also readable by anyone with repo-read access. No new disclosure                                                                                                                                                                   | n/a                                                                                                                                                                                                                                                                                                                              |
| **D** Denial of service             | drift-detect runs `gh api ...` once per CI invocation. Bounded by a few seconds. GitHub API rate limit (5000/hr authenticated) is nowhere near at risk                                                                                                                                    | n/a                                                                                                                                                                                                                                                                                                                              |
| **E** Elevation of privilege (HIGH) | Same as Tampering. The whole class of "weaken a security control by editing the file without applying it" — or by applying without updating the file — degrades the security posture. Drift-detect closes the loop                                                                        | **AC-3 + AC-4 (maintainer-merge gate on `.github/branch-protection.json` changes via CODEOWNERS)** combined                                                                                                                                                                                                                      |

**Threat-model verdict:** HAS-MITIGATIONS. The slice's entire value IS the mitigation chain. Without AC-3 (drift-detect), the threats above remain open.

## Acceptance criteria

### Reconcile (one-time)

- [ ] AC-1: Maintainer picks ONE direction for the file ↔ live reconcile and records the choice + rationale in `docs/audit-log/127-branch-protection-drift-fix-decisions.md` D1:
  - (a) Edit `.github/branch-protection.json` to match live (remove Playwright + vitest from required-checks)
  - (b) **Maintainer's lean**: Apply the file to live (restore Playwright + vitest as required-checks)
- [ ] AC-2: Whichever direction is chosen, the file and live config are in sync at PR-merge time. Verify via `diff <(jq -S .required_status_checks.contexts .github/branch-protection.json) <(gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks --jq '.contexts | sort')` returning zero-exit.

### Drift-detect CI job

- [ ] AC-3: New job `branch-protection-drift` added to `.github/workflows/ci.yml`. Runs on every PR (regardless of paths touched) + on `push` to main. Uses `gh api` to read live config, compares against the in-tree file, posts a sticky PR comment with a diff if any context list differs. `continue-on-error: true` — informational only. NOT added to `.github/branch-protection.json` required-checks (chicken-and-egg avoidance).
- [ ] AC-4: The job's sticky comment format follows the slice-120 pattern (HTML comment marker for re-find/replace; sample shape: `<!-- atlas-branch-protection-drift-marker -->` then a markdown block with the diff + a "run `gh api -X PUT ... --input .github/branch-protection.json` to reconcile" hint).
- [ ] AC-5: Stub-twin job per slice 061 path-filter convention (same job name on the no-code-changes path so branch protection sees the named check present). The drift-detect job is small enough that a stub may not be needed; engineer's JUDGMENT — if added, document why.
- [ ] AC-6: harden-runner step at the top of the job per slice 117 convention (`egress-policy: audit`, `disable-sudo: true`).

### Tests

- [ ] AC-7: Local repro: a shell script at `scripts/check-branch-protection-drift.sh` that runs the same comparison locally. Same exit semantics as the CI job (0 = in sync; 1 = drift detected). Reuses the same `jq` filter.
- [ ] AC-8: Integration test exercising the script against a fixture: 2 fixture configs (in-sync and drift). Asserts the script exits with the expected code for each fixture.

### Docs

- [ ] AC-9: `CONTRIBUTING.md` gets a "Branch protection" subsection (3-5 sentences) covering: where the file lives, when to update it, the apply command (`gh api -X PUT ...`), the drift-detect CI signal to watch for in PR comments.
- [ ] AC-10: `docs/audit-log/127-branch-protection-drift-fix-decisions.md` records: D1 (direction chosen for AC-1), D2 (rationale for stub-twin decision in AC-5), D3 (where the apply ritual is documented — pointer for future maintainers).

## Constitutional invariants honored

- **Tenant isolation enforced at DB layer via RLS** (canvas §5.4) — branch-protection is the GitHub-level governance layer; the file ↔ live drift doesn't touch DB-layer enforcement. But it does touch the "security controls must be explicit + observable" stance from CLAUDE.md.
- **CLAUDE.md "Surgical fixes only"** — the drift-detect job is a small additive CI job. The reconcile is a one-time apply. No refactor.
- **No proprietary collector agents** (canvas anti-pattern) — `gh api` is the official GitHub CLI; no third-party drift-detection product.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (CI/CD entry — informational CI job pattern aligns)
- Slice 050 AC-11 (the original branch-protection-as-code intent + the `gh api -X PUT` apply ritual)
- Slices 069, 089, 109, 120 (the informational CI job pattern — all use `continue-on-error: true` + NOT in required-checks)
- Slice 117 (harden-runner step at top of CI job convention)

## Dependencies

- None — pure additive infra slice. All referenced slices (050, 069, 089, 109, 117, 120) already merged.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT add the new `branch-protection-drift` job to `.github/branch-protection.json` required-checks. The job is informational by design — making it required creates a chicken-and-egg (the job that detects drift becomes part of the drift).
- **P0-A2**: Does NOT auto-apply drift (no `gh api -X PUT` from CI). The drift-detect job REPORTS only; the maintainer applies. Auto-apply would let a bad-actor PR weaken live config by merging a weakened file.
- **P0-A3**: Does NOT skip the harden-runner step. Slice 117's convention applies uniformly.
- **P0-A4**: Does NOT use vendor-prefixed test fixture tokens — neutral `test-*` only.
- **P0-A5**: Does NOT pick reconcile direction (a) — "edit file to match live" — WITHOUT documenting why the file's original intent is being weakened. The file's claim is the contract; weakening it requires explicit rationale, not silent acceptance.
- **P0-A6**: Does NOT change the `enforce_admins: true` flag or the `required_pull_request_reviews` shape. Out of scope.

## Skill mix

- GitHub Actions workflow authoring (informational job pattern — slices 069/089/109/120 are reference)
- `gh api` / GitHub REST API (`/repos/{owner}/{repo}/branches/{branch}/protection`)
- `jq` for context-list comparison
- shadcn-free bash scripting (the local repro script in AC-7)
- Branch-protection semantics (required-checks-by-name, stub-twin pattern for path-filtered jobs)

## Notes for the implementing agent

- **The local-repro script (AC-7) is the load-bearing piece for review.** Without it, contributors can't verify drift findings before pushing. Make it small (< 30 lines), well-commented, and runnable from any directory.
- **The CI job's sticky-comment pattern is established** (slice 120's `phantom-deps` is the closest reference). Reuse the marker-comment pattern + `gh pr comment --edit-last` semantics.
- **For AC-1 direction (b)** (the maintainer's lean): the apply command is `gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection --input .github/branch-protection.json`. Per slice 050's `$deviations_from_slice_050_AC11` note in the file itself, this PUT replaces the entire protection config (not a partial update) — so the in-tree file MUST be complete + valid before applying.
- **The reconcile direction (b) has a hidden constraint**: GitHub's API rejects unknown context names. If the file lists `Frontend · Playwright e2e` and that job hasn't run on a recent main commit, the PUT may succeed but the check stays "Expected — waiting for status to be reported" until the next PR triggers it. Note this in the decisions log D1.
- **AC-2's diff-zero-exit is the merge gate**. After whichever direction is chosen, the diff command MUST return exit 0 before this PR can merge. The drift-detect CI job that AC-3 ships will then keep it honest going forward.
- **Why this slice can't ship until slice 123 ships** (if option b is chosen): slice 119 fixed port-3000 + 122 fixed api_keys, but 4 specs still fail (slice 123 territory). If we restore `Frontend · Playwright e2e` as required NOW, those 4 spec failures become merge-blockers across the project. Option (b) implementation should wait until 123 lands; the slice itself can still merge with option (a) (file-to-live) immediately to stop the bleeding.

## Out-of-scope (would be separate slices)

- **128 (`not-ready`)**: drift detection for other GitHub-config files (`.github/dependabot.yml`, `.github/CODEOWNERS`, repo-level settings). Same pattern, different surfaces.
- **129 (`not-ready`)**: audit log of branch-protection / repo-settings changes (who pushed what to live via API, when). Cross-cuts with slice 124's audit-log aggregator — could feed into it.
- StepSecurity Harden-Runner's posture-check feature (would also catch this kind of config drift) — out of scope for the standalone-CI approach this slice takes.
