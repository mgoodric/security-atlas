# 351 — e2e critical multi-tenant flow coverage matrix

**Slice:** 351
**Date:** 2026-05-29
**Author:** continuous-batch 164 engineer
**Closes:** slice 333 Q-9 (critical multi-tenant flows unrepresented or
skipped in the merged Playwright gate)
**Cross-refs:** slice 334 P-4 (the `test.skip` quarantine inventory)

---

## Information-disclosure handling (slice doc threat model, I)

This matrix names which multi-tenant flows are tested vs untested — a
roadmap to an untested-flow attack surface. Per the slice doc threat
model and slice 333's discipline: this document lives in the repo and
inherits the repo's access-control surface. Do **not** publish it to a
public mirror or share it externally ahead of the flows being covered.

---

## Method

Critical flows were enumerated against the v1 binary success test
(`CLAUDE.md`: "does the solo security leader run their next SOC 2 audit
out of security-atlas, generate the next board pack from it, and not
reach for Vanta or a Google Sheet?"). Each flow was graded:

- **Spec status** — the actual `web/e2e/*.spec.ts` state on the branch
  base (`grep`-verified, not the spot-check in the slice doc which had
  two stale rows — see Reconciliation below).
- **Priority** — P0 (multi-tenant security-critical, or a v1 binary
  flow with zero coverage), P1 (v1 binary flow, partial coverage), P2
  (sound coverage; sharpening only).

The slice scope is **P0 spec-fill + quarantine triage**. P1/P2 gaps are
named here and deferred to follow-up slices per anti-criteria P0-1.

---

## Reconciliation with slice 333 Q-9 / slice doc spot-check

The slice doc table listed "8 `test.skip` quarantines" and named
`questionnaires` + `risks-create` among them. Verified on the branch
base, the real picture differs:

| Slice-doc claim                      | Verified reality                                                                                               |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| `questionnaires` is `test.skip`      | **FALSE** — `questionnaires.spec.ts` is a LIVE mocked spec (slice 263). The line-29 grep hit was a comment.    |
| `control-detail-tabs` is quarantined | **FALSE** — un-quarantined by slice 276 (mock-schema-conformance fix). No active `test.skip`.                  |
| "8 quarantines"                      | **6 active `test.skip()` guards** across 5 files (`bff-cookie` has 2 — one per assertion inside one describe). |

The 6 active guards, by file:

| File                                   | Guard expression                                                                                                     |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `auth-open-redirect.spec.ts`           | `test.skip(!HAS_BEARER, …)`                                                                                          |
| `bff-cookie-production-build.spec.ts`  | `test.skip(!RUN_AGAINST_PROD_BUILD,…)` (×2 — one inside each `authed(...)` is gated by the same describe-level skip) |
| `logo-render-production-build.spec.ts` | `test.skip(!RUN_AGAINST_PROD_BUILD,…)`                                                                               |
| `risks-create.spec.ts`                 | `test.skip(!PLAYWRIGHT_RUN_QUARANTINED,…)`                                                                           |
| `risks-create-control-link.spec.ts`    | `test.skip(!PLAYWRIGHT_RUN_QUARANTINED,…)`                                                                           |
| `audits-create.spec.ts`                | `test.skip(!PLAYWRIGHT_RUN_QUARANTINED,…)`                                                                           |

(`audits-create` was not in the slice-doc spot-check but is the same
legacy-quarantine pattern as `risks-create`, so it is triaged here too.)

---

## Coverage matrix

| #   | Critical flow                                     | v1-binary tie         | Spec (branch base)                                            | Status before 351        | Priority | 351 action                                             |
| --- | ------------------------------------------------- | --------------------- | ------------------------------------------------------------- | ------------------------ | -------- | ------------------------------------------------------ |
| 1   | **tenant-switch** (multi-tenant user)             | multi-tenant security | none                                                          | **NO SPEC**              | **P0**   | **AC-2 — authored (mocked)**                           |
| 2   | tenant-switch cross-tenant leak (RLS at the edge) | multi-tenant security | none                                                          | **NO SPEC**              | **P0**   | AC-2 partial (mocked) + spillover for real-RLS variant |
| 3   | **evidence push CLI → ingest → BFF → UI**         | v1 binary (SOC 2)     | `evidence-list.spec.ts` (UI side only, commented)             | partial                  | **P0**   | **AC-3 — authored (mocked e2e)**                       |
| 4   | super-admin operations                            | platform admin        | `super-admins.spec.ts` (LIVE)                                 | covered                  | P2       | none (verified live)                                   |
| 5   | first-time-login + admin-bootstrap                | onboarding            | `first-time-login.spec.ts` + `admin-bootstrap.spec.ts` (LIVE) | covered                  | P2       | none                                                   |
| 6   | auth open-redirect defense (security regression)  | auth security         | `auth-open-redirect.spec.ts`                                  | `test.skip` (vestigial)  | **P0**   | **un-skip (a)** — guard was stale                      |
| 7   | risk create                                       | risk register         | `risks-create.spec.ts`                                        | `test.skip` (legacy 082) | P1       | **un-skip (a)** — rewrite as mocked                    |
| 8   | risk create + control link                        | risk register         | `risks-create-control-link.spec.ts`                           | `test.skip` (legacy 082) | P1       | **un-skip (a)** — rewrite as mocked                    |
| 9   | audit-period create                               | v1 binary (SOC 2)     | `audits-create.spec.ts`                                       | `test.skip` (legacy 082) | P1       | **un-skip (a)** — rewrite as mocked                    |
| 10  | bff-cookie production-build standalone            | deploy regression     | `bff-cookie-production-build.spec.ts`                         | `test.skip` (real gap)   | P1       | **re-quarantine (b)** + spillover                      |
| 11  | logo/static-asset production-build standalone     | deploy regression     | `logo-render-production-build.spec.ts`                        | `test.skip` (real gap)   | P1       | **re-quarantine (b)** + spillover                      |
| 12  | board-pack export end-to-end                      | v1 binary (board)     | `board-pack-detail.spec.ts` (UI side only)                    | partial                  | P1       | named; deferred (P0-1) — spillover                     |
| 13  | questionnaires authoring (CAIQ/SIG)               | vendor diligence      | `questionnaires.spec.ts` (LIVE mocked)                        | covered                  | P2       | none (slice-doc row was stale)                         |
| 14  | dashboard / control-detail / audit-workspace      | daily driver          | dashboard/control/audit specs (LIVE)                          | covered                  | P2       | none                                                   |

---

## Triage outcomes for the 6 active quarantines (AC-4)

Each quarantine is classified (a) un-skip with a fix, (b) un-skip with a
justification + open spillover slice, or (c) delete if obsolete.

| Spec                                   | Disposition                       | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| -------------------------------------- | --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `auth-open-redirect.spec.ts`           | **(a) un-skip**                   | The spec body was _fixed_ by slice 161 (racy `waitForURL` → settled-pathname wait; decisions log `docs/audit-log/161-...`). The only remaining guard is `test.skip(!HAS_BEARER)`, evaluated at module-load. Slice 201's `globalSetup` now always mints a JWT and writes `TEST_BEARER` into `process.env` BEFORE workers import specs, so `HAS_BEARER` is true in CI. The guard is **vestigial** — a relic of the pre-201 era when the harness provided no bearer. Removed. The spec already drives the real login form against the live stack (not mocked), so it stays an honest end-to-end security regression gate.                                                                                                                  |
| `bff-cookie-production-build.spec.ts`  | **(b) re-quarantine + spillover** | The guard is `!ATLAS_PROD_BUILD`. The spec requires a Next.js **production-build standalone** server (`node .next/standalone/web/server.js`), but CI's Playwright job points `baseURL` at the `npm start` dev server. There is **no CI job that brings up the standalone server** (`grep ATLAS_PROD_BUILD .github/` is empty; `web/package.json` has a `build:standalone` script but nothing invokes it in CI). This is a **real seed-harness gap**, not green-washable: forcing it to run against the dev server would assert nothing about the standalone-only regression it guards (slice 146's `NODE_ENV`-coupled cookie bug). Re-quarantined with an updated justification comment citing the spillover. **Spillover: slice 387.** |
| `logo-render-production-build.spec.ts` | **(b) re-quarantine + spillover** | Same root cause as `bff-cookie` — `!ATLAS_PROD_BUILD`, needs the standalone server (slice 153's `output: "standalone"` does not copy `web/public/`). Same harness gap; same spillover. Re-quarantined with the updated justification. **Spillover: slice 387** (shared — one standalone-server CI harness unblocks both specs).                                                                                                                                                                                                                                                                                                                                                                                                         |
| `risks-create.spec.ts`                 | **(a) un-skip**                   | The legacy `!PLAYWRIGHT_RUN_QUARANTINED` guard + commented bodies date to the slice-082 era ("seed harness not landed yet"). The harness _did_ land (slice 082 + 201) and the page (`/risks/new`, `risk-form.tsx`) ships all the asserted testids. No underlying product bug. Rewritten as a **mocked spec** following the `questionnaires.spec.ts` `route.fulfill` precedent (anti-criterion P0-4 mandates the established mock pattern for new specs). The commented assertions were also **stale** — empty-title is now gated **client-side** (`validateRiskForm`), so the spec asserts the _actual_ inline `risks-create-title-error` behavior, not the obsolete "server 4xx bounce".                                               |
| `risks-create-control-link.spec.ts`    | **(a) un-skip**                   | Same legacy pattern. `ControlMultiSelect` + the mitigate-requires-link validation ship. Rewritten as a mocked spec.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `audits-create.spec.ts`                | **(a) un-skip**                   | Same legacy pattern. `/audits/new` + `audit-period-form.tsx` ship all asserted testids. Rewritten as a mocked spec.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |

**No (c) deletions.** Every quarantine had a nameable reason — none was
orphaned. The two `(b)` re-quarantines have a documented blocking issue
(slice 387). The four `(a)` un-skips are now live gates.

---

## P1 / P2 gaps named but deferred (anti-criterion P0-1)

- **board-pack export end-to-end** (#12) — `board-pack-detail.spec.ts`
  covers the UI render but not the generate → render-PDF → download
  chain end-to-end. P1; deferred. **Spillover: slice 388.**
- **tenant-switch real-RLS cross-tenant leak** (#2) — the `route.fulfill`
  mock variant authored here (AC-2) proves the switch _flow_ and the
  single-tenant _hide_ rule, but the highest-value assertion — that a
  tenant-A row is genuinely absent from a tenant-B view through real
  Postgres RLS — cannot run against the bring-up because
  `internal/api/testissuejwt.go` mints a **single-tenant** JWT
  (`AvailableTenants: []uuid.UUID{tenant}`). The harness cannot
  provision a multi-tenant user, and the token-exchange RPC requires
  the target tenant to be in `available_tenants[]`. **Spillover: slice
  389** (seed-harness multi-tenant JWT + the real-RLS leak spec).

---

## What this slice ships

- **AC-1:** this matrix.
- **AC-2:** `web/e2e/tenant-switch.spec.ts` (mocked).
- **AC-3:** `web/e2e/evidence-push-e2e.spec.ts` (mocked CLI→ingest→BFF→UI).
- **AC-4:** the 6-quarantine triage table above (4× un-skip, 2× re-quarantine).
- **AC-7:** cross-references slice 333 Q-9 + slice 334 P-4 (this header).

Spillovers filed: **387** (standalone-server CI harness), **388**
(board-pack e2e), **389** (multi-tenant JWT + real-RLS leak spec).
