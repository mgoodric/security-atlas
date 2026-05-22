# 207 — Edge deploy channel: per-commit images + Watchtower-driven `atlas-edge` instance

**Cluster:** Infra / Deploy
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`
**Parent:** maintainer-surfaced 2026-05-22. Iterating on slice 206's deployment-blocking fix required a manual cut-tag-release-redeploy cycle. The maintainer wants a continuous-deploy edge channel that ships every `main` push to a parallel Unraid instance (`atlas-edge.home.gmoney.sh`) without going through release-please's cadence. The versioned channel (`atlas.home.gmoney.sh`) keeps its slow stable cadence; edge runs hot.

## Narrative

Today the deploy pipeline is single-channel:

1. `release-please` opens PRs on every merge to `main`; the maintainer merges these on their own cadence (every 2-7 days observed).
2. Merging a release-please PR pushes a `v*.*.*` tag.
3. `.github/workflows/container-publish.yml` builds + pushes Docker images on the tag (cosign-signed, attestations, multi-arch).
4. Unraid's atlas instance is pulled to the new tag via Watchtower (or manual `docker compose pull`).

The gap: changes that land on `main` are not testable end-to-end on a real deployment until the next release cut. That makes iteration-loops like slice 206 (where the deployed UI was the only place a bug reproduced) painfully slow. **Slice 207 closes that gap** by adding a second deploy channel:

| Channel           | Hostname                    | Trigger              | Image tag                | Cadence             | Audience                                          |
| ----------------- | --------------------------- | -------------------- | ------------------------ | ------------------- | ------------------------------------------------- |
| Stable (existing) | `atlas.home.gmoney.sh`      | release-please tag   | `vX.Y.Z`                 | every 2-7 days      | the maintainer's "real" instance + future demo    |
| **Edge (new)**    | `atlas-edge.home.gmoney.sh` | every push to `main` | `:edge` + `:main-<sha7>` | ~10 min after merge | development testing, slice-204 audit fleet target |

The two instances run side-by-side on Unraid with **separate Postgres databases**, separate MinIO buckets, separate keystore paths — full isolation. Schema migrations apply independently. The edge channel can be wiped and recreated freely without affecting stable.

The UI's existing `VersionFooter` (slice 072) already renders `v<version> · <short-commit>` clickable to reveal full build_time + go_version — **this means the operator can immediately tell which channel + which exact commit they're looking at.** No UI work needed; the version-string is already plumbed via `/api/version`.

## Threat model

| STRIDE                | Threat                                                                                                                                            | Mitigation                                                                                                                                                                                                                                                                                                                                                                                                           |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Edge image is published with weaker provenance than stable (no cosign signature) → could be confused for a stable build by a downstream consumer. | AC-6: edge images use a distinct tag namespace (`:edge`, `:main-<sha7>`); cosign signing remains on tag-published images only. The OCI image label `org.opencontainers.image.source` + `org.opencontainers.image.revision` are set on edge images so a `docker inspect` reveals the build commit. Watchtower configured for `atlas-edge` ONLY watches the `:edge` tag — no risk of stable accidentally pulling edge. |
| **T** Tampering       | A malicious force-push to `main` could land code that goes straight to a running deployment.                                                      | Mitigated by `main` branch protection (slice 050 + 128 + 140 + 158): force-push to `main` is blocked; the required-checks set (slice 116 now includes Playwright) gates merge. Edge is downstream of `main`; the gate is at the merge boundary.                                                                                                                                                                      |
| **R** Repudiation     | The edge instance's audit-log mixes with the stable instance's.                                                                                   | AC-9: edge runs in a separate Postgres DB. The two instances have separate tenant_id values + separate audit-log tables. A single deploy never reads or writes across the boundary.                                                                                                                                                                                                                                  |
| **I** Info disclosure | Edge might ship a half-baked feature that accidentally exposes data the stable channel doesn't.                                                   | AC-10: edge is operator-only by default — the `atlas-edge.home.gmoney.sh` DNS record + NPM rule limit access to the maintainer's tailnet OR explicit allowlist. Public exposure of edge is OUT of scope.                                                                                                                                                                                                             |
| **D** DoS             | A bad commit on `main` could crashloop the edge instance.                                                                                         | Acceptable — that's the edge channel's job (find the bad commit before release). Watchtower's default behavior leaves the previous container running if the new pull fails image-pull; if the container crashloops after pull, the operator manually rolls back via `docker compose pull <prev-sha-tag>`. AC-12: operator runbook documents the rollback steps.                                                      |
| **E** EoP             | n/a — no new authz boundary.                                                                                                                      | n/a                                                                                                                                                                                                                                                                                                                                                                                                                  |

## Acceptance criteria

### Container publish

- [ ] **AC-1**: `.github/workflows/container-publish.yml` extended (or new `edge-publish.yml`) with a `push: branches: [main]` trigger. On every push to `main`, build the same multi-arch images already built on release events, tag them as `:edge` AND `:main-<sha7>` (where `<sha7>` is the 7-char short commit). Implementer picks single workflow vs split; document in D1.
- [ ] **AC-2**: Image labels: edge images set `org.opencontainers.image.revision=<full-sha>` and `org.opencontainers.image.source=https://github.com/mgoodric/security-atlas`. The existing `docker/metadata-action` already does this on release builds — verify it's preserved for edge builds.
- [ ] **AC-3**: Edge images are **NOT cosign-signed** (intentional — cosign signing is bound to release-cut tags via Sigstore keyless OIDC; the security-boundary in `release.yml`'s top comment block applies). Document this distinction in the workflow + the operator runbook.
- [ ] **AC-4**: GHCR retention: edge tags accumulate. Image tags older than 30 days OR more than 50 commits behind `main` are deletable. The implementer adds a `gh api -X DELETE` cleanup script at `cmd/scripts/edge-image-cleanup.sh` (or `.github/workflows/edge-image-prune.yml` scheduled weekly). Document the retention policy in D2.

### Edge instance docker-compose

- [ ] **AC-5**: NEW `deploy/docker/docker-compose.edge.yml` (or extend bundled with a `profiles:` section). The edge stack runs separate containers from stable: distinct postgres volume + name, distinct minio bucket prefix, distinct atlas keystore path, distinct NPM proxy host. Documented in D3.
- [ ] **AC-6**: Edge atlas service polls `ghcr.io/.../atlas:edge`. Watchtower is configured for label-based discovery (`com.centurylinklabs.watchtower.enable=true` on the edge atlas container only — stable atlas remains opt-out). The implementer picks polling interval (recommend 5 min); document in D4.
- [ ] **AC-7**: NEW `docs/operations/edge-deploy.md` covering: what edge is for, how to set up the parallel stack on Unraid, how to wire `atlas-edge.home.gmoney.sh` DNS + NPM, how to read the VersionFooter to confirm the running commit, how to roll back (pin to `:main-<prev-sha7>`), how to wipe + recreate the edge DB.

### Version visibility (already shipped by slice 072 — verify only)

- [ ] **AC-8**: VersionFooter (slice 072) already displays `v<version> · <short-commit>` and reveals full build_time on click. Verify edge images carry the correct git commit through goreleaser ldflags. If edge ldflags differ from release ldflags, document why in D5; otherwise confirm parity.

### Operator runbook + safety

- [ ] **AC-9**: Edge instance MUST point at a separate Postgres database. The operator runbook explicitly calls out the multi-DB Postgres setup OR a separate Postgres container. Cross-instance database sharing is forbidden (a single bad migration on edge would corrupt stable).
- [ ] **AC-10**: Edge access control documented: by default `atlas-edge.home.gmoney.sh` is operator-only (tailnet OR Cloudflare Access OR NPM IP allowlist — implementer picks; document in D6). Public exposure of edge is out of scope.
- [ ] **AC-11**: Migration safety note in `docs/operations/edge-deploy.md`: a schema-breaking commit on `main` will run its migrations against the edge DB on next Watchtower pull. The operator runs migrations on edge first; if they fail, the commit is reverted (or fixed forward) before the next release-please cut.
- [ ] **AC-12**: Rollback runbook: how to pin the edge atlas service to a previous `:main-<sha7>` tag if a bad commit lands.
- [ ] **AC-13**: CHANGELOG entry under "Added" describing the new edge channel.
- [ ] **AC-14**: Decisions log at `docs/audit-log/207-edge-deploy-channel-decisions.md` covering D1-D6 (workflow shape, retention policy, compose isolation, Watchtower interval, ldflag parity, access control).

## Constitutional invariants honored

- **#6 tenant isolation at DB layer**: edge runs its own DB; no cross-instance RLS leakage possible because the deploys never share a connection.
- **AI-assist boundary**: n/a (infra/deploy slice).
- **Tech stack lock-ins**: stays within current stack (docker-compose, GHCR, Watchtower, NPM). No new dependencies.

## Canvas references

- None — operations/deploy concern, not architectural.

## Dependencies

- **#072** (Version string surfaced in the UI) — merged. VersionFooter does the operator-visible commit-id surfacing; no UI work needed for this slice.
- **#117 + 118** (StepSecurity Harden-Runner) — merged. The edge workflow uses the same harden-runner pin as existing workflows; supply-chain discipline carries forward.
- **#128** (SHA-pinned action versions) — merged. Edge workflow inherits the same pin discipline.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT modify the existing release-cut workflow (`release.yml` or `container-publish.yml`'s release-event branch). The stable channel is untouched.
- **P0-A2**: DOES NOT cosign-sign edge images. The release-only cosign signing is a load-bearing supply-chain guarantee bound to tag identity; weakening it on edge would dilute the contract.
- **P0-A3**: DOES NOT auto-promote edge images to stable. Edge → stable promotion goes through release-please as it does today.
- **P0-A4**: DOES NOT share the Postgres database between stable and edge instances. The operator runbook calls this out; the docker-compose template enforces separate volumes.
- **P0-A5**: DOES NOT expose `atlas-edge.home.gmoney.sh` to the public internet by default. Operator-only access (tailnet / Cloudflare Access / IP allowlist).
- **P0-A6**: DOES NOT bypass the `main` branch-protection required-checks gate. Edge images build only AFTER the merge is gated.
- **P0-A7**: DOES NOT use vendor-prefixed test fixture tokens.

## Skill mix

- GitHub Actions workflow editor (extends `container-publish.yml` or adds `edge-publish.yml`)
- docker-compose template author (`deploy/docker/docker-compose.edge.yml`)
- Operator-runbook author (Markdown under `docs/operations/`)
- GHCR API call for image cleanup (small shell script or scheduled workflow)

## Notes for the implementing agent

This is **infra-side plumbing** that the maintainer then exercises live by setting up the parallel stack on Unraid. The slice ships the GitHub-side pieces (workflow + compose template + docs); the Unraid-side execution is operator work documented in `docs/operations/edge-deploy.md`.

**D1 — single workflow vs split.** Two options:

- (a) Extend `container-publish.yml` with a `push: branches: [main]` trigger + conditional tag computation (release event → `vX.Y.Z`; main push → `:edge` + `:main-<sha7>`). Single file, smaller diff, shared steps.
- (b) NEW `edge-publish.yml` with just the main-push trigger. Separate file, clearer audit trail, can diverge without affecting release builds.

Recommend (a) — keeps the existing supply-chain pin discipline (harden-runner, sha-pinned actions) in one place, reduces the chance of drift. Document the conditional tag logic clearly.

**D2 — GHCR retention.** Edge tags accumulate. Pick + document:

- (i) Delete `:main-<sha7>` tags older than 30 days via a weekly scheduled workflow.
- (ii) Keep last 50 main-sha tags; delete older.
- (iii) Hybrid: keep all `:main-<sha7>` tags <30d AND last 50 by recency.

Recommend (iii) for safety with bounded growth.

**D3 — compose isolation.** The edge compose stack MUST:

- Use distinct postgres volume name (`atlas_edge_pgdata` vs `atlas_pgdata`)
- Use distinct atlas keystore path (`/var/lib/atlas-edge/keys` vs `/var/lib/atlas/keys`)
- Use distinct MinIO bucket or path prefix
- Bind to distinct host ports (e.g., 3016 vs 3015) OR run on a separate Docker network
- Use distinct internal Docker network name

**D4 — Watchtower interval.** Watchtower's default poll interval is 24h. For edge, recommend 5 min — fast feedback. The operator can tune.

**D5 — ldflag parity.** Verify that edge images carry the correct git commit through goreleaser ldflags (the same `-X main.commit=<sha>` injection used by release builds). If goreleaser only runs on release events, the implementer may need a parallel `goreleaser build --snapshot` step for edge.

**D6 — Access control on `atlas-edge.home.gmoney.sh`.** Operator-only. Pick:

- (i) Tailscale Funnel / Tailnet-only (recommend if operator is already on tailnet)
- (ii) Cloudflare Access (if maintainer has Cloudflare Zero Trust set up)
- (iii) NPM IP allowlist (simplest; only operator's known IPs)
- (iv) Basic auth via NPM (deployment-stable but cookie-conflicting with atlas's own auth)

The slice doesn't IMPLEMENT the access control — the operator does that on their Unraid. The slice DOCUMENTS the recommendation.

**Migration risk note**: schema-breaking commits ship to edge first. The operator MUST validate migrations on edge before the next release-please cut. If migrations break, the operator either reverts the commit OR fixes forward. Document this discipline.

**Iteration loop this enables:**

1. Merge to main
2. Edge image builds (~5-10 min)
3. Watchtower pulls on Unraid (~5 min poll interval)
4. Test on `atlas-edge.home.gmoney.sh`
5. If good → wait for release-please cycle
6. If bad → fix forward OR revert; cycle repeats

The maintainer can now run slice 204's audit fleet against `atlas-edge.home.gmoney.sh` with confidence the deployed UI matches `main` — closes the operational blocker on slice 204.

Provenance: filed 2026-05-22 immediately after slice 206 (BFF cookie fix) merged. The slice 206 iteration loop required a manual cut-tag-release cycle; slice 207 makes the same shape automatic for future fixes.
