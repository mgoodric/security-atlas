<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./docs/images/logo-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="./docs/images/logo-light.png">
    <img alt="security-atlas node-graph A mark" src="./docs/images/logo-light.png" width="160" height="160">
  </picture>
</p>

# security-atlas

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![CI](https://github.com/mgoodric/security-atlas/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mgoodric/security-atlas/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/mgoodric/security-atlas/graph/badge.svg?token=SI2ZW30LS1)](https://codecov.io/gh/mgoodric/security-atlas)
[![Latest release](https://img.shields.io/github/v/release/mgoodric/security-atlas?sort=semver)](https://github.com/mgoodric/security-atlas/releases/latest)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/hero-dashboard-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="./docs/images/hero-dashboard.png">
  <img alt="security-atlas program dashboard: drift, freshness, top risks, upcoming reviews" src="./docs/images/hero-dashboard.png">
</picture>

Open-source, self-hostable GRC platform: a control-graph and evidence-pipeline that lets a single security program operate against many frameworks (SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, GDPR) from one source of truth.

The spine is the [Secure Controls Framework](https://securecontrolsframework.com/) (~1,400 controls crosswalked to 200+ frameworks via NIST IR 8477 STRM). The wire format is NIST OSCAL. The target user is the solo security leader at a 50–150-person security-product startup who runs the entire program (risk register, board reporting, SOC 2, vendor reviews, policies, exceptions) alone.

**v1 complete; operator-grade today.** All 69 v1 slices are merged on `main`; v2 follow-on work is well underway. The current release is **v1.10.0** (2026-05-18). 120+ slices have shipped, including the unified audit-log trio (124 + 125 + 126 + 129 + 130: admin-visible `/audit-log` aggregation across nine per-domain log surfaces with an external HMAC-signed sink) and the CI hardening trilogy (117 + 127 + 128: StepSecurity Harden-Runner, branch-protection drift detection, and SHA-pinned GitHub Actions with a BLOCKING pin-check guard). See [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) for the design canvas, [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) for the slice backlog, [`docs/issues/_STATUS.md`](./docs/issues/_STATUS.md) for the live merge trail, and [`CHANGELOG.md`](./CHANGELOG.md) for the per-release notes.

---

## Project status

security-atlas is a **pure-community open-source project** under the
[Apache 2.0 license](./LICENSE). There is **no hosted SaaS** offered by
the project owners and **no enterprise edition** with proprietary
features. This posture is time-bounded; the maintainer will re-evaluate
on **2028-05-20** (or earlier, if release-download stats cross 100
deployed self-hosts). The full governance model, funding posture, and
bus-factor / succession plan live in [`GOVERNANCE.md`](./GOVERNANCE.md).

---

## Why security-atlas

Existing GRC tools optimize for the first-SOC-2-in-90-days SMB sale. They model controls per-framework, store evidence in a vendor cloud, and the Year-2 renewal cliff is well-documented.

security-atlas inverts the model:

- **One control, N framework satisfactions.** The Unified Control Framework is a graph with STRM-typed edges through SCF anchors. Never duplicate controls per framework.
- **Append-only evidence ledger.** Ingestion and evaluation are separated stages; evaluation never writes to source-of-truth evidence. Point-in-time replay is always possible.
- **Self-hostable from day one.** Single mid-size VM runs the whole platform. NATS JetStream (single binary) · Postgres · S3-compatible artifact store.
- **OSCAL-native.** Ingest catalogs / profiles / component-definitions; export SSP / AP / AR / POA&M.

---

## What's new in v1.10.0 (2026-05-18)

The two recent capability batches that materially changed the operator experience:

- **Unified audit-log trio.** Slices 124 + 125 + 126 + 129 + 130 land a single admin-facing `/audit-log` page that aggregates across nine per-domain audit-log tables (decisions, evidence, exceptions, sample, audit-period, aggregation-rule, feature-flag, me, walkthrough) via a `GET /v1/admin/audit-log/unified` endpoint, with an HMAC-SHA256-signed external JSONL sink for tamper-evident off-host retention and a backpressure-to-fallback table so no record is ever silent-dropped. The role guard admits admin + auditor + grc_engineer; the wire format includes `actor_name` resolution via LEFT JOIN on `users` (per-tenant RLS isolation).
- **CI hardening trilogy.** Slices 117 + 127 + 128 close three supply-chain gaps: StepSecurity Harden-Runner audit mode on every CI job, branch-protection file ↔ live drift detection on every PR + push to main, and SHA-pinning of every GitHub Action across every workflow with a BLOCKING `actions-pin-check` guard that fails the build on any non-40-char-hex `uses:` line.

See [`CHANGELOG.md`](./CHANGELOG.md) for the full per-release notes; the [`docs/audit-log/`](./docs/audit-log/) directory holds per-slice decision logs that record the JUDGMENT calls behind each merged feature.

---

## Screenshots

Captured from the running app against the slice-057 hermetic stub-server demo fixtures (`fixtures/readme-demo/`). Run `ATLAS_DEMO_SEED=1 just refresh-screenshots` to regenerate them. The capture pipeline refuses to run unless `ATLAS_DEMO_SEED=1` is set and the upstream HTTP target is loopback / RFC1918 private (slice 132 information-disclosure safety gate; every captured PNG is public forever once the README merges). Light and dark variants below; the page selects per `prefers-color-scheme`.

### Control detail: UCF crosswalks

One control, N framework satisfactions. STRM-typed edges through an SCF anchor.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/control-detail-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="./docs/images/control-detail.png">
  <img alt="control detail view showing SCF anchor and multi-framework requirement mappings" src="./docs/images/control-detail.png">
</picture>

### Audit workspace: frozen audit period

The auditor's surface. Period header with frozen-at timestamp; sampling, walkthrough, and comments tabs per control.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/audit-workspace-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="./docs/images/audit-workspace.png">
  <img alt="audit workspace view showing frozen period header and sampling tab for a control" src="./docs/images/audit-workspace.png">
</picture>

### Board pack preview: the quarterly artifact

The v1 binary success-test artifact. Templated narrative per section, per-section approval, frozen on publish.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/board-pack-preview-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="./docs/images/board-pack-preview.png">
  <img alt="board pack preview showing the framework posture section with templated narrative" src="./docs/images/board-pack-preview.png">
</picture>

---

## Install

```sh
# clone
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas

# bring up local Postgres + apply migrations
just db-up
just migrate-up

# build everything
just build
```

Detailed local dev setup, prerequisites, and the full `just` recipe surface live in [`CONTRIBUTING.md`](./CONTRIBUTING.md).

### Your first sign-in (self-host)

The platform mints a one-time bootstrap admin bearer at startup. The `/login` page detects fresh-install state and shows three orthogonal ways to find the token:

- **docker-compose:** `docker compose logs atlas 2>&1 | grep BOOTSTRAP_TOKEN`
- **Helm:** `kubectl logs deploy/atlas --tail=200 2>&1 | grep BOOTSTRAP_TOKEN`
- **Filesystem:** `cat ${ATLAS_DATA_DIR:-/var/lib/atlas}/bootstrap-token` (mode 0600)

The bootstrap-token file is **deleted atomically on first successful sign-in**. If you get stuck (token rolled out of the log buffer; the file was already consumed but no session was established), see the [first-time login troubleshooting page](./docs-site/docs/troubleshooting/first-login.md); it documents the `atlas-cli credentials issue --reset-bootstrap --force` recovery path.

---

## Quickstart: first evidence in 5 minutes

```sh
# 1. start the platform locally
just db-up && just migrate-up
just build-go
./bin/atlas serve &

# 2. push a hello-world evidence record
./bin/atlas-cli evidence push \
  --evidence-kind=hello.world.v1 \
  --observed-at="$(date -Iseconds)" \
  --payload='{"message":"first record"}'

# 3. read it back
./bin/atlas-cli evidence list --evidence-kind=hello.world.v1
```

For a connector-driven walkthrough (AWS S3 encryption posture, GitHub branch-protection, osquery host posture), see [`docs/SELF_HOSTING.md`](./docs/SELF_HOSTING.md).

### Verifying your install

The build version, commit, and build time are baked into the binary at release time and surface in three places. All three report the same value (single source of truth: Go ldflags).

```sh
# Server binary: JSON, suitable for scripts
curl -s http://localhost:8080/v1/version

# CLI: human-readable banner
./bin/atlas-cli version

# Docker image: OCI image annotations
docker inspect ghcr.io/mgoodric/security-atlas:latest \
  --format '{{ index .Config.Labels "org.opencontainers.image.version" }}'
```

The same version also renders in the bottom-right of every page in the web UI; click the trigger to expand a small panel showing `commit`, `build_time`, and `go_version`. No phone-home; no "check for updates". The value is read once at app boot and cached for the session.

---

## Documentation

- **Design canvas:** [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) (vision, primitives, UCF, evidence engine, scope, risk, metrics, audit workflow, tech stack, roadmap, open questions)
- **Constitutional principles:** [`CLAUDE.md`](./CLAUDE.md) (10 architecture invariants, anti-patterns we reject, AI-assist boundary, licensing constraints)
- **Self-hosting guide:** [`docs/SELF_HOSTING.md`](./docs/SELF_HOSTING.md)
- **Measuring your program:** slice 076 lands a curated 40-metric catalog (board / program / team cascades) + the read/write API + a 15-minute evaluator cron. See the [metrics docs](./docs-site/docs/metrics.md) for what's in the catalog, how the cascade composes, and how to interpret a dip.
- **ADRs:** [`docs/adr/`](./docs/adr/)
- **Release readiness:** [`docs/RELEASE_READINESS.md`](./docs/RELEASE_READINESS.md)
- **Slice backlog:** [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md)

---

## Authentication

security-atlas authenticates request hot-path traffic via an internal OAuth 2.0 Authorization Server that issues JWT access tokens carrying tenant-in-claim (RFC 9068 JWT Profile + RFC 8693 Token Exchange). The architectural commitment is captured in [ADR-0003](./docs/adr/0003-oauth-authorization-server.md); the resolution context lives in [`Plans/canvas/11-open-questions.md`](./Plans/canvas/11-open-questions.md) item 21.

Slice 187 ships the cryptographic + discovery scaffolding (JWT signing keypair · JWKS endpoint at `/.well-known/jwks.json` · OIDC discovery at `/.well-known/openid-configuration`). The remaining auth-substrate-v2 spine slices (188-192) ship the OAuth grant flows, JWT validation middleware, frontend OAuth client, SDK migration, and multi-tenant tenant-switch. Existing bearer-token API keys (slice 034) remain valid through a 90-day deprecation window once the OAuth flows are live.

The atlas AS is layered on the slice-034 OIDC RP: the RP authenticates the human via an external IdP (atlas-as-OIDC-RP); the AS layer mints the atlas JWT (atlas-as-issuer). Two distinct roles, one server process.

## Security

security-atlas treats security as a first-class concern. The project ships with:

- **Reporting channel:** see [`SECURITY.md`](./SECURITY.md) for the private vulnerability disclosure process and response timelines. Please **do not** open a public issue for a security finding.
- **Pipeline hardening:** CodeQL static analysis (Go + JS/TS), GitGuardian secret scanning, and Dependabot version-bump alerts run on every PR.
- **Dependency vulnerability scanning:** [`Go · govulncheck`](./.github/workflows/ci.yml) (Go call-graph-aware CVE detection), [`Frontend · npm audit`](./.github/workflows/ci.yml) (runtime-shipped JS deps in `web/`), and [`Container · Trivy scan`](./.github/workflows/ci.yml) (OS-package CVEs in the built atlas image). All three fail on HIGH/CRITICAL; reports upload as workflow artifacts. Triage runbook + suppression-mechanism reference: [`docs/audit-log/089-dependency-vulnerability-scanning-decisions.md`](./docs/audit-log/089-dependency-vulnerability-scanning-decisions.md). These complement Dependabot: Dependabot opens PRs when an upgrade is available; these flag known CVEs on the current version when no upgrade exists yet.
- **Hardening headers:** HSTS / CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy applied on every response. See [`internal/api/securityheaders/`](./internal/api/securityheaders/).
- **Audit reports:** maintainer-led security audits live under [`docs/audits/`](./docs/audits/). The first-pass audit is [`2026-Q2-security-audit.md`](./docs/audits/2026-Q2-security-audit.md) (Q2 2026, performed at slice 085).
- **Audit cadence:** quarterly scheduled review, plus an additional audit after any major change to authentication, authorization, middleware, or evidence-ingestion code paths. First-pass audits are not a substitute for third-party penetration testing; they catch the high-yield patterns automated scanners miss.
- **Remediation tracking:** actionable findings from each audit are filed as discrete remediation slices under [`docs/issues/`](./docs/issues/) and tracked through the normal review/merge process. The audit report's "Remediation status" lines point at the merge commits that resolved each finding.
- **CLI HTTP timeouts:** atlas-cli HTTP calls timeout via [`cmd/atlas-cli/cmdhttp`](./cmd/atlas-cli/cmdhttp/client.go). Default 30s. See `cmdhttp/client.go`.

---

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for dev setup, the Conventional Commits convention, and the DCO sign-off requirement.

By participating in this project you agree to abide by the [`Code of Conduct`](./CODE_OF_CONDUCT.md).

Security issues: please **do not** open a public issue. See [`SECURITY.md`](./SECURITY.md) for the private disclosure channel.

---

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).

`SPDX-License-Identifier: Apache-2.0`
