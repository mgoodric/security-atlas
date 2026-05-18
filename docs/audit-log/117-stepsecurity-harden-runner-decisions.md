# 117 — StepSecurity Harden-Runner (audit mode) — decisions log

Slice 117 is `Type: AFK`. This log records the build-time judgment calls
made while wiring `step-security/harden-runner@v2` into every job of every
workflow under `.github/workflows/`. Format follows the JUDGMENT-slice
convention used by other AFK slices in this repo
(Diagnosis · Decision · Revisit-trigger · Confidence).

## D1 — Why audit mode first (vs immediate block mode)

**Decision:** Ship `egress-policy: audit` + `disable-sudo: true` only.
Block-mode promotion is filed as a separate follow-on slice 118 (`not-ready`)
gated on ~2 weeks of audit-mode data.

**Why:** Block mode without a known egress baseline = breaking every CI run
on day one. The repo runs six workflows (`ci.yml`, `release.yml`,
`release-please.yml`, `docs-publish.yml`, `container-publish.yml`,
`codeql.yml`) across 40 distinct jobs touching at least:

- GitHub Actions runner downloads (multiple CDN hosts)
- Go module proxy (`proxy.golang.org`, `sum.golang.org`)
- npm registry (`registry.npmjs.org`)
- Docker Hub + ghcr.io + (transitively) distroless mirror
- `codecov.io` (coverage uploads)
- `apt` mirrors (some setup-\* actions install packages)
- Sigstore / Fulcio / Rekor (release.yml only)
- StepSecurity's own `api.stepsecurity.io` for the action itself
- `app.stepsecurity.io` for dashboard surfacing

Enumerating that surface by reading workflow files would miss transitive
egress (e.g. a custom action that pulls a binary from a mirror declared
inside its `action.yml`, not in our workflow). The audit-mode soak makes
the list empirical instead of speculative. The two-week window is also
the StepSecurity-recommended minimum for baseline derivation (per their
own block-mode rollout guide).

**Revisit trigger:** When slice 118's gate condition holds (≥14 days
soak + zero unjustified egress observed), promote per its acceptance
criteria.

**Confidence:** HIGH. Pattern matches the staged rollout shape
used by every comparable security-instrumentation tool (CSP report-only
→ enforced, kubectl audit → enforce mode, eBPF observe → block, etc.).

## D2 — SHA pin + corresponding tag

**Decision:** Pin to commit SHA `ab7a9404c0f3da075243ca237b5fac12c98deaa5`,
which is the tip of `refs/tags/v2.19.3` (the latest release as of
2026-05-17, per `gh api repos/step-security/harden-runner/releases/latest`).
Every workflow's harden-runner step uses:

```yaml
uses: step-security/harden-runner@ab7a9404c0f3da075243ca237b5fac12c98deaa5 # v2.19.3
```

**Why:** The slice's anti-criterion P0-A2 forbids floating tags. SHA pins
on a security action defeat the action-substitution attack class (a
compromise of the upstream tag pointer cannot reach our CI). The
trailing `# v2.19.3` comment preserves human-readable diffs when a
maintainer later bumps the pin.

**Verification command (so the maintainer or a future auditor can
re-confirm the pin came from the upstream release we say it did):**

```sh
gh api repos/step-security/harden-runner/git/refs/tags/v2.19.3 \
  --jq '.object.sha'
# Expected output: ab7a9404c0f3da075243ca237b5fac12c98deaa5
```

**Revisit trigger:** Dependabot's `github-actions` ecosystem block in
`.github/dependabot.yml` will surface upstream releases as PRs (commit
prefix `deps(actions):` per slice 077). When such a PR opens, review the
upstream release notes for any change to the action's egress profile or
permissions surface before merging.

**Confidence:** HIGH. The SHA was retrieved live from the upstream
GitHub API, not copy-pasted from a third-party mirror; the API
round-trip is what the verification command above reproduces.

## D3 — StepSecurity account ownership (personal vs project-owned)

**Decision:** This slice does NOT create the StepSecurity account.
Account creation + repo enrollment is a maintainer-side step per
slice 117 AC-2 — it requires a GitHub identity with write access to
`mgoodric/security-atlas` and a billing relationship with
StepSecurity (free tier for OSS). This implementing agent has neither.

**Placeholder:** The maintainer (Matt Goodrich) is asked to:

1. Sign in at `app.stepsecurity.io` (GitHub OAuth)
2. Enroll `mgoodric/security-atlas` under the free Community Plan
3. Reply on the slice 117 PR with:
   - Whether the account was created against the personal GitHub identity
     `@mgoodric` or a project-owned bot identity (TBD)
   - The dashboard URL for the first PR-CI run that runs harden-runner
     (so contributors can click through to the egress baseline)
4. Update this decision entry inline (or in a follow-up commit) with the
   chosen identity so a future contributor knows who can revoke access if
   needed

**Why this is a maintainer-side step, not an agent step:** The agent does
not hold credentials to `mgoodric/security-atlas` repo settings, does
not have a StepSecurity billing relationship, and cannot meaningfully
choose between a personal identity (Matt's contact email is the only
recovery path) and a project-owned bot identity (which would need to be
provisioned, an email mailbox configured, and the credential rotated
into a project secrets vault that doesn't yet exist). Recommending
"personal identity" without doing the trade-off properly would lock in
a worse default than letting the maintainer decide.

**Maintainer recommendation (non-binding):** Personal identity is the
simpler choice for an OSS project of this size — the bot-identity path
adds operational overhead (mailbox, credential rotation, account
recovery delegate) that only pays off at multi-maintainer scale. If the
project grows past a solo maintainer, revisit with a project-owned bot.

**Revisit trigger:** (a) Matt fills in the choice on the slice PR, or
(b) the project gains a second maintainer.

**Confidence:** MEDIUM — the slice ships correctly without this entry
being final, but the entry is genuinely incomplete until the maintainer
records the choice. Calling it MEDIUM rather than LOW because the
slice's CI surface and decisions log are independently verifiable; only
the account-ownership row is pending.

## D4 — Free Community Plan covers our use case

**Decision:** Use the StepSecurity Community Plan (no paid tier).

**Why:** Per [StepSecurity's pricing page](https://www.stepsecurity.io/pricing)
(retrieved 2026-05-17), the Community Plan covers:

- Public open-source repositories
- GitHub-hosted runners (we use `runs-on: ubuntu-latest` exclusively;
  no self-hosted runners anywhere in `.github/workflows/`)
- Unlimited workflow runs
- The Harden-Runner action in both audit and block modes
- The hosted dashboard at `app.stepsecurity.io`

Our use case (one public OSS repo, GitHub-hosted runners only, no
self-hosted infra) lands cleanly inside the free tier. There is no
projected feature dependency in slices 117 or 118 that would require a
paid tier.

**Caveat:** Paid features StepSecurity does gate (custom enterprise
allowlists, multi-org admin, audit-log export to SIEM) are not on our
roadmap. If those become required (e.g. compliance customer asks for a
SIEM-ingestible audit trail), that would be a separate paid-tier
adoption slice with its own decision.

**Revisit trigger:** (a) Slice 118 (block mode) lands and we observe a
feature gap that requires upgrade, or (b) StepSecurity changes their
free-tier terms for OSS.

**Confidence:** HIGH (current as of 2026-05-17). The pricing page URL is
recorded above; if it changes, the verification is a one-click revisit.

## Bonus context — CLAUDE.md anti-pattern reconciliation

(Runner-side observer vs platform agent.)

The CLAUDE.md "Anti-patterns we explicitly reject" list includes
"Proprietary collector agents on endpoints (we use osquery / Fleet /
read-only APIs)." Harden-Runner is not in conflict with this anti-pattern
because:

1. **Scope:** Harden-Runner observes only the ephemeral CI runner during
   the run, not security-atlas users' endpoints or infrastructure.
2. **Telemetry surface:** What gets sent to `api.stepsecurity.io` is OUR
   CI's metadata (which actions ran, what egress they made) — never any
   tenant data, never customer evidence, never anything from a
   deployed security-atlas instance. The "AI inference defaults to local
   Ollama" privacy stance in the canvas governs what the deployed
   product does; harden-runner is a build-time / CI-time tool with a
   different threat model.
3. **License:** Apache 2.0 — fully open-source. The "proprietary" half
   of the anti-pattern is also not satisfied.
4. **Replaceability:** If we ever want to leave StepSecurity, we can.
   The action is replaceable with any other runner-instrumentation tool
   (we'd just lose the dashboard); we are not building product
   dependencies on the vendor.

This bonus note exists because a future contributor reading both
CLAUDE.md and this slice could reasonably ask the question. Document
ahead, not after.

## D5 — `disable-sudo: false` per-job exception for `frontend-playwright`

**Decision:** The `frontend-playwright` job in `.github/workflows/ci.yml` overrides the slice-117 default of `disable-sudo: true` with `disable-sudo: false`. Every other job in every workflow retains `disable-sudo: true`.

**Surfaced:** CI run [26008765330](https://github.com/mgoodric/security-atlas/actions/runs/26008765330) on PR #262 — the first post-merge-conflict-rebase CI run after slice 117 was applied. The `Install Playwright chromium` step failed with:

```
Switching to root user to install dependencies...
sudo: a terminal is required to read the password; either use the -S option to read from standard input or configure an askpass helper
sudo: a password is required
Failed to install browsers
Error: Installation process exited with code: 1
```

**Root cause:** `npx playwright install --with-deps chromium` uses `sudo apt-get install` to install chromium's system dependencies (libfontconfig, libnss3, libxss1, etc.) that the ubuntu-latest runner image doesn't pre-bundle. Harden-Runner's `disable-sudo: true` blocks sudo invocations, causing the install step to fail.

**Alternatives considered:**

1. **Drop `--with-deps`** — Run `npx playwright install chromium` without system-dep install. **Rejected:** GitHub-hosted runner images change over time; relying on the image to pre-bundle every chromium dep is fragile, and the failure mode if a future runner update removes a dep is a confusing browser-level error instead of a clear "missing system dep" install error.

2. **Per-job override (chosen)** — Set `disable-sudo: false` on JUST the `frontend-playwright` job. **Chosen because:** the playwright job runs e2e tests; the sudo it uses is for Playwright's own browser install (well-known, well-documented behavior), NOT for arbitrary user-controlled code. Egress audit still applies to this job, so any unexpected outbound calls are still observable. The narrow exception preserves slice 117's security stance everywhere else (40 of 41 jobs still have `disable-sudo: true`).

3. **File spillover slice, ship 117 with the regression** — Per Amendment 2 of the continuous-batch policy. **Rejected:** slice 117 is the _cause_ of the regression; fixing it within slice 117 is in-scope, not out-of-scope. Shipping 117 with a known regression that breaks every PR's Playwright job would invert the slice's value (it would block more PRs than it secures).

**Tradeoff (the security stance for that one job):** With `disable-sudo: false`, a compromised action (or malicious dep in the build chain) running on the `frontend-playwright` job COULD use sudo to install other things. Audit-mode egress logging will still show any unexpected outbound calls, but the in-runner blast radius is wider. Acceptable for now because:

- The job is read-only on the codebase (it does NOT push artifacts to any registry)
- The job has no secrets beyond test-bearer / ephemeral DB creds
- The block-mode promotion (slice 118) will add `allowed-endpoints` which still narrows the egress surface even with sudo enabled

**Slight scope-stretch of slice 117's AC-1:** AC-1 says "every job has `disable-sudo: true`". This exception breaks that letter-of-the-AC but preserves the spirit (defense-in-depth via Harden-Runner). Future contributors reading this row should know the exception exists and why. Slice 118 should re-evaluate whether `disable-sudo: true` can be restored for this job once block-mode `allowed-endpoints` are in place (the Playwright install's egress destinations would be in the allowlist, which might enable a different mitigation).

**Confidence:** HIGH (the failure mode is well-understood; the per-job exception is a clean YAML override; the in-line comment + decisions-log entry make the exception discoverable for future readers).
