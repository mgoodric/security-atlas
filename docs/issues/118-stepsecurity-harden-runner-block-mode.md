# 118 — Promote StepSecurity Harden-Runner to block mode

**Cluster:** Infra (CI security)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`
**Dependencies:** [117]

## Narrative

Slice 117 wired [step-security/harden-runner](https://github.com/step-security/harden-runner) into every job of every `.github/workflows/*.yml` in **audit mode** (`egress-policy: audit`). Audit mode is observe-only: it records outbound network calls, file writes, and process executions to the StepSecurity dashboard but never fails a job.

This slice promotes the policy from `audit` to `block`. In block mode, harden-runner enforces an allowlist of egress destinations; any outbound call to a destination not on the list is severed at the syscall layer and the job fails. Block mode is the actual supply-chain defense — audit mode only generates evidence that the defense would have worked.

The promotion is staged separately from slice 117 because flipping straight to block on day one would break every CI run whose legitimate egress destinations we hadn't yet enumerated. ~2 weeks of audit-mode soak gives us an empirically grounded `allowed-endpoints` list, derived from real runs of every workflow path across every job type (build, test, lint, integration, e2e, release, container-publish).

## Gating condition (status flip `not-ready` → `ready`)

All of the following must hold:

- ≥ 14 days of audit-mode data on the StepSecurity dashboard since slice 117 merged
- Zero unjustified egress destinations observed in that window across all workflows (`ci.yml`, `release.yml`, `release-please.yml`, `docs-publish.yml`, `container-publish.yml`, `codeql.yml`)
- A maintainer-curated `allowed-endpoints` list exported from the dashboard, sanity-checked against the canonical CI egress surface (GitHub API, npm registry, Go module proxy, Docker Hub, ghcr.io, distroless mirror, action runner downloads, codecov.io, sigstore, etc.)

## Acceptance criteria (draft — refine when slice flips `ready`)

- [ ] AC-1: Every job's harden-runner step flips `egress-policy: audit` → `egress-policy: block` and adds an `allowed-endpoints:` list. The list MAY differ per workflow (release.yml needs sigstore/cosign endpoints that ci.yml does not) but MUST be reviewed by the maintainer per workflow.
- [ ] AC-2: The exported audit-mode baseline is committed to `docs/audit-log/118-harden-runner-block-mode-decisions.md` so a future contributor can see what egress was observed and why each endpoint made the allowlist.
- [ ] AC-3: A single PR-CI run on the slice branch shows ALL jobs passing under block mode (no false positives from a missed endpoint). If any job fails, the missing endpoint is either added to the allowlist with rationale or the underlying step is rewritten to remove the dependency.
- [ ] AC-4: The decisions log entry documents the rationale for any endpoint on the allowlist whose purpose is not self-evident (e.g. why we allow a specific CDN that a transitive action pulls from).
- [ ] AC-5: `CONTRIBUTING.md` "Dependency hygiene" subsection updated — replace the audit-mode paragraph with a block-mode paragraph noting that unrecognized outbound destinations will now fail CI and how to propose adding an endpoint to the allowlist (PR + decisions-log entry).

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — one-line policy flip per workflow + one allowlist insertion per workflow
- **OSS thesis:** Apache 2.0 dep, free Community Plan still covers the workflow — no licensing surface change

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT promote without ≥ 14 days of audit data — the baseline IS the slice
- **P0-A2**: Does NOT use a wildcard allowlist (`*.example.com`) to silence false positives — specificity is the whole point
- **P0-A3**: Does NOT skip a workflow — all-or-nothing per slice 117's AC-1 invariant
- **P0-A4**: Does NOT change the SHA pin on `step-security/harden-runner` in the same PR — bump SHA as a separate concern via Dependabot; this slice is policy-only

## Notes for the implementing agent

- The dashboard exports `allowed-endpoints` lists in YAML form; harden-runner's documentation has the syntax: `allowed-endpoints: > endpoint1:443 endpoint2:443`. Each endpoint is `host:port` whitespace-separated on a single folded-scalar value.
- Slice 117's decisions log (`docs/audit-log/117-stepsecurity-harden-runner-decisions.md`) captured the SHA pin + the audit-mode rationale. This slice's decisions log builds on it.
- If the 2-week soak surfaces a transient outbound (e.g. a flaky CDN that responded once out of 200 runs), prefer pinning to the canonical endpoint over allowing every observed value — the goal is to lock down the legitimate surface, not to memorialize observed noise.

## Out-of-scope (would be separate slices)

- StepSecurity's workflow-posture-check action (separate adoption decision)
- Migration to self-hosted runners (would push us out of the free Community Plan)
- Per-environment policy variation (production-release block-mode is stricter than PR-CI block-mode)
