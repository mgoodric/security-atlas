# Slice 158 — Branch-protection drift: real permission fix — decisions log

> JUDGMENT-type slice. Per `Plans/prompts/04-per-slice-template.md`, the engineer
> makes the subjective build-time calls and records them here; the maintainer
> iterates post-deployment if any decision proves wrong. None of these calls
> touches the constitutional product-runtime AI-assist boundary.

## D1 — Auth path for `Administration: Read`: fine-grained PAT (chosen)

**Picked:** Fine-grained PAT stored in `secrets.BRANCH_PROTECTION_READ_TOKEN`.

**Alternatives considered:**

| Option               | For                                                                                                                                                                                          | Against                                                                                                                                                                    |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Fine-grained PAT     | One repo secret; one rotation ritual; no separate App-install + private-key management; matches the single-maintainer operating mode of the repo today                                       | Long-lived (90-day) credential — leak window measured in months not minutes                                                                                                |
| GitHub App           | Token minted per-run (`actions/create-github-app-token`); no long-lived secret in the repo; per-install audit trail in GitHub's app dashboard; cleaner story when the contributor base grows | Install + private-key management overhead; the private key IS still a long-lived secret (just stored differently); single-maintainer repo doesn't yet need the App surface |
| Stay on GITHUB_TOKEN | Zero new secrets                                                                                                                                                                             | Doesn't work — `administration` is not a valid `GITHUB_TOKEN` scope. This is the PR #311 mistake.                                                                          |

**Rationale:** The slice doc explicitly recommended PAT as the default
("lower operational cost; 90-day rotation"). The repo is single-maintainer.
The defenses layered on top of the PAT (repo-scoped, read-only, env-scoped to
one step, push-to-main only, harden-runner audit, actionlint guard against
future invalid-scope errors) materially constrain the blast radius. The App
option is filed as a re-evaluation trigger at the same threshold that
revisits required-PR-review-count (~3 active committers, per
`docs/RELEASE_READINESS.md` §10).

**Confidence:** High. The PAT is a routine pattern; the layered defenses are
material; the GitHub App option remains available later without rework cost
beyond swapping the secret type.

**Receipts:**

- `docs/adr/0005-branch-protection-pat-vs-app.md` — full ADR with maintainer setup steps.
- `.github/workflows/ci.yml::branch-protection-drift-live` — `env: GH_TOKEN: ${{ secrets.BRANCH_PROTECTION_READ_TOKEN }}` scoped to one step.
- `scripts/check-branch-protection-drift.sh` — header updated to reference the new secret name.

---

## D2 — PR-time vs push-on-main gating for the elevated drift step: push-on-main only (chosen)

**Picked:** Split into two jobs.

- `branch-protection-drift-validate` — runs on `pull_request:` events.
  Validates ONLY the file shape (`jq -e .` + `.required_status_checks.contexts`
  presence). No `gh api` call. No elevated token. Sticky-comment surface
  preserved for shape errors.
- `branch-protection-drift-live` — runs on `push: branches: [main]`. Calls
  `gh api` with the PAT. Drift findings surface in `$GITHUB_STEP_SUMMARY`
  and as a workflow artifact, not as a PR sticky comment (no PR exists on a
  push event).

**Alternatives considered:**

| Option                                                                    | For                                                                                                                                                            | Against                                                                                                                                                                                                                  |
| ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Push-on-main only (chosen)                                                | PAT never reachable from PR workflow; zero elevation surface for malicious PR; matches the same-repo Step-Security defense surface as other elevated workflows | Loses PR-time live-compare signal; drift surfaces on the post-merge run instead of the PR that caused it                                                                                                                 |
| Author-allowlist `if: github.actor == 'mgoodric'` on a single PR-time job | Keeps PR-time live signal                                                                                                                                      | An allowlisted single job is still run from the PR-branch workflow file — a maintainer-impersonation (or account compromise) could modify the file to exfiltrate the secret. The CI runs whatever the PR branch defines. |
| `pull_request_target` with author validation                              | Keeps PR-time live signal AND uses the main-branch workflow file (safer)                                                                                       | `pull_request_target` is well-known footgun territory; the GHA docs warn that mistakes here are how secrets leak; the additional surface is hard to justify for an informational check                                   |

**Rationale:** The slice-127 drift detector is informational, not required.
The PR-time signal is a convenience, not a load-bearing contract. The risk
profile of `pull_request_target` for a check that's informational only is a
bad trade. Push-on-main makes the secret unreachable from any PR-branch
workflow file by construction.

**Confidence:** High. The split is structural (two jobs) rather than
permission-trickery (one job with conditional logic), so the reasoning is
easy for the next reader to audit.

**Receipts:**

- `.github/workflows/ci.yml::branch-protection-drift-validate` — `if: github.event_name == 'pull_request'`
- `.github/workflows/ci.yml::branch-protection-drift-live` — `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`
- Sticky-comment text updated to point at the live-compare job for actual drift findings.

---

## D3 — actionlint integration: pre-commit hook + CI (both, chosen)

**Picked:** Both surfaces.

- Pre-commit hook (`local` entry pointing at the system `actionlint` binary)
  scoped to `^\.github/workflows/.*\.ya?ml$`. Contributors who install
  pre-commit via `just install-hooks` (the documented onboarding path) get
  the guard on every commit.
- CI step inside the existing `pre-commit · all hooks` job:
  - Install actionlint (download script pinned to v1.7.12).
  - The pre-commit suite picks the new hook up via `pre-commit run
--all-files` (no separate `actionlint` invocation needed in CI).
  - Smoke-test step (`bash scripts/check-actionlint-fixture.sh`) explicitly
    asserts the fixture is still caught, so a future actionlint upstream
    change that drops the `administration` diagnostic surfaces as its own
    named failing step.

**Alternatives considered:**

| Option               | For                                                                                                                                                          | Against                                                                                                                     |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| Pre-commit hook only | Catches at commit time; instant feedback                                                                                                                     | Requires contributor to have run `just install-hooks`; a drive-by commit via the GitHub UI editor would bypass it           |
| CI step only         | No contributor install required; uniform enforcement                                                                                                         | Feedback latency = PR creation + CI run; commits land in the PR before the guard fires                                      |
| Both (chosen)        | Fast local feedback for contributors who installed hooks; CI enforcement for everyone else; smoke-test fixture asserts the diagnostic itself is still firing | Tiny extra surface to maintain (one entry in `.pre-commit-config.yaml`, one install step + one smoke-test step in `ci.yml`) |

**Rationale:** The cost of "both" is small (an entry in
`.pre-commit-config.yaml` plus a few-second CI install step). The benefit is
that the wrong-permission-scope class of error gets caught at the earliest
possible point for every contributor path — local commits AND CI for UI
edits / forks / bypass cases. The smoke-test fixture is the durable check
that the guard ITSELF still works after upstream actionlint upgrades.

**Confidence:** High. This is the slice-117/128 pattern (`harden-runner`

- `actions-pin-check`) — guard via tooling at the earliest surface, then
  backstop with a CI step.

**Receipts:**

- `.pre-commit-config.yaml` — new `actionlint` local hook.
- `.github/workflows/ci.yml::precommit` — added `Install actionlint` step,
  added `Slice 158 actionlint fixture smoke test` step.
- `scripts/actionlint-fixture-invalid-scope.yml` — negative test fixture
  carrying the exact `administration: read` mistake.
- `scripts/check-actionlint-fixture.sh` — smoke test wrapper.

---

## Out of scope (filed elsewhere or deliberately deferred)

- The "push-on-main 0s failure" pattern observed at the e116b3b → 8df4e40
  merge stretch is explicitly out of scope per the slice doc. If the
  pattern recurs after this slice merges, file a separate spillover slice.
- Migration from PAT to GitHub App is deliberately deferred. Trigger:
  contributor base passes ~3 active committers (same trigger as
  `required_approving_review_count` per `docs/RELEASE_READINESS.md` §10).
- Pre-existing shellcheck warnings (SC2034 / SC2045) in CI workflow
  `run:` blocks are NOT addressed by this slice. actionlint is invoked
  with `-shellcheck ""` (shellcheck pass disabled) because the
  wrong-permission-scope error fires regardless of shellcheck level, and
  the existing warnings are stylistic nits in unrelated jobs. A future
  slice can enable shellcheck and fix those if there's appetite.
