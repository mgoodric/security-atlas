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
