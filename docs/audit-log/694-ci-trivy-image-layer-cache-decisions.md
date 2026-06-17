# Slice 694 — Docker layer caching on the trivy-image job — decisions log

JUDGMENT slice. The build-time subjective calls (buildx vs. plain build, the
cache mode, `load: true`, and the security-posture-equivalence argument) are
recorded here per the continuous-batch JUDGMENT convention; the maintainer
iterates post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional). It is a CI-config-only change — no platform
code, no migration, no schema.

Source: slice 693 pipeline-efficiency audit, Finding 1.1
(`docs/audit-log/693-ci-pipeline-efficiency-tier1-decisions.md` D1 deferred this
to its own slice precisely because it introduces a new Docker build path and
warranted isolated review).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a declarative CI-workflow edit whose
verification surface is the PR's own CI run — actionlint + check-yaml + prettier
validate the YAML, `actions-pin-check` validates the two new SHA pins, and the
real `trivy-image` job itself runs on this PR — see D5. No platform code path to
unit-test, so `actual == target == none`.)

---

## D1 — buildx + `docker/build-push-action`, not a hand-rolled `docker buildx build`

**Options considered:**
(a) Keep `docker build` and add `DOCKER_BUILDKIT=1` + inline `--cache-to/--cache-from` flags on a raw `docker buildx build` shell step.
(b) `docker/setup-buildx-action` + `docker/build-push-action` (the official GitHub-maintained actions).

**Chosen: (b).** The repo already standardizes on exactly this pair in
`.github/workflows/container-publish.yml` (the published-image path). Reusing the
same actions keeps the two Docker build sites consistent, lets us reuse the
already-vetted SHA pins (D3), and gets the `type=gha` cache backend wiring
(scope, token plumbing) for free — `build-push-action` integrates with the GitHub
Actions cache service without manual `actions/cache` key management. Approach (a)
would re-derive that plumbing by hand and drift from the publish path.

**Confidence: high.** This is the documented canonical pattern for GHA Docker
layer caching and it is already in use one workflow over.

## D2 — `cache-to: type=gha,mode=max` (not `mode=min`)

**Options:** `mode=min` (cache only the final-stage layers that ship in the
image) vs. `mode=max` (cache ALL stages, including intermediate build stages).

**Chosen: `mode=max`.** `atlas.Dockerfile` is a multi-stage build whose dominant
cost is the **builder** stage (a full `go build` of the atlas binary), and that
stage's layers are exactly what `mode=min` would NOT cache — it would cache the
thin distroless final stage and re-run the Go compile every time, defeating the
entire point of the slice (Finding 1.1's "~2-4 min per code PR" is the compile).
`mode=max` caches the builder layers so an unchanged `go.mod`/source warm-hits.
The cost is a larger GHA cache footprint; GHA caches are LRU-evicted under a
per-repo limit, so the worst case is cache churn, never a correctness or security
issue. For a single image build this is the right trade.

**Confidence: high.**

## D3 — SHA pins reused verbatim from container-publish.yml (slice 128 discipline)

`actions-pin-check` (slice 128, `scripts/check-action-pins.sh`) is a required
gate that fails on any non-SHA-pinned action. Rather than resolve fresh pins, I
copied the two pins already vetted and in production use in
`container-publish.yml`:

- `docker/setup-buildx-action@d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5 # v4`
- `docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7`

Reusing them (a) guarantees the pin-check passes, (b) keeps a single source of
truth for the Docker action versions across the repo, and (c) means any future
pin bump is a one-decision change that can sweep both sites.

**Confidence: high.**

## D4 — `load: true`, no `push` — the security posture is IDENTICAL

This is the load-bearing AC-2/AC-3 argument. Two parts:

1. **`load: true` is required, not optional.** Trivy scans by `image-ref:
atlas:trivy-scan`, which reads from the **local Docker daemon**. A buildx
   build without `load` leaves the result only in the buildx cache/exporter, not
   in the daemon — Trivy would then find no such image and fail. `load: true`
   materializes the built image into the local daemon exactly as the old `docker
build --tag atlas:trivy-scan` did, so Trivy's input is byte-equivalent.

2. **No `push`, no registry, no published artifact.** This is the SCAN image. It
   is never pushed anywhere. Layer caching changes only how fast the SAME image
   is built; it changes neither the Dockerfile, the build target, nor the bytes
   Trivy ingests. The Trivy step is unchanged (`severity: HIGH,CRITICAL`,
   `exit-code: "1"`, `ignore-unfixed: true`, `vuln-type: os,library`, same report
   upload). Therefore the vulnerability/supply-chain posture is identical — the
   anti-criteria ("does NOT change thresholds/ignore/which image", "does NOT
   weaken supply-chain posture") are honored.

A theoretical concern with build caching is a stale/poisoned cache layer masking
a vuln. It does not apply here: the GHA cache is the repo's own, scoped to the
repo, populated only by this job's own prior runs; and a cached layer is keyed by
its build inputs (base image digest + instructions), so a base-image bump or
Dockerfile change busts the relevant layers. Trivy re-scans the resulting image
every run regardless of where the layers came from.

**Confidence: high.**

## D5 — Does the real `trivy-image` job run on THIS PR? — YES

The `changes` job's `code` path filter (`.github/workflows/ci.yml`) lists
`'.github/workflows/**'` (and `'deploy/**'`). This PR edits
`.github/workflows/ci.yml`, so `changes.outputs.code == 'true'`, the
`if: needs.changes.outputs.code == 'true'` guard on `trivy-image` is satisfied,
and the REAL job runs on this PR (the `trivy-image-stub` twin is the one that
short-circuits, and it does NOT run here). So CI validation of the new build path
happens on this very PR — not deferred to a later code PR.

**AC-4 caveat (cross-run, not single-run):** the FIRST run on this branch is a
cold-cache populate (`cache-from` misses, `cache-to` writes), so it will not be
faster than the old plain build — it may be marginally slower (buildx setup +
cache export overhead). The warm-cache speedup is observable on the SECOND+ run
(a re-push, or the post-merge main run, or the next code PR that doesn't touch
the Dockerfile/go.mod). This is inherent to layer caching and is noted in the PR
body; AC-4 is a cross-run property, not a single-run assertion.

**Confidence: high** (the filter inclusion is verified by reading the filter
block directly).

---

## Revisit once in use

- **Cache hit-rate / size.** After a few weeks of code PRs, confirm the warm-run
  speedup is real (compare a Dockerfile-untouched PR's `trivy-image` wall-clock
  against the cold baseline) and that the GHA cache isn't thrashing under the
  per-repo cache limit alongside the slice-693 precommit `actions/cache`. If the
  builder layers evict too aggressively, the speedup degrades silently.
- **Pairing with slice 695.** Slice 695 ("share the prebuilt binary") proposes
  feeding `build-go`'s already-compiled binary into the image build instead of a
  second Go compile. If 695 lands, the `mode=max` builder-layer cache here
  becomes partly redundant (the expensive compile moves out of the Docker build);
  re-evaluate whether `mode=max` is still worth the cache footprint then, or
  whether `mode=min` suffices.
- **Pin drift.** When `container-publish.yml`'s docker action pins are bumped
  (Dependabot's docker ecosystem group, slice 693 D4), bump this site in the same
  PR so the two Docker build sites stay on one version.
- **`load: true` overhead.** If the loaded-image materialization ever becomes the
  bottleneck (large image), consider `outputs: type=docker` tuning — but for the
  current atlas image size this is not a concern.

## Confidence summary

| Decision                                         | Confidence |
| ------------------------------------------------ | ---------- |
| D1 — official buildx actions                     | high       |
| D2 — `mode=max`                                  | high       |
| D3 — reuse container-publish.yml pins            | high       |
| D4 — posture identical / `load: true` required   | high       |
| D5 — real job runs on this PR; AC-4 is cross-run | high       |
