# container-publish — native-arm64 build split + edge debounce (decisions log)

**Type:** JUDGMENT (build-time calls recorded, not blocked on human sign-off — see `feedback_judgment_slices` / `Plans/prompts/04-per-slice-template.md`).

**Trigger:** recurring "pipeline step failed" emails. Investigation of the last 300 CI runs (2 days, ~45 merges/day to `main`) found `container-publish` was the dominant source of failure emails, driven by two things working together: high per-merge build volume and a structurally fragile QEMU-emulated `arm64` build leg.

## Root cause

`container-publish` built `linux/amd64,linux/arm64` in a single `ubuntu-latest` job using **QEMU emulation** for arm64. The arm64 leg ran the entire image build under QEMU user-mode emulation, including `next build` (whose SWC compiler is a Rust native binary). QEMU intermittently mis-emulated an instruction and crashed the build:

```
#30 304.3 qemu: uncaught target signal 4 (Illegal instruction) - core dumped
#30 308.8 ⨯ Next.js build worker exited with code: null and signal: SIGILL
```

Observed failure classes across recent `container-publish` runs:

| Class                    | Example                                                               | Nature         |
| ------------------------ | --------------------------------------------------------------------- | -------------- |
| QEMU arm64 SIGILL        | `web` `next build` illegal instruction                                | **structural** |
| Go module proxy flake    | `proxy.golang.org ... stream error INTERNAL_ERROR received from peer` | transient      |
| GHA cache flake          | `error writing layer blob: not_found`                                 | transient      |
| provenance attest hiccup | `Attest provenance` step failure                                      | transient      |

At ~45 merges/day × 4 images × 2 platforms (with `cancel-in-progress: false`, so every merge built fully), the expected failure-email rate was several per day even at a low per-leg flake rate.

## Decisions

**D1 — Native-arch build split (eliminates the SIGILL class).**
Build each architecture on its own native GitHub-hosted runner instead of emulating:

- `linux/amd64` on `ubuntu-latest`
- `linux/arm64` on `ubuntu-24.04-arm` (free for public repos — this repo is public)

A `build` job (matrix: image × platform) pushes each per-arch image **by digest** (no tag); a `merge` job (matrix: image) assembles the tagged multi-arch manifest list via `docker buildx imagetools create`. This is the canonical buildx "distribute across runners" pattern. **No QEMU anywhere.** Faster builds, and the SIGILL failure class is gone.

**D2 — Edge debounce via `cancel-in-progress` for push events.**
`cancel-in-progress: ${{ github.event_name == 'push' }}`. A burst of rapid merges to `main` collapses to the latest commit. Release events live in a separate concurrency group and are **never** cancelled.

> **Relaxes slice 207's "every merge to main builds `:main-<sha7>`."** A superseded intermediate merge may be cancelled before its `:main-<sha7>` image is published. This is acceptable: the trust anchor for edge images is the provenance attestation on the surviving build, not the existence of one image per SHA (slice 451 AC-5 — edge/`main-<sha7>` are deliberately unsigned, moving, non-promoted targets). `:edge` always points at `main` HEAD. **Reversible** by removing the expression (back to `cancel-in-progress: false`).

**D3 — `paths-ignore` for docs-only merges.**
Docs-only merges (`Plans/**`, `docs/**`, `**/*.md` — e.g. the frequent `chore(status)` reconciliations and `docs(slices)` edits) change no image and no longer trigger a rebuild. A merge touching docs **and** code still builds (`paths-ignore` only skips when ALL changed paths match). Removes a large fraction of zero-delta build attempts.

**D4 — Provenance + SBOM + AC-13 preserved.**
`provenance: true` + `sbom: true` stay on the per-arch `build` job. The AC-13 dual-arch manifest assertion runs in `merge` against the assembled manifest list. `actions/attest-build-provenance` now binds the **final index digest** (resolved via `imagetools inspect`) in `merge`, rather than a single emulated build's digest — one attestation per image target, as before.

## Validation caveat

This workflow runs only on `push: main`, `release: published`, and `workflow_dispatch` — **not** on `pull_request`. The PR that lands this change does not exercise it. Validation plan:

1. `actionlint` (via `pre-commit`, `-shellcheck ""`) passes locally and in CI.
2. Pinned action SHAs (`upload-artifact@v4.6.2`, `download-artifact@v4.3.0`) verified to resolve to real tags.
3. First real exercise is the merge to `main`. **Watch the first 1–2 post-merge `container-publish` runs**; the change is a one-commit revert if the digest-merge or attestation flow misbehaves.

## Detection-tier classification

- `detection_tier_actual`: production (the QEMU SIGILL surfaced as failed `main`-push build emails)
- `detection_tier_target`: production (multi-arch publish + attestation can only be exercised on real push/release events; there is no pre-merge tier for this workflow — the mitigation is the watch-first-runs + trivial-revert plan above, not a new gate)
