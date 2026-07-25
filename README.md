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

**Run your whole security-compliance program — SOC 2, ISO 27001, and more — from one open-source app you host yourself.**

---

## What is this?

If you run security or compliance at a company, you know the drill: prove to auditors and customers that you actually do what your policies say. That means collecting evidence (screenshots, config exports, access reviews), mapping it to controls, and doing it again for every framework — SOC 2, then ISO 27001, then a customer's security questionnaire — usually in a pile of spreadsheets or a SaaS tool that holds your data hostage and gets expensive at renewal.

**security-atlas is a GRC platform** (GRC = Governance, Risk, and Compliance) that runs your entire program from a single source of truth — and you host it on your own server, so your evidence never leaves your control.

The core idea: **write a control once, satisfy many frameworks at once.** Most tools make you re-create the same control for every framework you're audited against. security-atlas keeps one set of controls and maps each to the frameworks it satisfies, so adding ISO 27001 on top of SOC 2 is mostly mapping, not re-work. It does this using the [Secure Controls Framework](https://securecontrolsframework.com/) (SCF) — an open catalog of ~1,400 controls already cross-referenced to 200+ frameworks.

## What it does

- **Collects evidence automatically** from the systems you already run — AWS, GitHub, Okta, GCP, Azure, Kubernetes, and more — through open, read-only connectors. Manual evidence (a signed policy, a meeting note) is a first-class citizen too.
- **Maps one control to many frameworks**, so SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, and GDPR draw from the same controls instead of duplicated copies.
- **Runs your SOC 2 audit** end to end: an auditor workspace, evidence sampling, and a frozen audit period so the auditor sees a stable snapshot while your live program keeps moving.
- **Generates the board report** — the quarterly security update for your leadership, drafted from real data with every number checked against the source.
- **Tracks risks, policies, exceptions, and vendor reviews** in the same place, linked to the controls they affect.
- **Exports in OSCAL** (the NIST open standard for compliance data), so your data is portable and not locked in.

## Who it's for

The first user we built for is the **solo security leader at a 50–150-person startup** who runs the entire program alone — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — and whose own customers will scrutinize how they handle security. If that's you, the goal is simple: run your next SOC 2 audit out of security-atlas and build your next board pack from it, without reaching for a spreadsheet to fill a gap.

## Project status

security-atlas is a **pure-community open-source project** under the
[Apache 2.0 license](./LICENSE). v1 is complete and operator-grade; active
v2 development continues. There is **no hosted SaaS** offered by the project
owners and **no paid edition** with locked-away features — you run the whole
thing yourself.

For what shipped and when, see the [latest release](https://github.com/mgoodric/security-atlas/releases/latest)
and [`CHANGELOG.md`](./CHANGELOG.md). The full governance model, funding
posture, and succession plan live in [`GOVERNANCE.md`](./GOVERNANCE.md); the
re-evaluation triggers for the no-SaaS posture are documented there.

---

## Why security-atlas (the design bets)

Existing GRC tools optimize for the first-SOC-2-in-90-days SMB sale. They model controls per-framework, store evidence in a vendor cloud, and the Year-2 renewal cliff is well-documented. security-atlas makes the opposite bets:

- **One control, N framework satisfactions.** The Unified Control Framework is a graph with STRM-typed edges through SCF anchors. Controls are never duplicated per framework.
- **Append-only evidence ledger.** Ingestion and evaluation are separate stages; evaluation never writes to source-of-truth evidence. Point-in-time replay is always possible, so a bug in scoring can never corrupt the record.
- **Self-hostable from day one.** A single mid-size VM runs the whole platform: NATS JetStream (single binary) · Postgres · an S3-compatible artifact store.
- **OSCAL-native.** Ingest catalogs / profiles / component-definitions; export SSP / AP / AR / POA&M.

The complete design rationale — invariants, anti-patterns we reject, and the AI-assist boundary — lives in the [architecture canvas](./Plans/ARCHITECTURE_CANVAS.md) and [`CLAUDE.md`](./CLAUDE.md).

---

## Screenshots

Captured from the running app against the hermetic demo fixtures (`fixtures/readme-demo/`). Run `ATLAS_DEMO_SEED=1 just refresh-screenshots` to regenerate them. The capture pipeline refuses to run unless `ATLAS_DEMO_SEED=1` is set and the upstream HTTP target is loopback / RFC1918 private (an information-disclosure safety gate; every captured PNG is public forever once the README merges). Light and dark variants below; the page selects per `prefers-color-scheme`.

### Control detail: framework crosswalks

One control, many framework satisfactions. STRM-typed edges through a single SCF anchor.

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

The leadership-facing report. Templated narrative per section, per-section approval, frozen on publish.

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

Detailed local dev setup, prerequisites, and the full `just` recipe surface live in [`CONTRIBUTING.md`](./CONTRIBUTING.md). For a production self-host walkthrough (docker-compose, service roles, backups), see [`docs/SELF_HOSTING.md`](./docs/SELF_HOSTING.md).

### Your first sign-in (self-host)

The platform mints a one-time bootstrap admin token at startup. The `/login` page detects fresh-install state and shows three orthogonal ways to find it:

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

- **User guide (docs site):** [`docs-site/docs/`](./docs-site/docs/) — install, configuration, first audit, framework setup, connector authoring, OAuth/OIDC setup, board reporting, metrics, backups, and upgrades.
- **Design canvas:** [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) — vision, primitives, the control graph, evidence engine, scope, risk, metrics, audit workflow, tech stack, roadmap, open questions.
- **Constitutional principles:** [`CLAUDE.md`](./CLAUDE.md) — the architecture invariants, anti-patterns we reject, the AI-assist boundary, and licensing constraints.
- **Self-hosting guide:** [`docs/SELF_HOSTING.md`](./docs/SELF_HOSTING.md)
- **Architecture decisions (ADRs):** [`docs/adr/`](./docs/adr/)
- **Release & verification:** [`docs/releases.md`](./docs/releases.md) · [`docs/RELEASE_READINESS.md`](./docs/RELEASE_READINESS.md)
- **Slice backlog (how the project is built):** [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) · live merge trail in [`docs/issues/_STATUS.md`](./docs/issues/_STATUS.md)

---

## Authentication

security-atlas authenticates request traffic via an internal **OAuth 2.0 Authorization Server** that issues short-lived **JWT access tokens** carrying the tenant in-claim (RFC 9068 JWT Profile + RFC 8693 Token Exchange for tenant switching). This is the live auth mechanism today — the JWKS endpoint (`/.well-known/jwks.json`), OIDC discovery (`/.well-known/openid-configuration`), and the grant flows (authorization-code + PKCE for the browser, device-code for the CLI, client-credentials for services) are all shipped.

The Authorization Server layers on an OIDC relying party: the relying party authenticates the human against your external IdP (Okta, Entra ID, Google, etc.); the AS layer mints the atlas JWT. Two roles, one server process — security-atlas is not itself an IdP. The architectural commitment is captured in [ADR-0003](./docs/adr/0003-oauth-authorization-server.md); operator setup lives in the [OAuth grants](./docs-site/docs/oauth-grants.md) and [OIDC setup](./docs-site/docs/oidc-setup.md) guides.

---

## Security

security-atlas treats security as a first-class concern. The project ships with:

- **Reporting channel:** see [`SECURITY.md`](./SECURITY.md) for the private vulnerability disclosure process and response timelines. Please **do not** open a public issue for a security finding.
- **Pipeline hardening:** CodeQL static analysis (Go + JS/TS), GitGuardian secret scanning, and Dependabot version-bump alerts run on every PR.
- **Dependency vulnerability scanning:** [`Go · govulncheck`](./.github/workflows/ci.yml) (Go call-graph-aware CVE detection), [`Frontend · npm audit`](./.github/workflows/ci.yml) (runtime-shipped JS deps in `web/`), and [`Container · Trivy scan`](./.github/workflows/ci.yml) (OS-package CVEs in the built atlas image). All three fail on HIGH/CRITICAL; reports upload as workflow artifacts. These complement Dependabot: Dependabot opens PRs when an upgrade is available; these flag known CVEs on the current version when no upgrade exists yet.
- **Hardening headers:** HSTS / CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy applied on every response. See [`internal/api/securityheaders/`](./internal/api/securityheaders/).
- **Audit reports:** maintainer-led security audits live under [`docs/audits/`](./docs/audits/). The first-pass audit is [`2026-Q2-security-audit.md`](./docs/audits/2026-Q2-security-audit.md).
- **Audit cadence:** quarterly scheduled review, plus an additional audit after any major change to authentication, authorization, middleware, or evidence-ingestion code paths. First-pass audits are not a substitute for third-party penetration testing; they catch the high-yield patterns automated scanners miss.
- **Remediation tracking:** actionable findings from each audit are filed as discrete remediation slices under [`docs/issues/`](./docs/issues/) and tracked through the normal review/merge process.

---

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for dev setup, the Conventional Commits convention, and the DCO sign-off requirement.

By participating in this project you agree to abide by the [`Code of Conduct`](./CODE_OF_CONDUCT.md).

Security issues: please **do not** open a public issue. See [`SECURITY.md`](./SECURITY.md) for the private disclosure channel.

---

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).

`SPDX-License-Identifier: Apache-2.0`
