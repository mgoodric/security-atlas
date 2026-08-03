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

**One open-source, self-hosted platform for your whole security program — compliance and audit, risk, third-party, policy, evidence, and board reporting — instead of a dozen spreadsheets and single-purpose tools.**

---

## What is security-atlas?

security-atlas is a **GRC platform** — Governance, Risk, and Compliance — that a security team runs as the single system of record for its program. It replaces the usual sprawl: evidence in screenshots and spreadsheets, a risk register in Excel, a separate vendor-review tracker, policy PDFs on a shared drive, and a SaaS compliance tool that holds your data and gets expensive at renewal.

You **host it yourself**, so your evidence, risk data, and audit trail never leave your control. It is Apache-2.0 open source — no paid edition, no features locked behind a license tier.

**The core idea: describe a control once, and satisfy every framework it maps to.** Most tools make you re-create the same control separately for SOC 2, then ISO 27001, then PCI. security-atlas keeps one set of controls and _crosswalks_ each to the frameworks it satisfies — built on the [Secure Controls Framework](https://securecontrolsframework.com/) (SCF), an open catalog of ~1,400 controls already mapped to 200+ frameworks. Adding a framework becomes mostly mapping, not re-work.

## What it covers

security-atlas spans the disciplines a security program runs day to day — not just audit prep:

| Discipline                                   | What the platform does                                                                                                                                                                                                                                                                                                          |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Compliance & audit management**            | One control catalog crosswalked to SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, and GDPR. A dedicated auditor workspace with evidence sampling, control walkthroughs, and reviewer comments. Export to [OSCAL](https://pages.nist.gov/OSCAL/) — the NIST open standard — as SSP, Assessment Plan, Assessment Results, and POA&M. |
| **Evidence & continuous control monitoring** | Read-only connectors pull evidence from the systems you already run (AWS, GitHub, Okta, GCP, Azure, Kubernetes, osquery, and more). Manual evidence — a signed policy, a meeting note — is first-class. Per-control freshness and drift tracking show when a control quietly stops passing.                                     |
| **Risk management**                          | A risk register with inherent-vs-residual scoring, a treatment lifecycle (mitigate / transfer / accept / avoid), and links from each risk to the controls that address it — organized as a tier that rolls up your org hierarchy (see below).                                                                                   |
| **Third-party / vendor risk**                | A vendor register, recurring vendor reviews with a review-due burndown, and intake for the security questionnaires your own customers send you.                                                                                                                                                                                 |
| **Policy & exception management**            | A policy library with acknowledgment tracking, an exception workflow (request → approve → active → auto-expire, with compensating controls and a hard expiry cap), and **Action Plans** for tracking remediation commitments to closure.                                                                                        |
| **Trust & questionnaires**                   | Answer inbound security questionnaires from one place, with AI-assist that drafts answers grounded in your actual evidence and policies (every suggestion cited; see the AI boundary below).                                                                                                                                    |
| **Metrics & board reporting**                | KPI and metric dashboards for program health, plus first-class **board pack** generation — the quarterly security update for leadership, built from real program data rather than hand-assembled slides.                                                                                                                        |
| **Identity, access & multi-tenancy**         | Sign-in through your existing IdP (Okta, Entra ID, Google) over OIDC, role-based and attribute-based access control, and multi-tenant isolation enforced in the database itself — not just in application code.                                                                                                                 |

## What makes it different

A few things security-atlas does that most GRC tools don't:

- **One control, many frameworks — for real.** Controls are never duplicated per framework. Each maps to the requirements it satisfies through a shared SCF reference, so a single control can answer SOC 2 CC-series, an ISO 27001 Annex A control, and a PCI requirement at once. Add a framework and you are mostly mapping, not rebuilding.

- **A tiered, methodology-aware risk register.** Risks roll up through your **org hierarchy** (a division's risk posture aggregates from its teams), carry both **inherent and residual** scores, and enforce a treatment discipline — marking a risk _mitigated_ requires linking the controls that mitigate it; marking it _accepted_ requires an explicit accept-until date. Scoring ships with NIST SP 800-30 and a qualitative 5×5 today, with FAIR, CIS RAM, and ISO 27005 modeled for future support. Most tools give you one flat 5×5 grid and a spreadsheet export.

- **An append-only evidence ledger you can replay.** Evidence is never overwritten or deleted — it is an immutable ledger. Ingestion (recording evidence) and evaluation (scoring controls against it) are separate stages, so a bug in scoring can never corrupt the underlying record, and you can reconstruct exactly what was true on any past date.

- **Frozen audit periods.** When you freeze an audit period, the auditor samples from a stable snapshot as of the freeze date, while your live program keeps moving underneath. No more "the evidence changed while the auditor was looking at it."

- **Framework-scoped applicability.** A PCI cardholder-data environment, a HIPAA ePHI boundary, and a SOC 2 system boundary are not the same scope. security-atlas computes whether a control applies as the _intersection_ of the control's applicability and the framework's scope — rather than pretending your whole estate is in scope for everything.

- **Remediation modeled precisely.** An **Exception** (accepted non-compliance with compensating controls and a fixed expiry) is a distinct object from an **Action Plan** (a forward-looking remediation with milestones, exported as an OSCAL POA&M). Most tools collapse both into one "issue," which is exactly what pushes teams back to a spreadsheet.

- **Private, auditable AI-assist.** AI features run on a **local model by default** — nothing leaves your deployment. Every AI suggestion (a questionnaire answer, a board-narrative section, a gap explanation) carries **mandatory citations** to the specific evidence or policy behind it, numeric claims are checked against the source data, and **nothing is published without one-click human approval**. No hallucinated audit answers.

## How it compares

Most teams choose a compliance tool from one of two categories. Here is where security-atlas sits — including the trade-offs, so you can judge the fit honestly:

| Dimension                     | SaaS compliance tools (e.g. Vanta, Drata)                         | Enterprise GRC suites (e.g. OneTrust, Archer) | security-atlas                                                         |
| ----------------------------- | ----------------------------------------------------------------- | --------------------------------------------- | ---------------------------------------------------------------------- |
| **Where it runs**             | Vendor-hosted SaaS                                                | Vendor-hosted or licensed enterprise install  | You self-host — a single VM up to Kubernetes                           |
| **Where your evidence lives** | The vendor's cloud                                                | The vendor's cloud or your enterprise estate  | Your infrastructure only                                               |
| **Cost model**                | Annual subscription, commonly scaling with frameworks + headcount | Enterprise licensing                          | Apache-2.0 open source — no license fee, no paid tier                  |
| **Adding a framework**        | More subscription and per-framework setup                         | A configuration project                       | Mostly crosswalk mapping — one control satisfies many frameworks (SCF) |
| **Data portability**          | Export varies by vendor                                           | Varies by vendor                              | OSCAL in and out (the NIST open standard)                              |
| **Breadth**                   | Focused on audit / compliance automation                          | Broad GRC, often heavy to operate             | Audit, risk, vendor, policy, evidence, and board reporting in one app  |

security-atlas is the right fit when you want to **own your data and your control graph**, run a broad program from one place, and avoid a renewal cliff. The honest trade-off: it is **not a managed service** — there is no vendor SOC to call and you run your own upgrades and backups. If you want zero-ops SaaS and don't mind vendor-hosted evidence, a hosted tool is the better choice.

## Who it's for

Built first for the **solo security leader at a 50–150-person company** who runs the entire program alone — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — and whose own customers scrutinize how they handle security. The goal is concrete: run your next SOC 2 audit out of security-atlas and build your next board pack from it, without reaching for a spreadsheet to fill a gap. It scales up from there to a small security team.

## Project status

security-atlas is a **community open-source project** under the
[Apache 2.0 license](./LICENSE). v1 is complete and operator-grade; active
v2 development continues (deeper PCI/HIPAA and privacy workflows are on the
roadmap). There is **no hosted SaaS** from the project owners and **no paid
edition** with locked-away features — you run the whole thing yourself.

For what shipped and when, see the [latest release](https://github.com/mgoodric/security-atlas/releases/latest)
and [`CHANGELOG.md`](./CHANGELOG.md). The governance model, funding posture, and
succession plan live in [`GOVERNANCE.md`](./GOVERNANCE.md). The full design
rationale — architecture invariants, the anti-patterns the project rejects, and
the AI-assist boundary — lives in the [architecture canvas](./Plans/ARCHITECTURE_CANVAS.md)
and [`CLAUDE.md`](./CLAUDE.md).

---

## Screenshots

Captured from the running app against the hermetic demo fixtures (`fixtures/readme-demo/`). Run `ATLAS_DEMO_SEED=1 just refresh-screenshots` to regenerate them. The capture pipeline refuses to run unless `ATLAS_DEMO_SEED=1` is set and the upstream HTTP target is loopback / RFC1918 private (an information-disclosure safety gate; every captured PNG is public forever once the README merges). Light and dark variants below; the page selects per `prefers-color-scheme`.

### Control detail: framework crosswalks

One control mapped to every framework requirement it satisfies — SOC 2, ISO 27001, and more — through a single shared SCF reference, instead of a duplicated control per framework.

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
export DATABASE_URL_APP="postgres://postgres:postgres@localhost:5432/security_atlas?sslmode=disable"
go build -o ./bin/ ./cmd/atlas ./cmd/atlas-cli
./bin/atlas &

# 2. push a hello-world evidence record (the CLI speaks gRPC on :50051)
export SECURITY_ATLAS_ENDPOINT=localhost:50051
export SECURITY_ATLAS_TOKEN="<a credential from \`atlas-cli credentials issue\`>"
./bin/atlas-cli evidence push --insecure \
  --kind hello.world.v1 \
  --control CTL-001 \
  --scope '{"environment":"dev"}' \
  --observed-at "$(date -u +%FT%TZ)" \
  --result pass \
  --payload '{"message":"first record"}' \
  --idempotency-key quickstart-1 \
  --actor-id quickstart

# 3. read it back over the HTTP API
curl -fsS http://localhost:8080/v1/evidence \
  -H "Authorization: Bearer $SECURITY_ATLAS_TOKEN"
```

`just build-go` runs `go build ./...` as a compile check — it does not write
binaries. Use the explicit `go build -o ./bin/` above (or `go run ./cmd/atlas-cli`)
when you want something to execute.

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
- **MCP server (assistant access):** [`docs-site/docs/mcp.md`](./docs-site/docs/mcp.md) — query and update your program from Claude Desktop / Claude Code.
- **Architecture decisions (ADRs):** [`docs/adr/`](./docs/adr/)
- **Release & verification:** [`docs/releases.md`](./docs/releases.md) · [`docs/RELEASE_READINESS.md`](./docs/RELEASE_READINESS.md)
- **Slice backlog (how the project is built):** [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) · live merge trail in [`docs/issues/_STATUS.md`](./docs/issues/_STATUS.md)

---

## Authentication

security-atlas authenticates request traffic via an internal **OAuth 2.0 Authorization Server** that issues short-lived **JWT access tokens** carrying the tenant in-claim (RFC 9068 JWT Profile + RFC 8693 Token Exchange for tenant switching). This is the live auth mechanism today — the JWKS endpoint (`/.well-known/jwks.json`), OIDC discovery (`/.well-known/openid-configuration`), and the grant flows (authorization-code + PKCE for the browser, device-code for the CLI, client-credentials for services) are all shipped.

The Authorization Server layers on an OIDC relying party: the relying party authenticates the human against your external IdP (Okta, Entra ID, Google, etc.); the AS layer mints the atlas JWT. Two roles, one server process — security-atlas is not itself an IdP. The architectural commitment is captured in [ADR-0003](./docs/adr/0003-oauth-authorization-server.md); operator setup lives in the [OAuth grants](./docs-site/docs/oauth-grants.md) and [OIDC setup](./docs-site/docs/oidc-setup.md) guides.

---

## Assistant access (MCP)

security-atlas ships an **MCP (Model Context Protocol) server** — `atlas-mcp` — so your security team can query and update the program from an AI assistant (Claude Desktop, Claude Code, or any MCP client) instead of clicking through the UI. Ask _"what are my top risks in treatment?"_ or _"which controls have stale evidence?"_ and the assistant answers from your live data.

- **Read tools** cover controls, risks, evidence, audit periods, policies, vendors, exceptions, and action plans — scoped to your tenant, with the same row-level isolation as the rest of the platform. A few ready-made [operator skills](./skills/) wrap them into one-command workflows (risk briefing, evidence-freshness sweep, audit-readiness snapshot).
- **Write tools** (create a risk, update a control's state, push evidence, change a risk's treatment) **never mutate your data unattended.** Each files a _proposal_ that a human approver must confirm — in the assistant via `confirm_write`, or with the Approve button in the web UI — and that boundary is enforced at the database layer. An AI cannot publish an audit-binding change on its own.
- The assistant authenticates with a normal atlas bearer token and sees only what that credential is allowed to see.

**Status: experimental** — the tool surface is in soak and may change; pin your MCP client to a specific `atlas-mcp` version. Full setup (client config for Claude Desktop / Claude Code, token handling, the complete tool list, and the approval flow) is in the **[MCP server guide](./docs-site/docs/mcp.md)** and [`cmd/atlas-mcp/README.md`](./cmd/atlas-mcp/README.md).

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
