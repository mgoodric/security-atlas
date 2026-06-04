# 178 — UI honesty audit harness + first-pass audit against v1 mockup spec

**Cluster:** Quality / Testing
**Estimate:** 1.5d (harness + first-pass audit + CHANGELOG/docs; spillover fix slices file separately, not bundled)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY (what's broken or missing today).** The v1 backlog completed at 69/69 slices (2026-05-15, `62372c2`) and the loop is happily shipping v2 slices — but no one has done an end-to-end pass to verify the live UI **honestly represents what's actually shipped**. The product principle is: if a feature isn't shipped yet, don't show it in the UI. Today we don't know whether placeholder cards, "coming soon" sections, dead links to unshipped routes, or other forward-looking UI elements have accumulated across the ~20 authenticated routes. The closest we have to systemic verification is slice 069's Playwright functional flows + slices 111-115's (not-ready) per-page spec extensions — but those test FUNCTIONAL CONTRACTS (does the dashboard render six panels with bound data), not UI HONESTY (does the dashboard show anything that isn't actually shipped). Different intent, different harness shape, different output.

A related signal: the `Plans/mockups/` directory has ten source-of-truth HTML mockups (dashboard, controls, control-detail, evidence, audits, risks, policies, board-pack, settings, questionnaire). Iteration-1 mockups; the production UI has had ~3 months of evolution since. Where the live UI diverges from the mockup, three things are possible:

- **SHIP-GAP** — mockup shows a feature that's still in flight (live UI is missing it; this is fine for in-flight slices, flag if no slice exists)
- **HONESTY-GAP** — live UI shows something the mockup doesn't because we've shipped a forward-looking placeholder without a backing feature (the anti-pattern this slice catches)
- **MOCKUP-STALE** — mockup describes a feature we've decided to defer indefinitely (the mockup should be updated or marked stale)

**WHAT this slice ships (the tracer bullet).**

1. A reusable **Playwright-based UI honesty audit harness** at `web/e2e-audit/` (separate Playwright project; does NOT extend the existing `web/e2e/` functional flow suite).
2. A **mockup-spec manifest** (`web/e2e-audit/mockup-spec.json`) mapping each audited route to its source-of-truth mockup file + expected element fingerprints.
3. A **first-pass audit run** against the seeded slice-037 docker-compose bring-up, with the report committed at `docs/audit-log/178-ui-honesty-first-pass.md`.
4. **CI integration** as an informational (not required) job: `Frontend · UI honesty (advisory)`, surfacing findings via a PR comment + report artifact.
5. **Spillover slices filed via `/idea-to-slice`** for every distinct finding in the first-pass report — one slice per discrete fix, NOT bundled into this PR.

**SCOPE DISCIPLINE — what's deliberately out.**

- **Admin-area routes** (`/admin/*`, `/settings/*` deep paths) — defer to a follow-on slice that uses a separate admin TEST_BEARER (per the threat-model E mitigation).
- **Routes without mockups** (`/framework-scopes`, `/vendors`, `/exceptions`, `/catalog/:anchor`, `/dashboards` plural) — first-pass covers only the ten routes that have a `Plans/mockups/*.html` counterpart, plus `/dashboard` (which has `dashboard.html`). Spillover slices can extend coverage.
- **Visual regression / pixel-level screenshot diff** — text/structure diff only for v1; pixel-level visual regression is a v2 nice-to-have.
- **Mutation testing** (clicking buttons, submitting forms) — explicitly forbidden by P0-178-1. The harness is read-only.
- **Promotion to a required CI check** — informational/advisory only in this slice. Promotion is a separate slice once the harness proves stable.
- **Production-deployment audit recipe** (running the harness against the maintainer's unraid deployment) — that's an operator-local workflow; the harness supports it via `PLATFORM_BASE_URL` override, but reports of production runs stay local (not committed). A follow-on slice can ship a dedicated operator-driven prod-audit walkthrough.
- **Fixing the findings** — this slice ships the AUDIT, not the FIXES. Findings file as spillover slices; the loop picks them up.

## Threat model

**S — Spoofing.** The harness reuses slice 069's existing auth fixture (`TEST_BEARER` env var OR `TEST_USER_EMAIL`/`TEST_USER_PASSWORD`). No new authentication surface. Credentials never logged. Mitigation: harness fixture inherits from existing `web/e2e/fixtures.ts` patterns, including the credential-redaction conventions.

**T — Tampering.** The harness's defining safety property: it must be **read-only**. The "verify all visible UI elements actually function" instruction in the original idea is intentionally narrowed in this slice: the harness clicks NAVIGATION elements (links, sort headers, filter pills, expand/collapse triggers) but does NOT submit forms, click destructive actions, or trigger any state mutation. Anti-criterion P0-178-1 enforces this via a guardrail decorator that fails the test if a form submit is attempted; CI test (AC-7) validates the guardrail itself.

**R — Repudiation.** Audit reports are documentation artifacts, not audit-binding records. No new audit-log writes. Mitigation: reports include timestamp + git SHA + harness version for traceability ("audit run against `<git-sha>` at `<ts>` produced N findings").

**I — Information disclosure.** Rendered UI in screenshots could leak tenant-scoped data. The principal mitigation (P0-178-2): the **first-pass audit AND the CI job both run against the seeded slice-037 docker-compose bring-up**, which contains only fixture data (no real tenant data). The harness supports `PLATFORM_BASE_URL` override for operator-local prod runs, but such runs are operator-driven and reports stay local — never committed to the repo, never uploaded as a CI artifact (the override is dev-only).

**D — Denial of service.** The harness is an internal test artifact, not exposed to external callers. Bounded runtime (~11 routes × ~5s per route + screenshot overhead). Not a server-side concern.

**E — Elevation of privilege.** The harness uses a non-admin TEST_BEARER. Admin-surface audits (which require a separate admin bearer) are deferred to a follow-on slice (per scope discipline). Mitigation: the audit-route allowlist is data (JSON manifest), so adding admin routes requires both (a) updating the manifest AND (b) provisioning the admin bearer — neither happens in this slice.

**Verdict.** **has-mitigations** — primary safety properties are the read-only constraint (T → P0-178-1) and the seeded-data constraint (I → P0-178-2). Both are baked into the slice's structural design, not deferred to operator discipline.

## Acceptance criteria

### Harness construction (web/e2e-audit/)

- **AC-1.** NEW directory `web/e2e-audit/` with its own `playwright.config.ts`. Separate Playwright project from `web/e2e/`. Independent test runner invocation: `cd web && npx playwright test --config=e2e-audit/playwright.config.ts`.
- **AC-2.** NEW spec `web/e2e-audit/ui-honesty.spec.ts` iterates through the route list from the manifest (AC-10). One test case per route.
- **AC-3.** For each route, the harness: (a) authenticated-navigates to the route; (b) waits for `networkidle`; (c) captures a DOM-element fingerprint via `getByTestId(...)` enumeration; (d) captures a single full-page screenshot to `web/e2e-audit/reports/screenshots/<timestamp>/<route>.png`.
- **AC-4.** NEW module `web/e2e-audit/lib/mockup-diff.ts` that takes (a) the live-route fingerprint and (b) the mockup-spec entry for that route, and produces a categorized diff (SHIP-GAP, HONESTY-GAP, MOCKUP-STALE).
- **AC-5.** Forward-looking-UI heuristics: the harness detects (a) anchor elements whose `href` resolves to a 404 on the live app; (b) buttons with `disabled` + a tooltip/aria-label containing "coming soon" or "not yet"; (c) elements with `data-feature-flag` attributes pointing at flags not in the current flag set. Each heuristic is testable; AC-5a/5b/5c separately.
- **AC-6.** Structured JSON report at `web/e2e-audit/reports/ui-honesty-<timestamp>.json` AND a markdown summary at the same path with `.md` extension. Both report types are deterministic given the same inputs (the harness sorts findings for stable diffs).
- **AC-7.** Read-only guardrail: a test-only Playwright decorator (`makeReadOnly(page)`) that wraps `page.click` with a check — if the clicked element is `<form>`, `<button type="submit">`, or has `data-mutating="true"`, the test fails immediately. Unit test asserts the guardrail fires.

### Route coverage (v1)

- **AC-8.** Routes audited in v1: `/dashboard`, `/controls`, `/controls/<first-shipped-anchor-id>`, `/evidence`, `/audits`, `/risks`, `/policies`, `/board-packs`, `/settings`, `/calendar`. Ten routes total. (`/controls/:id` resolves at test time to the first anchor in the seeded data.)
- **AC-9.** Routes explicitly deferred to follow-on slices: `/admin/*` (needs admin bearer), `/framework-scopes`, `/vendors`, `/exceptions`, `/catalog/:anchor`, `/dashboards/*`. Each gets a separate spillover slice or is captured in the first-pass report's "coverage gaps" section.
- **AC-10.** NEW manifest `web/e2e-audit/mockup-spec.json` is a JSON array of `{ route, mockupPath, expectedTestIds, allowedExtraTestIds }`. Manifest is data-only — no test logic embedded. AC-10a: manifest schema is validated by a JSON Schema check in CI (informational job).

### Mockup comparison

- **AC-11.** For each audited route + mockup pair, the diff logic categorizes findings as one of three types: SHIP-GAP (in mockup, not in live), HONESTY-GAP (in live, not in mockup AND not on allow-list), MOCKUP-STALE (in mockup but slice owner confirms feature deferred indefinitely). Categorization rules are tested via fixture inputs (AC-11a/11b/11c).
- **AC-12.** Report includes per-finding metadata: route, category, element selector or testid, mockup file reference, suggested action (file spillover slice / remove placeholder / mark mockup stale).

### CI integration

- **AC-13.** NEW CI job `Frontend · UI honesty (advisory)` in `.github/workflows/frontend.yml` (or a new dedicated workflow) that runs the harness against the slice-037 docker-compose bring-up. Job follows slice-069 path-filter pattern (only fires on PRs touching `web/` or audited surfaces).
- **AC-14.** Job is INFORMATIONAL — not added to branch-protection required-checks (`.github/branch-protection.json` is not modified in this slice). Failure does not block merge.
- **AC-15.** Job posts a PR comment summarizing the delta vs `main`: count of new findings introduced by the PR, categorized. Comment uses GitHub Actions's `commentOn: pull-request` pattern (slice 089 has an example shape).

### First-pass audit + spillover discipline

- **AC-16.** Run the harness once locally against the seeded docker-compose stack; commit the first-pass report as `docs/audit-log/178-ui-honesty-first-pass.md`. Report includes: total findings count, categorization summary, per-route summary, recommended next actions.
- **AC-17.** Each substantive finding in the first-pass report has a corresponding **NEW spillover slice** filed via `/idea-to-slice` (one slice per discrete fix). Spillover slices follow the existing pattern: cite "Surfaced during slice 178 first-pass audit" in their narrative. Trivial findings (e.g., "dashboard.html mockup has a typo on line 47") can be batch-bundled into a single doc-only spillover.
- **AC-18.** First-pass report has a count + categorization table at the top: total findings | SHIP-GAP | HONESTY-GAP | MOCKUP-STALE | spillover-slice-numbers-filed.

### Documentation

- **AC-19.** CHANGELOG entry under `[Unreleased] / Added`: "UI honesty audit harness at `web/e2e-audit/` (#178); first-pass audit committed at `docs/audit-log/178-ui-honesty-first-pass.md`."
- **AC-20.** NEW file `web/e2e-audit/README.md` explaining: (a) the harness's read-only constraint, (b) how to run locally against the seeded docker-compose stack, (c) how to override `PLATFORM_BASE_URL` for operator-local prod-run audits, (d) the "reports of prod runs stay local — do NOT commit" rule, (e) the spillover-slice discipline for findings.

## Constitutional invariants honored

- **Manual evidence is first-class** (invariant #9). The audit treats manual-evidence flows (e.g., `/evidence` upload, `/controls/:id` manual sample form) as in-scope — the read-only constraint means we navigate the manual-evidence surface without submitting it, asserting only that the surface renders honestly.
- **AI-assist boundary (hard)**. When slice 173 (MCP server write tools) lands, the harness will encounter AI-assist UI; the heuristics for forward-looking UI catch any unshipped AI affordance that leaks into the live build. P0-178-1 (read-only) prevents the harness from triggering AI-assist actions.
- **Multi-tenancy not branched on tenant count** (canvas §11 #13). The harness asserts that the live UI does NOT show different chrome based on `tenant_count == 1` vs `>= 2` — the "tenant chrome MAY be hidden" rule means the LIVE UI should still render correctly with single-tenant seeded data, which the audit verifies.

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — "vanity trust centers" anti-pattern (the broader principle: don't ship UI for features that don't have backing functionality)
- `Plans/canvas/09-tech-stack.md` — Next.js 16 + shadcn/ui + Tailwind 4 + Playwright (the stack the audit runs against)
- `docs/research/opengrc-borrow-inventory.md` L10 — the "marketing-language feature pages without docs" anti-pattern that motivates this slice's existence

## Dependencies

- **#005** (Frontend bootstrap) — `merged`. The Next.js 16 + shadcn/ui surface this audits.
- **#037** (Docker-compose self-host bundle) — `merged`. The audited target environment.
- **#069** (Playwright e2e CI promotion) — `merged`. The CI infrastructure + auth-fixture pattern this extends.
- **#089** (CI job pattern with PR-comment posting) — `merged`. Reference shape for AC-15.
- **#177** (Exceptions list-page UI) — `merged`. The most recent UI slice; ensures the audit sees a current production-shipped state.

## Anti-criteria (P0 — block merge)

- **P0-178-1.** Harness does NOT submit forms, click destructive actions, or trigger any state mutation. Read-only enforcement via `makeReadOnly(page)` decorator (AC-7). Violation = harness can corrupt seeded data, which violates the entire premise.
- **P0-178-2.** First-pass audit AND the CI job both run against the **seeded slice-037 docker-compose bring-up only**. NOT the maintainer's unraid production deployment. Production-against runs are operator-driven and reports stay local. Violation = real tenant data could leak into committed artifacts.
- **P0-178-3.** Audit reports MUST NOT contain real tenant data. The seeded stack uses fixture data; operator-local prod runs DO render real data but reports of those runs are gitignored (`web/e2e-audit/reports/local-prod/`).
- **P0-178-4.** Findings file as SPILLOVER slices, NOT bundled into THIS slice's PR. A first-pass-bundled 30-finding mega-PR is rejected at review; one slice per discrete fix. Trivial doc-only findings can batch into a single spillover.
- **P0-178-5.** Harness does NOT extend `web/e2e/`. Separate Playwright project at `web/e2e-audit/`. Reason: `web/e2e/` tests functional contracts (does the dashboard render with bound data); `web/e2e-audit/` tests UI honesty (does the dashboard show anything not actually shipped). Different intent, different harness shape, different output. Conflation muddies both.
- **P0-178-6.** CI job is INFORMATIONAL only; `.github/branch-protection.json` is NOT modified. Promotion to required-checks is a future slice once the harness proves stable across 5+ PRs.
- **P0-178-7.** Mockup-spec manifest at `web/e2e-audit/mockup-spec.json` is **data-only**. NO test logic embedded in the manifest. The harness's diff module (AC-4) consumes the manifest; the manifest is human-readable and human-editable.
- **P0-178-8.** Neutral `test-*` fixture tokens only. NO vendor-prefixed test identifiers (per slice 005 convention; this is well-trodden ground in the existing `web/e2e/` suite).
- **P0-178-9.** This slice ships the AUDIT, not the FIXES. The first-pass-audit report identifies findings; spillover slices ship the fixes. Mixing fix work into this slice's PR is rejected at review.
- **P0-178-10.** Harness does NOT add UI components, modify routes, or change any user-facing surface. It is purely a test/audit artifact under `web/e2e-audit/` + a new CI job + the first-pass report + (separately) spillover slice docs.
- **P0-178-11.** Mockup-spec manifest does NOT include routes whose mockup file does not exist. AC-10 requires manifest entries to reference existing `Plans/mockups/*.html` files; an entry for a non-existent mockup is a CI failure of the JSON Schema check.

## Skill mix (3-5)

1. **Playwright API + test-organization discipline** — slice 069 conventions extend; reuse the `fixtures.ts` auth pattern.
2. **JSON Schema for the mockup-spec manifest** — schema-as-code pattern from existing schemas in the repo (search `schemas/*.json` if present).
3. **CI-job authoring + PR-comment posting** — slice 089 reference shape for AC-15; existing `frontend.yml` reference for slice 069 path-filter pattern.
4. **Mockup diff heuristics** — text/structure-level diff; consider `parse5` or `cheerio` for parsing the mockup HTML if a structured parser is needed.
5. **`/idea-to-slice` skill for spillover discipline** — engineer files spillovers via the existing `.claude/skills/idea-to-slice` pipeline (one per finding).

## Notes for the implementing agent

### Phase 2 grill findings (applied above)

- **Terminology fix:** original idea used "UI walk-through"; slice uses **"UI honesty audit"** consistently. This matches OpenGRC borrow-inventory L10's vocabulary ("marketing-language feature pages without docs behind them") and is more specific to the intent than "walk-through."
- **Scope-creep guard:** original idea bundled (a) harness + (b) first-pass audit + (c) spillover slices. Kept the bundle (it's a tracer-bullet shape — A is structure, B is proof-of-life, C is captured-not-bundled) but scoped the route list to 10 mockup-backed routes for v1. Admin / framework-scopes / vendors / exceptions / catalog detail defer to spillover.
- **Reusable harness vs one-shot audit:** committed to the reusable harness path (matches existing slice 069 Playwright infrastructure; CI cost is small once the harness exists; the spillover-slice discipline is self-reinforcing as new UI is shipped).

### Phase 3 threat-model findings (applied above)

- Read-only is the load-bearing safety property (P0-178-1; AC-7).
- Seeded-data-only constraint is the data-leak mitigation (P0-178-2/3; AC-13).
- Admin surface deferred to follow-on (E mitigation).

### Implementation order suggestion

1. **Skeleton first.** New `web/e2e-audit/` directory + `playwright.config.ts` + a one-route spec (`/dashboard` only) + the `makeReadOnly` decorator + a minimal manifest. CI-job-less; runs locally. Validate the shape.
2. **Manifest + diff logic.** Add all 10 routes to the manifest. Implement the diff-categorization module + heuristics. Tests for the diff logic with fixture inputs (AC-11a/11b/11c).
3. **CI integration.** Add the workflow job. PR-comment posting. JSON Schema validation. Confirm the job runs against the seeded docker-compose stack.
4. **First-pass audit.** Run locally; capture the report; file spillover slices via `/idea-to-slice` (one per substantive finding). Trivial findings batch into one doc-only spillover.
5. **Docs + CHANGELOG.** README, CHANGELOG entry, audit-log report committed.

### Spillover candidates the grill surfaced (file as separate slices)

If during this slice an out-of-scope bug, scope expansion, or unrelated tech-debt finding emerges that the engineer cannot land in this PR, file the finding as a separate slice per Amendment 2:

- **Visual regression / pixel-diff harness** — v2 nice-to-have. File as a deferred slice if maintainer wants it tracked.
- **Admin-surface UI honesty audit** — needs separate admin bearer; file as follow-on slice (gated on this slice merging + admin bearer provisioning being clean).
- **CI promotion to required-check** — file as follow-on slice gated on 5+ PRs of stable advisory-mode operation.
- **Operator-driven prod-audit recipe** — `just ui-honesty-prod` shell recipe + docs. File as follow-on operator-tooling slice.
- **Specific findings from the first-pass report** — each filed as its own AFK spillover slice (one per finding), per AC-17.

### Provenance

Filed 2026-05-20 via `/idea-to-slice` from maintainer's Tier C decision-walkthrough session: "I would like to use /idea-to-slice to create a backlog item that spins up or uses our unraid deployment of security-atlas to do a full walk-through to see what is working and what is not working in the UI and then creates follow up slices for all items that are found." The slice as written narrows the original scope to the harness + seeded-data first-pass + spillover discipline; operator-driven unraid prod runs remain supported via `PLATFORM_BASE_URL` override but are out of the AFK loop's scope.
