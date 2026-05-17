# 080 — Fix release-tag infrastructure (GoReleaser + mkdocs publish)

**Cluster:** Infra
**Estimate:** 1d
**Type:** AFK

## Narrative

Surfaced during the 2026-05-15 post-batch-29 CI-failure investigation. Two distinct release-time workflows have **failed on every release tag** (v1.4.0, v1.5.0, v1.5.1 — 100% failure rate, 3/3):

1. **`Release` workflow's `GoReleaser · build · sign · publish` job** — exits with code 127, "Unable to validate cosign version: 'v2.4.1'". The `cosign-installer` action's setup step is broken (or its expected cosign release artifact moved / hash changed). Result: **the GoReleaser-built binaries (CLI tarballs, container manifests with signed cosign attestations) are NOT being published for any release tag.** The GitHub Releases page shows tag + notes, but no actual downloadable binaries beyond what release-please attaches.

2. **`Docs publish` workflow's `Build (mkdocs --strict)` job** — exits with code 2, "tar: Error is not recoverable: exiting now". A tar extraction in a setup step (likely the `uv tool install mkdocs-material` cache fetch or the mkdocs-material package archive) is corrupting on this runner. Result: **the docs site (slice 058's mkdocs site) is NOT being deployed to GitHub Pages on release tags** — the tag-only deploy in `.github/workflows/docs-publish.yml` runs but the build step fails first.

**Why this is invisible:** neither job is in the live `required_status_checks.contexts`. Both workflows trigger on tag pushes (not PR pushes), so they don't show up as required-checks on PRs. The maintainer sees a green PR merge → release-please opens a chore(release) PR → it merges → tag is created → both workflows trigger on the tag → both fail silently → no notification unless the maintainer specifically clicks into Actions.

**v1.5.0 + v1.5.1 are effectively tags without binaries or docs-site deploys.** The maintainer should know this; self-hosted users following the README's "download the latest release" instruction would find no usable artifact.

Two distinct fix surfaces (the engineer's grill investigates each separately and records findings):

### Fix surface A — `GoReleaser · build · sign · publish` cosign-install failure

Inspect `.github/workflows/release.yml` (the release-tag-triggered workflow). The `sigstore/cosign-installer` action is failing setup with `Unable to validate cosign version: 'v2.4.1'` + "Fetched public key does not match expected digest, exiting" + "unsupported OS Linux". This looks like the cosign release artifact at v2.4.1 has either moved or had its checksum/signing-key change.

Remediation options:

1. **Bump `cosign-installer` to a newer version** that uses the current cosign release-artifact layout. Check the action's release notes.
2. **Pin to a different cosign version** (`v2.4.2` or `v2.4.0`) that the current action knows how to validate.
3. **Drop cosign signing temporarily** from the GoReleaser config (loses signature attestation; can be re-added later).

The grill investigates the actual cosign release artifact state + the action version + picks the smallest viable fix.

### Fix surface B — `Build (mkdocs --strict)` tar failure

Inspect `.github/workflows/docs-publish.yml`'s tag-only deploy path. The `tar` error suggests an extraction step. Most likely candidates (in order of probability):

1. `uv tool install mkdocs-material` — the tarball fetched from PyPI is being extracted in setup-uv; could be a transient network issue OR a permanent action-version issue
2. `actions/cache` restore step — corrupted cache from a prior run
3. `actions/setup-python` or `astral-sh/setup-uv` — the underlying Python or uv tarball

Remediation options:

1. **Bump `astral-sh/setup-uv`** to a newer version (if action drift is the cause)
2. **Disable cache restore for now** (forces fresh setup on every run; slower but unblocks deploys)
3. **Install mkdocs-material via pip directly** instead of `uv tool install` (one less moving part)

The grill investigates the actual failing step (which the run logs make clear).

**Test plan for both surfaces:** after fix, manually trigger a release-tag-equivalent workflow run (either by creating a test branch tag like `v1.5.1-test-080` and pushing it, OR using `workflow_dispatch` to trigger the workflow on `main`). Confirm both jobs go green. Then on the next real release tag (v1.6.0 or v1.5.2 — whichever release-please opens next), confirm both go green for real.

## Acceptance criteria

- [ ] AC-1: `Release` workflow's `GoReleaser · build · sign · publish` job passes against a tag (either a real release tag if one happens during the slice OR a manually-pushed test tag like `v0.0.0-slice080-test` that the engineer creates + deletes after verification). Record the path-A / -B / -C choice for cosign in decisions log.
- [ ] AC-2: `Docs publish` workflow's `Build (mkdocs --strict)` job passes against a tag (same test-tag strategy as AC-1). Record the path-1 / -2 / -3 choice for the tar-failing step.
- [ ] AC-3: A `docs/audit-log/080-fix-release-tag-infrastructure-decisions.md` records the diagnosis steps (what the actual log line was that revealed the cosign / tar failure) and the chosen remediation per surface.
- [ ] AC-4: `docs/RELEASE_READINESS.md` (or wherever release-process docs live) gets a "Verifying a release shipped" subsection: how to confirm post-tag that (a) GoReleaser uploaded the expected artifacts to the GitHub Release, (b) the GitHub Pages site shows the new docs build. Includes the curl/gh commands to verify each.
- [ ] AC-5: If a v1.5.x re-tag is feasible (e.g., release-please opens a v1.5.2 cleanup release), the engineer DOES NOT manually re-tag v1.5.0 or v1.5.1 to backfill artifacts. Those are released-tags-of-record; the missing artifacts stay missing as a historical observation. AC-5 is verified by the engineer NOT having created any `v1.5.0-binaries` / `v1.5.1-binaries` retroactive tags.
- [ ] AC-6: Pre-commit clean. CI green on required checks. The non-required `Frontend · Playwright e2e` may still be red (separate from this slice's scope — slice 079 covers that).

## Constitutional invariants honored

- **Working norms — Cite sources** (CLAUDE.md): the decisions log cites the specific cosign-installer version + cosign release artifact URLs that were verified. Same for the tar-failing step.
- **AI-assist boundary**: nothing AI-generated; this is workflow YAML + action-version bumps + a small doc paragraph.

## Canvas references

- _(none — release-tag infrastructure is operational hygiene; canvas doesn't speak to it)_

## Dependencies

- **039** (CLI binary distribution + release pipeline, merged) — the slice that introduced the GoReleaser config + cosign signing path
- **058** (user docs scaffold + 5 core pages, merged) — the slice that introduced the mkdocs site that the Docs publish workflow targets
- **050** (public release readiness + release automation, merged) — release-please workflow setup

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT retroactively re-tag v1.5.0 or v1.5.1 to "backfill" the missing artifacts. Those tags are historical; the missing artifacts stay missing.
- **P0-A2**: Does NOT remove cosign signing from `.goreleaser.yaml` as the path-of-least-resistance fix UNLESS the engineer's grill verifies no consumer of the signed artifacts is documented. Removing cosign loses an audit-binding property of the release.
- **P0-A3**: Does NOT add a "release-pre-flight" PR-gate check that runs GoReleaser dry-mode on every PR. The release workflow is intentionally tag-only-triggered; running on PRs is a separate slice (would catch regressions but adds CI minutes).
- **P0-A4**: Does NOT silence either workflow's failure (`continue-on-error: true`). The whole point is that these failures need to be FIXED, not hidden. Slice 079 is the quarantine-pattern slice; this one is the actual-fix slice.
- **P0-A5**: Does NOT bundle this with non-release-infra work. Solo run. The release-tag testing pattern (manual tag → observe → tag-delete) is fiddly enough on its own.

## Skill mix (3–5)

- GitHub Actions versioning + the action ecosystem (cosign-installer, setup-uv, setup-python release rhythm)
- GoReleaser configuration + cosign signing concepts (sigstore.dev, key management)
- mkdocs Material packaging (the uv/pip install paths)
- `simplify` (the decisions log + RELEASE_READINESS subsection stay tight)
- `engineering-advanced-skills:runbook-generator` (the "Verifying a release shipped" subsection IS a runbook)

## Notes for the implementing agent

- **Read the actual run logs FIRST.** Failed run IDs as of 2026-05-15: `25934259652` (GoReleaser, v1.5.1), `25922538725` (GoReleaser, v1.5.0), `25898793554` (GoReleaser, v1.4.0); `25934259678` (Docs publish, v1.5.1), `25922538717` (Docs publish, v1.5.0), `25898793507` (Docs publish, v1.4.0). The grill reads each one's failing step + identifies whether the failure pattern is consistent across all three (suggests action / config drift) or differs (suggests transient flake).
- **The `Fetched public key does not match expected digest, exiting` line in the GoReleaser failure is load-bearing.** That suggests cosign's release-artifact signing key was rotated or its expected digest changed in a way the cosign-installer action doesn't know about. The fix is almost certainly an action-version bump.
- **For testing the fix without waiting for a real release tag:** create a branch + push a test tag like `v0.0.0-slice080-cosign-test` that triggers the workflow on a stable commit; observe; delete the tag after. This is a normal CI-config debugging pattern; the test tag should NOT be on the release-please release-PR branch (where it would confuse the manifest).
- **The Vitest co-occurring failures (2 today)** — the investigation noted Vitest fails alongside Playwright in some runs. This is OUT OF SCOPE for this slice (release-tag infrastructure, not PR-time CI); it goes to slice 079's grill instead. Don't mix them.
