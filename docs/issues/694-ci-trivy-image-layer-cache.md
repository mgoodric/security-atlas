# 694 — Docker layer caching on the trivy-image job

**Cluster:** CI / Infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2).

## Narrative

The `trivy-image` job (`.github/workflows/ci.yml`) builds the atlas image with a plain
`docker build --file deploy/docker/atlas.Dockerfile --tag atlas:trivy-scan .` — no buildx,
no `cache-from`/`cache-to`. There is zero layer caching anywhere in ci.yml (`grep -c
'buildx\|type=gha'` == 0). Every code PR therefore recompiles the entire atlas binary
inside the Docker multi-stage build from scratch (a second full Go compile on top of the
host `go build` in `build-go`). Estimated cost: ~2–4 min per code PR.

Switch the job to `docker/setup-buildx-action` + `docker/build-push-action` with
`cache-from: type=gha` / `cache-to: type=gha,mode=max`. Trivy scans whatever image is
produced, so caching the build layers does not change the scan inputs — the security
posture is identical.

## Acceptance criteria

- [ ] **AC-1.** `trivy-image` uses buildx with GHA layer caching (`type=gha`), SHA-pinned
      per slice 128 (`actions-pin-check` passes).
- [ ] **AC-2.** The image Trivy scans is byte-equivalent in content to the pre-change
      build (same Dockerfile, same target) — caching only.
- [ ] **AC-3.** Trivy still scans `vuln-type: os,library` (unchanged) and still gates/reports
      exactly as before.
- [ ] **AC-4.** A warm-cache run is measurably faster than the cold build (note the delta in
      the PR body).

## Anti-criteria

- Does NOT change Trivy's severity thresholds, ignore-file, or which image is scanned.
- Does NOT weaken the supply-chain posture (this is the scan image, not a published image).

## Dependencies

- Independent. Pairs conceptually with slice 695 (share the prebuilt binary) but neither
  blocks the other.

## Notes

Source: slice 693 audit Finding 1.1. Decisions log of origin:
`docs/audit-log/693-ci-pipeline-efficiency-tier1-decisions.md`.
</content>
