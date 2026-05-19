# ADR 0005 — Branch-protection drift CI auth: fine-grained PAT (not GitHub App)

- **Status:** Accepted
- **Date:** 2026-05-18
- **Slice:** [#158](https://github.com/mgoodric/security-atlas/issues/158)
- **Decisions log:** [`docs/audit-log/158-branch-protection-drift-real-fix-decisions.md`](../audit-log/158-branch-protection-drift-real-fix-decisions.md)

## Context

The slice-127 `branch-protection-drift` CI job calls
`gh api repos/.../branches/main/protection/required_status_checks` to
compare the file-as-source-of-truth (`.github/branch-protection.json`)
against the live GitHub branch-protection config on `main`. That API
endpoint requires the `Administration: Read` repo permission.

The per-run `GITHUB_TOKEN` minted for every workflow job cannot be
granted `Administration` — the GHA workflow schema has no
`administration` scope. Valid `permissions:` scopes are enumerated by
actionlint and the GHA docs (`actions`, `artifact-metadata`,
`attestations`, `checks`, `contents`, `deployments`, `discussions`,
`id-token`, `issues`, `models`, `packages`, `pages`, `pull-requests`,
`repository-projects`, `security-events`, `statuses`). PR #311
attempted a one-line fix by adding `administration: read` to the job's
permissions block; the workflow file was schema-invalid and GHA
silently dropped it at parse, which is why the slice-127 drift detector
never actually fired.

Two viable auth paths exist for granting `Administration: Read` to a
CI step:

- **(A) Fine-grained PAT** stored in
  `secrets.BRANCH_PROTECTION_READ_TOKEN`. Scoped to this repo only;
  `Administration: Read` and `Contents: Read` only; 90-day expiry;
  rotation via maintainer ritual.
- **(B) GitHub App** installed only on this repo with the same
  permissions. Token minted per-run via
  `actions/create-github-app-token` (SHA-pinned). No long-lived
  secret in the repo — the App's private key is the secret.

## Decision

**(A) Fine-grained PAT.**

The repo is currently single-maintainer (slice 050 dropped
`required_approving_review_count` to 0 specifically because there is
no second reviewer). Operational overhead of a GitHub App — install,
private-key management, mint-per-run, rotation — is materially higher
than a PAT for a single-maintainer surface, and the security delta
versus a 90-day-rotating fine-grained PAT scoped to one repo + one
permission is small.

The trade is explicitly recorded so the GitHub App option can be
re-evaluated when the contributor base grows past ~3 active
committers (the same trigger that revisits
`required_approving_review_count` per `docs/RELEASE_READINESS.md` §10).

### Defenses on top of the PAT

- PAT scoped to `Administration: Read` + `Contents: Read` only — no
  Write of any kind.
- PAT scoped to this repo only (`mgoodric/security-atlas`) — not
  user-wide.
- 90-day expiry — maintainer ritual to rotate; the script falls back
  to a clear "BRANCH_PROTECTION_READ_TOKEN not configured" message
  with a pointer to this ADR when the secret is missing or expired.
- PAT only exposed to the one `gh api` step via `env:` block — not
  to any other step in the job.
- Drift-compare job split from the validate job and gated to
  `github.event_name == 'push' && github.ref == 'refs/heads/main'`
  — the PAT is never reachable from a `pull_request:` workflow run
  (which would let a malicious PR modify the workflow to exfiltrate
  the secret). Slice 158 D2 documents that gating.
- StepSecurity Harden-Runner (slice 117) audits outbound calls on
  every job, including the drift-live job. Anomalous egress would
  surface on the StepSecurity dashboard.
- `actionlint` pre-commit + CI hook (slice 158 D3) prevents the next
  contributor from re-introducing an invalid permission scope. The
  guard is checked by the fixture
  `scripts/actionlint-fixture-invalid-scope.yml` + smoke test
  `scripts/check-actionlint-fixture.sh`.

## Maintainer setup steps

The PAT does not exist yet. On first deploy of this slice:

1. Create a fine-grained PAT at https://github.com/settings/tokens?type=beta
   with these settings:
   - Resource owner: `mgoodric`
   - Repository access: **Only select repositories** → `security-atlas`
   - Repository permissions:
     - `Administration` → **Read-only**
     - `Contents` → **Read-only** (required for `actions/checkout` parity in some `gh` operations)
   - Expiration: 90 days
2. Add the PAT to repository secrets at
   https://github.com/mgoodric/security-atlas/settings/secrets/actions
   as `BRANCH_PROTECTION_READ_TOKEN`.
3. Calendar a 90-day reminder to rotate. Rotation = generate a new PAT,
   update the secret, delete the old PAT. The drift-live job will
   silently use the new secret on the next push to `main`.
4. (Optional) After rotation, verify by pushing a no-op commit to
   `main` and confirming the `Infra · branch-protection (live drift)`
   workflow run reports either "No drift detected" or a specific drift
   diff (not "secret not configured").

## Alternatives considered

- **GitHub App.** Rejected per the operational-cost trade above. Will
  re-evaluate at the contributor-base trigger noted in §1.
- **Stay on `GITHUB_TOKEN`.** Rejected — the call requires
  `Administration: Read` which `GITHUB_TOKEN` cannot have. PR #311
  proved this fails silently.
- **Drop the drift detector entirely.** Rejected — drift caused 4
  PRs to sit held for hours on 2026-05-17/18 (slice 127's filing
  incident). The detector has real load-bearing value.
- **Skip the live compare and validate the file shape only.**
  Rejected as the only surface — file-shape validation alone cannot
  catch the slice-127 incident (file moved forward, live did not).
  Slice 158 keeps shape validation as the PR-time surface AND adds
  the live compare on push-to-main.

## Consequences

- One new repo secret (`BRANCH_PROTECTION_READ_TOKEN`) for the
  maintainer to manage on a 90-day rotation. Documented in this ADR
  - in CONTRIBUTING.md "Branch protection" subsection.
- The PR-time signal for live drift is dropped — drift now surfaces
  on the push-to-main workflow run, not on the PR that caused it.
  This is acceptable because the check is informational (not
  required) and the latency penalty is at most one merge cycle.
- The actionlint guard prevents the PR-#311 mistake class from
  recurring. Cost: ~2 seconds added to the existing `pre-commit ·
all hooks` CI job for the install + run.
