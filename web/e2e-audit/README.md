# UI honesty audit harness (slice 178)

A reusable Playwright-based audit harness that checks whether the live
security-atlas UI **honestly represents what is actually shipped**. It
is intentionally separate from `web/e2e/` (functional flows) — this
suite asks a different question and produces different output.

| `web/e2e/` (slice 069+)               | `web/e2e-audit/` (slice 178)                                                      |
| ------------------------------------- | --------------------------------------------------------------------------------- |
| Does the dashboard render six panels? | Does the dashboard show anything we have not actually shipped?                    |
| Bound to real backend endpoints       | Bound to the mockup-spec manifest + AC-5 heuristics                               |
| **Required** CI check (slice 069 P0)  | **Informational** CI job — surfaces findings via a PR comment, never blocks merge |
| Functional contract — pass / fail     | Audit signal — categorized findings + spillover slices                            |
| Read-write where the spec demands     | **Read-only** (P0-178-1) — never submits forms, never clicks destructive actions  |

## What it checks

Each audited route is fingerprinted on the live page and compared
against its manifest entry. Findings fall into three categories:

- **SHIP-GAP** — the mockup describes an element we have not shipped.
  Fine for in-flight slices; flag if no slice exists.
- **HONESTY-GAP** — the live UI shows something that is **not** backed
  by a shipped feature. Forward-looking placeholders, dead anchors,
  `disabled` buttons whose tooltip reads "coming soon", elements
  gated on an unset feature flag. **This is the anti-pattern this
  slice catches.**
- **MOCKUP-STALE** — the mockup describes a feature we have decided
  to defer indefinitely. The mockup needs updating, not the live UI.

## The read-only constraint (P0-178-1)

The harness MUST NOT submit forms, click destructive actions, or
trigger any state mutation. The fixture wires every `Page.click` and
`Locator.click` through `lib/make-read-only.ts`'s mutation detector;
any click on a `<button type="submit">`, `[data-mutating="true"]`,
`[data-testid$="-submit"]`, `[data-testid$="-revoke-button"]`, or a
button whose accessible name contains a mutating verb (delete, remove,
revoke, rotate, approve, publish, …) throws
`ReadOnlyGuardrailViolation` and fails the test immediately.

The constraint is what makes operator-local prod-run audits safe —
the harness can read a real tenant's UI without touching their data.

## How to run locally (seeded docker-compose stack)

```sh
# 1. Bring up the slice-037 docker-compose stack with seed fixtures.
cd deploy/docker
cp .env.example .env
docker compose up -d
# wait for atlas + web to be healthy on :8080 and :3000
cd ../..

# 2. Seed the audit's preconditions. The harness reuses the slice-082
# seed harness's `dashboard` fixture — base tenant + scope + framework
# + one control + the api_keys row matching TEST_BEARER.
cd web
DATABASE_URL=postgres://atlas:atlas@localhost:5432/security_atlas \
  BEARER_HASH_KEY=test-bearer-hash-key-32-bytes-ok!! \
  npx tsx -e "import('./e2e/seed').then(m => m.seedFromFixture('dashboard'))"

# 3. Run the audit. Reports land under `web/e2e-audit/reports/`.
PLATFORM_BASE_URL=http://localhost:3000 \
  TEST_BEARER=test-bearer-e2e \
  npx playwright test --config=e2e-audit/playwright.config.ts
```

Reports are gitignored. Commit only the curated summary at
`docs/audit-log/178-ui-honesty-first-pass.md` (and per-PR follow-ups
file as new audit-log entries or spillover slices).

## How to run against a non-seeded deployment (operator-local prod)

The harness supports a `PLATFORM_BASE_URL` override so an operator
can audit their production self-host (`https://atlas.example.com`)
without touching real tenant data:

```sh
cd web
PLATFORM_BASE_URL=https://atlas.example.com \
  TEST_BEARER="$(security-atlas evidence push-credentials issue --tenant my-tenant --ttl 1h)" \
  npx playwright test --config=e2e-audit/playwright.config.ts
```

**Reports of operator-local prod runs MUST stay local. Do not commit
them. Do not upload them as a CI artifact.** They contain real tenant
data in the screenshots + DOM dumps (per the slice 178 threat model,
mitigation I → P0-178-3). The harness writes such runs under
`web/e2e-audit/reports/local-prod/` which is doubly gitignored. The
detection rule for an operator-local run is `baseURL !==
http://localhost:*`; the report includes a banner reminding the
operator not to commit it.

## Finding a spillover slice for a finding

When the audit surfaces a finding the maintainer wants to act on:

1. Compute the next slice number from `docs/issues/`.
2. Write the finding as `docs/issues/<NNN>-<slug>.md` using the slice
   template at `Plans/prompts/04-per-slice-template.md`. Cite "Surfaced
   during slice 178 first-pass audit" (or "during slice 178 advisory
   CI run on PR #<N>") in the narrative.
3. Status `ready` if the dependencies are merged; `not-ready` otherwise.
4. **Do not bundle the fix into a PR that touched any other slice's
   work.** Per slice 178 P0-178-4, each discrete fix is its own slice;
   trivial doc-only findings can batch into a single spillover.

The `/idea-to-slice` skill scaffolds this — supply the finding's
category, route, and subject and it produces the slice doc.

## Files

| File                      | Role                                                                     |
| ------------------------- | ------------------------------------------------------------------------ |
| `playwright.config.ts`    | Separate Playwright project. Independent of `web/playwright.config.ts`.  |
| `fixtures.ts`             | `authedReadOnlyPage` fixture + `:id` route resolver.                     |
| `ui-honesty.spec.ts`      | The spec. One test case per manifest entry; afterAll writes the report.  |
| `mockup-spec.json`        | The manifest. Data-only (P0-178-7).                                      |
| `mockup-spec.schema.json` | The manifest's JSON Schema (AC-10a).                                     |
| `lib/manifest.ts`         | Manifest loader + validator (file-existence check enforces P0-178-11).   |
| `lib/heuristics.ts`       | AC-5 heuristics: dead anchors, coming-soon buttons, unset feature flags. |
| `lib/mockup-diff.ts`      | AC-4 diff categorization (SHIP-GAP / HONESTY-GAP / MOCKUP-STALE).        |
| `lib/make-read-only.ts`   | AC-7 read-only guardrail.                                                |
| `lib/report.ts`           | AC-6 deterministic JSON + Markdown report writer.                        |
| `lib/*.test.ts`           | vitest coverage for the pure-logic modules.                              |
| `reports/`                | Gitignored. Per-run JSON + Markdown + screenshots.                       |

## Coverage of v1 routes (AC-8)

`/dashboard` · `/controls` · `/controls/:id` · `/evidence` · `/audits` ·
`/risks` · `/policies` · `/board-packs` · `/settings` · `/calendar`.

`/admin/*`, `/framework-scopes`, `/vendors`, `/exceptions`,
`/catalog/:anchor`, and `/dashboards/*` are deferred to follow-on
slices per AC-9 — they either need a separate admin TEST_BEARER, post-
date the mockup set, or both.
