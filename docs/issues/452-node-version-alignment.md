# 452 — Node version alignment: @types/node + CI + runtime + engines + container base

**Cluster:** Infra
**Estimate:** M (1-2d)
**Type:** JUDGMENT

**Status:** `ready`

> **MAINTAINER DECISION (2026-06-12): target Node 22 (Active LTS).** This resolves the
> P0-452-6 JUDGMENT (the LTS-line choice). Align all four surfaces to **Node 22**:
> `web/package.json` `@types/node` `^25` → `^22`; `engines.node` `>=20` → `>=22`; the
> CI `node-version` pin (×8 in `.github/workflows/ci.yml`) `"20"` → `"22"`; and any
> container base image → a pinned `node:22-alpine` tag. Verify the standalone Next.js
> build + the self-host bundle still run on 22. **Build-then-hold:** because P0-452-6
> exists, the loop BUILDS this targeting 22 but leaves the PR OPEN for a one-click
> maintainer confirm (the LTS judgment is pre-made here, so the review is a formality).

## Narrative

The project's Node version is specified in four places that have **drifted
apart**:

| Surface                                                   | Current          | Wanted-by                                                          |
| --------------------------------------------------------- | ---------------- | ------------------------------------------------------------------ |
| `web/package.json` `devDependencies.@types/node`          | `^25`            | dependabot **#637** wants the type defs realigned (filed at 20→25) |
| `web/package.json` `engines.node`                         | `>=20`           | — (floor, unpinned ceiling)                                        |
| CI runner `node-version` (×8 jobs in `ci.yml`)            | `"20"`           | —                                                                  |
| Container base (`deploy/docker/web.Dockerfile`, 3 stages) | `node:22-alpine` | dependabot **#153** wants `node:26-alpine`                         |

So today the type definitions advertise one Node, the CI runner builds on a
second, and the Docker runtime ships a third — with dependabot pulling a fourth
and fifth target. A `@types/node` major can introduce type signatures for APIs
that the _runtime_ Node doesn't have, and a CI runner older than the container
base means "green on CI" doesn't prove "runs in prod."

This slice **decides the target Node LTS** (the JUDGMENT call — 22 or 24; both
are active LTS, 26 is not yet LTS) and then aligns all four surfaces to it in
one PR: `@types/node`, the CI `node-version` pin (×8), `engines.node`, and the
Dockerfile base. It **supersedes dependabot PRs #637 and #153**, which each
move one surface in isolation and would deepen the drift.

**Scope discipline.** Version alignment only. No Next.js / React / build-config
changes, no new CI jobs, no Dockerfile restructuring beyond the `FROM` base
tags. If a chosen Node forces a real code change (e.g. a removed Node API), that
is surfaced — not silently patched into this slice.

## Threat model

STRIDE pass — the security-relevant axis is **running an unsupported/EOL Node in
production** and **build-vs-runtime version skew** hiding a runtime fault.

**S — Spoofing / R — Repudiation / E — Elevation of privilege**

- _Threat:_ Not directly applicable — this is a runtime/toolchain version, not
  an auth or identity surface.
- _Mitigation:_ n/a; confirm the change touches no auth/RLS/identity code.

**T — Tampering**

- _Threat:_ A Docker base-image major bump (`node:22-alpine` → newer) changes
  the underlying Alpine + OpenSSL + libc, which can alter TLS behavior or pull
  in a base with a different CVE surface.
- _Mitigation:_ Pin to a specific Node LTS Alpine tag; verify the standalone
  Next.js server still builds + runs in the runtime stage (the Dockerfile is a
  3-stage distroless-style build — deps/builder/runtime).
- _Anti-criterion:_ P0-452-3.

**I — Information disclosure**

- _Threat:_ None specific to the version bump; the web runtime is a BFF/proxy
  layer (no direct tenant DB access — that is the Go platform).
- _Mitigation:_ Confirm no new diagnostic/verbose-error default ships with the
  newer Node runtime in the container.

**D — Denial of service / availability (the real risk: EOL Node in prod)**

- _Threat 1 — EOL Node._ Picking a Node line that is past or near end-of-life
  (or an odd/non-LTS line like 23 or 25) means shipping a runtime that stops
  receiving security patches. `node:26` is not yet an LTS line.
- _Mitigation:_ The JUDGMENT decision picks an **active LTS** (22 "Jod" or 24)
  with a documented EOL date and a refresh-cadence note. The decisions log
  records the LTS calendar reasoning.
- _Anti-criterion:_ P0-452-1.
- _Threat 2 — build-vs-runtime skew._ CI builds/tests on Node 20 while prod
  runs Node 22 (today). A behavior that works on 20 but faults on the prod Node
  ships green. After alignment, the same Node version gates CI and runtime.
- _Mitigation:_ AC aligns the CI `node-version` pin (×8) to the same LTS as the
  Dockerfile base and `engines`. The `engines.node` floor is raised to match.
- _Anti-criterion:_ P0-452-2.

## Acceptance criteria

- [ ] **AC-1.** A target Node LTS is **decided and documented** (22 or 24) in
      the decisions log, with EOL date + the reason an LTS (not 25/26) was
      chosen.
- [ ] **AC-2.** `web/package.json` `@types/node` aligned to the matching major
      for the chosen Node LTS.
- [ ] **AC-3.** `web/package.json` `engines.node` raised to the chosen LTS
      (e.g. `>=22` or `>=24`), consistent with the runtime.
- [ ] **AC-4.** All 8 `node-version: "20"` pins in `.github/workflows/ci.yml`
      updated to the chosen LTS major.
- [ ] **AC-5.** `deploy/docker/web.Dockerfile` all three stages
      (`deps`/`builder`/`runtime`) updated to `node:<LTS>-alpine`; the
      top-of-file comment (line 8) updated to match.
- [ ] **AC-6.** `npm install`, `npm run build -w web`, and
      `npm run typecheck -w web` all succeed under the new `@types/node` + Node.
- [ ] **AC-7.** The `web` container image builds and the standalone server
      starts in the runtime stage (verified locally or in CI).
- [ ] **AC-8.** Full `Frontend · vitest` + `Frontend · Playwright e2e` CI jobs
      green on the new CI Node.
- [ ] **AC-9.** If the chosen Node forces a code change (removed/changed Node
      API), it is surfaced in the PR body and recorded — NOT silently bundled as
      scope creep.
- [ ] **AC-10.** `pre-commit run --all-files` passes. PR body notes "Supersedes
      #637 and #153".
- [ ] **AC-11.** JUDGMENT decisions log at
      `docs/audit-log/452-node-version-alignment-decisions.md` records the LTS
      choice, the four-surface alignment, and a refresh-cadence note, with
      per-decision confidence + detection-tier fields.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md four surfaces).** Aligning the CI Node to the
  runtime Node closes the build-vs-runtime skew that the four-surface gate
  otherwise cannot catch.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "Frontend — Next.js 16 App Router";
  "Container — Distroless base images; multi-stage builds".
- CLAUDE.md tech-stack table (frontend + container rows).

## Dependencies

- Supersedes **dependabot #637** (@types/node 20 → 25) and **#153**
  (container base node 22 → 26-alpine).
- None blocking (this is a self-contained alignment).

## Anti-criteria (P0 — block merge)

- **P0-452-1.** Does NOT ship a non-LTS / EOL Node line (no 23/25; 26 only if it
  has reached LTS by build time — otherwise pick 22 or 24).
- **P0-452-2.** Does NOT leave the CI `node-version` pin out of sync with the
  Dockerfile base + `engines` — all four surfaces move together.
- **P0-452-3.** Does NOT restructure the 3-stage Dockerfile beyond the `FROM`
  base tags; the build topology stays as-is.
- **P0-452-4.** Does NOT bundle Next.js/React/build-config upgrades — version
  alignment only.
- **P0-452-5.** Does NOT silently patch a forced code change as part of the
  bump; surface it (AC-9).
- **P0-452-6.** Does NOT auto-merge — carries a JUDGMENT LTS decision.

## Skill mix (3-5)

- `dependency-auditor` — Node LTS calendar + `@types/node` major mapping.
- `ci-cd-pipeline-builder` — the 8 CI pin updates.
- `migration-architect` — the four-surface coordinated bump.
- `simplify` — pre-PR pass.

## Notes for the implementing agent

- The 8 `node-version: "20"` occurrences are all in
  `.github/workflows/ci.yml` (lines ~684, 763, 1029, 1084, 1204, 1436, 1640,
  2249 as of filing — re-grep, do not trust the line numbers).
- The Dockerfile base is `node:22-alpine` in three stages
  (`deploy/docker/web.Dockerfile` lines 19/30/42) plus a comment at line 8.
- The JUDGMENT call: Node 22 ("Jod") and Node 24 are both active LTS at filing.
  Node 22 is the conservative choice (matches the current container base, lower
  churn); Node 24 is the forward choice (longer remaining LTS window). Either is
  defensible — pick one, record the EOL math + a refresh-cadence note (this will
  drift again; document the cadence like the board-model-refresh note in
  CLAUDE.md).
- Close dependabot #637 and #153 in favor of this PR.
