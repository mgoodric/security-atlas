# security-atlas

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![CI](https://github.com/mgoodric/security-atlas/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mgoodric/security-atlas/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/mgoodric/security-atlas/graph/badge.svg?token=SI2ZW30LS1)](https://codecov.io/gh/mgoodric/security-atlas)
[![Latest release](https://img.shields.io/github/v/release/mgoodric/security-atlas?sort=semver)](https://github.com/mgoodric/security-atlas/releases/latest)

Open-source, self-hostable GRC platform — a control-graph and evidence-pipeline that lets a single security program operate against many frameworks (SOC 2, ISO 27001, NIST CSF, PCI DSS, HIPAA, GDPR) from one source of truth.

The spine is the [Secure Controls Framework](https://securecontrolsframework.com/) (~1,400 controls crosswalked to 200+ frameworks via NIST IR 8477 STRM). The wire format is NIST OSCAL. The target user is the solo security leader at a 50–150-person security-product startup who runs the entire program — risk register, board reporting, SOC 2, vendor reviews, policies, exceptions — alone.

**Early implementation.** 32 of 58 v1 slices are merged on `main`. See [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) for the design canvas and [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md) for the slice backlog.

---

## Why security-atlas

Existing GRC tools optimize for the first-SOC-2-in-90-days SMB sale. They model controls per-framework, store evidence in a vendor cloud, and the Year-2 renewal cliff is well-documented.

security-atlas inverts the model:

- **One control, N framework satisfactions.** The Unified Control Framework is a graph with STRM-typed edges through SCF anchors. Never duplicate controls per framework.
- **Append-only evidence ledger.** Ingestion and evaluation are separated stages; evaluation never writes to source-of-truth evidence. Point-in-time replay is always possible.
- **Self-hostable from day one.** Single mid-size VM runs the whole platform. NATS JetStream (single binary) · Postgres · S3-compatible artifact store.
- **OSCAL-native.** Ingest catalogs / profiles / component-definitions; export SSP / AP / AR / POA&M.

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

---

## Quickstart — first evidence in 5 minutes

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

---

## Documentation

- **Design canvas** — [`Plans/ARCHITECTURE_CANVAS.md`](./Plans/ARCHITECTURE_CANVAS.md) (vision, primitives, UCF, evidence engine, scope, risk, metrics, audit workflow, tech stack, roadmap, open questions)
- **Constitutional principles** — [`CLAUDE.md`](./CLAUDE.md) (10 architecture invariants, anti-patterns we reject, AI-assist boundary, licensing constraints)
- **Self-hosting guide** — [`docs/SELF_HOSTING.md`](./docs/SELF_HOSTING.md)
- **ADRs** — [`docs/adr/`](./docs/adr/)
- **Release readiness** — [`docs/RELEASE_READINESS.md`](./docs/RELEASE_READINESS.md)
- **Slice backlog** — [`docs/issues/_INDEX.md`](./docs/issues/_INDEX.md)

---

## Contributing

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for dev setup, the Conventional Commits convention, and the DCO sign-off requirement.

By participating in this project you agree to abide by the [`Code of Conduct`](./CODE_OF_CONDUCT.md).

Security issues: please **do not** open a public issue. See [`SECURITY.md`](./SECURITY.md) for the private disclosure channel.

---

## License

Apache License 2.0. See [`LICENSE`](./LICENSE).

`SPDX-License-Identifier: Apache-2.0`
