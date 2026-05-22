# Slice 207 — edge deploy channel — decisions log

**Slice:** [`docs/issues/207-edge-deploy-channel-watchtower.md`](../issues/207-edge-deploy-channel-watchtower.md)
**Branch:** `infra/207-edge-deploy-channel`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-22
**Type:** AFK (infra/deploy slice; no Go code change)

This log captures the JUDGMENT calls made while building slice 207.
The slice spec records WHAT to do; this log records HOW it was done
and the trade-offs weighed inline. All decisions are reviewable
post-merge by the maintainer.

---

## D1 — Single workflow vs split

**Decision:** **Single workflow.** Extended
`.github/workflows/container-publish.yml` with `push: branches: [main]`
trigger + event-conditional tag computation. NO new top-level
workflow file for edge builds. Spec called this the recommended path
(slice doc lines 105-110); locking it in.

**Why single (chosen):**

| Benefit                                                                                                            | Cost                                                                                                                                                                                                     |
| ------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| One file to keep supply-chain pin discipline aligned (harden-runner + SHA pins).                                   | Slightly more conditional logic in the metadata-action `tags:` block; the `enable=` predicates need to discriminate on `github.event_name`.                                                              |
| Shared build steps (QEMU + Buildx + checkout + GHCR login + provenance attestation) — zero duplication.            | Audit trail conflates stable + edge runs in one workflow log; operators have to filter by `event=release` vs `event=push` to disentangle. Mitigated by `concurrency.group` keyed on `github.event_name`. |
| The `concurrency` group now keys on event-type AND ref so release-cut + main-push can race safely on the same SHA. | None — this was an upgrade required by the dual-channel shape and would have been required even in the split path.                                                                                       |

**Why split (rejected):**

A separate `edge-publish.yml` would have a cleaner audit trail but
duplicates the harden-runner + checkout + Buildx + QEMU + GHCR-login

- build-push + provenance-attest steps. Three new failure modes:
  (a) the two workflows drifting on action versions (slice 128
  discipline says every action SHA-pinned; two files = two drift
  opportunities); (b) a SHA-pin bump landing in only one of the two
  files; (c) the `permissions:` block being miscopied (release builds
  need `id-token: write` for cosign; edge builds don't, but if the new
  file accidentally also requests `id-token: write` it gives more
  permission than required).

The single-workflow path closes all three.

**Verification of the event-conditional tag selection:**

I confirmed the docker/metadata-action's expression engine accepts
`enable=` predicates that reference `github.event_name` via the
`${{ }}` workflow-context interpolation. The relevant tag rules
in the extended workflow are:

```yaml
tags: |
  type=ref,event=tag                            # release-only (gated by event=tag)
  type=semver,pattern={{version}}               # release-only
  type=semver,pattern={{major}}.{{minor}}       # release-only
  type=semver,pattern={{major}}                 # release-only
  type=raw,value=latest,enable=${{ github.event_name == 'release' }}
  type=raw,value=edge,enable=${{ github.event_name == 'push' && github.ref == 'refs/heads/main' }}
  type=sha,prefix=main-,format=short,enable=${{ github.event_name == 'push' && github.ref == 'refs/heads/main' }}
```

On a release event, only the first 4 rules + `latest` produce tags.
On a push-to-main event, only `edge` + `main-<sha7>` produce tags.
The two events never collide on the same image digest with the same
tag name.

**`meta.outputs.version` for the edge case:** docker/metadata-action
sets `version` to the highest-priority matching tag rule. For the
edge case that's `:edge` (the `type=raw,value=edge` rule). So the
edge atlas binary's ldflag `main.version=edge` and the VersionFooter
displays `edge · <short-commit>` — which is exactly the desired
discriminator for D5 below (no parallel goreleaser step needed).

**Side-fix latent in the old `:latest` rule:** the pre-slice-207
workflow used `enable={{is_default_branch}}` for `:latest`. The
docker/metadata-action `is_default_branch` expression returns true
only when `github.ref` matches the repo's default branch. On a
`release: [published]` event, `github.ref` is `refs/tags/<tag>`,
NOT `refs/heads/main` — so `is_default_branch` was always FALSE on
the old release-only trigger, and `:latest` was never actually
applied. The new explicit predicate
`enable=${{ github.event_name == 'release' }}` makes `:latest`
apply on release events (where the operator semantics actually wants
it). Recording this as a benign side-fix, NOT scope creep — the
new behaviour is what the workflow's old comment was already
documenting it intended to do.

---

## D2 — GHCR retention policy (hybrid)

**Decision:** **Hybrid: keep all `:main-<sha7>` tags <30 days AND the
last 50 by recency.** Implemented as a shell script at
`scripts/edge-image-cleanup.sh` (NOT `cmd/scripts/` — see below)
called by a scheduled workflow `.github/workflows/edge-image-prune.yml`
on Monday 09:00 UTC + `workflow_dispatch` for ad-hoc runs.

**Why hybrid (chosen):**

The two single-axis policies both fail under realistic load:

| Policy                    | Failure mode                                                                                                                                                                                                                                                              |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Always keep < N days"    | A hot day on `main` (slice 196's morning shipped 12 commits in 4 hours; some days are noisier) accumulates 100+ tags within the 30-day window before the first deletion fires. GHCR has a soft storage cap that the OSS account brushes against during build-heavy weeks. |
| "Always keep last K tags" | A quiet 6 weeks followed by a burst of 50 commits in one day prunes a tag the operator is STILL rolling back from — the rollback-target tag is 51st-newest by the time the prune fires, and now it's gone.                                                                |

Hybrid takes the union: a tag survives if EITHER condition holds.
That's `KEEP_LAST=50` and `RETENTION_DAYS=30` for the floor, both
configurable via env overrides on the scheduled workflow.

**Script location — `scripts/` vs `cmd/scripts/`:** the slice spec
proposed `cmd/scripts/edge-image-cleanup.sh`, but every existing
shell script in the repo lives under `scripts/` (see
`scripts/check-action-pins.sh`, `scripts/check-openapi-drift.sh`,
etc.). `cmd/scripts/` is used for Go-tool wrappers (the
coverage-gate binaries). Putting the bash script under `scripts/`
matches established convention and is what other CI scripts grep for.
Documenting the deviation here so it isn't read as drift.

**Safety: the script's jq filter ONLY considers GHCR versions whose
tag list is composed ENTIRELY of `main-<sha7>` entries.** That
means:

- A version that ALSO carries `:edge` (the floating tag) is skipped
  because its tag list has both `main-<sha7>` AND `edge` — and `edge`
  doesn't match the `^main-[0-9a-f]{7,40}$` regex.
- A version that ALSO carries a release tag (`v*.*.*` / `latest`)
  is similarly skipped.
- Only "pure" `:main-<sha7>` versions (those whose ENTIRE tag set is
  one or more `main-<sha7>` entries — yes, two `main-<sha7>` entries
  can attach to the same digest if the same SHA was pushed twice)
  are eligible for deletion.

This is the load-bearing safety invariant. Without it, the FIRST
prune run could accidentally untag `:edge` and break the entire edge
channel until the next main push restores it.

**Numeric input validation:** the script aborts if
`EDGE_RETENTION_DAYS` or `EDGE_RETENTION_KEEP_LAST` is non-numeric
or zero. A typo like `EDGE_RETENTION_DAYS=` would otherwise
arithmetic-expand to 0 and prune everything.

**Cross-platform date math:** the script tries GNU `date -d` first
(Linux/CI runners) and falls back to BSD `date -j -f` (macOS
operator workstations). Both produce the same epoch-seconds value
for the cutoff comparison.

---

## D3 — Compose isolation

**Decision:** **Full namespace + volume + port + network isolation.**
The edge stack runs as a separate compose project
(`-p security-atlas-edge`), uses distinct named volumes
(`pg-data-edge`, `minio-data-edge`, `nats-data-edge`,
`atlas-data-edge`, `atlas-bootstrap-data-edge`), distinct host ports
(5532/4322/9100/9101/8180/50151/3100 vs 5432/4222/9000/9001/8080/
50051/3000), and an isolated default Docker network (compose-project
default networks are per-project by construction).

**Why a separate compose file (chosen over `profiles:`):**

I considered extending `docker-compose.yml` with a `profiles:` block
so `docker compose --profile edge up` would bring up an additional
fleet of edge services alongside stable. Rejected for three reasons:

1. **Image source asymmetry.** Stable services use `build:` from the
   in-tree Dockerfile (so a `docker compose up` against the repo
   builds the binary from current source). Edge services use
   `image: ghcr.io/.../:edge` (Watchtower-pulled from registry).
   Mixing `build:` and `image:` on the same compose graph forces
   operators to remember which subset rebuilds and which doesn't —
   error-prone.
2. **Volume namespacing.** Compose-profile services share the
   parent project's volume namespace. Two services in the same
   project pointing at distinct volume names like `pg-data` and
   `pg-data-edge` works mechanically but doesn't enforce the
   isolation invariant at the project level — a `docker compose
down -v` without a profile filter would wipe BOTH.
3. **`docker compose down`.** Profile-scoped down doesn't reliably
   remove the right subset on all Docker versions. A separate
   project name (`-p security-atlas-edge`) is the clean shape that
   has worked since compose v2.

**Why distinct host ports:** so both stacks can run on the same
Unraid host without port conflicts. The reverse proxy (NPM) does
hostname-based splitting at the public layer; internally the two
stacks listen on different ports so neither needs to know about
the other.

**Why isolated networks:** the stable `atlas` service and the edge
`atlas-edge` service don't share an in-network DNS namespace. An
in-cluster lookup of `atlas` from inside the edge stack resolves to
the edge service (because compose's per-project DNS sees only its
own services). This means there's no way to ACCIDENTALLY share a
DSN — a stray `postgres://postgres:5432/...` DSN in the edge stack
resolves to the EDGE postgres because that's the only `postgres`
visible from the edge network. Stable's postgres is hidden by network
isolation.

---

## D4 — Watchtower interval (5 minutes)

**Decision:** **5 minutes via `--interval 300`.** Documented in the
operator runbook + the compose file's header comment.

**Why 5 min (not 1 min, not 30 min):**

| Interval  | Pros                                                                                                              | Cons                                                                                                                                                                                           |
| --------- | ----------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1 min     | Snappy feedback                                                                                                   | GHCR rate-limit pressure on a 4-image fleet polled every minute is non-trivial; under load it would trip the unauthenticated-pull limit. Operator-only auth helps but is unnecessary overhead. |
| **5 min** | Good balance; image build (~5-10 min on CI) is the bottleneck, not the poll cadence; one poll per build is enough | 5 min of latency from merge to deployed change.                                                                                                                                                |
| 30 min    | Lowest registry-rate-limit footprint                                                                              | Iteration-loop signal too slow to be useful; defeats the point of the channel.                                                                                                                 |

The container-publish workflow takes ~5–10 min to build + push 4
multi-arch images (slice 092 timing data). With Watchtower polling
every 5 min, the worst-case time from merge to deployed change is
~10–15 min. Best case (poll arrives just as the image finishes
pushing) is ~5–7 min. Both are acceptable for the
maintainer-debugging-a-deployed-bug use case.

**Operator can tune via `--interval N` on the Watchtower run command
— the runbook documents the env-var.**

---

## D5 — ldflag parity for edge builds

**Decision:** **No additional goreleaser step needed; the existing
container-publish.yml `build-args:` already injects the right values
on edge builds.** Verified by reading the file.

**Verification:**

`.github/workflows/container-publish.yml` lines 158-161 (existing,
unchanged by this slice):

```yaml
build-args: |
  VERSION=${{ steps.meta.outputs.version }}
  COMMIT=${{ github.sha }}
  BUILD_TIME=${{ steps.buildtime.outputs.value }}
```

These flow into `deploy/docker/atlas.Dockerfile` ARGs (lines 39-41):

```dockerfile
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
RUN ... go build ... -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_TIME}" ...
```

Both events (release + push-to-main) hit the SAME build step with
the SAME build-args wiring. The values differ:

| Event        | `meta.outputs.version` | `github.sha`       |
| ------------ | ---------------------- | ------------------ |
| release      | `v0.4.2` (semver tag)  | the release commit |
| push-to-main | `edge`                 | the merge SHA      |

So edge images have `main.version = "edge"` and `main.commit =
"<full-sha>"`. The VersionFooter (`web/lib/version.ts` reads
`GET /v1/version`) renders `edge · <short-commit>`. AC-8 is
satisfied — verify only, no code change.

**The `org.opencontainers.image.revision` label is also explicitly
set to `${{ github.sha }}`** (added by this slice — line 131 of the
extended workflow). So `docker inspect <image>` reveals the commit
SHA independent of the binary's ldflags, and Watchtower's polling
also has a digest-comparable reference.

---

## D6 — Access control on `atlas-edge.home.gmoney.sh`

**Decision:** **Document the four options; do NOT prescribe one.**
The slice ships docs that recommend Tailscale (option A) for solo
operators and lists three fallbacks (Cloudflare Access / NPM IP
allowlist / NPM basic auth, the last NOT recommended due to cookie
conflict). The slice does NOT implement the access control on the
GitHub side — that's an Unraid-side config the operator wires.

**Why doc-only (no implementation):**

The access control lives at the reverse-proxy or
firewall layer, NOT in the docker-compose template. NPM is the
maintainer's reverse proxy on Unraid; Cloudflare Access is the cloud
gateway some operators use; Tailscale is the VPN mesh. None of these
are configured via docker-compose — they all run alongside it.

The slice's responsibility ends at "operator-only by default + the
docs explain how to enforce that." P0-A5 is satisfied because the
default Unraid+NPM setup does NOT publish the new hostname publicly
until the operator explicitly creates a proxy host for it — and the
runbook walks them through the NPM setup with the access control
choice highlighted at step 4.

**Why not just "internal LAN only" with no DNS at all:**

The maintainer wants `atlas-edge.home.gmoney.sh` to be hostname-
addressable from the maintainer's laptop wherever they are (home,
office, travel). Pure internal-LAN doesn't satisfy that. Tailscale +
public DNS over tailnet IP solves it cleanly. Cloudflare Access
solves it too but adds a vendor dependency that the maintainer can
choose to take.

---

## D7 — CI-delta scan (honest)

This slice touches:

1. `.github/workflows/container-publish.yml` (modified)
2. `.github/workflows/edge-image-prune.yml` (new)
3. `deploy/docker/docker-compose.edge.yml` (new)
4. `scripts/edge-image-cleanup.sh` (new)
5. `docs/operations/edge-deploy.md` (new)
6. `docs/audit-log/207-edge-deploy-channel-decisions.md` (new)
7. `CHANGELOG.md` (modified — Added section)

**Path filter (`ci.yml` `changes.code`):**

The CI path filter at `.github/workflows/ci.yml:106-107` includes
`'deploy/**'` AND `'.github/workflows/**'`. Both are touched by this
slice, so `changes.code = true`, which means the Go + Frontend test
jobs run, as does `test-self-host-bundle`.

**Will `Self-host bundle · end-to-end` still pass?** The bundled
smoke test references `docker-compose.yml`, NOT `docker-compose.edge.yml`
(confirmed by inspecting `deploy/docker/test-self-host-bundle.sh:70`).
The new edge compose file is parallel; the existing harness is
untouched. So the bundled smoke continues to exercise the SAME
bundled stack and the test outcome should be unchanged.

**`actions-pin-check`:** every NEW `uses:` line in this slice (two of
them, both in `.github/workflows/edge-image-prune.yml` — the harden-
runner pin + the checkout pin) reuses the SHA already in use in the
existing workflows. Local repro:

```sh
$ bash scripts/check-action-pins.sh
check-action-pins: no tag-pinned actions detected (132 pinned across 7 files)
```

Clean — 132 `uses:` lines across 7 workflows all SHA-pinned (was
130 across 6 before this slice).

**`actionlint`:** clean against the modified container-publish.yml
AND the new edge-image-prune.yml:

```sh
$ actionlint .github/workflows/container-publish.yml .github/workflows/edge-image-prune.yml
(no output → no findings)
```

**`shellcheck`:** clean against the new script. The script uses
`set -Eeuo pipefail` and handles every documented exit code path.

**`pre-commit run --all-files`:** to be run before commit (see
section below).

**Path-filter gap-multiplier risk (per feedback_path_filter_gap_multiplier.md):**
this slice modifies a workflow file (`.github/workflows/container-publish.yml`)
that is NOT itself gated by the path filter — it runs on its OWN
trigger (release + now also push:main). That's the SAME shape as
other workflow-edits and doesn't accumulate gap-multiplier debt
because the workflow is fully self-contained (no other CI job
imports its outputs).

**Smoke risk for the NEW workflow (`edge-image-prune.yml`):** the
scheduled `cron: "0 9 * * 1"` only fires once weekly. The first
production invocation will be on the next Monday after merge. The
`workflow_dispatch` path with `dry_run=true` lets the maintainer
do a dry-run sanity check on the first business day after merge
WITHOUT risking accidental deletion. Documented in the workflow's
top-of-file comment and the operator runbook.

**Spillover candidate surfaced:** there's no automated smoke test
for the EDGE compose file itself — the bundled smoke (slice 037 +
slice 065 + slice 202 lineage) covers `docker-compose.yml` but not
`docker-compose.edge.yml`. Building an edge equivalent of
`test-self-host-bundle.sh` that pulls `:edge` from GHCR (or
substitutes `build:` for `image:`) would close the gap but is
out-of-scope for this 1-day slice. Filing as a possible spillover.

---

## Spillover candidates

| #   | Description                                                                                                                                                                                                                        | Cluster        | Priority |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- | -------- |
| 1   | Edge-equivalent of `test-self-host-bundle.sh` that exercises `docker-compose.edge.yml`. Either substitutes `build:` for `image:` to skip the GHCR dependency, OR pulls the latest `:edge` images and exercises the published flow. | infra / CI     | low      |
| 2   | `EDGE_ATLAS_TAG` env-var to make rollback ergonomic — the compose file would read `image: ghcr.io/.../:${EDGE_ATLAS_TAG:-edge}` so operators can pin to a previous SHA without editing the compose file inline.                    | infra / deploy | medium   |
| 3   | Slack/Discord webhook notification when Watchtower pulls a new image (so the operator gets a ping when the edge is updated rather than discovering it by visiting the hostname).                                                   | observability  | low      |
| 4   | Refining the GHCR retention defaults (30 days, 50 tags) after the first 3-month period of operator data — likely tuning down to 14 days / 30 tags if usage stays steady.                                                           | infra / deploy | low      |

None of these block the slice's primary success criterion. All four
are independently sliceable.
