# 348 — Test framework polish round 1 decisions log

**Slice:** 348
**Type:** JUDGMENT
**Date:** 2026-05-28
**Companion slice doc:** `docs/issues/348-test-framework-polish-round-1.md`
**Source audit:** `docs/audits/334-test-framework-review.md`
**Source decisions:** `docs/audit-log/334-test-framework-review-decisions.md`

The slice bundles 17 medium + low findings from slice 334's framework
audit into 5 clusters (A-E). This log captures the JUDGMENT calls made
during implementation — particularly the cluster D `fullyParallel`
experiment outcome, the U-3 golden-file scope, the P-4 `test.skip`
triage rationale, and the P-5 chromium-only re-evaluation.

---

## Cluster A — Go unit hygiene

### D-A1 — `t.Parallel()` sweep scope: 6 files (eval + oauth), not 108

**Decision:** Limit the `t.Parallel()` sweep to the audit's named target
files (`internal/eval/state_test.go` and `internal/api/oauth/token_test.go`)
and the non-integration sibling files in the same two packages:

- `internal/eval/state_test.go`
- `internal/eval/rego_test.go`
- `internal/api/oauth/token_test.go`
- `internal/api/oauth/authorize_test.go`
- `internal/api/oauth/oauth_test.go`
- `internal/api/oauth/export_test.go`

**Rejected scopes:**

- **Full codebase sweep (108 files lacking `t.Parallel()`).** Out of
  scope for a 1.5d polish slice; injects real risk per the premortem
  (a test with subtle shared state misses the review and surfaces as a
  flaky data race in CI).
- **Single-file sweep (only `state_test.go` + `token_test.go`).** Misses
  the sibling pattern — leaves the convention drift unresolved.

The 6-file scope captures the audit's concrete examples plus the
immediate package siblings, which is what a contributor inheriting the
convention would read first. Future polish rounds can extend the sweep
to additional packages.

**Confidence:** high.

### D-A2 — Inline rationale comment for any file NOT receiving `t.Parallel()`

(Populated during EXECUTE — captures per-file results.)

### D-A3 — `TestRender_ProducesRealPDF_TenIterations` → `_FiveIterations`

**Decision:** Rename per the audit finding U-4. The body unambiguously
runs 5 iterations (line 121 `const iterations = 5`); the name says 10.
Renaming is a one-line edit; no surrounding rationale needs adjustment
(the in-line comment already explains why iterations is 5).

**Confidence:** high.

---

## Cluster B — Go integration sharpening

### D-B1 — `-p 1` rationale comment additions: 4 specific augmentations

**Decision:** Sharpen the existing comment block (ci.yml line 301-307)
with the 4 additions per slice 334 audit's "-p 1 rationale review"
section:

1. Name colliding tables explicitly (`evidence_kind_schemas`,
   `scf_anchors`, `evidence_records`).
2. Clarify the constraint is platform-layer (shared seeds + append-only
   ledger), NOT RLS — the misreading the audit explicitly corrects.
3. Reference `docs/audits/334-test-framework-review.md` "-p 1 rationale
   review" section so the deeper analysis is discoverable.
4. Name the v3 split-phase relaxation path (Phase A serial, Phase B
   parallel) so future relaxation is recoverable when wall-clock
   pressure surfaces.

**P0-348-3 honored:** the `-p 1` FLAG itself is not modified. The
comment block sharpens; the flag stays.

**Confidence:** high.

---

## Cluster C — Vitest helpers

### D-C1 — Vitest `include` collapse: keep escape-bracket route-group entries

**Decision:** Collapse the `include` array to a generic `**/*.test.ts`
plus an `exclude` for non-test paths. The escaped route-group entries
(`app/[(]authed[)]/**/*.test.ts`) become unnecessary under the generic
glob — the directory walk surfaces those files automatically.

**Reasoning:** Vitest uses fast-glob (via tinyglobby in v3+) which
walks directories regardless of literal-paren naming. The escape was a
workaround for the explicit-path-array shape; the generic glob doesn't
need it.

**Risk:** A `node_modules` test file or `.next/` build artifact gets
accidentally included. Mitigation: the existing exclude list already
covers `**/node_modules/**`, `**/.next/**`, `**/dist/**`, `e2e/**`,
`e2e-audit/**/*.spec.ts` — keep all of these.

**Confidence:** high.

### D-C2 — NextResponse mock sweep: 46 files via shared helper

**Decision:** Extract the duplicated mock into
`web/lib/test-utils/next-mocks.ts` and sweep all 46 vitest files that
re-declare it.

**Helper shape:** export a `vi.mock`-callable factory function rather
than a top-level `vi.mock("next/server", ...)` call (top-level mocks
are hoisted but not auto-applied across imported modules). Each call
site imports the factory and calls `vi.mock("next/server", () => mockNextServer())`.

This preserves the hoisting semantics vitest requires while
centralizing the mock body.

**Confidence:** high.

### D-C3 — Test-bearer literals: centralize the existing-neutral shapes

**Decision:** Create `web/lib/test-utils/test-tokens.ts` exporting the
neutral literals already in use. The shape today is fine — `test-bearer-094`,
`test-bearer-263`, `test-bearer-fixture`, `test-bearer-value`,
`test-bearer-a/b` are all GitGuardian-neutral (no `ghp_`, `sk_`, `gho_`,
`eyJ`, `AKIA`). The slice centralizes for future migration ease (per
slice 197 / 201 lessons).

**Naming:** keep slice-numbered shapes where they have meaning;
introduce a baseline `TEST_BEARER_DEFAULT = "test-bearer-default"` as
the new-test default.

**Confidence:** high.

---

## Cluster D — Playwright

### D-D1 — `fullyParallel: true` 3-CI-run experiment outcome

**Decision:** Enable `fullyParallel: true` in
`web/playwright.config.ts`. Push branch + 3 empty commits to trigger
3 consecutive CI runs. Observe.

**Experiment protocol (per slice 348 task instructions):**

1. Set `fullyParallel: true` in the Playwright config.
2. Push the branch (run #1).
3. Push 3 trivial empty commits via `git commit --allow-empty -s` to
   trigger reruns #2, #3, #4.
4. Watch the 3 CI runs after the cluster-D commit. If all 3 green,
   the flag stays + this section gets `kept-enabled` verdict. If any
   1 of 3 surfaces a real race, revert + document the race shape +
   this section gets `reverted-due-to-race` verdict.

**P0-348-6 enforcement:** if ANY of the 3 CI runs surfaces a real
race, the flag is reverted in this PR (NOT a follow-up); the failure
mode lands here for the next polish round to triage.

**Outcome (populated after observation):**

- Run #1 result: TBD
- Run #2 result: TBD
- Run #3 result: TBD
- **Verdict:** TBD

**Confidence:** medium pre-experiment (slice 201's JWT scoping should
have eliminated the static-bearer race, but the experiment is the
only honest way to verify — CI is the gate).

### D-D2 — `test.skip` triage: 6 actual quarantines, all env-gate-legitimate

**Decision:** Read each of the 8 audit-named files. Result:

| File                                   | Line | Skip reason                                                              | Disposition                  |
| -------------------------------------- | ---- | ------------------------------------------------------------------------ | ---------------------------- |
| `audits-create.spec.ts`                | 42   | `!process.env.PLAYWRIGHT_RUN_QUARANTINED`                                | env-gate-legitimate          |
| `auth-open-redirect.spec.ts`           | 38   | `!HAS_BEARER` (TEST_BEARER env-gate)                                     | env-gate-legitimate          |
| `bff-cookie-production-build.spec.ts`  | 61   | `!RUN_AGAINST_PROD_BUILD` (ATLAS_PROD_BUILD)                             | env-gate-legitimate          |
| `logo-render-production-build.spec.ts` | 47   | `!RUN_AGAINST_PROD_BUILD` (ATLAS_PROD_BUILD)                             | env-gate-legitimate          |
| `risks-create.spec.ts`                 | 32   | `!process.env.PLAYWRIGHT_RUN_QUARANTINED`                                | env-gate-legitimate          |
| `risks-create-control-link.spec.ts`    | 31   | `!process.env.PLAYWRIGHT_RUN_QUARANTINED`                                | env-gate-legitimate          |
| `control-detail-tabs.spec.ts`          | 34   | NO active `test.skip` — documentation comment about slice 276 resolution | already-resolved (no action) |
| `questionnaires.spec.ts`               | 29   | NO active `test.skip` — documentation comment about slice 254 playbook   | already-resolved (no action) |

**Spillover slices filed: 0.**

All 6 actual `test.skip` calls are guarded behind documented env-gates
that trace back to the slice-082 seed harness or production-build
prerequisites. No test debt; the convention is correct as-is. Two of
the audit-named files (`control-detail-tabs.spec.ts`,
`questionnaires.spec.ts`) carry only documentation comments — no
active quarantines — and are already-resolved per slice 276 and slice 254.

**Cap-at-3 guidance NOT triggered** — 0 spillover slices is under cap.

**Confidence:** high. Every skip reads as a legitimate gate; none reads
as latent test debt.

### D-D3 — chromium-only browser matrix: re-confirm chromium-only for v1

**Decision:** Re-confirm. No code change.

**Reasoning:**

1. The audited surface (P-5) is the v1 ship matrix. v1 has shipped;
   the cost-benefit of adding firefox + webkit has not changed
   materially.
2. The dominant browser-specific bug class is the BFF surface. The
   security-atlas BFF runs on the `node` runtime (Next.js server
   components + route handlers), NOT in a browser engine. A chromium
   pass already covers the React render path for the largest engine
   share.
3. Adding firefox + webkit triples Playwright wall-clock in CI for a
   marginal additional bug surface. The slice 069 deferral remains
   correct.
4. **Revisit trigger:** firefox + webkit projects added in playwright
   config if and only if (a) a customer-reported browser-specific
   bug surfaces, or (b) the BFF surface gains a meaningful browser-side
   component-render path (currently it does not).

**Confidence:** high.

---

## Cluster E — Tracking docs

### D-E1 — Coverage `excludes` audit: read-only categorization, no lifts

**Decision:** Produce a categorized table at
`docs/audits/348-coverage-excludes-audit.md`. Categories per slice
instruction:

- (a) auto-generated (legitimate, keep)
- (b) integration-tested elsewhere (verify and document)
- (c) unaudited debt (file a coverage-lift slice)

**Scope discipline:** This is an AUDIT PASS only. Actual coverage lifts
are out of scope; each one is its own future slice per the
slice-069 monotonic-ratchet contract ("write the missing tests in the
SAME PR as the floor lift").

**Confidence:** high.

### D-E2 — Golden-file pattern: adopt for `buildBriefHTML` (slice-031 brief)

**Decision:** Add a golden-file test for `buildBriefHTML` using the
`testdata/board_brief.golden.html` pattern.

**Scope:** ONE golden file for the slice-031 brief. The existing
`pdf_html_test.go` `strings.Contains` assertions stay — golden is
ADDITIVE (additional coverage), not REPLACEMENT (the existing tests
catch missing-string regressions that a golden-file replace miss
during the update flow).

**Update mechanism:** `go test -update` style — add a `-update` flag
that rewrites the golden file. The PR review process catches
unintentional golden updates.

**Why not pack PDF too:** The slice-031 brief is the simpler
deterministic surface. The pack PDF render is broader (slice 032+) and
introduces more update-flow risk per round. Single golden establishes
the pattern; future polish rounds extend.

**Confidence:** medium. The golden-file pattern can be over-engineered;
the `strings.Contains` discipline already in place is honest about
what's actually asserted. The golden adds visual-shape coverage that
the contains-checks don't catch (whitespace, attribute order, tag
nesting). Worth the small surface investment.

### D-E3 — `internal/testpgx/` helper extraction: v3 task, not this slice

**Decision:** Document the `internal/testpgx/` extraction as a v3
follow-on. The full helper would unify:

- `appPool, adminPool` setup pattern (I-4)
- `pgErrForeignKeyViolation` SQLSTATE constants (I-5)
- per-package RLS-aware abstractions

**Why not now:**

1. The polish slice budget is 1.5d. A correct `testpgx` helper is a
   multi-day extraction with RLS-aware semantics that need their own
   review.
2. The existing duplication is functional. The drag is sustainability,
   not correctness — defer-able.
3. Slice 069's "informational ratchet" promise sat 250 slices before
   slice 347 retired it. Tracking notes work in this project.

**v3 task shape:**

- Slot: open (next infra slice slot)
- Estimate: 3-5d
- Type: AFK (mechanical extraction)
- ACs:
  - `internal/testpgx/sqlstates.go` centralizes SQLSTATE constants
  - `internal/testpgx/pools.go` exposes `NewAppPool(t)`,
    `NewAdminPool(t)`, `WithTenant(t, tenantID)` helpers
  - At least 3 packages migrated to the helper as proof-of-shape
  - The remaining ~20 packages have a tracked migration plan

**Confidence:** high — the deferral is the right call for budget; the
shape captured here is enough for a future implementer.

---

## Confidence summary

| Decision                                                  | Confidence |
| --------------------------------------------------------- | ---------- |
| D-A1 — `t.Parallel()` sweep scope (6 files in eval+oauth) | high       |
| D-A3 — `_TenIterations` → `_FiveIterations` rename        | high       |
| D-B1 — `-p 1` rationale 4-line augmentation               | high       |
| D-C1 — Vitest `include` glob collapse                     | high       |
| D-C2 — NextResponse mock sweep via shared helper          | high       |
| D-C3 — Test-bearer literal centralization                 | high       |
| D-D1 — `fullyParallel` experiment + protocol              | medium     |
| D-D2 — `test.skip` triage (0 spillover slices)            | high       |
| D-D3 — chromium-only browser matrix re-confirm            | high       |
| D-E1 — Coverage excludes audit categorization             | high       |
| D-E2 — Golden-file precedent: `buildBriefHTML` brief HTML | medium     |
| D-E3 — `internal/testpgx/` extraction deferred to v3      | high       |

`medium`-confidence decisions (D-D1 outcome, D-E2 scope) are the
revisit candidates after this slice merges and the next polish round
opens.

---

## Engineer-as-collaborator adjacent gaps surfaced

The audit script for the coverage `excludes` (U-2) surfaced one
adjacent gap that wasn't in the slice 334 audit:

- **`internal/proto/` is a stale `excludes` entry.** The directory
  doesn't exist on disk; the entry was likely left over from an
  earlier rename. Disposition: documented in the audit doc's
  "(e) MISSING — stale entry" section as a one-line cleanup candidate
  for a future polish round. NOT folded into slice 348 to keep the
  P0-348-5 "no slice-345/346/347 overlap" boundary clean — the
  cleanup belongs alongside the slice 345 enrolment-discovery
  primitive landing, not in this polish round.

No other adjacent gaps surfaced during the cluster sweeps. The
NextResponse mock helper extension to cover both the plain and
null-safe variants (D-C2) was anticipated; the variant discovery
during the sweep is what the audit predicted ("the helper signature
isn't a perfect drop-in" risk in PRD risks).
