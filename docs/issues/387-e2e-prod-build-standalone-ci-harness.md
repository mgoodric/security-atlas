# 387 — CI harness for the production-build standalone Playwright specs

**Cluster:** Quality / e2e
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 351, captured per continuous-batch policy.

Two Playwright specs guard regressions that ONLY manifest under the
Next.js production-build standalone server (`node
.next/standalone/web/server.js`), not under the `npm start` dev server
the CI Playwright job runs against:

- `web/e2e/bff-cookie-production-build.spec.ts` (slice 146 — the
  `NODE_ENV`-coupled cookie attribute that turned BFF panel JSON into
  login HTML under standalone).
- `web/e2e/logo-render-production-build.spec.ts` (slice 153 — the
  `output: "standalone"` tracer not copying `web/public/`, so logo +
  OG/Twitter assets 404'd in the deployed image).

Both are gated by `test.skip(!process.env.ATLAS_PROD_BUILD, …)`. Slice
351's audit (AC-4) re-quarantined them with disposition (b): the guard
is NOT vestigial — forcing the specs green against the dev server would
assert nothing about the standalone-only path (green-washing). The real
gap is that **no CI job brings up the standalone server** (`grep
ATLAS_PROD_BUILD .github/` is empty; `web/package.json` has a
`build:standalone` script but nothing in CI invokes it).

## What

Add a CI job (or a matrix leg of the existing `Frontend · Playwright
e2e` job) that:

1. Runs `npm run build:standalone` in `web/`.
2. Boots `node .next/standalone/web/server.js` against the same
   docker-compose atlas + Postgres + NATS + MinIO bring-up the dev-server
   leg uses.
3. Runs Playwright with `ATLAS_PROD_BUILD=1` so the two prod-build specs
   un-skip and execute.

When that lands, the two specs' `ATLAS_PROD_BUILD` guards become
satisfied in CI and the regressions they guard are gated on every PR.

## Scope discipline

- DOES NOT modify the two spec bodies — they already assert the right
  thing; they just need a server to run against.
- DOES NOT promote `e2e-audit/` ui-honesty harness (that is slice 333
  Q-10 / slice 353's path).
- DOES NOT change the dev-server Playwright leg.

## Acceptance criteria

- [ ] AC-1: CI builds + boots the standalone server.
- [ ] AC-2: `bff-cookie-production-build.spec.ts` +
      `logo-render-production-build.spec.ts` execute (not skipped) in
      that CI leg and pass.
- [ ] AC-3: the dev-server Playwright leg is unaffected.
- [ ] AC-4: decisions log records whether this is a new job vs a matrix
      leg + the wall-clock cost.

## Dependencies

- #146, #153 — merged. The specs this unblocks.
- #082, #201 — merged. The seed + JWT harness the standalone leg reuses.
- #351 — the audit that re-quarantined the specs + filed this.

## Cross-references

- Slice 351 coverage matrix
  (`docs/audits/351-e2e-critical-flow-coverage-matrix.md`) — flows #10,
  #11; disposition (b).
