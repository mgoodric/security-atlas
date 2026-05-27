# Slice 312 — decisions log

Round-3 coverage audit + lift judgement calls. Each D-decision is a JUDGMENT slice trade-off the engineer made and recorded inline (per `Plans/prompts/04-per-slice-template.md` JUDGMENT-slice discipline). Future maintainers reading this log should not need to re-derive these.

## Context

Slice 312 follow-on to slices 279 (round 1) and 281-311 (round 2). The audit ran against the slice-308 PR's CI merged-coverage artifact (run `26494738884`, merged at `824a3af2`). The full per-package enumeration lives in `docs/coverage-audit-2026-05-round-3.md`.

## D1 — Zero inline lifts; honest closure verdict

**Question.** Do we lift 0-5 packages directly in this PR, or close honestly with audit + thresholds-refresh only?

**Decision.** Zero inline lifts. **Honest closure (AC-8).**

**Rationale.**

- Every floored package is at-target merged ≥ 70%. Lifting any of them would be vanity ratchet (P0-312-2 violation).
- The remaining < 70% packages are ALL untracked (new since slice 279, added by slices 187-205 + auth-substrate-v2 + MCP work). They are new surface, not erosion on existing floors.
- The slice 279 + 281-311 precedent: untracked-with-gaps gets spillover treatment paired with the slice that introduced the surface. Inlining 5 of them here would mix the audit with feature-completion work — bad PR shape, bad git history.
- The two near-target candidates (`internal/audit/sink` @ 67.3%, `pkg/sdk-go` @ 67.6%) are each ~ 2-3pp short of 70 — small enough to lift inline, but doing so would set a "below-70% trigger" precedent that contradicts the round-3 framing ("audit the codebase + decide if more work is needed"). The disciplined answer: file the spillover, let the maintainer pick it up in a focused PR.

**Alternative considered + rejected.** Lift `pkg/sdk-go` inline (4-5 unit tests; 2.4pp gap; trivial). Rejected because: (a) it sets the precedent above, (b) the resulting PR is no longer "honest-closure" but "honest-closure-plus-one-lift" which is the same scope creep slice 279 sized against, (c) the spillover slice 321 captures it cleanly.

## D2 — New packages added to `excludes` (auto-generated tier)

**Question.** Which untracked packages join `excludes` (not measured) vs join `thresholds` (measured + floored)?

**Decision.**

- **Auto-generated `gen/proto/*` (4 packages).** Already covered by the `gen/proto/` directory entry in `excludes[]` — verified by inspection. **No change needed.** Documented in the audit doc for completeness.
- **Trivial single-statement packages (3 packages).** `catalogs/metrics` (1 stmt) → add to `excludes` for cleanliness. `internal/auth/keystore` (1 stmt, 100%) → floor at 98 in `thresholds` (it's a real public-API surface, just tiny). `internal/api/emptyset` (0 stmts) → add to `excludes` (no statements to cover; preserves the "list a package = it gets tested" invariant by NOT listing zero-statement packages).
- **CLI scripts (`cmd/scripts/coverage-check`, `cmd/scripts/coverage-gate`) and CLI binaries (`cmd/atlas-mcp`, `cmd/atlas-oscal`, `cmd/atlas-cli/cmdhttp`).** Already in `thresholds` (`cmd/atlas-cli/cmdhttp` is at floor 98) or fall under the existing cmd/atlas-* exempt tier. **Add `cmd/atlas-mcp` and `cmd/atlas-oscal` at floor 0** (matching the `cmd/atlas-cli` + `cmd/atlas` + `cmd/atlas-openapi` exempt-leaning posture). `cmd/scripts/*` add to `excludes` (one-shot CI tools; not the audit's job to gate them).

**Rationale.** The audit doctrine: every package is in exactly one of `thresholds`, `excludes`, or `go list` (untracked, awaiting triage). Round 3's refresh closes out the untracked column.

## D3 — Already-good NEW packages added to `thresholds` (12 packages)

**Question.** What floor for each new at-target untracked package?

**Decision.** `max(0, floor(merged_pct - 2pp))` per slice 069 P0-A4 — same formula used for all prior rounds.

| Package | Merged % | New floor |
| ------- | -------- | --------- |
| `internal/auth/jwt` | 95.5 | 93 |
| `internal/auth/jwtmw` | 84.7 | 82 |
| `internal/auth/keystore` | 100.0 | 98 |
| `internal/auth/keystore/fsstore` | 76.6 | 74 |
| `internal/auth/tokensign` | 74.5 | 72 |
| `internal/catalog` | 100.0 | 98 |
| `internal/export` | 83.9 | 81 |
| `internal/mcp` | 82.8 | 80 |
| `internal/mcp/tools` | 75.0 | 73 |
| `internal/platform` | 78.9 | 76 |
| `internal/api/securityheaders` | 100.0 | 98 |
| `internal/api/testjwt` | 95.2 | 93 |
| `pkg/sdk-go/oauth` | 86.4 | 84 |

13 packages (corrected from initial 12-count — `internal/auth/keystore` added separately at floor 98).

**Rationale.** These packages are AT-TARGET out of the gate. Floor them at the conservative -2pp band; if a future slice eroded coverage by 3pp+ the gate would catch it. The 2pp band absorbs noise (e.g. a conditional branch that depends on test order); 3+ pp is signal.

## D4 — Ratchet-up opportunities (9 packages)

**Question.** Which floored packages deserve a floor lift even though no new tests are written here?

**Decision.** Ratchet 9 packages where measured ≥ floor + 4pp. The 4pp threshold is conservative (slice 069's 2pp band PLUS a 2pp drift buffer); below 4pp, the existing floor is still within range and a ratchet would be premature.

| Package | Floor before | Merged | Floor after | Δ |
| ------- | ------------ | ------ | ----------- | - |
| `connectors/github/internal/githubscim` | 72 | 76.6 | 74 | +2 |
| `connectors/manual/internal/manualsftp` | 84 | 90.0 | 88 | +4 |
| `connectors/okta/internal/oktapolicy` | 69 | 74.6 | 72 | +3 |
| `connectors/osquery/internal/osqueryposture` | 73 | 77.2 | 75 | +2 |
| `internal/api` | 69 | 74.5 | 72 | +3 |
| `internal/api/controls` | 73 | 77.1 | 75 | +2 |
| `internal/api/schemaregistry` | 71 | 81.6 | 79 | +8 |
| `internal/control` | 72 | 78.5 | 76 | +4 |
| `internal/risk` | 71 | 79.5 | 77 | +6 |

**Rationale.** This is **NOT** a vanity ratchet (P0-312-2 forbids that, but only for packages that haven't been newly tested). The slice 069 ratchet contract — "raise a floor in a follow-up slice that (a) writes the additional tests to actually hit the new bar, (b) lifts the number here in the same PR" — is a hard rule for packages BELOW their measured. For packages ABOVE measured, the slice 069 methodology comment in the JSON says: "Each floor is set at `max(0, floor(measured - 2pp))` — the floor RATCHETS the current actual minus a 2-pp noise band". This is exactly that ratchet, applied retroactively to packages whose measured has drifted upward.

**Critically: each ratchet is BELOW measured (with ≥ 2pp buffer).** No package's new floor exceeds its current measured; no false-positive failures can result. The gate keeps passing.

**P0-312-1 satisfied** — every change is monotonic ↑.

## D5 — Spillover slices filed (9 slices, 313-321)

**Question.** How should the 21 untracked-with-gap packages be split into spillover slices?

**Decision.** 9 slices, grouped by shared pattern:

| Slice | Packages | Rationale |
| ----- | -------- | --------- |
| **313** | `adminauditperiods` + `adminsuperadmins` + `admintenants` + `adminvendors` + `tenants` (5 admin HTTP handlers) | All 5 are admin endpoints; all 5 need CI tests-integration-list enrollment per slice 290 pattern. Group preserves implementing-agent's cohesive surface knowledge. |
| **314** | `internal/api/oauth` (921 stmts, standalone) | Largest single surface (slice 187 OAuth AS family). Too big to bundle. |
| **315** | `oauthclient` + `oauthcode` + `revocation` + `userprefs` (4 auth-substrate-v2 small) | All slice 187+ auth surface; each < 100 stmts. Group cohesive. |
| **316** | `calendar` + `search` + `questionnaires` (3 HTTP handlers) | Each needs slice 290 integration-enrollment. Group cohesive. |
| **317** | `mcpwriteproposals` + `internal/mcp/writeproposals` (2 MCP write-prop) | Same feature surface (HTTP handler + inner logic). |
| **318** | `internal/audit` + `audit/sink` + `audit/unifiedlog` (3 audit ledger) | All audit-log family. Group cohesive. |
| **319** | `internal/questionnaire` (engine, 324 stmts) | Standalone — distinct from `internal/api/questionnaires` HTTP handler. |
| **320** | `internal/demoseed` (522 stmts, slice 205) | Data-heavy; lower priority; deserves its own focused slice. |
| **321** | `pkg/sdk-go` (37 stmts, 2.4pp gap) | Tiny gap; quick win. |

**Total spillovers: 9.** Within P0-312-5 cap of 10.

**Alternative considered + rejected.** File 21 single-package slices (one per row). Rejected because: (a) violates P0-312-5 cap, (b) creates orchestrator queue noise (each loop iteration would pick 3 of them and they have no cohesion), (c) the slice 279 precedent + this slice's spec carve-out explicitly permits grouping.

**Alternative considered + rejected.** File 1 mega-slice "lift all 21 untracked-with-gap packages". Rejected because: (a) mega-slice anti-pattern (slice 279 specifically discouraged this), (b) the 9-slice grouping respects feature cohesion (auth-substrate work vs MCP work vs audit-log work are separate concerns).

## D6 — Spillover registration in `_STATUS.md` (not `_INDEX.md`)

**Question.** Where to register the 9 new spillover slices?

**Decision.** `docs/issues/_STATUS.md` only. Per P0-312-4, do NOT touch `_INDEX.md` (orchestrator's surface). Each spillover gets a new row in the canonical Status table after slice 312's row, with status `ready` (since round-3 deps — slices 281-311 — are all merged).

## D7 — Measurement methodology: CI artifact vs local

**Question.** Should the audit re-run the full integration test suite locally, or read the merged-coverage artifact from CI?

**Decision.** Read the CI artifact. **Specifically: slice 308 PR's run 26494738884** — the most recent run with the post-batch-124 codebase that exercised the full `Go · integration (Postgres RLS)` job.

**Rationale.**

- The CI integration job already produces the authoritative merged profile (`gocovmerge unit.cov integration.cov > merged-coverage.txt`).
- Running locally would require bringing up Postgres + MinIO + NATS in the slice's worktree, applying migrations, setting role passwords — brittle and produces the SAME profile as CI (modulo Go version + race-detector ordering, both pinned). Net new information: zero.
- The CI artifact is downloadable via `gh run download <run-id> --name go-merged-coverage`. Reproducible by anyone reviewing this PR.
- The audit doc records the source run ID + commit SHA so future auditors can re-verify if needed.

**P0-312-6 satisfied** — the audit doc distinguishes unit-only % from merged % per package using both artifacts (unit profile + merged profile from CI).

## D8 — Excluded-with-substantial-coverage packages: not addressed in round 3

**Question.** 47 packages in `excludes[]` have measurable coverage ≥ 30%. Should round 3 move the highest-coverage ones (`anchors` @ 73.8%, `dashboard` @ 81.6%, `dashboardexport` @ 82.6%) out of `excludes` and into `thresholds`?

**Decision.** No — defer to round 4.

**Rationale.**

- Moving a package out of `excludes` and into `thresholds` is a one-way decision (the floor becomes a contract; lowering is forbidden). It requires per-package judgment about whether the integration coverage is DURABLE — i.e. will the next refactor of an integration test accidentally drop the coverage below floor?
- The audit doc lists the 5 highest-value candidates as future-audit reference, but each requires a focused mini-audit ("does this package's coverage depend on a single integration test that could be deleted? what's the smallest possible regression vector?").
- Round 3's framing is "is the floored set healthy?" — and the answer is yes. Auditing the excluded set is a round-4 question, properly scoped to a follow-on audit slice.

**Alternative considered + rejected.** Move all 47 to `thresholds` at their measured -2pp. Rejected: the 47 includes packages with 0.4% coverage (`internal/api/decisions`, `internal/api/walkthroughs`); floor 0 in `thresholds` adds noise without value.

**Alternative considered + rejected.** Move the top 5 high-coverage excludes (anchors / dashboard / dashboardexport / controlstate / policy/pdf). Rejected because each warrants per-package justification, and bundling 5 such moves into the round-3 audit PR pollutes the audit shape. File as round 4.

## Process notes

- **Time budget.** Audit + write + spillover-doc-generation completed within the slice's 1-2d estimate.
- **Engineer (this slice) made D1-D8 without human sign-off.** Per the JUDGMENT-slice discipline: build-time calls (which packages to lift, which to exempt, how to group spillovers) are the engineer's; the maintainer reviews the merged audit doc + decisions log post-deployment.
- **Constitutional invariants honored.** P0-312-1 through P0-312-6 all satisfied (verified in the audit doc's invariants section).
