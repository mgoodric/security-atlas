# 370 — Split `web/lib/api.ts` god-file into per-domain `web/lib/api/*.ts`

**Cluster:** Web
**Estimate:** 3d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Slice 328's comprehensive code-review audit (`docs/audits/328-code-review-comprehensive-report.md` finding **H-2**, severity **High**) flagged `web/lib/api.ts` as a 2901-LOC god-file with 219 exported symbols, violating the per-domain split convention already established next door at `web/lib/api/*.ts`.

The split convention is in place: `web/lib/api/bff.ts` (80 LOC, 2 exports), `web/lib/api/audit-server.ts` (40 LOC), and 9 other per-domain files (`anchors-export.ts`, `controls-export.ts`, `metrics.ts`, `risks-export.ts`, `audit-log.ts`, `audit.ts`, `exceptions-export.ts`, `controls-history-export.ts`, `activity.ts`). `web/lib/api.ts` is the legacy outlier predating this convention.

### What ships

1. **Split `web/lib/api.ts` into per-domain files under `web/lib/api/`.** Suggested split (engineer can refine):

   - `web/lib/api/anchors.ts` — lines 85-265 in current file (anchors + scope cells + tenant controls)
   - `web/lib/api/vendors.ts` — lines 366-475
   - `web/lib/api/framework-scopes.ts` — lines 473-590
   - `web/lib/api/attest.ts` — lines 590-700
   - `web/lib/api/artifacts.ts` — lines 634-704
   - `web/lib/api/admin-credentials.ts` — lines 704-781
   - `web/lib/api/feature-flags.ts` — lines 781-829
   - `web/lib/api/super-admins.ts` — lines 829-917
   - `web/lib/api/admin-tenants.ts` — lines 917-980
   - `web/lib/api/admin-demo.ts` — lines 980-1076
   - `web/lib/api/admin-sso.ts` — lines 1076-1306
   - `web/lib/api/control-coverage.ts` — lines 1306-1400
   - Plus remaining domains beyond the sample (the engineer maps the remaining 1500 LOC during Phase 1).

2. **Backward-compat re-export shim** — `web/lib/api.ts` becomes a `export * from "./api/anchors"; export * from "./api/vendors"; …` shim during the transition window so the 200+ existing import sites do not need to change in the same PR.

3. **Per-route import-site migration** — switch each `import { foo } from "@/lib/api"` to the specific submodule (`import { foo } from "@/lib/api/anchors"`). Land as a separate PR (Phase 2) so the structural slicing and the import migration are reviewable independently.

4. **Retire the shim** — once import-site migration is mechanical-only, delete `web/lib/api.ts`. Land as Phase 3.

5. **eslint rule** preventing regression — soft cap on `web/lib/api*.ts` files (suggested: 600 LOC). The rule would be `max-lines` from eslint's `eslint:recommended` set, scoped to `web/lib/api/`.

### JUDGMENT calls

The engineer makes the following design calls and records them in `docs/audit-log/370-web-api-client-split-decisions.md`:

- **Split granularity.** Per-domain (12-15 files) OR per-resource (40+ files matching every REST resource)? Recommend per-domain — matches the `web/lib/api/audit-*.ts` precedent.
- **Bundle M-3 (SESSION_COOKIE rename) into this slice?** Slice 328 audit report §M-3 suggests bundling. If H-2's Phase 2 (import-site migration) opens 200+ files for `lib/api` rewrites anyway, adding the `SESSION_COOKIE → ATLAS_JWT_COOKIE` rename is marginal cost. Engineer's call.
- **Shim retention.** One release window OR remove immediately? Recommend one slice / one PR worth of import-site migration time; do not let the shim linger.
- **eslint rule strength.** Hard CI fail OR `warn` only? Recommend hard fail to prevent regression.

### Why this matters

1. **Editor performance.** TypeScript Language Server reanalyses every dependent file when any of the 219 symbols changes. The file is on the dependency path of essentially every dashboard route.
2. **Merge conflict density.** Any two slices touching different domains both touch this file.
3. **vitest coverage gate.** Per-file floors in `web/coverage-thresholds.json` (slice 347) gate the slowest-moving function in any of 219 against the fastest-moving — coarse-grained. Per-domain files allow per-domain floors.
4. **Convention drift.** The split convention IS established next door. The god-file is the outlier.
5. **AI-navigability.** Search for "vendors client functions" surfaces 219 hits today; should surface ~10 (one file) after the split.

### Why now

H-2 from the slice 328 audit. Independent of platform-side work; can ship any time. The mechanical nature of the rewrite makes it a comfortable "Friday afternoon" slice (split + shim) plus a follow-on (import-site migration).

**Trigger:** filed 2026-05-28 from slice 328 audit.

## Threat model

Code-quality split only. STRIDE pass on the migration activity:

- **S (Spoofing):** N/A.
- **T (Tampering):** N/A.
- **R (Repudiation):** N/A.
- **I (Information disclosure):** N/A.
- **D (Denial of service):** N/A.
- **E (Elevation of privilege):** N/A.

The split is a TypeScript structural rewrite. No new auth surface, no new fetch targets, no new error shapes. Wire-shape compatibility is the AC-1 contract.

## Acceptance criteria

- [ ] **AC-1.** Per-domain files under `web/lib/api/` together export the same 219 symbols `web/lib/api.ts` does today (verified via TypeScript-aware grep + the existing import-site test).
- [ ] **AC-2.** `web/lib/api.ts` is either deleted OR is a one-liner re-export shim (`export * from "./api/anchors"; ...`) at end of this slice.
- [ ] **AC-3.** All 200+ import sites under `web/app/`, `web/components/`, `web/lib/` migrate from `@/lib/api` to the per-domain path (`@/lib/api/anchors`, etc.) — land as Phase 2 PR.
- [ ] **AC-4.** vitest test suite passes; Playwright e2e suite passes; no new test failures.
- [ ] **AC-5.** eslint rule enforces `max-lines: 600` on `web/lib/api/*.ts`.
- [ ] **AC-6.** No HTTP-wire change — every BFF route returns the same status + body shape as before.
- [ ] **AC-7.** `pre-commit run --all-files` passes; CI green.
- [ ] **AC-8.** Decisions log enumerates the split mapping (file-by-file → which symbols moved where).

## Constitutional invariants honored

- **Article VII (Simplicity Gate).** Splits 1 file × 219 exports into ~15 files × ~15 exports each.
- **Article VIII (Anti-abstraction Gate).** Per-domain split mirrors the established `web/lib/api/*.ts` convention — does not introduce new abstraction.

## Canvas references

- Slice 328 audit report `docs/audits/328-code-review-comprehensive-report.md` finding H-2
- `web/lib/api/bff.ts` and `web/lib/api/audit-server.ts` as the precedent split convention

## Dependencies

- **#178** (UI honesty audit harness) — `merged`. Audit harness pattern is conceptually reused.
- **#347** (vitest coverage ratchet) — `merged`. Per-file floors in `web/coverage-thresholds.json` will benefit from per-domain split.

## Anti-criteria (P0 — block merge)

- **P0-370-1.** Does NOT change any HTTP-wire behavior — the function signatures are pure-mechanical relocations.
- **P0-370-2.** Does NOT cross into M-3 (SESSION_COOKIE rename) territory in Phase 1; bundling into Phase 2 (import-site migration) is permissible per the JUDGMENT call.
- **P0-370-3.** Does NOT leave the shim `web/lib/api.ts` in place beyond one slice's worth of follow-up; the shim is a transition aid, not a permanent layer.
- **P0-370-4.** Does NOT auto-merge.
- **P0-370-5.** Does NOT regress vitest or Playwright suites — both must pass at PR-time for every phase.

## Skill mix

- `tdd` — RED-first tests for any per-domain function added during the split (none expected; should be pure relocation)
- `simplify` — pre-PR quality pass

## Notes for the implementing agent

Suggested phased approach:

1. **Phase 1 (1.5d):** Map the 219 exports to per-domain files. Create the new files via `git mv`-equivalent (TypeScript: cut + paste with annotation). Add the re-export shim in `web/lib/api.ts`. Run vitest + Playwright; PR opens with shim-only behavior change (zero import-site changes). Land as PR #1.

2. **Phase 2 (1d):** Mechanized migration of 200+ import sites from `@/lib/api` to specific paths. Use TypeScript's "Update Imports On File Move" if VS Code is available, OR a `tsmod`-style codemod. Diff will be large but mechanical. Land as PR #2.

3. **Phase 3 (0.5d):** Delete the shim. Add the eslint rule. Land as PR #3.

The split-mapping is the engineer's first deliverable in Phase 1; record it in the decisions log as a 219-row table (export name → target file) so the rewrite is reviewable.

If a function in `web/lib/api.ts` depends on a private (non-exported) helper, the helper moves with the function. If two domains share a helper (e.g., a common error-shape parser), extract to `web/lib/api/_shared.ts` (underscore prefix matches the convention for internal-to-package modules).

Per the audit report's H-2 §"Recommended mitigation," consider bundling the M-3 SESSION_COOKIE rename into Phase 2's import-site migration — both are TypeScript-side mechanical renames. The bundle JUDGMENT is the engineer's call; decisions log §D2 records the choice.
