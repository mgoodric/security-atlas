# 096 — Repo cleanup deletion candidates (follow-on to slice 071)

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK (mechanical execution after maintainer approval)
**Status:** `in-review` (PR open · 47/47 worktrees removed cleanly · awaiting CI + merge)

## Execution record (2026-05-16)

Maintainer issued blanket per-row approval verbally: _"You can clear the stale worktrees."_ Per AC-1, the verbal blanket approval satisfies the per-row gate; all rows in §Category 15 below are treated as `[x] approve`.

Pre-flight ground truth: `git worktree list` at slice start surfaced 47 stale worktrees on disk, not 49. The slice doc's row 45 (`security-atlas-074`) was already manually removed prior to slice execution; the row-49 placeholder did not materialize. Worktrees for slices 075, 076 (merged this session) were also already absent.

Clean-tree audit (AC-2): all 47 reported `git status --short` empty. No removals required `--force`; no uncommitted work was discarded.

| Result        | Count | Notes                                                                    |
| ------------- | ----- | ------------------------------------------------------------------------ |
| Removed clean | 47    | `git worktree remove <path>` succeeded without `--force` on every entry. |
| Skipped       | 2     | `security-atlas-074` (already gone), placeholder row 49 (never existed). |
| Force-removed | 0     | Per AC-3, no entry required `--force`.                                   |
| Deferred      | 0     | No rows marked `defer to slice NNN`.                                     |
| Rejected      | 0     | No rows rejected by maintainer.                                          |

`git worktree prune -v` followed the removal sweep (AC-4); output was empty (no orphan `.git/worktrees/` metadata).

Final `git worktree list` state (AC-5):

```
/Users/gmoney/Development/security-atlas 6e25202 [infra/096-repo-cleanup-deletions]
```

1-worktree state (main only), the AC-5 expected end state when executed from the main worktree.

Branches associated with the removed worktrees were NOT deleted, per P0-A5. The branches remain in the local clone as a recoverable reference; cleanup is out of scope.

## Narrative

Follow-on to slice 071 (the 16-category repo cleanup audit). Slice 071 surfaced + classified every deletion candidate but explicitly DOES NOT delete anything — deletions are the maintainer's call, not the engineer's. This slice carries every deletion candidate as a maintainer-reviewable list. Maintainer approves each row (or rejects, or asks for modification); engineer then executes the approved deletions in one PR.

**Gating condition:** slice 071 merged AND maintainer has reviewed this file and either (a) approved-all in a PR comment, OR (b) edited the rows below with per-row approval markers (`[x] approve` / `[ ] reject` / `[x] defer to slice NNN`). Until that happens, slice stays `not-ready`.

The slice ships ONE PR with one Conventional Commit (`chore(infra): execute approved deletions from slice 071 audit (#096)`). Pre-commit clean. CI green. No new tests required (deletions are mechanical; the existing test suite is the regression gate).

## Deletion candidates by category

### Category 15 — Stale git worktrees on disk (49 candidates)

These are filesystem entries under the maintainer's `~/Development/` directory tree, NOT in-repo files. The deletion is `git worktree remove <path>` for each. Every branch listed is already merged to `main` (verified by slice 071 audit). All 49 are safe to remove.

| #   | Worktree path           | Branch                                                    | Evidence (merged at)           |
| --- | ----------------------- | --------------------------------------------------------- | ------------------------------ |
| 1   | `../security-atlas-007` | `catalog/007-soc2-crosswalk-loader`                       | merged at slice 007 PR         |
| 2   | `../security-atlas-008` | `catalog/008-ucf-graph-traversal-api`                     | merged at slice 008 PR         |
| 3   | `../security-atlas-009` | `control-as-code/009-control-bundle-format`               | merged at slice 009 PR         |
| 4   | `../security-atlas-010` | `control-as-code/010-soc2-control-kit`                    | merged at slice 010 PR         |
| 5   | `../security-atlas-011` | `control-as-code/011-manual-control-attestation`          | merged at slice 011 PR         |
| 6   | `../security-atlas-012` | `controls/012-control-state-evaluation`                   | merged at slice 012 PR         |
| 7   | `../security-atlas-013` | `evidence-pipeline/013-evidence-ledger-write-api`         | merged at slice 013 PR         |
| 8   | `../security-atlas-014` | `evidence-pipeline/014-schema-registry-service`           | merged at slice 014 PR         |
| 9   | `../security-atlas-015` | `evidence-pipeline/015-nats-jetstream-ingestion-stage`    | merged at slice 015 PR         |
| 10  | `../security-atlas-017` | `scope/017-scope-dimensions-applicability`                | merged at slice 017 PR         |
| 11  | `../security-atlas-018` | `scope/018-framework-scope-intersection`                  | merged at slice 018 PR         |
| 12  | `../security-atlas-019` | `risk/019-risk-register-crud`                             | merged at slice 019 PR         |
| 13  | `../security-atlas-021` | `risk/021-exception-waiver-workflow`                      | merged at slice 021 PR         |
| 14  | `../security-atlas-022` | `policies/022-policy-library`                             | merged at slice 022 PR         |
| 15  | `../security-atlas-023` | `policies/023-policy-acknowledgment`                      | merged at slice 023 PR         |
| 16  | `../security-atlas-024` | `vendor/024-vendor-lite-module`                           | merged at slice 024 PR         |
| 17  | `../security-atlas-025` | `auth/025-auditor-role-scoped-access`                     | merged at slice 025 PR         |
| 18  | `../security-atlas-026` | `audit/026-sample-pull-primitives`                        | merged at slice 026 PR         |
| 19  | `../security-atlas-027` | `audit/027-walkthrough-recording`                         | merged at slice 027 PR         |
| 20  | `../security-atlas-028` | `audit/028-audit-period-freezing`                         | merged at slice 028 PR         |
| 21  | `../security-atlas-029` | `audit/029-audit-hub-comments`                            | merged at slice 029 PR         |
| 22  | `../security-atlas-033` | `auth/033-postgres-rls-enforcement`                       | merged at slice 033 PR         |
| 23  | `../security-atlas-034` | `auth/034-oidc-rp-local-users`                            | merged at slice 034 PR         |
| 24  | `../security-atlas-035` | `auth/035-rbac-abac-opa`                                  | merged at slice 035 PR         |
| 25  | `../security-atlas-036` | `infra/036-s3-artifact-store`                             | merged at slice 036 PR         |
| 26  | `../security-atlas-037` | `infra/037-docker-compose-self-host`                      | merged at slice 037 PR         |
| 27  | `../security-atlas-039` | `infra/039-cli-release-pipeline`                          | merged at slice 039 PR         |
| 28  | `../security-atlas-042` | `frontend/042-audit-workspace-view`                       | merged at slice 042 PR         |
| 29  | `../security-atlas-044` | `connectors/044-github-connector`                         | merged at slice 044 PR         |
| 30  | `../security-atlas-045` | `connectors/045-okta-connector`                           | merged at slice 045 PR         |
| 31  | `../security-atlas-046` | `connectors/046-1password-connector`                      | merged at slice 046 PR         |
| 32  | `../security-atlas-047` | `connectors/047-osquery-fleet-connector`                  | merged at slice 047 PR         |
| 33  | `../security-atlas-048` | `connectors/048-jira-linear-connector`                    | merged at slice 048 PR         |
| 34  | `../security-atlas-049` | `connectors/049-manual-upload-csv-connector`              | merged at slice 049 PR         |
| 35  | `../security-atlas-050` | `infra/050-public-release-readiness`                      | merged at slice 050 PR         |
| 36  | `../security-atlas-051` | `fix/051-admincreds-tenant-derivation`                    | merged at slice 051 PR         |
| 37  | `../security-atlas-052` | `risk/052-risk-hierarchy-schema`                          | merged at slice 052 PR         |
| 38  | `../security-atlas-053` | `risk/053-risk-theme-tagging`                             | merged at slice 053 PR         |
| 39  | `../security-atlas-054` | `risk/054-aggregation-rules-engine`                       | merged at slice 054 PR         |
| 40  | `../security-atlas-059` | `spine/059-feature-flags`                                 | merged at slice 059 PR         |
| 41  | `../security-atlas-060` | `frontend/060-admin-settings-ui`                          | merged at slice 060 PR         |
| 42  | `../security-atlas-061` | `ci/061-path-filter`                                      | merged at slice 061 PR         |
| 43  | `../security-atlas-062` | `admin/062-admin-bff-backend-endpoints`                   | merged at slice 062 PR         |
| 44  | `../security-atlas-063` | `frontend/063-admin-sso-form-enable`                      | merged at slice 063 PR         |
| 45  | `../security-atlas-074` | `?` (slice 074 logo design — `f3d95d4`)                   | merged at slice 074 PR #180    |
| 46  | `../security-atlas-079` | `infra/079-quarantine-playwright-e2e`                     | merged at slice 079 PR         |
| 47  | `../security-atlas-080` | `infra/080-fix-release-tag-infrastructure`                | merged at slice 080 PR #166    |
| 48  | `../security-atlas-081` | `infra/081-pre-push-hook-status-flip-guidance`            | merged at slice 081 PR #165    |
| 49  | (placeholder)           | (verify with `git worktree list` at slice-execution time) | post-071 worktrees may surface |

**Anti-evidence (what removing them breaks):** nothing in the in-repo code path. Each worktree is an on-disk dev artifact; the canonical source is `main` in the canonical clone. Removing a worktree DOES delete any uncommitted local changes in that worktree's working tree — verify each with `cd <path> && git status` before `git worktree remove`. Recommend the engineer scripts a pre-flight `for d in ../security-atlas-*; do echo "=== $d ==="; cd $d && git status --short; cd -; done` and aborts on any non-clean tree.

**Proposed action:** `git worktree remove ../security-atlas-NNN` for each. Use `--force` only after verifying the working tree is clean. ALSO prune the `.git/worktrees/` metadata via `git worktree prune` once all removals complete.

**Total disk reclaim:** estimated multiple GB (each worktree mirrors the repo's git objects + a checked-out tree).

### Category 13 — Dead Go code (0 candidates)

`go vet ./...` is silent on `main`. No KEEP-vs-CANDIDATE classification needed at this audit pass. Re-evaluate in a future cleanup slice when `staticcheck` or `golangci-lint`'s `unused` linter is adopted (slice 071 decisions log D2).

### Category 14 — Dead `web/` code (0 candidates this pass)

`tsc --noEmit` + `eslint` + `knip` not run locally per slice 071 decisions log D3 (would require multi-minute `npm install -w web` and add a new devDep). CI's `Frontend · vitest` + `Frontend · lint` (slice 078) green on `main` is the operative current-state signal. Re-evaluate in a future targeted slice when actual dead-export noise surfaces.

### Category 12 — Orphan fixtures (0 candidates)

All 14 `fixtures/readme-demo/` files have an explicit consumer in the capture pipeline (verified by slice 071 audit). No deletion candidates.

### Categories 1-11, 16 — No deletion candidates

These categories produced only "Updated-in-place" or "Up-to-date" findings in slice 071. No deletions. Per the slice doc's load-bearing constraint, file deletions stay deletion-candidates only if a positive case for deletion was made.

## Acceptance criteria

- [ ] AC-1: maintainer has approved each row in §"Category 15" (via PR comment or per-row checkbox edit). Rows marked `defer to slice NNN` move to that slice instead of executing here.
- [ ] AC-2: every approved worktree removal is preceded by a clean-tree verification (`git status --short` exits with no output). Engineer aborts on any non-clean tree and surfaces the conflict for maintainer review.
- [ ] AC-3: `git worktree remove` (or `git worktree remove --force` when explicitly approved) is executed for every approved row.
- [ ] AC-4: `git worktree prune` is run after the removal sweep to clean up `.git/worktrees/` metadata.
- [ ] AC-5: a final `git worktree list` is committed as a `chore(infra)` follow-up note in the slice PR body, showing the canonical 2-worktree state (main + this slice's worktree) or 1-worktree (main only) if executed from the main worktree.
- [ ] AC-6: this file is updated in the same PR to record per-row execution status (`approved + removed` / `approved + deferred to slice NNN` / `rejected by maintainer`).
- [ ] AC-7: no in-repo file is deleted, renamed, moved, or modified by this slice (except this file itself recording AC-6 status). The slice's scope is filesystem hygiene outside `git`'s tracked file tree.
- [ ] AC-8: pre-commit clean (trivially — the only in-repo edit is the AC-6 status update). CI green (trivially — no code changes).

## Constitutional invariants honored

- **CLAUDE.md "Ask before destructive operations":** every destructive op (worktree removal) is gated by maintainer per-row approval. The default state of every row is `not-approved`; only an explicit maintainer act flips it to `approved + execute`.
- **CLAUDE.md style — no emojis, Conventional Commits, DCO sign-off.**

## Dependencies

- Slice 071 (`backlog/071-repo-cleanup-audit`) **merged** AND its audit report + decisions log + this slice all on `main`.
- **Maintainer approval** captured in a comment on this slice's PR OR per-row checkbox edit on this file.

## Anti-criteria (P0 — block merge)

- **P0-A1:** does NOT execute any worktree removal that the maintainer has not explicitly approved. Default state of every row is `not-approved`; approval is an explicit edit.
- **P0-A2:** does NOT delete any in-repo file. The slice's surface is `git worktree remove` (filesystem dev-artifact hygiene), NOT `git rm` (tracked-file deletion). If any tracked-file deletion is wanted, it requires its own slice with its own review.
- **P0-A3:** does NOT run `git worktree remove --force` without explicit per-row approval AND a recorded clean-tree-verification step. Force-removal silently discards uncommitted work.
- **P0-A4:** does NOT batch with any in-repo cleanup. This slice is filesystem hygiene only.
- **P0-A5:** does NOT delete any branch (local or remote). Branch deletion is a separate decision; the worktree removal is unrelated.

## Skill mix (3–5)

- `engineering-advanced-skills:tech-debt-tracker` (the deletion-candidate list IS a debt list — each row has evidence + anti-evidence + proposed action)
- `engineering-advanced-skills:runbook-generator` (each approved row becomes a runbook step: pre-flight verify clean tree → execute removal → record result)
- `simplify` (the slice is small; keep the PR + the commit message lean)

## Notes for the implementing agent

- The slice executes ONLY after maintainer approval. The default approval state of every row is `not-approved`; the slice does nothing until the maintainer either (a) edits this file with per-row checkboxes or (b) leaves a PR comment that explicitly approves the full sweep.
- Re-run `git worktree list` at slice-execution time to discover any new worktrees created since slice 071's audit. Add them as rows requiring approval before they can be removed; the discovery is in-scope, the action is gated.
- The "Stale worktrees still on disk" line in `_STATUS.md` will need updating after this slice's execution. That update is in scope.
- Do NOT touch any of the 16 audited categories' in-repo files in this slice. Those were either updated-in-place by slice 071 or explicitly out-of-scope.
- If the maintainer surfaces a new deletion target during the approval-cycle (e.g., "while you're at it, remove the orphan `tools/logo-gen/` files"), that's a separate slice — don't bundle.
