# 082 — cosign v3 + goreleaser-action@v7 migration

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** not-ready (waiting on follow-on dependency)

## Narrative

Spillover-as-slice from slice 080 (release-tag infrastructure fix). Slice 080 landed the workflow-ordering fix (removing the broken pre-install `goreleaser check` step) and the mkdocs path fix on the **known-good cosign v2.4.1 + goreleaser-action@v6 stack**. Bumping either of those toward their newer majors during slice 080 surfaced a cascade of breaking changes that didn't fit the slice's time budget. This follow-on slice handles the coordinated migration.

**The cascade observed in slice 080** (full iteration log: `docs/audit-log/080-fix-release-tag-infrastructure-decisions.md`):

1. `goreleaser-action@v7` (latest as of 2026-05-15) added an internal cosign pre-flight verify of its own downloaded goreleaser binary. The binary is signed in the new Sigstore protobuf-bundle format. cosign v2 cannot verify the protobuf-bundle format → action fails with `bundle does not contain cert for verification, please provide public key`.

2. Bumping `cosign-release` from `v2.4.1` → `v3.0.6` (with `sigstore/cosign-installer@v3`) trips the cosign-installer's bootstrap-verify step (exit 22): cosign-installer@v3 bundles a trust-anchor `public.key` for the v2 release line, not v3.

3. Bumping `sigstore/cosign-installer@v3` → `@v4.1.2` resolves the installer bootstrap. But then `cosign sign-blob` with our existing args (`--output-signature` + `--output-certificate`) fails at runtime: cosign v3 changed the default to write a Sigstore protobuf bundle, and the v3 CLI rejects the legacy two-file output unless one of `--new-bundle-format` / `--bundle <path>` is explicitly specified.

4. Adding `--new-bundle-format=false` to the `.goreleaser.yaml` `signs:` args doesn't help — cosign v3's error message says `must provide --new-bundle-format or --bundle where applicable with --signing-config or --use-signing-config`, suggesting the flag is a boolean-toggle (not a value flag), and the keyless flow may require coordinated changes to the COSIGN_EXPERIMENTAL env + the args set.

5. Even if `sign-blob` is made v3-compat, the consumer flow (the `Self-verify signed checksums` step in `release.yml` + the `cosign verify-blob --certificate ... --signature ...` snippet in the GitHub Release notes header) uses the legacy two-file shape. Either the args migrate and consumer docs migrate together, or the args stay legacy + cosign v3 emits the legacy format opt-in.

This is a real migration — not a single-line bump — and deserves its own slice with proper testing.

## Acceptance criteria

- [ ] AC-1: A real release tag (or a test tag like `v0.0.0-slice082-test`) produces a green `Release` workflow run on `goreleaser-action@v7` + `cosign-installer@v4.x` + `cosign v3.x`.
- [ ] AC-2: The `Self-verify signed checksums` step in `release.yml` passes against the just-published artifacts.
- [ ] AC-3: The `cosign verify-blob` snippet in the GitHub Release notes header (rendered from `.goreleaser.yaml` → `release: header:`) is valid against the published artifacts. If the args need to change (e.g., consumers now run `cosign verify-blob --bundle ...` instead of `--certificate ... --signature ...`), update the header snippet + `docs/RELEASE_READINESS.md §11.1` together so the published verify story is internally consistent.
- [ ] AC-4: Decisions log records (a) which cosign-v3 sign-blob args shape works for our keyless OIDC flow, (b) which goreleaser-action verify-pre-flight is doing internally and which cosign version it expects, (c) whether `--new-bundle-format` is a boolean flag or a value flag in the cosign v3 release we land on.
- [ ] AC-5: Pre-commit clean. CI green on required checks.

## Constitutional invariants honored

- **P0-A2 from slice 080 still applies here:** do NOT remove cosign signing as the path of least resistance. The audit-binding property of release artifacts is preserved.

## Canvas references

- _(none — release-tag infrastructure is operational hygiene; canvas doesn't speak to it)_

## Dependencies

- **080** (Fix release-tag infrastructure, in-review) — landed the workflow-ordering + mkdocs path fixes on the v2 cosign stack. This slice graduates the cosign + goreleaser-action versions.
- **Upstream trigger:** dependabot will surface `goreleaser-action@v6 → @v7` + `cosign-installer@v3 → @v4.x` proposals in its weekly run. When both are open, this slice consolidates the bumps + the `signs:` args migration into one coordinated PR.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT bump cosign-installer + goreleaser-action without also reworking the `signs:` args in `.goreleaser.yaml` (and the consumer docs if the legacy two-file shape can't be preserved). Half a migration is worse than no migration.
- **P0-A2**: Does NOT remove cosign signing as the path-of-least-resistance fix. Same constraint as slice 080.
- **P0-A3**: Does NOT bundle this with non-release-infra work.

## Skill mix (3–5)

- Sigstore cosign v3 migration guide (new bundle-format vs legacy two-file output)
- goreleaser-action@v7 internals (the pre-flight verify path it added in v7)
- GoReleaser `signs:` block semantics
- `simplify` (the args block stays tight)
- `engineering-advanced-skills:secrets-vault-manager` (only insofar as cosign-keyless is the canonical "signing without secret-storage" pattern)

## Notes for the implementing agent

- **Read slice 080's decisions log first** — `docs/audit-log/080-fix-release-tag-infrastructure-decisions.md`. The full iteration log of which versions tripped which failure modes is there. Don't re-discover; pick up from the slice 080 hand-off.
- **The cosign v3 release notes** (https://github.com/sigstore/cosign/releases/tag/v3.0.0) are the canonical source for the breaking changes. The new bundle-format flag set is part of CVE-tracked Sigstore Sigstore-protobuf-bundle work.
- **Test approach:** same as slice 080 — push a `v0.0.0-slice082-test` tag at the branch tip, observe the workflow, verify the published artifacts cosign-verify cleanly from a clean shell (no environment state from the action), then delete the test tag + the GitHub Release object.
- **Maintainer-action note:** the `Self-verify signed checksums` step is the authoritative round-trip test. If it passes against a v3-signed artifact, the migration is complete; if it doesn't, either the args migration is incomplete or the verify-blob args also need updating.
