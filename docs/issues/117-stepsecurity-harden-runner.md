# 117 — Adopt StepSecurity Harden-Runner (audit mode → block mode)

**Cluster:** Infra (CI security)
**Estimate:** 0.5d (audit-mode wiring) + 2 weeks of soak before block-mode flip (separate slice)
**Type:** AFK
**Status:** `ready`

## Narrative

Adds [step-security/harden-runner](https://github.com/step-security/harden-runner) (Apache 2.0) to all GitHub Actions workflows in `.github/workflows/`. Harden-Runner instruments the runner to monitor outbound network calls, file writes, and process executions during a CI job, catching supply-chain attacks (malicious package post-installs, exfiltration, compromised actions) that PR-time CHANGELOG analysis cannot see.

Surfaced during the 2026-05-16 conversation about the dep-review loop's 24h calendar cooldown (which was theater). The cooldown was trying to defend against the "compromised package gets yanked within 24h" case, but the real defense is runtime instrumentation, not waiting. Harden-Runner is the right tool; it's free for OSS public repos on GitHub-hosted runners ([Community Plan](https://www.stepsecurity.io/pricing)).

This slice ships **audit mode only**. Block-mode promotion is a separate follow-on slice that lands after ~2 weeks of audit-mode data has established the egress baseline.

## Acceptance criteria

- [ ] AC-1: Every job in every workflow under `.github/workflows/` (currently: `ci.yml`, `release.yml`, `docs-publish.yml`, `codeql.yml`, and any other `*.yml`) has `step-security/harden-runner@v2` as its FIRST step, with `egress-policy: audit` and `disable-sudo: true`. Action pinned to a SHA, not a floating tag (per CLAUDE.md security stance + StepSecurity's own recommendation).
- [ ] AC-2: A maintainer-side account exists at app.stepsecurity.io with the `mgoodric/security-atlas` repo enrolled in the Community Plan. The PR body links to the dashboard URL so future contributors can find the egress baseline + posture findings.
- [ ] AC-3: A single PR-CI run (any branch with the new harden-runner step) shows the action initializing successfully + the job summary surfaces an "Action Security Insights" link to the StepSecurity dashboard. Capture the link in the slice decisions log.
- [ ] AC-4: `CONTRIBUTING.md` "Local CI parity" subsection updated with a one-paragraph note: "CI runs through StepSecurity Harden-Runner in audit mode. If you see new outbound destinations flagged in the workflow summary that you can't justify, surface them in the PR description; we treat unexplained egress as a review-blocker even while we're in audit mode."
- [ ] AC-5: Slice 118 (block-mode promotion) is filed as `not-ready` with a 2-week gating condition (`>= 14 days of audit-mode data + zero unjustified egress in observed runs`). The block-mode flip belongs in 118, not here, to preserve the audit ratchet shape.
- [ ] AC-6: Decisions log at `docs/audit-log/117-stepsecurity-harden-runner-decisions.md`. Required entries:
  - Why audit mode first (vs immediate block) — staged rollout protects against the "we don't know what our CI's legitimate egress destinations are" problem
  - The exact SHA pinned for `step-security/harden-runner@v2` (so a maintainer can verify it against the upstream release later)
  - Whether the StepSecurity account was created against a personal GitHub identity or a project-owned bot identity — affects who can revoke access if needed
  - Confirmation that the free Community Plan covers our use case (GitHub-hosted runners, public repo, no caps observed)

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — adding harden-runner is a one-step-per-job addition, not a refactor
- **Tech-stack table (CLAUDE.md):** existing entry "CI/CD · GitHub Actions" remains accurate; harden-runner is a complementary security layer, not a CI/CD replacement
- **OSS thesis:** Apache 2.0 dep, free tier for public repos — aligns with the project's "no proprietary collector agents" anti-pattern; harden-runner is a per-job runner-side observer, not an agent on customer infra

## Canvas references

- `Plans/canvas/09-tech-stack.md` (CI/CD line — to be referenced, not modified by this slice)
- `Plans/canvas/01-vision.md` (anti-pattern: "proprietary collector agents on endpoints" — harden-runner is per-CI-run, not per-endpoint, and is Apache 2.0 — not in conflict)
- `Plans/prompts/08-dependabot-pr-review.md` (the dep-review loop — harden-runner is the runtime complement to the loop's PR-time analysis)

## Dependencies

- None — pure addition. The repo has all needed surface (`.github/workflows/*.yml` files) already.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT flip `egress-policy` to `block` in this slice. Block mode without a baseline = breaking every CI run on day one. Audit first; slice 118 promotes.
- **P0-A2**: Does NOT use a floating tag like `@v2` without a SHA pin. Floating tags on a security action defeat the action's own threat model. Pin to a SHA + add a comment with the corresponding tag for human-readable diffs.
- **P0-A3**: Does NOT add harden-runner to one workflow and skip another. All-or-nothing per AC-1 — partial coverage gives false confidence.
- **P0-A4**: Does NOT remove or weaken any existing CI security step (GitGuardian, CodeQL, secret-scanning) — harden-runner is additive.
- **P0-A5**: Does NOT enable any harden-runner feature beyond `egress-policy: audit` and `disable-sudo: true` in this slice. Things like `allowed-endpoints`, custom policies, etc. are scope expansion — file separately.

## Skill mix

- GitHub Actions workflow editing (4-5 files, mechanical)
- SHA-pinning discipline for actions (slice 069's CodeQL setup has the pattern)
- One-time StepSecurity account creation + repo enrollment (maintainer-side, manual)
- CONTRIBUTING.md prose update

## Notes for the implementing agent

- Harden-Runner in audit mode is opt-in passive — it adds ~5-10s startup overhead per job but does NOT fail jobs. So even a buggy first revision is safely revertible.
- The action MUST be the first step of the job (before checkout, before any other setup) — that's where it sets up the instrumentation. Putting it after `actions/checkout` defeats the purpose.
- Telemetry note: harden-runner sends per-run egress data to api.stepsecurity.io. This is OUR CI's metadata, not security-atlas USERS' deployment data — different threat model than the canvas's "AI inference defaults to local Ollama" stance. Worth a sentence in the decisions log clarifying the distinction.
- Block-mode promotion (slice 118, not this slice) will need an `allowed-endpoints` list derived from the audit-mode baseline. The dashboard makes that list exportable; the 118 slice will codify it.

## Out-of-scope (would be separate slices)

- **118 (`not-ready`):** Block-mode promotion (`egress-policy: block` + `allowed-endpoints`) after 2-week audit-mode soak
- StepSecurity's workflow-posture-check action (separate action; would be a separate slice if we adopt it)
- StepSecurity's secret-scanning add-on (we already use GitGuardian; coverage overlap not worth duplicating)
- Migrating to self-hosted runners (would push us out of the free Community Plan; revisit only if free-tier limits become binding)
