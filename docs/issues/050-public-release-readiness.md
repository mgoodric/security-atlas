# 050 — Public release readiness + release automation

**Cluster:** Infra
**Estimate:** 3d
**Type:** HITL

## Narrative

Make security-atlas ready to flip to a public open-source repository, with automated semantic-version releases + multi-arch container images + Watchtower-driven auto-deploy on self-host targets. This slice has four concerns that are tightly coupled by the launch gate:

1. **Sanitize repo content** — remove personally-identifying references (the primary user persona is currently anchored on the maintainer; rewrite to a generic persona for public consumption), audit for anything internal-only, anything embarrassing, any hardcoded internal infrastructure.
2. **Enable GitHub security features** that are free for public repos — code scanning (CodeQL), secret scanning, Dependabot, branch protection.
3. **Add public-facing project docs** — README rewritten for outside readers, LICENSE finalized, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md` (vulnerability disclosure), issue + PR templates.
4. **Release automation** — `release-please` for semantic-version PR generation + changelog, multi-arch container images (amd64 + arm64) pushed to GHCR on every release tag, Watchtower configuration example for Unraid (and any compatible Docker host) auto-deploy.

Pre-merge requirement: resolve the three relevant open questions from `Plans/canvas/11-open-questions.md` — Project license (Apache 2.0 vs AGPL), SCF redistribution terms, and Hosted offering vs pure OSS governance. None can be auto-decided; each is a maintainer call. Surface them explicitly in `RELEASE_READINESS.md` and block AC-15 on their resolution.

Final step is a pre-flight checklist confirming readiness. The actual `gh repo edit --visibility public` flip is a one-line maintainer command AFTER all ACs pass — this slice does not perform the flip.

## Acceptance criteria

- [ ] AC-1: `docs/RELEASE_READINESS.md` exists with a pre-flight checklist enumerating every concern in this slice; all items checked ✓ before requesting the public flip.
- [ ] AC-2: `grep -rIi "matt\|mgoodric" .` (excluding `.git/`, the `LICENSE` author field, `CODE_OF_CONDUCT.md` enforcement contact, `SECURITY.md` disclosure contact, and `CHANGELOG.md` historical entries) returns only files explicitly whitelisted in `docs/RELEASE_READINESS.md` with justification.
- [ ] AC-3: `Plans/canvas/01-vision.md §1.4` primary persona rewritten as generic ("the solo security leader at a 50–150-person security-product startup") with the maintainer-specific anchor removed. Design rationale preserved.
- [ ] AC-4: `Plans/canvas/10-roadmap.md §10.1` v1 success test rewritten as generic ("a solo security leader can run their next SOC 2 audit out of security-atlas, generate the next board pack from it, and not reach for Vanta or a Google Sheet to fill a gap").
- [ ] AC-5: `LICENSE` file present at repo root with finalized license text (Apache 2.0 or AGPL — open question resolved before merge per `RELEASE_READINESS.md`).
- [ ] AC-6: `README.md` rewritten for a public audience: project description, status, install, quickstart, links to docs / contributing / security. Internal dev-setup notes moved to `CONTRIBUTING.md`.
- [ ] AC-6a: README displays a tasteful 4-badge row at the top, each badge linking to its source:
  - **License** — shields.io · reflects `LICENSE` file content · links to the LICENSE file
  - **Build status** — GitHub Actions native badge for the CI workflow · links to the workflow runs page
  - **Test coverage** — Codecov badge · links to the Codecov project page · requires CI to upload coverage reports for Go (`go test -coverprofile`), TypeScript (jest/vitest coverage), and Python (`pytest --cov`) via the `codecov/codecov-action` step in each language's CI job · Codecov is free for public OSS projects
  - **Latest release** — shields.io · hooks into release-please-generated GitHub release tags (AC-12) · links to the latest release page
- [ ] AC-6b: Badge row renders correctly on GitHub web view + raw README; no broken images, no auth-walled URLs, all badges resolve within a couple of seconds on first page load.
- [ ] AC-7: `CODE_OF_CONDUCT.md` present (Contributor Covenant v2.1 standard text); `CONTRIBUTING.md` present (dev setup + Conventional Commits + DCO/CLA decision); `SECURITY.md` present (vulnerability-disclosure policy + private reporting contact).
- [ ] AC-8: `.github/ISSUE_TEMPLATE/bug.yml`, `.github/ISSUE_TEMPLATE/feature.yml`, and `.github/PULL_REQUEST_TEMPLATE.md` present with sensible fields.
- [ ] AC-9: `.github/dependabot.yml` configured to scan Go modules, npm workspaces, Python (uv), Docker, and GitHub Actions on a reasonable cadence.
- [ ] AC-10: CodeQL workflow at `.github/workflows/codeql.yml` runs on push + PR + scheduled; languages covered: Go, TypeScript, Python.
- [ ] AC-11: Branch protection on `main` enforces:
  - **Require PR review** — ≥1 approval before merge
  - **Require passing CI status checks** — named explicitly: build, lint, test, codeql, codecov (coverage), container-publish (skipped on non-release commits is fine)
  - **Require linear history** — no merge commits; squash- or rebase-only
  - **Require conversation resolution** — all PR review threads resolved before merge
  - **Dismiss stale PR approvals** — when new commits are pushed after approval, the approval auto-invalidates
  - **Block force-push** on `main` — no exceptions, including admins
  - **Block direct push** to `main` — PR-only
  - **Block branch deletion** for `main`
  - **Restrict who can push to `main`** to the maintainer + the `release-please` GitHub App bot (so release PRs can be merged by the bot but no human bypasses review)
  - **(Optional, document the call)** Require signed commits — trust signal vs. friction for community contributors; if disabled, capture the rationale in `RELEASE_READINESS.md`
  - Configuration committed as `.github/branch-protection.json` (preferred — reviewable in PRs) OR applied via the GitHub MCP and documented in `RELEASE_READINESS.md` with the exact API call
  - **Verification:** a test PR with a failing CI check cannot be merged; an attempt to force-push from a clone is rejected; a manual push directly to `main` is rejected.
- [ ] AC-12: `.github/workflows/release-please.yml` configured; a Conventional Commit pushed to `main` opens (or updates) a release PR with auto-incremented semver + populated changelog entry. Release PRs are NOT auto-merged.
- [ ] AC-13: `.github/workflows/container-publish.yml` builds + publishes multi-arch (`linux/amd64` + `linux/arm64`) images to `ghcr.io/<owner>/security-atlas:<tag>` on every release tag. `docker manifest inspect` shows both architectures.
- [ ] AC-14: `deploy/watchtower/` contains a documented Watchtower compose/labels example demonstrating opt-in (`com.centurylinklabs.watchtower.enable=true`) auto-update from GHCR; `docs/SELF_HOSTING.md` references it with a worked example for Unraid.
- [ ] AC-15: Pre-flight checklist confirms: zero secrets in code (gitleaks + trufflehog clean) · all `RELEASE_READINESS.md` checkboxes ✓ · the three pre-merge open questions resolved · CI green on `main` · final visibility flip command documented but not executed.

## Constitutional invariants honored

- **AI-assist boundary** (`CLAUDE.md`): `release-please` opens release PRs but never auto-merges; human approval required per release. Audit trail preserved.
- **Licensing constraints** (`CLAUDE.md`): no non-permissive catalog (CCM, CAIQ, SIG) bundled in release artifacts. SCF bundling gated on open-question #01 clearance.
- **Working norms — Ask before scaffolding** (`CLAUDE.md`): the final `gh repo edit --visibility public` flip is explicitly out of scope for this slice — maintainer-only action.

## Canvas references

- `CLAUDE.md` — "Licensing constraints", "Open decisions remaining", "Working norms", "Planned repository layout" (`deploy/`, `.github/workflows/`)
- `Plans/canvas/10-roadmap.md §10.1` — v1 self-host story (one-binary core + Watchtower-driven auto-deploy validates the docker-compose path)
- `Plans/canvas/11-open-questions.md` — Project license, SCF redistribution terms, hosted-offering governance

## Dependencies

- **039** (CLI binary distribution + release pipeline) — `release-please` extends the release pipeline that 039 establishes. Multi-arch container build can layer on top.

## Anti-criteria (P0)

- Do NOT execute `gh repo edit --visibility public` as part of this slice. The flip is a maintainer-only action after AC-15 passes.
- Do NOT auto-merge `release-please`'s release PRs. Each release requires human approval (AI-assist boundary).
- Do NOT bundle the SCF catalog in container images until open-question #01 (SCF redistribution terms) is fully cleared.
- Do NOT remove the maintainer's name from `LICENSE` author field, `CODE_OF_CONDUCT.md` enforcement contact, or `SECURITY.md` disclosure contact — those are appropriate places for a named maintainer in any public project.
- Do NOT commit secrets, tokens, or credentials. Gitleaks + GitHub secret scanning must pass before merge.
- Do NOT block the public flip on `mgoodric` GitHub username — moving the repo to a GitHub org is a separate decision; flag it in `RELEASE_READINESS.md` as a "consider" item, do not require it.
- Do NOT silently broaden the AI-assist boundary while drafting `CONTRIBUTING.md` or `SECURITY.md` text. Match the boundary defined in `CLAUDE.md` exactly.

## Skill mix (3–5)

- `security-review` (mandatory — public-release path; spans secrets, branch protection, vulnerability disclosure)
- `engineering-advanced-skills:release-manager` (release-please conventions + changelog seeding)
- `engineering-advanced-skills:dependency-auditor` (Dependabot config + license-compat audit of dependencies before public release)
- `engineering-advanced-skills:ship-gate` (definition-of-done for the public-release gate)
- `changelog-generator` (initial `CHANGELOG.md` seeded from existing Conventional Commits on main)

## Notes for the implementing session

- Run a `grep -rIi "matt\|mgoodric\|goodrich"` sweep FIRST to catalog every personal reference. Many will be in `Plans/canvas/` files. Build the SANITIZE / KEEP / REMOVE list in `docs/RELEASE_READINESS.md` BEFORE editing any source files.
- The persona rewrite in `01-vision.md §1.4` is judgment-heavy. Generic phrasing must still convey the design rationale ("why this persona") — don't bland it down to the point that the rationale is lost.
- For license, default recommendation is **Apache 2.0** unless there's a strong reason for AGPL. AGPL has known commercial-adoption friction; the OSS GRC ecosystem is mostly Apache/MIT.
- For container images, target both `linux/amd64` (Unraid baseline) AND `linux/arm64` (future Pi / Apple Silicon hosting). Use `docker/build-push-action` with QEMU on GitHub Actions.
- The Watchtower example should use label-based opt-in so users opt in per-container, not globally. Include a "how to verify auto-update worked" snippet (check container hash before/after a release tag).
- Consider a `RELEASE_READINESS_REVIEW.md` summary at the bottom of `RELEASE_READINESS.md` enumerating what changed and what to do AFTER the public flip (e.g., enable Discussions, add a code-of-conduct-enforcement contact email, set up Grypegate or similar if desired).
- If `gh repo edit --visibility public` is run later, the first thing to do post-flip is verify branch protection still applies (some settings reset on visibility change).
- **Badges discipline:** keep the row to 4 (License · Build · Coverage · Latest Release). Resist adding Stars, Forks, Issues count, or anything vanity. The Go Report Card and CodeQL badges are good optional additions if the implementing session has appetite — but they don't ship as P0 ACs.
- **Codecov setup quickstart:** for public OSS, no auth token is needed in CI — Codecov's GitHub App auto-pairs the upload. Add `codecov/codecov-action@v4` to each language's test job after coverage generation. Set `fail_ci_if_error: false` initially so a transient Codecov outage doesn't block merges.
