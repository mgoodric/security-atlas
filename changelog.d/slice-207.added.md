- **infra:** slice 207 — edge deploy channel for `atlas-edge.home.gmoney.sh`.
  `.github/workflows/container-publish.yml` extended with a
  `push: branches: [main]` trigger; every merge to `main` now builds
  the same multi-arch images (atlas, atlas-cli, web, bootstrap) and
  tags them `:edge` + `:main-<sha7>`. Edge images carry the same
  provenance attestation + SBOM as release builds, but are
  intentionally NOT cosign-signed (the keyless-OIDC signature stays
  bound to release-cut tag identity in `release.yml`). New
  `deploy/docker/docker-compose.edge.yml` template ships a parallel
  Unraid stack with full isolation from the stable channel: separate
  Postgres data dir + named volumes (`pg-data-edge` /
  `minio-data-edge` / `nats-data-edge` / `atlas-data-edge` /
  `atlas-bootstrap-data-edge`), separate host ports (5532/4322/9100/
  9101/8180/50151/3100), separate Docker network (compose-project
  default network). Watchtower opt-in via the
  `com.centurylinklabs.watchtower.enable=true` label on `atlas-edge`
  - `web-edge` ONLY — stable services stay pinned to whatever tag
    they were started with. New operator runbook at
    `docs/operations/edge-deploy.md` covers Unraid setup, DNS + NPM
    reverse-proxy wiring, Watchtower config, version-string
    verification via the existing slice-072 VersionFooter
    (edge images report `version = "edge"` + the full commit SHA so
    the footer renders `edge · <short-commit>`), four access-control
    options (Tailscale recommended; Cloudflare Access / NPM IP
    allowlist / NOT basic auth — cookie conflict documented),
    migration safety guidance, rollback procedure, and the
    wipe-and-recreate cycle. New
    `scripts/edge-image-cleanup.sh` + companion scheduled
    `.github/workflows/edge-image-prune.yml` (Mondays 09:00 UTC +
    `workflow_dispatch` with a `dry_run` input) implement the D2
    hybrid retention policy: a `:main-<sha7>` tag survives if it's
    younger than 30 days OR among the most recent 50 by recency. The
    jq filter only considers versions whose tag list is composed
    ENTIRELY of `main-<sha7>` entries — `:edge`, `:latest`, and every
    `vX.Y.Z` tag are unconditionally preserved. Decisions log at
    `docs/audit-log/207-edge-deploy-channel-decisions.md` records the
    six judgment calls (single workflow vs split, retention policy,
    compose isolation, Watchtower interval, ldflag parity, access
    control) plus the honest CI-delta scan.
