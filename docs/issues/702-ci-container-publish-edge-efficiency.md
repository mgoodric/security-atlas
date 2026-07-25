# 702 — container-publish edge-build efficiency

**Cluster:** CI / Release
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P3
**Spillover from:** slice 693 (pipeline-efficiency audit — auxiliary workflows).

## Narrative

`container-publish.yml` rebuilds 4 images (atlas/cli/web/bootstrap) for 2 platforms
(amd64 + arm64-via-QEMU) on EVERY push to `main` to produce `:edge` + `:main-<sha7>` tags.
QEMU arm64 emulation is the slowest path in the whole CI estate (commonly 4–8× native). Two
inefficiencies:

1. **Docs/status-only `main` merges still rebuild all 8 image variants.** This project merges
   `chore(status)` / docs commits frequently, none of which change the images.
2. **A release merge triggers TWO full multi-arch builds** for the same SHA — the `:edge`
   build on the merge push AND the `vX.Y.Z` build on the release event; the edge build for the
   commit being released is largely wasted.

Edge images are explicitly unsigned, provenance-only (not the release supply chain), so their
arch coverage is a lower-stakes call than release images. Options (maintainer's pick):
(a) path-filter the edge build so docs-only `main` pushes skip it; (b) make the edge channel
`linux/amd64`-only and keep full multi-arch for `release`; (c) skip the edge build when the
push SHA also carries a release tag. Option (a) is the safest pure win; (b) trades arm64-edge
coverage for speed and needs confirmation that edge-arm64 is actually consumed.

## Acceptance criteria

- [ ] **AC-1.** Docs/status-only `main` pushes do NOT rebuild the edge images (path filter), OR
      a documented decision that they should.
- [ ] **AC-2.** A release merge does not redundantly build `:edge` for the same SHA it builds
      `vX.Y.Z` for (skip-edge-when-release-tag), OR a documented decision to keep it.
- [ ] **AC-3.** RELEASE images keep full multi-arch (amd64 + arm64) and full signing/provenance
      — untouched.
- [ ] **AC-4.** If edge goes amd64-only (option b), confirm edge-arm64 is unused first and
      record it.

## Anti-criteria

- Does NOT reduce release-image arch coverage, signing, or provenance.
- Does NOT weaken the release supply chain — edge changes only.
- Does NOT proceed on option (b) without confirming edge-arm64 is unconsumed.

## Dependencies

- Independent.

## Notes

Source: slice 693 audit Findings 1A + 3B (auxiliary-workflow investigation).
</content>
