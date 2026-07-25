# 740 — vitest 4 coverage-floor re-baseline (AST-aware v8 provider)

**Cluster:** Infra / test-tooling
**Estimate:** S (0.5d)
**Type:** AFK

**Status:** `superseded` — **FOLDED INTO slice 450 per maintainer approval
(2026-06-12).** The maintainer reviewed the 450↔740 coupling (the slice-347 gate
is vitest's built-in threshold check, so 450's `Frontend · vitest` job cannot go
green without the floors matching the new provider) and explicitly approved
applying this re-baseline INSIDE slice 450's PR (#1347), landing the vitest-4
bump and the floor re-baseline atomically. The 68-floor / 48-file re-baseline
(each re-seeded at the standard `max(0, floor(measured-2pp))` of the
coverage-v8-4 numbers) now lives in `web/coverage-thresholds.json` on the slice
450 branch; this doc is retained for the dataset + audit record and will be
marked merged/superseded in the reconcile. **No separate slice ships.**

> Original status was `not-ready` (blocked-by 450); the dataset and AC below
> describe the work as it was delivered inside 450.

## Parent

Spillover of **slice 450** (`vitest 2 → 4 + @vitest/coverage-v8 2 → 4 paired
migration`). Slice 450 is the tooling bump; this slice is the floor
re-baseline that the bump's provider-behaviour change forces. Per **P0-450-3**
a tooling bump must NOT edit the slice-347 per-file floors — floor changes
belong to a dedicated floor slice. This is that slice.

## Narrative

`@vitest/coverage-v8` 4 maps raw V8 coverage through the new **AST-aware
remapper** (`ast-v8-to-istanbul`, a hard dependency of coverage-v8 4, invoked
unconditionally — vitest 4 removed the toggle for the legacy `v8-to-istanbul`
path). The AST-aware mapper is strictly _more honest_: it no longer credits a
module with statement/line coverage merely for being transitively
module-loaded by a test that never calls its functions. That "phantom
coverage" is the exact artefact the `web/coverage-thresholds.json` `$comment`
already documents for the slice-396 barrel-retire — vitest 4 now eliminates it
globally.

The slice-347 per-file floors were seeded on the OLD (vitest-2) provider's
generous numbers. Under the new mapper, the _same 1760 tests on the same
source_ measure differently, and **68 metric-floors across 48 files** now read
below their recorded value — not because coverage regressed (no test was lost;
the suite is identically green) but because the ruler changed. The largest
gaps (98pp) are the phantom-coverage files: `lib/api/risks.ts`,
`lib/api/policies.ts`, `lib/api/audit.ts`, `components/control/strm.ts`,
`components/dashboards-metrics/format.ts` — all of which the new mapper
correctly reports at 0% functions/branches because no test calls their bodies.

This slice re-baselines those 48 files' floors to the AST-accurate measured
numbers (each floor = `max(0, floor(measured − 2pp))`, the slice-347/069
methodology, applied to the new ground truth). It does NOT raise any floor and
does NOT add tests — it re-denominates the existing ratchet into the new
provider's units so the ratchet continues from truth (slice-347 P0-347-3:
"start at truth, not aspiration").

## Why this is its own slice (not folded into 450)

- **P0-450-3** forbids slice 450 (a tooling bump) from editing the per-file
  floors. Re-baselining 48 files IS a floor edit.
- The re-baseline is a numeric recalibration that wants its own reviewable
  diff (slice-347 contract: floor changes land as "a clean numerical diff").
- Keeping the two separate preserves the audit story: 450 = "the provider
  changed its ruler (proven, gate still has teeth)"; 740 = "we re-denominated
  the ratchet to the new ruler".

## The 48 files / 68 breaches to re-baseline

Measured under coverage-v8 4.1.8 on the slice-450 branch (`vitest run
--coverage`), `got < recorded-floor`:

- app/(authed)/audits/filters.ts — branches:92.3<98
- app/(authed)/audits/format.ts — statements:96.87<97
- app/(authed)/exceptions/filters.ts — branches:93.75<98
- app/(authed)/risks/filters.ts — statements:96<98
- app/(authed)/risks/sort.ts — statements:94.87<98
- app/api/admin/audit-periods/export/route.ts — branches:87.5<88
- app/api/admin/demo/status/route.ts — branches:66.66<69
- app/api/admin/evidence/export/route.ts — branches:87.5<88
- app/api/admin/exceptions/export/route.ts — branches:87.5<88
- app/api/admin/policies/export/route.ts — branches:87.5<88
- app/api/admin/samples/export/route.ts — branches:87.5<88
- app/api/admin/vendors/export/route.ts — branches:87.5<88
- app/api/audit-log/export/route.ts — branches:87.5<88
- app/api/calendar/subscription/route.ts — branches:33.33<73
- app/api/controls-list/route.ts — branches:66.66<73
- app/api/controls/[id]/history/route.ts — branches:66.66<73
- app/api/controls/[id]/policies/route.ts — branches:66.66<73
- app/api/controls/[id]/risks/route.ts — branches:66.66<73
- app/api/dashboard/proxy.ts — branches:66.66<73
- app/api/me/sessions/[id]/route.ts — statements:69.23<85, branches:50<64, lines:69.23<85
- app/api/me/sessions/route.ts — branches:75<79
- app/api/oscal/component-claims/[id]/scf-anchor/route.ts — branches:70<74
- app/api/policies/[id]/route.ts — statements:86.66<92, branches:66.66<79, lines:86.66<92
- app/api/policies/route.ts — branches:66.66<69
- app/api/questionnaires/[id]/answers/[qid]/route.ts — statements:90.9<92, lines:90.9<92
- app/api/questionnaires/[id]/export-pdf/route.ts — branches:75<81
- app/api/requirements/[id]/coverage/route.ts — branches:66.66<73
- app/api/risks/[id]/route.ts — statements:85.71<90, branches:60<78, lines:85.71<90
- app/api/version/route.ts — branches:50<73
- components/control/strm.ts — branches:0<98, functions:0<98
- components/dashboards-metrics/format.ts — branches:0<98, functions:0<98
- lib/api/\_shared.ts — statements:30.76<48, branches:33.33<98, lines:33.33<48
- lib/api/admin.ts — branches:39.13<70
- lib/api/audit-log.ts — branches:86.11<93
- lib/api/audit-periods.ts — statements:0<5, branches:0<98, lines:0<5
- lib/api/audit.ts — branches:0<98, functions:0<98
- lib/api/bff.ts — branches:75<98
- lib/api/calendar.ts — statements:42.42<52, branches:43.47<86, lines:42.3<52
- lib/api/controls-list.ts — statements:7.89<11, branches:4.54<64, lines:8.82<11
- lib/api/dashboard.ts — branches:50<98
- lib/api/metrics.ts — branches:51.4<93
- lib/api/policies.ts — statements:35.71<45, branches:0<98, lines:38.46<45
- lib/api/risks.ts — branches:0<98
- lib/auth/oauth-client.ts — branches:10<85
- lib/display-name.ts — branches:87.5<91
- lib/page-names.ts — statements:93.33<98
- lib/safe-redirect.ts — statements:88.88<98
- lib/secure-cookie.ts — branches:83.33<98

(Note: the phantom-coverage files that drop to 0% functions/branches —
`strm.ts`, `format.ts`, `audit.ts`, `risks.ts`, `policies.ts`,
`audit-periods.ts` — should be moved into the `$omitted_zero_pct` set per the
existing thresholds-file rationale, NOT floored at 0, so they regain a real
floor only when a future test exercises their bodies. That mirrors the
slice-396 barrel-retire decision verbatim.)

## Acceptance criteria

- [ ] **AC-1.** On the post-450 `main` (vitest 4), `web/coverage-thresholds.json`
      is re-baselined so `npm run test:coverage -w web` exits 0 — every floor
      passes against AST-accurate measured numbers.
- [ ] **AC-2.** No floor is RAISED (this is a recalibration, not a lift); the
      48 files above are re-floored at `max(0, floor(measured − 2pp))` of the
      coverage-v8 4 numbers, and files measuring true 0% move into the
      documented `$omitted_zero_pct` set rather than carrying a 0 floor.
- [ ] **AC-3.** No test files added/changed; no production `web/` runtime code
      changed — diff is confined to `web/coverage-thresholds.json` (+ its
      `$omitted_zero_pct_count` / `$comment` housekeeping).
- [ ] **AC-4.** A short note added to the thresholds `$comment` recording the
      vitest-4 AST-remapper re-baseline (so the next reader knows the floors
      are denominated in coverage-v8 4 units).
- [ ] **AC-5.** `Frontend · vitest` CI job green; `pre-commit run --all-files`
      passes.

## Anti-criteria (P0 — block merge)

- **P0-740-1.** Does NOT lower a floor below the AST-accurate
  `floor(measured − 2pp)` (the ratchet stays at truth; this re-baseline is the
  one sanctioned re-denomination, not an open licence to drop bars).
- **P0-740-2.** Does NOT add or modify tests to "recover" the phantom coverage
  — phantom coverage was never real; recovering it would re-introduce the
  artefact vitest 4 correctly removed. Real coverage for the 0% fetch-wrapper
  modules is the slice-349/392 contract-tier rollout's job, not this slice's.
- **P0-740-3.** Does NOT touch `web/vitest.config.ts` or the vitest/coverage-v8
  versions (slice 450 owns those).

## Dependencies

- **Blocked-by / sequenced-after slice 450.** Slice 450 lands the vitest 4 +
  coverage-v8 4 bump. Because the slice-347 gate IS vitest's built-in
  threshold check, slice 450 cannot land `Frontend · vitest`-green WITHOUT the
  floors already matching the new provider — so in practice **450 and 740 must
  land together** (one merge train: bump + re-baseline) OR 740's threshold
  edits must be cherry-picked into 450's merge by the orchestrator. The clean
  separation is documented for the audit trail; the orchestrator decides the
  merge mechanics. See slice 450 PR + `docs/audit-log/450-*.md` D-section.
- Preserves slice 069 (vitest tier) and slice 347 (ratchet) — re-denominates
  347's floors, does not retire them.

## Skill mix (3-5)

- `dependency-auditor` — confirm the AST-remapper behaviour is the cause.
- `simplify` — keep the diff to the JSON sidecar only.
