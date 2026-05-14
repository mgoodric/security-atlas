# v1 Slice Status

> Live tracker. Companion to [`_INDEX.md`](./_INDEX.md) (static backlog spec).
> Updated by `Plans/prompts/04-per-slice-template.md` (per-slice) and `Plans/prompts/05-parallel-batch.md` (parallel batch). Run `Plans/prompts/06-status-reconcile.md` when drift is suspected.

**Last reconciled:** 2026-05-14 (batch 23 claim-stake — 040 + 055 + 064 → in-progress · 53/64 on main)

## Drift detected — 2026-05-14 (batch 23 claim-stake — 040 AFK + 055 AFK + 064 AFK)

Three slices flipped to `in-progress` for parallel batch 23. All AFK. Conflict-free trio:

- **040** (Program dashboard view) — `web/**`-only · zero overlap with any Go/infra/migration surface · the most isolated slice in the ready set.
- **055** (Decision Log CRUD + linkage) — `internal/decision/**` + `internal/api/**` + `cmd/atlas/main.go` (AC-6 daily job) · builds CRUD on slice 052's existing schema · **no migration**.
- **064** (Control-detail backend read endpoints) — `internal/api/**` read handlers · read-only over existing schema · **no migration**.

Shared touch-points are all the documented known-safe ones: `internal/api/httpserver.go` mount-append (055 + 064), `internal/db/dbx/*` sqlc-regen (055 + 064), `CHANGELOG.md` merge (all three). **No migration sequence allocated** — no pick adds a migration. No spine-file touches.

Skipped from the ready set: 030 (OSCAL export — `pyproject.toml` spine touch + 4-5d JUDGMENT, focused batch), 038 (Helm chart — possible `justfile` spine touch, leaf), 058 (docs scaffold — `justfile` spine touch + JUDGMENT 3d), 031 (clean + conflict-free but lowest downstream-unblock value of the four clean slices; N=3 cap).

Counts table corrected this claim-stake: the batch-22 final reconcile updated the drift/ready/in-flight sections but missed the `## Counts` table (it still read the pre-batch-22 50/3/3). Now reconciled to reality + this claim-stake.

| Row | Transition              | Evidence                                                                                                     |
| --- | ----------------------- | ------------------------------------------------------------------------------------------------------------ |
| 040 | `ready` → `in-progress` | batch 23 · branch `frontend/040-program-dashboard-view` · AFK · `web/**`-only · ~2.5d                        |
| 055 | `ready` → `in-progress` | batch 23 · branch `risk/055-decision-log` · AFK · no migration (builds on slice 052 schema) · ~2d            |
| 064 | `ready` → `in-progress` | batch 23 · branch `controls/064-control-detail-backend-endpoints` · AFK · no migration (read-only) · ~1.5-2d |

## Drift detected — 2026-05-14 (batch 22 merged — 016 + 020 + 041)

All three batch-22 slices land as `merged`:

| Row | Transition               | Evidence                                                                                                                                                                                                                                                      |
| --- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 016 | `in-progress` → `merged` | commit `6a34472` (gh#94) · evidence freshness + drift · migration `_028` (`evidence_freshness` four-policy RLS + `control_drift_snapshots` append-only) · drift = worst-cell rollup, stale-excluded, daily snapshots · decisions log committed                |
| 020 | `in-progress` → `merged` | commit `841647a` (gh#96) · risk→control linkage + residual derivation · migration `_029` (`risk_control_links`) · residual = inherent × (1 − weighted effectiveness) · `risk_residual_worker` durable consumer on `evidence.ingest` · decisions log committed |
| 041 | `in-progress` → `merged` | commit `6db7395` (gh#93) · control detail view + UCF mini-viz · 6/7 ACs (AC-4 PARTIAL — `GET /v1/evidence?control_id=` not on main) · 4 BFF proxies · decisions log committed                                                                                 |

**Newly unblocked → `ready`:** 031 (Monthly board brief — deps 012/016/020), 040 (Program dashboard view — deps 005/012/016/020/024), 055 (Decision Log CRUD — deps 052/020/021). All three had 016+020 as their last unmerged deps.

**New backlog slice → `ready`:** 064 (Control-detail backend read endpoints) — created from slice 041's decisions log. Slice 041 shipped 4 binding placeholders (evidence stream + linked-policies/risks/audit-log rail) because no per-control read endpoints exist on main; 064 fills them following the 060→062 precedent. `docs/issues/064-control-detail-backend-endpoints.md` added this reconcile.

**Merge queue notes.** All three worktrees were cut at `b77931f` — before the batch-22 claim-stake (#92) landed — so each rebased onto post-stake main, then re-rebased sequentially as the queue drained (93 → 94 → 96). 016 and 020 both append a migration, a `sqlc.yaml` entry, and wire a durable NATS consumer in `cmd/atlas/main.go`; 020's re-rebase resolved those as the documented "keep both" coexistence (sqlc auto-regen produced no `dbx/*` drift). Prettier re-pad of `_STATUS.md` was needed on the 016 and 020 status commits — long notes cells widened the table column past the committed alignment; `pre-commit run prettier` before push is the fix.

## Drift detected — 2026-05-14 (batch 22 claim-stake — 016 AFK + 020 AFK + 041 AFK)

Three slices flipped to `in-progress` for parallel batch 22. All AFK. Three disjoint clusters — evidence (016), risk (020), frontend (041) — zero production-code overlap, zero spine touches. Shared touch-points are the documented known-safe ones (sqlc `dbx/*` regen, `httpserver.go` mount-append, `CHANGELOG.md`).

Migration sequences allocated: `_028` (016 — freshness/drift read-model), `_029` (020 — `risk_control_links`). 041 adds no migration (pure frontend).

Skipped from the ready set: 030 (OSCAL export — 4-5d JUDGMENT, lands the first Python `oscal-bridge/` + a `pyproject.toml` spine touch; deserves a focused batch), 038 (Helm chart — leaf slice, lower critical-path value than 041), 058 (docs scaffold — JUDGMENT, `justfile` spine touch; capped out at N=3).

| Row | Transition              | Evidence                                                                                                                |
| --- | ----------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| 016 | `ready` → `in-progress` | batch 22 worktree `../security-atlas-016`, branch `evidence/016-evidence-freshness-drift`, AFK-clean, ~2d, slot `_028`  |
| 020 | `ready` → `in-progress` | batch 22 worktree `../security-atlas-020`, branch `risk/020-risk-control-linkage-residual`, AFK-clean, ~2d, slot `_029` |
| 041 | `ready` → `in-progress` | batch 22 worktree `../security-atlas-041`, branch `frontend/041-control-detail-view`, AFK-clean, ~2.5d, pure frontend   |

## Drift detected — 2026-05-13 (batch 21 merged — 012 keystone + 037 self-host)

Both batch-21 slices land as `merged`:

| Row | Transition               | Evidence                                                                                                                                                                                                                                                         |
| --- | ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 012 | `in-progress` → `merged` | commit `2a07bdc` (gh#89) · control state evaluation engine · `control_evaluations` append-only ledger (migration `_027`) · invariant 2 structurally enforced · the critical-path keystone                                                                        |
| 037 | `in-progress` → `merged` | commit `42660e9` (gh#88) · docker-compose self-host bundle · option-B scope call: ~50-line in-scope Go touch (`/health` route + `AttachAuthHandler` wiring) so the slice's own ACs pass — recorded in `docs/audit-log/037-docker-compose-self-host-decisions.md` |

**Slice 012 was the keystone — its merge cascaded 5 slices to `ready`:** 016 (Evidence freshness — dep 012), 020 (Risk→control linkage — deps 019/012), 030 (OSCAL SSP+POA&M export — deps 008/012/017/018/026/028 all now merged), 041 (Control detail view — deps 005/008/012). Slice 037's merge unblocked 038 (Helm chart — dep 037).

**Two grill-stalls this batch, both recovered with one resume each:** 012's engineer surfaced the `control_state` vs `control_evaluations` naming-drift question (resolved: append-only ledger, matches `evidence_audit_log` precedent); 037's engineer surfaced the deploy-only-scope-vs-own-ACs conflict (resolved: option B, the slice's own ACs are the boundary). Both were well-pre-answered design questions the engineers should have self-resolved per the JUDGMENT model rather than returning — the behavioral reflex persists despite the PR #62/#82 template hardening. Candidate for a stronger template revision: model the in-transcript "design question → my answer → continuing" shape explicitly.

**Scope correction logged:** the batch-21 claim-stake described 037 as "deploy config only." That held for conflict-prediction (012 + 037's `cmd/atlas/main.go` + `httpserver.go` overlaps auto-merged or were the known-safe mount-append kind), but 037 did touch `internal/api/` + `cmd/atlas/` for the ~50-line option-B Go touch. The planning-time `_STATUS.md` scope estimate was wrong; the slice's own ACs were the real boundary.

## Drift detected — 2026-05-13 (slice 012 → in-review)

Slice 012 (Control state evaluation engine) flipped `in-progress` → `in-review`. PR [gh#89](https://github.com/mgoodric/security-atlas/pull/89) opened against main. **7/7 ACs + 3/3 P0 anti-criteria PASS.** New `internal/eval` package — the evaluation stage (canvas §4.3): a read-only consumer of the slice-013 evidence ledger that computes `(control × scope_cell × time) → {pass, fail, na, inconclusive}` + `freshness_status` and appends to the new `control_evaluations` table (migration `_027`). **Constitutional invariant 2 enforced structurally** — the engine's only writer has one INSERT target (`control_evaluations`); no `evidence_records` write code exists, and the ledger is append-only at the RLS layer (slice 013). Point-in-time replay (AC-7) reproduces identical computed state because state derives purely from the immutable ledger. `state.go` holds the pure deterministic rollup (wall clock enters only as the freshness-window cutoff, never the result — AC-3 idempotency). Rego evidence-query path runs in slice 054's capabilities-restricted OPA sandbox (`http.send`/`net.*`/`opa.runtime` stripped, compile-time rejection). Two read endpoints `GET /v1/controls/{id}/state` (`?scope=` + `?as-of=`) + `/effectiveness` appended onto the platform router. AC-2 background job: a NATS `IngestSubscriber` (2nd durable consumer on slice 015's stream) + a time-based `Scheduler`, both wired in `cmd/atlas`. **Migration `_027`** `control_evaluations` — append-only ledger, FORCE RLS + `tenant_read`/`tenant_write` policies only, composite FKs for D3; up→down→up byte-clean; `audit-rls.sh` passes. **Naming-drift resolved** (grill-with-docs): the issue spec's literal `control_state` is superseded by `control_evaluations` — an append-only evaluation ledger is what makes AC-7's point-in-time replay meaningful and matches the `evidence_audit_log` / `aggregation_rule_evaluations` precedent; recorded in `CONTEXT.md`. **Verification:** `go build ./...` clean, `golangci-lint run` 0 issues, `pre-commit run --all-files` all passed, unit + integration tests pass with `-race` (DB never mocked), ship-gate CLEAR TO SHIP (0 critical / 0 high / 0 advisory). **CI** added `internal/eval` + `internal/api/controlstate` to the integration-test allowlist. **Time spent:** ~50 min end-to-end. **Surprises:** (1) `scope_cells` had no `(tenant_id, id)` composite key — added `scope_cells_tenant_id_unique` in `_027` so the cross-tenant-safe FK works; (2) the AC-7 replay test initially conflated the evidence horizon with the evaluation-row read horizon — pinning the evidence horizon while reading latest state via `FarFuture` is the honest semantics; (3) simplify pass caught 3 over-generated sqlc queries (removed).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 012 | `in-progress` → `in-review` | gh#89 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (batch 21 claim-stake — 012 AFK + 037 AFK)

Two slices flipped to `in-progress` for parallel batch 21. Both AFK. Conflict-safe: 012 is pure-Go evaluation engine (`internal/eval/*`, migration `_027`), 037 is deployment config (`deploy/docker/**` + the batch's one `justfile` spine touch). Zero production-code overlap.

Slice 058 (User docs scaffold) deliberately skipped — it's the only other `justfile`-touching ready slice, and batching it with 037 would violate the one-spine-touch-per-batch rule. It stays `ready` for the next batch.

| Row | Transition              | Evidence                                                                                                                       |
| --- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| 012 | `ready` → `in-progress` | batch 21 worktree `../security-atlas-012`, branch `controls/012-control-state-evaluation`, AFK-clean, ~1.5d, slot `_027`       |
| 037 | `ready` → `in-progress` | batch 21 worktree `../security-atlas-037`, branch `infra/037-docker-compose-self-host`, AFK-clean, ~2.5d, justfile spine touch |

## Drift detected — 2026-05-13 (batches 19 + 20 final reconcile + release-please fix)

Five slices land as `merged`, closing batches 19 (010 + 027 + 063) and 20 (042 + 054):

| Row | Transition             | Evidence                                                                                                                                                          |
| --- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 010 | `in-review` → `merged` | commit `1192b16` (gh#77) · 50 SOC 2 control YAML bundles · 43/43 TSC coverage · HITL signed "010 looks good" · unblocks 012, 037                                  |
| 027 | (already merged)       | commit `10de8dc` (gh#78) · walkthrough recording · unblocked 042                                                                                                  |
| 042 | `in-review` → `merged` | commit `fe86f9c` (gh#80) · audit workspace view · orchestrator-closed-out after 2 stalls · CodeQL js/xss-through-dom dismissed                                    |
| 054 | `in-review` → `merged` | commit `c3ce306` (gh#81) · aggregation rules engine · **first JUDGMENT-type slice merged with no sign-off gate** · OPA sandbox hardened (capabilities-restricted) |
| 063 | (already merged)       | commit `5f813a6` (gh#76) · `/admin/sso` form save                                                                                                                 |

**Newly unblocked → `ready`:** 012 (Control state evaluation engine — deps 010/013/017 all merged), 037 (docker-compose self-host — deps 010/034 merged), 058 (User docs scaffold — deps 005/050 merged; was ready-eligible, never flipped).

**Release 1.0.0 shipped** (PR #59, commit `1963cee`). The release-please CI deadlock — GitHub's anti-recursion rule blocking the CI matrix on `GITHUB_TOKEN`-authored release branches — was fixed via a GitHub App token (`actions/create-github-app-token`, with a `GITHUB_TOKEN` fallback) in `.github/workflows/release-please.yml` (gh#83, commit `ecf2289`). App is created + installed; `RELEASE_PLEASE_APP_ID` var + `RELEASE_PLEASE_APP_PRIVATE_KEY` secret are set. `.prettierignore` added for `CHANGELOG.md` (ends the recurring prettier-CHANGELOG CI failures). Future releases need no manual intervention.

**Process change (gh#82, commit `9c01581`):** dev-process `HITL` slice type replaced with `JUDGMENT` — Claude makes subjective build-time calls + writes a decisions log; no human sign-off gate. The product's constitutional runtime AI-assist boundary is untouched. Slice 054 is the first slice merged under the new model.

## Drift detected — 2026-05-13 (batch 20 claim-stake — 042 AFK + 054 HITL · slice 027 + 063 carry-over reconcile)

Two slices flipped to `in-progress` for parallel batch 20. Also implicit reconcile of batch 19's 027 + 063 (merged 2026-05-13 at `10de8dc` + `5f813a6`); slice 010 stays in-review pending HITL signoff on the 50-control spot-check log.

Zero production-code overlap between 042 and 054 — pure Next.js frontend (042) vs pure Go backend (054):

- **042** — Audit workspace view (~2.5d AFK). Next.js + shadcn/ui · `web/app/audit/**` + `web/components/audit/**` + `web/lib/api/audit.ts` + Playwright spec. Binds slice 025 (auditor period), slice 026 (populations + samples), slice 027 (walkthroughs), slice 029 (threaded comments).
- **054** — Declarative aggregation rules engine (~3d HITL). Migration slot `_026` · `internal/risk/aggregation/*` + `internal/api/aggregation_rules/*` + sqlc · OPA Rego DSL for rule activation · binds slice 052 (risk hierarchy) + slice 053 (theme tagging).

Migration slot `_026` allocated to 054. 042 adds zero migrations.

HITL load this batch: ONE new HITL (054 rule activation) plus the pending 010 spot-check. Slice 058 deliberately deferred to a future batch to avoid stacking three HITLs concurrently.

| Row | Transition                  | Evidence                                                                                                                         |
| --- | --------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| 042 | `not-ready` → `in-progress` | batch 20 worktree `../security-atlas-042`, branch `frontend/042-audit-workspace-view`, AFK-clean, ~2.5d                          |
| 054 | `not-ready` → `in-progress` | batch 20 worktree `../security-atlas-054`, branch `risk/054-aggregation-rules-engine`, HITL on rule activation, ~3d, slot `_026` |

## Drift detected — 2026-05-13 (slice 063 → in-review)

Slice 063 (Enable `/admin/sso` form save) flipped `in-progress` → `in-review`. PR gh#76 opened against main. **9/9 ACs + 4/4 P0 anti-criteria PASS.** New BFF proxy at `web/app/api/admin/sso/route.ts` (GET + PATCH) mirroring slice 060's credentials proxy pattern (auth-header forwarding + slice 051 D1 tenant_id strip + empty client_secret strip so upstream "leave existing" branch fires). `web/app/admin/sso/page.tsx` refactored to TanStack Query (useQuery GET pre-fill + useMutation PATCH + invalidateQueries re-fetch on success). Submit-button state machine: idle / submitting / success / error. Success Alert auto-dismisses ~3s; error Alert renders backend `error` field verbatim and preserves user input. **client_secret stays write-only end-to-end** — `type="password"` + `autoComplete="new-password"`, GET response omits it (slice 062 GetResponse struct has no client_secret field), input wiped after successful save. **Playwright E2E extended** under existing ifPlaywright shim with fill / submit / reload assertions including the critical write-once check that the client_secret input is empty after reload. **Pre-commit clean** — prettier auto-fixed once on first run (the known #1 failure mode), clean on re-run. **TypeScript clean**, **ESLint clean**, **Next build succeeds**. **Zero backend / migration / go.mod edits** (P0-4). **HITL audit log appended** at `docs/audit-log/admin-ui-review.md` documenting that the slice 060 stopgap is lifted and the slice 060 HITL signoff carries forward (no new HITL required — this slice wires up an already-reviewed surface). **CHANGELOG** entry under `[Unreleased]/Changed`. **Constitutional invariants honored**: #6 RLS (admin gate inherited from slice 060 layout + slice 062 requireAdmin defense-in-depth), slice 034 AC-9 (write-once secret), AI-assist boundary (every submit is a human click). **Time spent:** ~25 min end-to-end. **Surprises:** (1) initial `useEffect` form-seed pattern tripped `react-hooks/set-state-in-effect` lint; switched to the React 19 "store previous value in state" pattern (`seededFrom` tracker, sync-during-render only on identity change). (2) Decision to drop the slice 060 "Provider name" form field — slice 062's handler hardcodes `name='primary'` for the v1 single-IdP model, so the field would be inert; surfaced `Issuer URL` instead which IS user-supplied. Documented in HITL log.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 063 | `in-progress` → `in-review` | gh#76 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (batch 19 claim-stake — 010 HITL + 027 AFK + 063 AFK)

Three slices flipped to `in-progress` for parallel batch 19. Zero production-code overlap — three perfectly disjoint surfaces:

- **063** — frontend admin/sso form save wire-up (~0.5d AFK · `web/app/admin/sso/*` + new BFF proxy + Playwright E2E)
- **027** — walkthrough recording (~2d AFK · migration slot `_025` · S3 multipart + canonical sha256 hash + PDF render · possible go.mod touch for PDF lib)
- **010** — SCF-anchored control kit (~5-7d HITL · 50 YAML bundles in new `controls/` dir + review log at `docs/audit-log/control-kit-review.md`)

Wall-clock dominated by 010 (~5-7d). 063 and 027 will finish first and sit in-review waiting for 010. HITL spot-check on slice 010 will be the biggest reviewer-time ask of the session.

Migration slot `_025` allocated to 027. Slice 010 adds zero migrations (pure YAML authoring). Slice 063 adds zero migrations.

| Row | Transition              | Evidence                                                                                                                      |
| --- | ----------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| 010 | `ready` → `in-progress` | batch 19 worktree `../security-atlas-010`, branch `control-as-code/010-soc2-control-kit`, HITL on 50-control accuracy, ~5-7d  |
| 027 | `ready` → `in-progress` | batch 19 worktree `../security-atlas-027`, branch `audit/027-walkthrough-recording`, AFK-clean, ~2d, slot `_025`              |
| 063 | `ready` → `in-progress` | batch 19 worktree `../security-atlas-063`, branch `frontend/063-admin-sso-form-enable`, AFK-clean, ~0.5d, removes 060 stopgap |

## Drift detected — 2026-05-13 (slice 063 added — SSO form save wire-up)

Surfaced by slice 060's merge with the form save-wiring stopgap. Slice 062 shipped the backend endpoint; slice 063 is the thin frontend slice that flips the `disabled` attribute and wires the `onSubmit` handler. Deps 060 + 062 both merged on 2026-05-13. AFK-clean, ~0.5d.

| Row | Transition      | Why                                                                                                          |
| --- | --------------- | ------------------------------------------------------------------------------------------------------------ |
| 063 | (new) → `ready` | Enable `/admin/sso` form save · spawned by slice 060 stopgap · deps 060 + 062 merged · completes v1 admin UX |

**Counts delta:** total +1 · ready +1.

## Drift detected — 2026-05-13 (slice 060 → merged with HITL signoff)

Slice 060 (Admin settings UI) merged at `42c3a79` on main. HITL signed off by Matt Goodrich 2026-05-13 with comment "60 looks good to me." All 10 ACs PASS (the previously-PARTIAL AC-2 / AC-3 / AC-6 flipped to PASS once slice 062 landed the backend), all 5 P0 anti-criteria PASS. UI shells + BFF proxies + 5 admin pages + Playwright E2E spec all on main.

The form save-wiring (currently disabled on `/admin/sso` per the stopgap) is a follow-up slice — slice 062 shipped the backend, but the form's `disabled` attribute and `onSubmit` handler need a thin frontend slice to enable the save path now that the backend exists.

| Row | Transition             | Evidence                                                                                                                                                                                                                            |
| --- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 060 | `in-review` → `merged` | commit `42c3a79` on main (gh#66 squashed 2026-05-13; 10/10 ACs + 5/5 P0; HITL signoff captured at `docs/audit-log/admin-ui-review.md` from Matt Goodrich — "60 looks good to me"; rebased onto post-batch-18 main without conflict) |

## Drift detected — 2026-05-13 (batch 18 merged — 029 AFK + 062 AFK · slice 060 backend gap closed)

Two slices flipped to `merged`. Slice 060's backend dependency closed — its 3 PARTIAL ACs (AC-2 SSO, AC-3 Users, AC-6 audit-log) now bind to endpoints on main via slice 062. PR gh#66 needs only a rebase to flip those ACs to PASS, then your HITL signoff (role-permission matrix + SSO config UX + flag descriptions) before merge.

Two operational notes from this batch:

1. **Engineer-029 stalled with a new shape:** announced "Now invoking database-designer..." as the final line and the Agent runtime ended. Different from the earlier "return grill output as final report" pattern. One explicit resume directive recovered it cleanly. Worth updating the per-slice template to also forbid the "announce-and-stop" pattern.

2. **CodeQL caught a real SSRF in slice 062's OIDC preflight handler** — a TOCTOU window between `guardSSRF()` pre-check and `client.Do()` dial allowed DNS rebinding + redirect-based bypass. Fixed via `newSafeHTTPClient()` whose `Transport.DialContext` re-validates the resolved IP at connect-time and refuses HTTP redirects. Alert #13 dismissed via gh CLI with explicit justification — CodeQL's data-flow taint analysis can't model the custom Transport's safety property. The actual security fix is in the code (3-layer defense in depth: handler-level pre-check + dial-time re-check + redirect refusal).

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                   |
| --- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 062 | `in-review` → `merged` | commit `671407f` on main (gh#70 squashed 2026-05-13; 10/10 ACs + 5/5 P0; admin_audit_log_v UNION ALL across 7 audit-log tables; 22 integration tests; SSRF-hardened OIDC preflight via Transport.DialContext IP re-check + redirect-disabled; CodeQL alert #13 dismissed with security justification)                      |
| 029 | `in-review` → `merged` | commit `a335e40` on main (gh#71 squashed 2026-05-13; 6/6 ACs + 3/3 P0; migrations `_023` audit_notes_threading converting to append-only + `_024` notifications spine; ListThreadForScope recursive CTE; in-app notification dispatch; slice 025's grc_engineer-deny test inverted to slice 029's allow per design change) |

## Drift detected — 2026-05-13 (batch 18 claim-stake — 029 AFK + 062 AFK)

Two slices flipped to `in-progress` for parallel batch 18:

- **062** (Admin BFF backend endpoints) — spawned by slice 060's PARTIAL AC analysis. Adds `/v1/admin/sso`, `/v1/admin/users`, `GET /v1/admin/audit-log` (UNION ALL view across 7 audit-log source tables in new migration `_022_admin_audit_log_view`). Unblocks slice 060's final merge.
- **029** (Audit Hub threaded comments) — extends slice 025's `audit_notes` table (migration `_023_audit_notes_threading`): adds `parent_note_id` self-FK, swaps the `visibility` CHECK from `auditor_only` to allow `shared`, adds `walkthrough` to scope_type if not already present, adds notification dispatch (in-app channel as minimum viable).

Conflict-safety: zero overlap on production Go packages. 062 lives in `internal/api/admin/*`; 029 extends `internal/audit/notes/*` + `internal/api/auditnotes/*`. Different OPA Rego files. Different migration slots (`_022` vs `_023`). sqlc regen is the only shared artifact, resolved post-rebase per playbook.

| Row | Transition              | Evidence                                                                                                                      |
| --- | ----------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| 062 | `ready` → `in-progress` | batch 18 worktree `../security-atlas-062`, branch `admin/062-admin-bff-backend-endpoints`, AFK-clean, ~1.5d, slot `_022`      |
| 029 | `ready` → `in-progress` | batch 18 worktree `../security-atlas-029`, branch `audit/029-audit-hub-comments`, AFK-clean, ~1.5d, slot `_023` (extends 025) |

## Drift detected — 2026-05-13 (batch 17 partial reconcile — 025 merged · 060 waterfall on 062)

Slice 025 (Auditor role) merged at `ec431ec` on main. 6/6 ACs + 3/3 P0 anti-criteria green. Slice 060 (Admin settings UI) PR gh#66 is CI-green (12/12) but **blocked from merge** pending two prerequisites:

1. **Slice 062 — Admin BFF backend endpoints** (new, spawned from 060's PARTIAL ACs): three missing endpoints surfaced during 060's build — `/v1/admin/sso` (CRUD), `/v1/admin/users` (list + roles PATCH), `GET /v1/admin/audit-log` (union view across 7 source tables). 060's frontend wire-shape contracts are committed as a binding spec for this slice. AFK-clean, ~1.5d.
2. **HITL spot-check on slice 060** — three sign-off items in `docs/audit-log/admin-ui-review.md` (visible after 060 merges, but reviewable from the PR diff now): role-permission matrix · SSO callback URL preflight · feature-flag descriptions.

Per the design call: waterfall block on 060 — don't merge until both 062 lands and HITL signs off. Slice 060 PR stays open in `in-review` state with a fleeting longer-than-usual window. Total count moved from 61 → 62 with slice 062 addition.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                        |
| --- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 025 | `in-review` → `merged` | commit `ec431ec` on main (gh#67 squashed 2026-05-13; 6/6 ACs + 3/3 P0; `auditor_assignments` + `audit_notes` 4-policy RLS; OPA `auditor.rego` with `auditor_period_matches`; AttrsResolver hot-path auditor-only; query-layer visibility filter resolves slice-035 grc_engineer read collision) |
| 060 | —                      | stays `in-review` · WATERFALL on slice 062 + HITL signoff · PR gh#66 open and CI-green; 7/10 ACs PASS, 3/10 PARTIAL (await 062), 5/5 P0 PASS                                                                                                                                                    |
| 062 | (new) → `ready`        | spawned by 060's PARTIAL AC analysis · backend endpoints for `/v1/admin/sso` + `/v1/admin/users` + `/v1/admin/audit-log` · unblocks 060 final merge · AFK-clean · ~1.5d                                                                                                                         |
| 027 | `not-ready` → `ready`  | **NEWLY UNBLOCKED** · deps 025, 036 both merged · Walkthrough recording                                                                                                                                                                                                                         |
| 029 | `not-ready` → `ready`  | **NEWLY UNBLOCKED** · dep 025 merged · Audit Hub threaded comments                                                                                                                                                                                                                              |

## Drift detected — 2026-05-13 (slice 025 → in-review)

Slice 025 (auditor role + scoped read-only access) flipped `in-progress` → `in-review`. PR gh#67 opened against main. **9/9 ACs + P0 anti-criteria green** (6 ACs + 3 P0). Two new tables under migration `20260511000021_audit_notes.sql`: `auditor_assignments` (composite PK, 4-policy RLS under FORCE, composite FK to `audit_periods(tenant_id, id)`) drives the OPA ABAC attribute `input.user.attrs.audit_period_ids` via a new `internal/audit/auditor.DBAttrsResolver` hooked into a backwards-compatible `authz.Engine.WithAttrsResolver` setter; `audit_notes` (4-policy RLS under FORCE, CHECK on `scope_type ∈ {control,finding,sample,period}`, CHECK on `visibility = 'auditor_only'` to pin the §8.5 deferral) backs `POST/GET /v1/audit-notes`. Query layer enforces `author_user_id = caller.UserID` so auditees who hit the GET endpoint get an empty list (P0-2 visibility is at the query layer, not OPA — grc*engineer's tenant-wide read in slice 035 still fires the rego allow, but the data layer denies). `GET /v1/me/audit-period(s)` returns the auditor's assignment(s) (AC-5 + AC-6); ordered by `period_start DESC`. **Default-deny is the AC-3 mechanism** — no allow rule fires for auditor on any mutation; `TestSlice025_AuditorMutationsDenied` tables 9 endpoints (POST risks/policies/exceptions/vendors/audit-periods/:upload-bundle, PATCH submit/approve, POST freeze). **OPA Rego** mirrored to both `policies/authz/auditor.rego` and `internal/authz/rego_bundle/auditor.rego`; new rules: audit-notes read/write (write requires `auditor_period_matches`), `/v1/me` read, `audit-periods` added to `auditor_readable_resources`. **AttrsResolver hot path is auditor-only** — non-auditor requests scan the roles slice once and skip the DB hit (no latency regression for the 99% case). **NewEngine signature unchanged** — slice 035 callers and existing decision/matrix tests compile without modification. **Tests:** 4 unit (AttrsResolver hook), 9 rego-level (slice025_test.go), 8 integration (notes/integration_test.go covering AC-4, AC-5, AC-6, P0-2, P0-3, idempotent assignment, AttrsResolver wire shape). **Migration round-trip parity verified** (up → down → up restores byte-identical state). **Slice 028 integration tests still pass** after migration round-trip. **CHANGELOG** entry under `[Unreleased]/Added`. **Pre-commit clean** — prettier auto-fixed CHANGELOG once on first run (the known #1 failure mode), clean on re-run. **golangci-lint** 0 issues. **No vendor token prefixes** in test fixtures (no okta*/ghp*/AKIA/AIza/eyJ/sk*/xox\*-/ya29./ops\_). **Constitutional invariants honored:** #6 (4-policy RLS under FORCE on both new tables); #10 (notes pin to audit_period_id; auditor reads flow through the slice-026/028 frozen-horizon predicate — this slice adds no new horizon predicate). **Open questions surfaced** (recorded on PR): (1) auditor read of slice-028's `GET /v1/audit-periods/{id}` direct endpoint stays admin-only at the handler-level `canWrite` check — out of scope; auditor reads route through `/v1/me/audit-period(s)`; (2) `audit_notes_visibility_chk` requires a future migration to support the §8.5 shared auditor-auditee thread; (3) per-request AttrsResolver cache is a v2 nicety — v1 hits DB once per auditor request. **Time spent:** ~28 min end-to-end (PRD + grill + tests + migration + integration + CHANGELOG + commit + PR). **Surprises:** (1) grc_engineer's slice-035 tenant-wide read fires the OPA allow rule for audit-notes; design pivoted to enforce P0-2 at the query layer (author_user_id filter) rather than restructure the auditee role — cleaner because the empty-list response is functionally equivalent to "cannot see auditor's notes." Test renamed `TestSlice025_GRCEngineerReadAuditNotesAllowedButFiltered` to document the design choice. (2) Pre-commit prettier auto-fixed CHANGELOG once on first run as predicted by the playbook. (3) Fresh PG container needed for integration tests (slot 5460 chosen to avoid colliding with 5455/5440/5433 from prior worktrees).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 025 | `in-progress` → `in-review` | gh#67 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (batch 17 claim-stake — 025 AFK + 060 HITL)

Two slices flipped to `in-progress`. Zero overlap on production code paths (025: `internal/audit/notes/*`, `internal/api/auditnotes/*`, migration `_021`, new `policies/authz/auditor.rego`, sqlc; 060: `web/app/admin/**`, `web/components/admin/**`, `web/lib/api/admin.ts`, `web/e2e/admin-bootstrap.spec.ts`). Backend slice vs pure frontend slice — cleanest possible pairing for N=2.

Migration slot `_021` allocated to 025 (audit_notes table for AC-4). 060 adds zero migrations.

Slice 010 (SOC 2 control kit) deliberately deferred — 5-7d HITL on 50-control accuracy review is a focused-session slice, not a parallel-batch candidate.

| Row | Transition                  | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| --- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 025 | `ready` → `in-progress`     | batch 17 worktree `../security-atlas-025`, branch `auth/025-auditor-role-scoped-access`, AFK-clean, ~1.5d, slot `_021`                                                                                                                                                                                                                                                                                                                                                   |
| 060 | `ready` → `in-progress`     | batch 17 worktree `../security-atlas-060`, branch `frontend/060-admin-settings-ui`, HITL (role-permission matrix + SSO + flags), ~3d                                                                                                                                                                                                                                                                                                                                     |
| 060 | `in-progress` → `in-review` | gh#66 opened 2026-05-13 on `frontend/060-admin-settings-ui`; 7/10 ACs PASS (AC-1/4/5/7/8/9/10), 3/10 BLOCKED-BY-BACKEND (AC-2 SSO CRUD, AC-3 user list, AC-6 unified audit-log — see PR description for slice 060.5 follow-up); 5/5 P0 anti-criteria PASS; admin layout + 5 sub-area pages + BFF proxies for slice 034 + slice 059; HITL log at `docs/audit-log/admin-ui-review.md` awaiting Matt sign-off on role-permission matrix + SSO preflight + flag descriptions |

## Drift detected — 2026-05-13 (batch 16 merged — 028 AFK)

Slice 028 (AuditPeriod + freezing primitive) merged at `0ceea9a` on main. 7/7 ACs + 3/3 P0 anti-criteria all green. **ADR 0003** published documenting the hash-input strategy (content-only; `frozen_at` recorded alongside but excluded from hash so re-hash idempotence holds). Migration slot `_020` reversible (down-migration tested in CI's round-trip step).

**Slice 025 (Auditor role) is now actually shippable** — original ask from the user — since the `audit_periods` table it references for AC-2/4/5/6 now exists on main. Recommend slice 025 as the next batch.

Engineer stalled after grill-with-docs (same pattern as engineer-061 in batch 15). One explicit resume directive got it past the gate. Pattern is now confirmed twice — worth a doc PR updating the per-slice template to make the anti-stall rule the literal first line of the agent brief.

Mid-build, a separate PR (#57 — Go coverage + Codecov, plus setup-go 1.25 → 1.26 bump) merged ahead of slice 028. Slice 028's branch needed a trivial rebase (CHANGELOG `[Unreleased]/Added` interleave); CI re-fired clean.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                               |
| --- | ---------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 028 | `in-review` → `merged` | commit `0ceea9a` on main (gh#58 squashed 2026-05-13; 7/7 ACs + 3/3 P0; `audit_periods` 4-policy RLS + `audit_period_audit_log` append-only; deterministic sha256 hash over content-only inputs; freeze idempotence + 409 on re-freeze; slice 026 populations honor `frozen_at` via the column slice 026 already added) |

## Drift detected — 2026-05-13 (batch 16 claim-stake — 028 AFK)

Slice 028 (AuditPeriod + freezing primitive) flipped from `not-ready` to `in-progress`. Stale `waits on 013, 016` dep note corrected — slice 028's own doc explicitly says #016 was DROPPED per D6 review decision (freezing uses raw `observed_at` from the ledger, not the freshness read-model). Only dep is #013, which is merged.

This batch was prompted by a surfaced gap: AFK N=1 on slice 025 (Auditor role) surfaced that its AC-2/4/5/6 reference `audit_period` machinery that doesn't exist on main yet. Per the design call: ship 028 first, then 025 in a follow-up batch. Serial; total ~3.5d.

| Row | Transition                  | Evidence                                                                                                                                                                        |
| --- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 028 | `not-ready` → `in-progress` | dep #013 merged · #016 dep dropped per slice doc · worktree `../security-atlas-028`, branch `audit/028-audit-period-freezing`, AFK-clean, ~2d                                   |
| 028 | `in-progress` → `in-review` | gh#58 opened 2026-05-13 · 7/7 ACs + 3/3 P0 anti-criteria green · migration 20260511000020 reversible · ADR 0003 (hash inputs) published · all 10 integration tests pass locally |

## Drift detected — 2026-05-13 (batch 15 merged — 059 AFK + 061 AFK)

Two slices flipped to `merged`. **Slice 060 (Admin settings UI) newly unblocked** — deps `#005, #034, #035, #059` are now all `merged`. Batch 15 was AFK N=2; both slices AFK-clean. Surprises: (1) engineer-061 stalled after grill-with-docs; recovered with one explicit resume; (2) slice 061's PR hit the known prettier-on-docs pattern (one-character-style fix on `docs/ci/PATH_FILTERING.md`); (3) slice 059 needed rebase against post-061 main with a CHANGELOG conflict (resolved per playbook — both bullets in `[Unreleased]/Added`).

**Slice 061 went live on main BEFORE the final reconcile**, so the final-reconcile PR for THIS batch is the first PR to benefit from the new path-filter optimization — the docs-only edit should resolve all 6 expensive jobs in stub-pass mode (<30s each) and only CodeQL + GitGuardian + pre-commit will run full. Expected billable minutes: ~2 vs the ~10 that prior reconcile PRs consumed.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                     |
| --- | ---------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 061 | `in-review` → `merged` | commit `e48c9c2` on main (gh#52 squashed 2026-05-13; 9/9 ACs; 4/4 P0 anti-criteria; dorny/paths-filter@v3 + stub-job name-match pattern; touched .github/workflows/ci.yml + docs/ci/PATH_FILTERING.md + Plans/canvas/09-tech-stack.md §9.6 + CHANGELOG.md; resolved one prettier auto-format iteration on PATH_FILTERING.md) |
| 059 | `in-review` → `merged` | commit `ad3fc09` on main (gh#54 squashed 2026-05-13; 10/10 ACs; 4/4 P0 anti-criteria; `feature_flags` 4-policy RLS + `feature_flag_audit_log` append-only; 12 seed flags; `featureflag.Enabled`/`Gate` + admin API + `atlas-cli features` CLI; rebased onto post-061 main with CHANGELOG `[Unreleased]/Added` integration)   |
| 060 | `not-ready` → `ready`  | deps 005, 034, 035, 059 all `merged` (HITL on role-permission matrix)                                                                                                                                                                                                                                                        |

## Drift detected — 2026-05-13 (batch 15 claim-stake — 059 AFK + 061 AFK)

Two slices flipped to `in-progress` for parallel batch 15. Both AFK-clean, zero file overlap on production code paths (059: `internal/featureflag` + migration slot `_019` + sqlc + http handlers + CLI; 061: `.github/workflows/**` + `docs/ci/` + 2-line canvas edit). Migration sequence `20260511000019` allocated to 059. 061 ironically can't help its own PR — but if 061 lands before the final reconcile, the reconcile PR gets the ~80% billable-minute savings.

| Row | Transition              | Evidence                                                                                      |
| --- | ----------------------- | --------------------------------------------------------------------------------------------- |
| 059 | `ready` → `in-progress` | batch 15 worktree `../security-atlas-059`, branch `spine/059-feature-flags`, AFK-clean, ~1.5d |
| 061 | `ready` → `in-progress` | batch 15 worktree `../security-atlas-061`, branch `ci/061-path-filter`, AFK-clean, ~0.5d      |

## Drift detected — 2026-05-13 (batch 14 merged — 023 AFK + 035 HITL)

Two slices flipped to `merged`. **Slice 025 (Auditor role + scoped read-only access) newly unblocked** — deps `#033, #035` are now both merged. First batch under the branch-protection-via-PR pattern, completed end-to-end (claim-stake PR #45 → slice PRs #47/48 → this final-reconcile PR). Slice 035 was the first HITL pair-review with ZERO agent stalls — the explicit anti-stall briefing pattern continues to land cleanly.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                                                                                |
| --- | ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 035 | `in-review` → `merged` | commit `1941a1c` on main (gh#47 squashed 2026-05-13; 7/7 ACs incl HITL-signed AC-7; 5 roles + 10 Rego + decision audit log; OPA v1.16.2 spine touch; zero agent stalls)                                                                                                                                                                                                                 |
| 023 | `in-review` → `merged` | commit `456d9e3` on main (gh#48 squashed 2026-05-13; 6/6 ACs + 3/3 P0 anti-criteria; slot `_017`; `policy.acknowledgment.v1` evidence_kind added to DefaultSeed; post-public-flip CI cycle surfaced 5 mechanical fixes — pgx type-inference, nil ownerRoles, predecessor_id collision with slice-022 partial UNIQUE, integer-castable versions for slice-022 down-migration round-trip) |
| 025 | `not-ready` → `ready`  | deps 033, 035 `merged`                                                                                                                                                                                                                                                                                                                                                                  |

**Slice-022 follow-up surfaced by slice 023:** the subsequent-publish path in `internal/policy/store.go` (line 354+) INSERTs a new row with `predecessor_id = v1ID`; combined with a staging row's same `predecessor_id`, the `policies_predecessor_unique_when_set` partial UNIQUE blocks the second insert. Slice 023's test helper sidesteps this via direct admin DB SQL. Tracked as a v1.x slice-022 follow-up — slice 022 publishing past v2 will hit the UNIQUE.

**Counts delta:** merged +2 · in-review −0 · in-progress −2 · ready +1 (slice 025) · not-ready −1.

## Drift detected — 2026-05-13 (slice 023 → in-review, archived)

Slice 023 (policy acknowledgment workflow + role-required attestation) flipped `in-progress` → `in-review`. PR gh#48 opened against main. **6/6 ACs + 3/3 P0 anti-criteria PASS.** Ships migration `_017_acknowledgments` (new `policy_acknowledgments` table under FORCE RLS + four-policy split + two composite FKs + partial UNIQUE idempotency + three indexes; adds `UNIQUE (tenant_id, id)` to `users` as composite FK target), three HTTP routes appended per the Mount-append convention (`GET /v1/me/acknowledgments`, `POST /v1/policies/{id}/acknowledge`, `GET /v1/policies/{id}/acknowledgment-rate`), new evidence kind `policy.acknowledgment.v1` (schema JSON + `DefaultSeed` entry), and the slice-013 ingest integration that emits one evidence record per ack with `control_id = "policy:<policy_id>:v<policy_version_id>"` so the ledger stores it in `control_ref` only. Annual recurrence (365 d) computed at READ time via `LEFT JOIN LATERAL` in `ListPendingAcksForUser` — no cron, no N+1. Rate denominator uses the slice-034 stand-in (`api_keys.owner_roles + is_admin`) with a `TODO(slice-035)` marker; the orchestrator should expect slice 035's OPA-RBAC graduation to replace that query in the same surface — no follow-up required ahead of that. New test-only bridge: `credstore.Store.RebindUserIDForTests` + `api.Server.RebindBearerUserIDForTests` lets bootstrap creds bind to seeded `users` rows so the composite FK passes (slice-034 OIDC callback handles this in production). pre-commit clean; golangci-lint 0 issues. **Time spent:** ~80 min end-to-end (rebase + grill + PRD + Go code + migration + sqlc regen + ship-gate + CHANGELOG + commit + PR + status flip).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 023 | `in-progress` → `in-review` | gh#48 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (slices 059 + 060 added to backlog)

Two new slices added per maintainer request: per-tenant feature flags + capability toggles (059) and an in-app admin settings UI (060). Rationale: adopters often have existing tools that cover some capability areas (OneTrust for vendor, Jira for risk, etc.); forcing every capability on is a false-binary that drives them off. Feature flags let operators turn whole capability areas (risk / vendor / policy / OSCAL export / board / etc.) on or off per-tenant; admin UI surfaces SSO config, user role assignment, API keys, feature toggles, and unified audit log views without requiring CLI access.

| Row | Transition          | Why                                                                                |
| --- | ------------------- | ---------------------------------------------------------------------------------- |
| 059 | (new) → `ready`     | Per-tenant feature flags · deps 002, 033, 034 all merged · AFK-clean · unlocks 060 |
| 060 | (new) → `not-ready` | Admin settings UI · waits on 005, 034, 035, 059 · HITL on role-permission matrix   |
| 061 | (new) → `ready`     | CI path-based filtering (skip Go/Frontend on docs-only PRs) · AFK-clean · no deps  |

Spine flags (RLS, tenancy, auth, schema registry, scope, evidence ledger, framework crosswalks) are deliberately non-toggleable per 059's anti-criterion P0 — the seed flag inventory only includes capability-area flags.

Slice 061 is a CI cost / DX optimization motivated by PR #49 (batch 14 reconcile) — `.md`-only PR that ran 9 expensive CI jobs for a 1-line edit. Pattern: `dorny/paths-filter@v3` in-workflow, stub jobs preserving required-check names, security scans (CodeQL + GitGuardian) always-on.

**Counts delta:** total +3 · ready +2 · not-ready +1.

## Drift detected — 2026-05-13 (batch 14 claim-stake — 023 AFK + 035 HITL, archived)

Two slices flipped `ready` → `in-progress`. **N=2 batch · 1 AFK + 1 HITL** — first batch under the post-batch-13 branch-protection-via-PR pattern. Claim-stake is this PR (status-only); subagent in-review flips ride on their slice PR branches; final reconcile is one more status-only PR. Per-batch overhead from status PRs: ~12 min (was ~3 min when direct push to main was allowed).

| Row | Transition              | Branch                               |
| --- | ----------------------- | ------------------------------------ |
| 023 | `ready` → `in-progress` | `policies/023-policy-acknowledgment` |
| 035 | `ready` → `in-progress` | `auth/035-rbac-abac-opa`             |

Migration slots: 023 → `_017`, 035 → `_018`. Spine touch: 035 only (OPA Go SDK). DefaultSeed touch: 023 only (`policy.acknowledgment.v1`). HITL gate (035 only): orchestrator pair-reviews the 5-role enum + ~10 seed Rego policies pre-merge.

**Counts delta:** ready −2 · in-progress +2.

## Drift detected — 2026-05-13 (batch 13 merged — public release + policies, archived)

**The repo is now public.** Apache 2.0 LICENSE on main, GitHub Actions on unlimited public-repo minutes, full release-readiness scaffold in place (CONTRIBUTING / SECURITY / CODE_OF_CONDUCT v2.1 inlined / Dependabot / CodeQL / release-please / multi-arch container / Watchtower self-host example). Two slices merged this batch: 050 (public release readiness) and 022 (policy library + 5 stock policies). Slice 023 (Policy acknowledgment workflow) newly unblocked — last dep 022 just landed (034 was already merged).

| Row | Transition               | Evidence                                                                                                                                                                                                                                               |
| --- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 050 | `in-progress` → `merged` | commit `95f0faf` on main (gh#34 squashed 2026-05-13; 14/15 ACs at merge + AC-7 closed by post-merge CoC curl-inline commit `7be8f23`; 36 files; Apache 2.0; sanitization sweep; bootstrap-exception merge with empty CI per docs/RELEASE_READINESS.md) |
| 022 | `in-review` → `merged`   | commit `3af9cb0` on main (gh#33 squashed 2026-05-13; 7/7 ACs incl HITL-signed AC-6; chromedp PDF render; required post-public-flip CI fix cycle for lint nits + slice-002 helper + created_by CHECK satisfaction — agent's local runs missed these)    |
| 023 | `not-ready` → `ready`    | deps 022, 034 `merged`                                                                                                                                                                                                                                 |

**Bootstrap sequence executed:**

1. PR #34 (slice 050) merged with empty CI (Actions quota exhausted) — single documented exception per docs/RELEASE_READINESS.md
2. `gh repo edit --visibility public` — repo flipped public, Actions minutes unlimited
3. CoC inlined via curl + direct main commit `7be8f23` (AC-7 closed)
4. PR #33 (slice 022) rebased + force-pushed; CI re-run surfaced 14 lint nits + go.mod tidy + test-helper drift (created_by CHECK + body_md NOT NULL)
5. Orchestrator close-out: 3 follow-up commits (8407f13 → d628fb2 → 0bdb1a7) fixed all
6. PR #33 merged green

**Outstanding known-issue:** CodeQL workflow's Analyze (python) job fails on the repo's minimal Python surface (oscal-bridge only). Tracked as a slice-050 follow-up; not a slice-022 regression.

**Counts delta:** merged +2 · in-review −1 · in-progress −1 · ready +1 · not-ready −1.

## Drift detected — 2026-05-13 (slice 050 → in-review · gh#34, archived)

Slice 050 (public release readiness + release automation) flipped `in-progress` → `in-review`. PR gh#34 opened against main. **14/15 ACs PASS · AC-7 PARTIAL** (`CODE_OF_CONDUCT.md` ships as a placeholder pointing at the canonical Contributor Covenant v2.1 URL — the full text reliably trips the API content-moderation filter on agent output, two prior agent runs blocked on it; maintainer inlines via `curl -sSL https://www.contributor-covenant.org/version/2/1/code_of_conduct.md > CODE_OF_CONDUCT.md` post-merge, one docs-only follow-up commit, AC-7 graduates to PASS at that point). **Three pre-merge open-questions resolved 2026-05-13:** OQ #1 SCF redistribution → do NOT bundle pre-built SCF data (users import their own, consistent with slice 006); OQ #3 project license → **Apache 2.0** (permissive licensing is the canonical instance of canvas §1.2's "license that lets the platform be embedded in commercial deployments" requirement, the same requirement that disqualifies OpenGRC's CC BY-NC-SA; `LICENSE` carries the full Apache License Version 2.0 text with `Copyright 2026 Matt Goodrich and security-atlas contributors`); OQ #5 hosted offering vs OSS governance → defer the call, ship public OSS now. **Public-facing docs landed:** `README.md` rewritten for a public audience with the 4-badge row at the top (License via shields.io · Build via GitHub Actions · Coverage via Codecov · Latest release via shields.io); dev setup moved to new `CONTRIBUTING.md` (Conventional Commits + DCO sign-off requirement, no separate CLA); new `SECURITY.md` documents the GitHub private-vulnerability-reporting channel as the primary disclosure path; `CODE_OF_CONDUCT.md` placeholder per the AC-7 note. **GitHub repo hardening:** Dependabot config covers `gomod` / `npm` / `pip` / `docker` / `github-actions` ecosystems on a weekly cadence with grouping rules; CodeQL workflow runs Go + TypeScript + Python on push + PR + scheduled weekly; branch-protection ruleset committed as reviewable JSON at `.github/branch-protection.json` (≥1 approving review · all CI status checks required · linear history · conversation resolution · stale-approval auto-dismissal · force-push blocked · direct-push blocked · branch deletion blocked · push restricted to maintainer + release-please bot; signed-commit enforcement OFF with rationale documented inline as `$rationale_required_signatures_off`); issue templates (`bug.yml` + `feature.yml`) include a constitutional-invariants checkbox block, PR template has the full review checklist. **Release automation:** `release-please` workflow opens / updates release PRs on every Conventional-Commit push to `main` (manifest mode at `release-please-config.json` + `.release-please-manifest.json`; **NEVER auto-merges** per AI-assist boundary in `CLAUDE.md` — every release requires human approval); container-publish workflow builds multi-arch (`linux/amd64` + `linux/arm64` via QEMU + `docker/build-push-action@v5`) and pushes to `ghcr.io/mgoodric/security-atlas` on release tag with SBOM + provenance attestation; `docker manifest inspect` step asserts both architectures present (AC-13). **Watchtower self-host:** opt-in label-based pattern at `deploy/watchtower/docker-compose.example.yml` with the platform container labelled `com.centurylinklabs.watchtower.enable=true` and Postgres deliberately NOT labelled (major upgrades need manual dump+restore); `docs/SELF_HOSTING.md` documents the full pattern with an Unraid worked example. **Sanitization sweep verified:** persona phrasing in `Plans/canvas/01-vision.md §1.4` and the v1 success test in `Plans/canvas/10-roadmap.md §10.1` use the generic "solo security leader at a 50–150-person security-product startup" framing; mockup demo data scrubbed (Matt → Sam Rivera placeholder); test fixtures de-personalized (`matt` → `sample-user`); every remaining `grep -rIi "matt|mgoodric"` hit is whitelisted with justification in `docs/RELEASE_READINESS.md §3` (LICENSE author field · Go module path · buf module name · `.goreleaser.yaml` Homebrew tap owner + provenance regex · `docs/audit-log/` reviewer attribution · `docs/issues/_STATUS.md` append-only drift entries · CHANGELOG historical entries · slice issue files referenced by `057`). **CI quota constraint known at PR-open time:** GitHub Actions private-repo minutes exhausted, so gh#34's CI checks fail with workflow-level "no runner" errors. **This is not a slice failure.** The maintainer merges with red CI (admin bypass, single-PR exception — same pattern as the bootstrap PR's CI-baseline gap), then flips the repo to public (`gh repo edit --visibility public`) which immediately enables unlimited Actions minutes; branch protection enforcement begins on the NEXT PR per `docs/RELEASE_READINESS.md §5`. **Anti-criteria honoured:** `gh repo edit --visibility public` NOT executed by this slice (P0); release-please workflow NEVER auto-merges release PRs (P0); no CCM / CAIQ / SIG / OpenGRC content bundled (licensing); SCF data NOT bundled (OQ #1 resolution); maintainer name retained at `LICENSE` author field + `docs/audit-log/` reviewer attribution per anti-criterion. **Post-merge maintainer checklist** at `docs/RELEASE_READINESS.md §7`: (1) inline CoC text via curl (AC-7 → PASS); (2) `gh repo edit --visibility public`; (3) `gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection --input .github/branch-protection.json`; (4) re-trigger CI on PRs gh#32 + gh#33; (5) enable Discussions + Security Advisories via repo settings UI. **Time spent on this fresh-context run:** ~75 min end-to-end (the prior agent's two earlier runs blocked on the AC-7 content-filter; this run sidesteps via the placeholder pattern, the fresh-context restart was the unblock).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 050 | `in-progress` → `in-review` | gh#34 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (slice 050 claim-stake — public release readiness)

Slice 050 (public release readiness + release automation) flipped `ready` → `in-progress`. **User pivot:** rather than wait ~19 days for GitHub Actions free-tier private-repo minutes to reset (exhausted ~03:42Z UTC), ship slice 050 → manually flip repo to public → public repos get unlimited Actions minutes → unblock PR #33 (slice 022) merge.

**Open-q resolutions** (3 of slice 050's pre-merge gates resolved this turn):

- **#1 SCF redistribution** — resolved by policy: don't bundle pre-built SCF data; users import their own (consistent with slice 006's pattern)
- **#3 Project license** — **Apache 2.0** (user ratified 2026-05-13)
- **#5 Hosted offering vs OSS governance** — defer; ship public OSS now, hosted offering is a future commercial call. Agent surfaces this in `RELEASE_READINESS.md`.

| Row | Transition              | Branch                               |
| --- | ----------------------- | ------------------------------------ |
| 050 | `ready` → `in-progress` | `infra/050-public-release-readiness` |

Slice ships 15 ACs: repo content sanitization (remove personally-identifying refs), public docs (README/LICENSE/CoC/CONTRIBUTING/SECURITY), GitHub security features (CodeQL, Dependabot, branch protection, signed-commits decision), release automation (release-please semver + multi-arch GHCR + Watchtower auto-deploy for Unraid), and a pre-flight `RELEASE_READINESS.md` checklist. **Final `gh repo edit --visibility public` flip is NOT in scope** — that's the maintainer's manual action post-merge.

**CI quota constraint:** agent runs local-only verification; PR's CI will be quota-blocked at open time. Sequence: (1) agent ships PR with full content + local gates green; (2) user manually flips repo public; (3) CI re-runs unlimited; (4) merge slice 050; (5) re-run + merge PR #33.

**Counts delta:** ready −1 · in-progress +1.

## Drift detected — 2026-05-13 (slice 022 → in-review · HITL pending, archived)

Slice 022 (policy library + version chain + 5 stock policies + chromedp PDF render) flipped `in-progress` → `in-review`. PR gh#33 opened against main. **HITL gate pending pre-merge** — agent shipped machinery + 5 drafted stock policies (Information Security · Access Control · Vendor Management · Incident Response · Change Management), all attributed `community_draft`. Orchestrator + user pair-review the policy bodies before squash-merge (same shape as batch 9's slice-007 SOC 2 mapping pair-review). **6/7 ACs PASS · AC-6 marked PENDING-HITL** (the per-policy approval rows + sign-off block in `docs/audit-log/stock-policies-review.md` are intentionally left for the reviewer). The slice graduates the slice-002 placeholder `policies` table to its v1 shape via `_016_policies` ALTER migration: adds `predecessor_id` self-FK composite `(tenant_id, predecessor_id) → (tenant_id, id)` so version chains cannot span tenants, `owner_role`/`approver_role` (replacing the unused slice-002 `owner`/`approver`), `linked_control_ids UUID[]`, `source_attribution` enum (`community_draft` | `tenant_authored` | `vendor_provided`), 7 workflow timestamp+actor columns, `created_by`; reshapes `version` INTEGER → TEXT (operator-supplied semver) and `status` enum → TEXT + CHECK; replaces the single `tenant_isolation` policy with the four-policy RLS split (verified via `pg_policies`: `tenant_read SELECT + tenant_write INSERT WITH CHECK + tenant_update UPDATE USING+WITH CHECK + tenant_delete DELETE`); partial UNIQUE `policies_predecessor_unique_when_set` enforces linear version chains. **Publish is two-step atomic** for second-and-later versions: supersede prior + insert new with `predecessor_id` set, single tx. First-publish is single-UPDATE in place. **Approver-role gate** on `under_review → approved` AND `approved → published` (defense in depth — publish is audit-binding). **Orphan-policy warning** (`warning: orphan_policy`) surfaces on every read response when `len(linked_control_ids) == 0`; orphan publish returns 409 (`ErrOrphanPublish`) per anti-criterion P0. **PDF render is real** — `internal/policy/pdf` uses chromedp + `page.PrintToPDF` against a `data:text/html` URL; integration test asserts leading `%PDF-` magic bytes against `chromedp/headless-shell:latest`. When Chrome is unavailable on the host the renderer returns `ErrChromeUnavailable` and the handler responds 503 so the platform runs without Chrome installed (test skips gracefully). **CHROME_DEBUG_URL** env override lets CI point at a sidecar headless-shell container instead of launching Chrome locally. **Spine touch:** `go.mod` adds `github.com/chromedp/chromedp` + `cdproto` (the slice's one allowed touch — Go-native, headless-Chrome-driven; preferred over wkhtmltopdf which would add a runtime binary dependency for docker-compose). **5 stock policies** ship under `policies/stock/*.md` with YAML frontmatter (title, version, owner_role, approver_role, linked_control_ids as SCF anchor codes, acknowledgment_required_roles, source_attribution: community_draft); loader rejects any directory whose count ≠ 5 (constitutional anti-pattern 1.6 enforced at code level — unit tests cover 4/6/0). **CLI `atlas-cli policy seed-stock --tenant-id=...`** loads + INSERTs as draft rows (mirrors slice 007's `catalog import-soc2` pattern); resolves SCF anchor codes to `controls.scf_id` UUIDs via DISTINCT ON lookup; missing anchors surface in the seed Report (the resulting policy may surface orphan_policy warning until slice 010 SOC 2 control kit lands). **HITL audit-log stub** at `docs/audit-log/stock-policies-review.md` pre-populated with per-policy review priority order + SCF-anchor verification rubric + sign-off block (reviewer name + per-policy decisions + signature/commit SHA left unfilled). **CONTEXT.md** got a full "Policy (slice 022)" entry canonicalising the state-machine vocabulary, orphan-policy semantics, approver-role gate, stock bundle table, and source_attribution values. **Tests:** 8 policy integration tests (create happy path · orphan warning · state-machine transitions · publish first version · orphan publish rejected · approve-from-draft rejected · cross-tenant boundary · ErrNotFound · validation table) + 2 PDF integration tests (real PDF magic bytes against headless-shell · cancelled-context fast return) + 5 seed unit tests (real-bundle parse · count-guard cases 4/6/0 · missing frontmatter · NoopAnchorResolver). **Migration round-trip verified** clean (up applied against `sa-022-pg` end-to-end). **Constitutional invariants honoured:** #6 (FORCE RLS + four-policy split verified via runtime pg_catalog), #7 (linked_control_ids references controls anchored at SCF), D3 (cross-tenant FK leakage blocked by composite self-FK), slice 033 invariant (handlers do NOT call `tenancy.WithTenant`; every endpoint inherits `app.current_tenant` via `tenancymw.Middleware`). **pre-commit run --all-files PASS** (prettier auto-fixed CONTEXT.md + CHANGELOG.md inline). **Time spent:** ~75 min end-to-end (PRD + grill + tests + Go code + handlers + httpserver wire + 5 policy markdown bodies + seed CLI + ship-gate + CHANGELOG + commit + PR + status flip).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 022 | `in-progress` → `in-review` | gh#33 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (slice 053 → in-review)

Slice 053 (risk theme tagging + manual aggregation API + org_units CRUD) flipped `in-progress` → `in-review`. PR gh#32 opened against main. **10/10 ACs PASS.** Builds entirely on slice 052's freshly-merged schema; **NO new migration consumed**. The slice ships three endpoint groups under `/v1/`: (1) theme management (`GET /v1/themes` + `POST/DELETE /v1/risks/{id}/themes`); (2) manual aggregation (`POST /v1/risks/aggregate` + `GET /v1/risks/{id}/aggregation` live recompute); (3) full org_unit CRUD with cycle detection via recursive CTE. **Severity functions** implemented per canvas §6.6 with concrete fixture numbers: 3 children at severities 15/12/9 → `max=15`, `weighted_max=⌈15·(1+log10(3))⌉=23`, `sum=36→capped at 25`. Single child (5,4)=20 → `weighted_max=20` (log10(1)=0). Children 20+9 → `sum=29→capped at 25`. **AC-7 idempotency** via sha256_hex(parent_title + "|" + sorted_child_uuids) stored in `inherent_score.aggregation_key`; same `(title, child_set)` returns existing parent (verified by `TestAggregate_Idempotent_AC7`). **AC-10 cross-tenant denial** confirmed by `TestAggregate_CrossTenantChildDenial_AC10`: tenant A aggregating with tenant B's `child_risk_id` → `ErrChildrenNotFound` → HTTP 404 with non-enumerating body `"one or more child risks not found"`. Mechanism is RLS-first: `ListRisksByIDs` inside the tenant tx returns only visible children; short row count → 404. **AC-4 cycle detection** uses new `ParentChainIDs :many` recursive CTE — rejects self-parent + arbitrary chain cycles; CTE bounded by `tenant_id = $1` on every JOIN. **Three design decisions** documented in CHANGELOG: (a) `severity_function` lives in parent's `inherent_score` JSONB (no schema churn — slice 052's `risk_aggregations` deliberately has no `severity_function` column because it's per-rule per §6.6 and slice 054 owns rules); (b) aggregation children must use `nist_800_30` or `qualitative_5x5` (5×5 grid scalar `L*I`) — mixed methodology → 400 `ErrIncompatibleMethodology`; (c) `(likelihood, impact)` on parent derived from severity via `L=min(5,ceil(sqrt(S)))`, `I=min(5,ceil(S/L))` — raw `severity` is the load-bearing field, (L,I) keeps the qualitative_5x5 schema + slice-019 heatmap happy. **Constitutional invariants honoured:** #6 (every handler inherits `app.current_tenant` via `tenancymw.Middleware`; store calls go through `inTx` → `tenancy.ApplyTenant`; zero app-level `WHERE tenant_id = $X` outside sqlc parameterised pattern) and #9 (manual aggregation peer to rule-driven; `rule_id IS NULL`). **Tests:** 14 new integration tests (cross-tenant denial + idempotency + CRUD round-trip + cycle detection + every severity function + mixed-methodology rejection + tenant-private theme + remove-theme-idempotent + e2e flow); 12 new unit tests on severity math + aggregation key + grid-cell derivation. **sqlc regen clean** — no hand-edits to `internal/db/dbx/`. **pre-commit run --all-files PASS** (gofmt + prettier auto-fixed inline). **Drive-by:** local Postgres wasn't running — spun up `sa-053-pg` on port 55453 to apply all migrations + atlas_app role + run integration tests before commit. **Pre-existing failures untouched** (same as slice 052 surprises): `internal/risk` slice-009 `bundle_id` fixture drift on slice-019 tests, out of scope. **Time spent:** ~95 min end-to-end (PRD + grill + tests + Go code + handlers + httpserver wire + ship-gate + CHANGELOG + commit + PR + status flip). **Files touched (17):** `CHANGELOG.md`, `internal/api/httpserver.go`, `internal/api/orgunits/handlers.go` (new), `internal/api/risks/aggregate.go` (new), `internal/api/themes/handlers.go` (new), `internal/db/dbx/{org_units,querier,risks}.sql.go` (sqlc regen), `internal/db/queries/{org_units,risks}.sql`, `internal/risk/{aggregate,orgunit,severity,severity_test,slice053_integration_test,theme}.go` (new), `internal/risk/store.go` (Risk struct: +Level/+OrgUnitID/+Themes surface). **Surprises:** (1) `sqlc` parser cannot resolve column references through recursive CTE outer SELECT — needed three iterations to land an alias scheme it accepts (`node_id`/`up_id` named columns on both CTE arms, no outer SELECT alias). Postgres accepts every earlier variant. (2) `org_units.acceptance_authorities` CHECK constraint `jsonb_typeof = 'array'` (slice 052 §6.4) bit on first integration test run — default `{}` empty-object failed; changed default to `[]` empty-array. (3) `GetRiskByAggregationKey` sqlc inferred parameter type as `[]byte` from the `inherent_score->>'aggregation_key'` JSONB column on the LHS of `=`; pinned to `text` via `$2::text` cast, which makes the generated param a `string`.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 053 | `in-progress` → `in-review` | gh#32 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (batch 12 claim-stake — 053 AFK + 022 HITL)

Two slices flipped `ready` → `in-progress`. **N=2 batch · 1 AFK + 1 HITL** — user picked the parallel AFK+HITL pattern (053 runs end-to-end; 022's 5 stock policy bodies get a ~30min pair-review at merge time). File surfaces are completely disjoint (risk module vs policies module). 022 takes the single allowed spine touch (`go.mod` for chromedp PDF rendering).

| Row | Transition              | Branch                        |
| --- | ----------------------- | ----------------------------- |
| 053 | `ready` → `in-progress` | `risk/053-risk-theme-tagging` |
| 022 | `ready` → `in-progress` | `policies/022-policy-library` |

Migration slot allocation: 053 → none (uses 052 + 019 schemas); 022 → `_016` main, optional `_017` for stock-policy seed. Spine touch: 022 only (`chromedp` for AC-5 PDF render). Shared touches: `internal/api/httpserver.go` (Mount-append both — known-safe 3-way merge), sqlc regen on `dbx/{models,querier}.go`, `sqlc.yaml`, `CHANGELOG.md`.

HITL gate (022 only): orchestrator presents drafted policy bodies (Information Security · Access Control · Vendor Management · Incident Response · Change Management) for spot-check before squash-merge. Same shape as batch 9's slice-007 pair-review.

**Counts delta:** ready −2 · in-progress +2.

## Drift detected — 2026-05-13 (batch 11 merged — slice 052 risk hierarchy schema, archived)

Slice 052 flipped `in-review` → `merged`. **Slice 053 (risk theme tagging) newly unblocked** — its sole dep (052) is now merged. No other downstream unlocks this batch (054 still waits on 053; 055 still waits on 020). **First clean end-to-end agent run since slice 051** — no stalls, no resumes needed. Agent's anti-stall briefing landed cleanly. Pure-schema slice fits the AFK pattern perfectly: 10 ACs, all binary-testable, no architectural decisions, no HITL.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                             |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 052 | `in-review` → `merged` | commit `bdfc55d` on main (gh#31 squashed 2026-05-13; 10/10 ACs PASS · 8 new tables + ALTER risks · 4-policy RLS across all new tables · migration slots `_014` main + `_015` seed (10 default themes per canvas §6.5) · zero business logic (053/054/055 will land it) · clean migration round-trip + idempotent seed · zero stalls) |
| 053 | `not-ready` → `ready`  | dep 052 `merged`                                                                                                                                                                                                                                                                                                                     |

**AC-7 transitional bypass:** role-based write restriction stubbed via `COALESCE(current_setting('app.current_role', true), '*') <> ''` sentinel on `decisions` policies. The `'*'` bypass becomes load-bearing until slice 035 (RBAC + ABAC via OPA) wires real role identifiers via the tenancy-context plumbing.

**Counts delta:** merged +1 · in-review −1 · ready +1 · not-ready −1.

## Drift detected — 2026-05-13 (slice 052 → in-review, archived)

Slice 052 (risk hierarchy + themes + Decision Log schema) flipped `in-progress` → `in-review`. PR gh#31 opened against main. **Pure-schema slice — 10/10 ACs PASS.** The slice lands eight new tenant-scoped tables (`org_units`, `org_themes`, `risk_aggregations`, `decisions`, four `decision_*` link tables), ALTER on `risks` (`level` enum + `org_unit_id` composite FK + `themes` text[] with GIN index), and migration slot `20260511000014` (main) + `20260511000015` (companion default-theme seed — 10 themes per canvas §6.5, idempotent via `ON CONFLICT (theme_name) WHERE tenant_id IS NULL DO NOTHING`). **RLS coverage universal:** runtime pg_catalog audit confirms 8/8 new tables `force_rls=t, rls_enabled=t, n_policies=4` (slice-014/017/019 four-policy split). **AC-7 role-based write gating** stubbed via `COALESCE(current_setting('app.current_role', true), '*') <> ''` sentinel on `decisions` policies — the `'*'` transitional bypass becomes load-bearing once slice 035 (RBAC) wires real role identifiers. **Four decision-link tables stay separate** (P0 anti-criterion enforced — no polymorphic `(target_kind, target_id)` table). **No auto-close** behavior on `risk_aggregations` (canvas §6.4 + §6.6 explicit — parent risks represent patterns that may persist beyond children). **Themes flat** — no `parent_theme_id` (canvas §6.5 explicit). **Defense-in-depth:** new composite UNIQUE on `framework_scopes (tenant_id, id)` enables cross-tenant-safe composite FK from `decision_scope_predicates` (matches slice 019/006 pattern on `risks`/`vendors`). **sqlc** queries shipped for 053/054/055 starter surface (5 query files: `org_units`, `org_themes`, `risk_aggregations`, `decisions`, `decision_links`); regenerated cleanly with no hand-edits. **Tests pass:** 8 new integration tests (5 cross-tenant RLS negative + positive INSERT smoke + risks-columns round-trip + default-theme seed + partial-unique collision); `ok internal/db 1.517s`. **Migration round-trip verified** clean (up → down → up restores byte-identical state); seed re-apply returns `INSERT 0 0`. **Constitutional invariants honored:** #4 (multidimensional scope — risk hierarchy is its own dimension), #6 (RLS at DB layer — 8 new tables FORCE + four-policy), #9 (manual evidence first-class — manual aggregation has same shape as future automatic, distinguished only by `rule_id IS NULL`). **Drive-by:** prettier auto-fixed pre-existing table-padding whitespace drift in `Plans/canvas/06-risk.md` and `docs/issues/{053,058,_INDEX}.md` introduced by commit 5d08816 (backlog add that did not run through pre-commit). Whitespace-only; rolled into slice 052 PR to unblock CI's `pre-commit run --all-files` step. **Pre-existing failures untouched** (same as slice 008 surprises): `TestSchema_TenantScopedTablesAcceptInserts` slice-013 baseline FK drift on `evidence_records`; `internal/scope` + `internal/risk` slice-009 `bundle_id` NOT NULL fixture drift. Both out of scope. **Time spent:** ~45 min end-to-end (PRD + grill + tests + migration + RLS audit + ship-gate + CHANGELOG + commit + PR). **Surprises:** (1) the in-worktree pre-existing prettier drift was a CI blocker — included whitespace-only fixes in the PR rather than leaving CI red. (2) Local Postgres wasn't running; spun up `sa-052-pg` on port 55452 to exercise the round-trip and integration tests before commit. (3) sqlc regenerated `risks.sql.go` because the underlying `risks` table got new columns; the generated diff is mechanical (struct fields added) and the existing CreateRisk signature is unchanged (still doesn't list the new columns — they default). **Migration slot:** single forward slot `_014` for main schema + companion `_015` for seed. **Slice-002 test helpers NOT patched** — verified existing `mustInsertControl`/`mustInsertRisk` paths only set columns with safe defaults, so adding `level`/`org_unit_id`/`themes` with NOT NULL DEFAULTs requires no helper change. The new `mustInsertOrgUnit`/`mustInsertRisk`/`mustInsertDecision` helpers in `risk_hierarchy_integration_test.go` are slice-052-local.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 052 | `in-progress` → `in-review` | gh#31 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-13 (batch 11 claim-stake — slice 052 risk hierarchy schema)

Slice 052 (risk hierarchy + themes + Decision Log schema) flipped `ready` → `in-progress`. **N=1 batch · pure AFK** — user picked Option A (solo 052 to restore AFK throughput; HITL slice 010 deferred to dedicated session). Schema-only slice: new tables (`org_units`, `org_themes`, `risk_aggregations`, `decisions`, 4 decision-link tables), ALTER on `risks` (level/org_unit_id/themes with safe defaults), full four-policy RLS on new tables. No business logic.

| Row | Transition              | Branch                           |
| --- | ----------------------- | -------------------------------- |
| 052 | `ready` → `in-progress` | `risk/052-risk-hierarchy-schema` |

Migration slot allocated: `20260511000014`. Spine touch: none (no go.mod changes). Shared touches: sqlc regen on `dbx/{models,querier}.go`, `sqlc.yaml` (append migration to list), `CHANGELOG.md`. Existing `risks` table ALTER adds defaulted columns (`level=team` NOT NULL DEFAULT, `themes='{}'` NOT NULL DEFAULT) — slice-002 test helpers should not require patching.

**Counts delta:** ready −1 · in-progress +1.

## Drift detected — 2026-05-13 (batch 10 merged — slice 008 UCF graph traversal, archived)

Slice 008 (UCF graph traversal query API) flipped `in-review` → `merged`. **No new ready-set unblocks** — both downstream consumers (slice 030 OSCAL export + slice 041 control-detail UI) still wait on slice 012 (control state evaluator), which is the next bottleneck on the chain. 012 in turn waits on slice 010 (50 SOC 2 controls, currently `ready` and HITL-gated).

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 008 | `in-review` → `merged` | commit `06d1875` on main (gh#30 squashed 2026-05-13; 6/6 ACs PASS · zero new migrations · 10 files · AC-5 benchmark mean 5.89ms / p50 5.88ms / p95 6.91ms at 1.4k anchors + 60 reqs + 10k edges + 5k controls — 34× under 200ms target · slice-006 in-memory `/v1/anchors/{id}/requirements` handler retired in favor of DB-backed · `anchorseed` package becomes unreferenced — future cleanup slice removes it · effectiveness field deferred to slice 012) |

**Orchestrator notes:** Agent stalled THREE times during run (post-grill, post-implicit, post-security-review). Each stall resolved via single `SendMessage` resume. Eventual end-to-end success but slower than ideal. The pattern: agent does excellent intermediate work (15-decision grill outcome, clean security review) but treats every phase boundary as a checkpoint. The DO-NOT-STALL hard rule is doing its job — without it, the slice would have shipped four turns later. Future brief: emphasize "skill-clean → next-skill" chaining explicitly.

**Counts delta:** merged +1 · in-review −1.

## Drift detected — 2026-05-13 (slice 008 → in-review, archived)

Slice 008 (UCF graph traversal query API) flipped `in-progress` → `in-review`. PR gh#30 opened against main. The slice ships three new read-only HTTP endpoints — `GET /v1/requirements/{id}/coverage` (forward traversal), `GET /v1/anchors/{id}/requirements` (DB-backed reverse traversal, replacing the slice-006 in-memory `anchorseed` placeholder), and `GET /v1/controls/{id}/coverage` (control-centric) — backed by a new `internal/api/ucfcoverage/` Go package and six new sqlc queries in `internal/db/queries/ucf_traversal.sql`. **Zero new migrations consumed.** Traversal is a two-hop JOIN through the SCF anchor spine; recursion isn't needed per `UCF_GRAPH_MODEL.md` §7 (bounded fan-out). **AC-5 benchmark crushes target:** mean **5.89 ms** / p50 **5.88 ms** / p95 **6.91 ms** against 1,400 SCF anchors + 60 SOC 2 reqs + 10,000 STRM edges + 5,000 tenant controls — **34× under the 200 ms gate**. No new index added; existing slice-006/007/009 indexes sufficient. **Constitutional invariant 1 honored:** every traversal joins through `scf_anchors`; `TestNoFrameworkToFrameworkEdgeTable` asserts at `information_schema` level. **Constitutional invariant 6 honored:** only tenant-scoped read (`ListControlsForAnchors` on `controls`) runs inside `inTenantTx` + `tenancy.ApplyTenant`; no app-level `WHERE tenant_id = ?` clause in any traversal SQL. Cross-tenant integration tests confirm: tenant B traversing tenant A's requirement sees global catalog rows but empty controls list (correct per canvas §3.5); tenant B looking up tenant A's control id returns 404 (RLS makes the foreign row invisible). **Behavior shift announced under CHANGELOG `## [Unreleased] / Changed`:** `anchors.New(q *dbx.Queries)` constructor signature drops its second `anchorseed.Store` parameter — internal-package signature change, single in-tree caller (`internal/api/httpserver.go`), no public API impact. The `internal/api/anchorseed` package becomes unreferenced; a future cleanup slice removes the directory + unit tests. Effectiveness field on `controls` array deferred to slice 012 (canvas §3.3) — field omitted rather than null so slice 012 can add it without breaking change. `?as-of=<RFC3339>` and `?scf_release=<version>` query params accepted-and-no-op in v1; slice 012 / future SCF-release-import work will activate them. **Surprises:** (1) inter-package parallel test execution against shared DB races on catalog wipe-and-reimport; CI already uses `-p 1` so non-issue. (2) The grill-with-docs decision to leave `requirementsForAnchor` + `anchorseed.Store` field in place as "dead code" was overridden by `golangci-lint`'s `unused` check, which would have blocked CI — removed entirely with CHANGELOG note. **Pre-existing unrelated failures** in `internal/scope` + `internal/risk` integration tests (bundle_id NOT NULL fixture drift from slice 009) confirmed by stashing slice-008 diff and re-running; out of scope for this slice.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 008 | `in-progress` → `in-review` | gh#30 opened 2026-05-13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-12 (new slices 052–058 added to backlog)

Canvas §6.4–6.7 extended with risk hierarchy + theme taxonomy + aggregation rules + Decision Log. 7 new slices added to the backlog (no transitions on existing rows; only additions):

| Row | Transition          | Why                                                                   |
| --- | ------------------- | --------------------------------------------------------------------- |
| 052 | (new) → `ready`     | Schema + migrations for risk hierarchy + themes + DL · dep 002 merged |
| 053 | (new) → `not-ready` | Theme tagging + manual aggregation API · waits on 052                 |
| 054 | (new) → `not-ready` | Aggregation rules engine · waits on 053                               |
| 055 | (new) → `not-ready` | Decision Log CRUD + linkage · waits on 052 + 020 + 021                |
| 056 | (new) → `not-ready` | Hierarchical risk dashboard view · waits on 005 + 053 + 054 + 055     |
| 057 | (new) → `not-ready` | README screenshots · waits on 040 + 041 + 042 + 043                   |
| 058 | (new) → `not-ready` | User docs scaffold · waits on 005 + 050                               |

Note: my originally-numbered slices 051–057 collided with the already-merged `051-admincreds-tenant-derivation` hotfix; renumbered to 052–058 to preserve the merged slice's number.

**Counts delta:** total +7 · ready +1 · not-ready +6.

## Drift detected — 2026-05-13 (batch 10 claim-stake — slice 008 UCF graph traversal)

Slice 008 (UCF graph traversal query API) flipped `ready` → `in-progress`. **First non-HITL slice executed since batch 8.** User-approved pick — bidirectional traversal over `fw_to_scf_edges` + `controls.scf_anchor_id` powering dashboard/control-detail/questionnaire flows. Three new REST endpoints, recursive CTEs in sqlc, AC-5 benchmark gate (200ms target, 1.4k anchors + 60 SOC 2 reqs), AC-6 RLS verification via slice 033 middleware.

| Row | Transition              | Branch                                |
| --- | ----------------------- | ------------------------------------- |
| 008 | `ready` → `in-progress` | `catalog/008-ucf-graph-traversal-api` |

Migration slot: none (uses existing tables from 002/006/007). Spine touch: none. Shared touches: `internal/api/httpserver.go` Mount-append (3 new routes), sqlc regen for `dbx/{models,querier}.go`.

**Counts delta:** ready −1 · in-progress +1.

## Drift detected — 2026-05-12 (batch 9 merged — slice 007 SOC 2 crosswalk, archived)

Slice 007 (SOC 2 v2017 TSC crosswalk loader) flipped `in-review` → `merged` after HITL pair-review session (orchestrator + reviewer Matt Goodrich, 2026-05-12). **Single biggest critical-path unlock in v1** — slices 008 (UCF graph traversal) + 010 (50 SOC 2 controls) both transition to `ready`. Downstream of 010 the chain advances: slices 012 (control state eval), 016 (freshness/drift), 020 (risk→control), 037 (docker-compose, gated on 010 specifically), 042 (audit workspace) all wait one or two hops behind. The biggest single-slice unlock in v1 is now on main.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                                                                                           |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 007 | `in-review` → `merged` | commit `b12cfea` on main (gh#29 squashed 2026-05-12; 56 community_draft edges across CC1–CC9 + A1 + C1 + PI1; HITL approved 56/56 as-is per `docs/audit-log/soc2-mapping-review.md` signed 2026-05-12; ZERO `no_relationship` or revisions; PI1.x family shipped as low-confidence intersects_with per explicit decision — SCF PI coverage is structurally narrow) |
| 008 | `not-ready` → `ready`  | dep 007 `merged`                                                                                                                                                                                                                                                                                                                                                   |
| 010 | `not-ready` → `ready`  | deps 009, 007 `merged` (HITL on 50-control accuracy)                                                                                                                                                                                                                                                                                                               |

**HITL gate worked cleanly.** Agent produced a full review-ready artifact in one pass (machinery + drafted mappings + structured spot-check report). User reviewed 9 low-confidence rows + a sample of 47 high-confidence, approved all 56 as-is. ~30 min pair-review session, no agent re-run needed. The pattern is reproducible for slices 010 (50 SOC 2 controls) + 022 (5 stock policies) + 035 (role enum) — same machinery+draft+pair-review shape works.

**Counts delta:** merged +1 · in-review −1 · ready +2 · not-ready −2.

## Drift detected — 2026-05-12 (slice 007 → in-review · HITL pending, archived)

Slice 007 (SOC 2 v2017 TSC crosswalk loader) flipped `in-progress` → `in-review`. PR gh#29 opened against main. The slice lands the second half of the UCF graph (canvas §3): two new tables (`framework_requirements` + `fw_to_scf_edges`) via migration `20260511000013`, two new DB enums (`strm_relationship_type` with the five canvas-spec NIST IR 8477 literals + `crosswalk_source_attribution` with `scf_official | community_draft | org_internal`), a new `internal/api/soc2import/` Go package (Load + idempotent Import with reuse of slice-006's two-query upsert pattern), the new HTTP route `GET /v1/requirements/{id}/anchors` for reverse traversal (accepts UUID, `slug:version:code`, or `slug::code` convenience form), and a new `atlas-cli catalog import-soc2 <path>` CLI + `just import-soc2 path` recipe. **Constitutional invariant 1 enforced at DDL level** — no `fw_to_fw_edges` table exists; `TestImport_NoDirectRequirementToRequirementTableExists` queries `information_schema` to assert at most one FK points at `framework_requirements`. **AI-assist boundary enforced** — every drafted row carries `source_attribution: community_draft`; the loader rejects rows missing `relationship_type` or `strength`, eliminating silent `equal/1.0` defaults. **DRAFT mapping data ships at `data/crosswalks/soc2-tsc-2017.yaml`:** 43 SOC 2 TSC criteria (CC1.1–CC9.2 + A1.1–A1.3 + C1.1–C1.2 + PI1.1–PI1.5), 56 drafted edges, 9 flagged low-confidence (`strength ≤ 0.5`) for HITL priority — these cluster around COSO-flavored CC1.x and Processing-Integrity PI1.x where SCF anchor coverage is narrow. **HITL pre-merge gate is the next blocker:** AC-4 (20-mapping spot-check signed in `docs/audit-log/soc2-mapping-review.md`) remains open until the orchestrator + user pair-review the drafts. Agent does NOT self-merge. Source: Option B (agent-authored — SCF's published SOC 2 STRM crosswalk artifact was not available offline; future SCF-published ingest will use `source_attribution=scf_official` and supersede). Migration slot consumed: `20260511000013`. Patches slice-006 `truncateCatalog` test helper for FK cascade order; `fw_to_scf_edges.scf_anchor_id` uses `ON DELETE CASCADE` so SCF wipe-and-reimport drops stale edges automatically.

| Row | Transition                  | Evidence                                                   |
| --- | --------------------------- | ---------------------------------------------------------- |
| 007 | `in-progress` → `in-review` | gh#29 opened 2026-05-12; HITL spot-check pending pre-merge |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-12 (batch 9 claim-stake — slice 007 HITL)

One slice flipped `ready` → `in-progress`. **N=1, HITL-gated batch** — ready set after slice 051 merged is 100% HITL. User picked Path A (focused human-review session on slice 007 — the biggest critical-path unlock available, unblocking eight downstream slices via the 010 chain).

| Row | Transition              | Branch                              |
| --- | ----------------------- | ----------------------------------- |
| 007 | `ready` → `in-progress` | `catalog/007-soc2-crosswalk-loader` |

HITL gate: pre-merge. Engineer agent ships the SOC 2 TSC loader machinery (parser, validator, importer, CLI, integration tests) plus a DRAFT set of SCF→TSC mappings for ~50 SOC 2 controls. Orchestrator presents proposed mappings to user for pair-review BEFORE squash-merge. Same standard slice shape, with an explicit content-approval gate inserted between the agent's PR-open and the merge.

**Counts delta:** ready −1 · in-progress +1.

## Drift detected — 2026-05-12 (slice 051 P0 fix merged, archived)

Slice 051 flipped `in-review` → `merged`. **Cross-tenant escalation vulnerability closed.** No new ready-set unblocks (051 is a leaf fix). Orchestrator note: PR #28's initial CI workflow run was silently suppressed by an add/add merge conflict on the issue file (orchestrator-written stub at claim-stake vs agent's richer threat-model version). Rebase against post-claim-stake main resolved the conflict AND restored the `pull_request` workflow trigger immediately. **Useful learning:** a merge-conflict-state PR receives no `pull_request` event from GitHub — diagnostic signature for "CI is silent but main pushes still run" → rebase first.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                                        |
| --- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 051 | `in-review` → `merged` | commit `81a9a76` on main (gh#28 squashed 2026-05-12; 7/7 ACs PASS · zero migrations · 218+/30- across 4 files · post-rebase prettier-format on issue file resolved final CI hook gap. Rotate/Revoke handler bodies byte-unchanged verified at merge — anti-criterion preserved through rebase.) |

**Counts delta:** merged +1 · in-review −1.

## Drift detected — 2026-05-12 (slice 051 → in-review, archived)

Slice 051 (admincreds Issue/List derive tenant from credential, not request body) flipped `in-progress` → `in-review`. PR gh#28 opened against main. The slice closes the P0 follow-up surfaced at the bottom of slice 033's PR body: pre-fix, an admin in tenant A could mint an admin credential into tenant B by supplying `{"tenant_id":"<B>"}` in the Issue body, and enumerate tenant B's credentials by passing `?tenant_id=<B>` to List — RLS did not catch this because the handler explicitly called `tenancy.WithTenant(ctx, req.TenantID)`, overriding slice-033's middleware GUC; the handler was internally consistent so it both set the GUC and wrote the row under the attacker-supplied tenant. The fix removes both `tenancy.WithTenant` override calls and reads the tenant strictly from `authctx.CredentialFromContext(r.Context()).TenantID`, matching the pattern Rotate + Revoke already use (those two handlers byte-unchanged by this slice — verified via `git diff` produces zero hunks inside their function bodies). API contract changes (BREAKING) announced in CHANGELOG under `## [Unreleased] / Changed`: `IssueRequest.tenant_id` JSON field rejected with HTTP 400 if non-empty, `?tenant_id=` query parameter on List rejected with HTTP 400 if non-empty; `IssueRequest.TenantID` Go struct field retained (with `omitempty`) so legacy callers get a descriptive 400 instead of a JSON decode failure or silent acceptance. Zero migrations, zero new dependencies, zero environment variables. Net diff: 4 files (`internal/api/admincreds/http.go` + `http_integration_test.go` + new `docs/issues/051-...md` + `CHANGELOG.md`), 218 insertions / 30 deletions. Constitutional invariant 6 (canvas §5.4) and slice-033 design decision D1 ("`tenancy.Middleware` sets `app.current_tenant` strictly from `cred.TenantID`; no handler-level overrides") now enforced uniformly across all four admincreds handlers.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 051 | `in-progress` → `in-review` | gh#28 opened 2026-05-12 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-12 (slice 051 P0 patch added + claim-stake)

New slice 051 added to the backlog as a P0 follow-up patch for the cross-tenant escalation surfaced by slice 033's PR body. Scope: `admincreds.Issue` + `admincreds.List` handlers derive tenant strictly from `cred.TenantID`, not from request body / query parameter. Sibling handlers `admincreds.Rotate` + `admincreds.Revoke` already correct — left alone. AFK-clean (~0.5d), single-slice batch.

| Row | Transition            | Branch                                 |
| --- | --------------------- | -------------------------------------- |
| 051 | (new) → `in-progress` | `fix/051-admincreds-tenant-derivation` |

Migration slot: none. Spine touch: none. Shared touches: `internal/api/admincreds/{http.go,http_integration_test.go}` (edit-in-place), `CHANGELOG.md` (breaking-API-change announcement for the `tenant_id` field/query removal).

**Counts delta:** Total 50 → 51 (new row). in-progress +1.

## Drift detected — 2026-05-12 (parallel batch 8 merged, archived)

Slice 033 (Postgres RLS enforcement + tenancy middleware) flipped `in-review` → `merged`. Slice 035 (RBAC + ABAC via OPA embedded) unblocks — its deps `#033, #034` are now both merged. 035 is HITL on role design but its primitives are ready.

| Row | Transition             | Evidence                                                                                                                                                                                                                                                                |
| --- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 033 | `in-review` → `merged` | commit `c534c85` on main (gh#27 squashed 2026-05-12; zero new migrations — service-account role added via idempotent DO block in `migrations/bootstrap/01-roles.sql`, slot `_013` stays free. 21 files, +1231/-69 LOC, 6/6 ACs PASS, 44/44 ISC PASS, ~60min wall-clock) |
| 035 | `not-ready` → `ready`  | deps 033, 034 `merged` (HITL on role design)                                                                                                                                                                                                                            |

**P0 follow-up required:** admincreds.Issue + admincreds.List handlers source `tenant_id` from request body/query, not from the calling credential. The handler explicitly calls `tenancy.WithTenant(ctx, req.TenantID)` overriding 033's middleware GUC, so RLS does NOT catch the cross-tenant escalation path (initially hypothesized to be inert under RLS — proven not). A new issue should land in `docs/issues/` against the v1.x backlog: "admincreds handlers must derive tenant from calling credential, not request body."

**Counts delta:** merged +1 · in-review −1 · ready +1 · not-ready −1.

## Drift detected — 2026-05-12 (slice 033 → in-review, archived)

Slice 033 (Postgres RLS enforcement on every tenant-scoped table + `tenancy.Middleware` + `just audit-rls` CI gate) flipped `in-progress` → `in-review`. PR gh#27 opened against main. The slice ships the runtime half of constitutional invariant 6 (canvas §5.4): chi middleware that lifts `cred.TenantID` onto every request context, deletes the redundant `tenancy.WithTenant(ctx, cred.TenantID)` boilerplate across 10 handler packages, adds the `atlas_service_account` BYPASSRLS role (NOLOGIN NOINHERIT, GRANT'd to atlas_app for `SET LOCAL ROLE` — no v1 production caller), and wires the `just audit-rls` script (pg_class + pg_policy join, fails CI on any uncovered tenant_id table) between migrate-up and the integration-test slate. **Zero new versioned migrations** — every existing tenant-scoped table already carried the right policy + FORCE shape; the slice ships only the bootstrap delta + middleware + audit machinery. Surfaces one pre-existing authorization bug for a P0 follow-up: admincreds Issue/List handlers source tenant from request body/query rather than the calling credential (RLS does NOT catch this because the handler is internally consistent — writes tenant B's row under tenant B's GUC). Unlocks slice 035 (RBAC + ABAC via OPA embedded; 034 already merged).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 033 | `in-progress` → `in-review` | gh#27 opened 2026-05-12 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-12 (parallel batch 8 claim-stake)

One slice flipped `ready` → `in-progress`. **N=1 batch** — 033 is the only AFK-clean slice in the ready set; the rest are HITL (007, 022, 050), open-q-gated (050), or genuinely not-ready (037, see correction below).

**Also corrects a batch-7 reconcile drift:** slice 037 was incorrectly flipped `not-ready` → `ready` based only on the most-recently-merged dep (034). The slice file's full dep list is `#002, #004, #005, #006, #010, #013, #014, #015, #034, #036`. Slice #010 is still `not-ready` (waits on slice 007 HITL). AC-4 of 037 ("50 SOC 2 controls visible in catalog") directly requires #010. Flipping 037 back to `not-ready`.

| Row | Transition              | Branch / Reason                                                 |
| --- | ----------------------- | --------------------------------------------------------------- |
| 033 | `ready` → `in-progress` | `auth/033-postgres-rls-enforcement`                             |
| 037 | `ready` → `not-ready`   | drift correction — dep #010 not-ready (batch-7 reconcile error) |

Migration slot reserved: 033 → `20260511000013` if needed (033 may ship audit-only with no migration; the agent decides). Spine touch: none expected (stdlib + existing pgx). Shared touches: `internal/api/httpserver.go` middleware-attach (single in-place edit, not Mount-append) · every existing handler under `internal/api/**` will gain `tenancy.Middleware` wiring.

**Counts delta:** ready −2 · in-progress +1 · not-ready +1.

## Drift detected — 2026-05-11 (parallel batch 7 merged)

Two slices flipped to `merged`. Slice 034 unlocks **slice 037 (docker-compose self-host bundle)** — the last dep was 034 (OIDC RP + local users). The other two consumers of 034 (slices 023 + 035) still wait on additional deps (022 and 033 respectively).

| Row | Transition             | Evidence                                                                                                                                                                                                                                   |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 021 | `in-review` → `merged` | commit `858f52b` on main (gh#25 squashed 2026-05-11)                                                                                                                                                                                       |
| 034 | `in-review` → `merged` | commit `ee0a333` on main (gh#26 squashed 2026-05-11; orchestrator rebased branch against post-021 main, cleaned 4 conflicts via canonical recipes — sqlc.yaml merge, CHANGELOG manual, httpserver Mount-append, sqlc regen for querier.go) |
| 037 | `not-ready` → `ready`  | dep 034 `merged`                                                                                                                                                                                                                           |

**Pre-existing CHANGELOG.md merge-marker artifacts** from slice 049's earlier squash (`||||||| parent of dd95004` + bare `=======`) were carried forward through batch-6 merges. Cleaned up as part of slice 034's rebase resolution. No more conflict-marker residue in CHANGELOG.

**Counts delta:** merged +2 · in-review −2 · ready +1 · not-ready −1.

## Drift detected — 2026-05-11 (slice 034 → in-review, archived)

Slice 034 (OIDC RP + local users + `api_keys` admin) flipped `in-progress` → `in-review`. PR gh#26 opened against main. The slice ships the auth machinery consumed by every existing connector — OIDC code+PKCE flow, local password login, opaque server-side sessions, and the DB-backed `api_keys` table for bearer credentials. Introduces ADR-0002 (bearer-token storage: HMAC-SHA256 keyed with `BEARER_HASH_KEY`, distinct from argon2id for local passwords). Migration slot `20260511000012` consumed (single migration, five tables: users / local_credentials / sessions / oidc_idp_configs / api_keys).

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 034 | `in-progress` → `in-review` | gh#26 opened 2026-05-11 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-11 (slice 021 → in-review, archived)

Slice 021 (exception/waiver workflow + auto-expiry + calendar API) flipped `in-progress` → `in-review`. PR gh#25 opened against main.

| Row | Transition                  | Evidence                |
| --- | --------------------------- | ----------------------- |
| 021 | `in-progress` → `in-review` | gh#25 opened 2026-05-11 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-11 (parallel batch 7 claim-stake)

Two slices flipped `ready` → `in-progress`. **N=2 batch** (not 3) — the remaining ready set is split between HITL (007, 022), universal-conflict (033), and open-q-gated (050), leaving 021 + 034 as the only AFK-clean pair.

| Row | Transition              | Branch                               |
| --- | ----------------------- | ------------------------------------ |
| 021 | `ready` → `in-progress` | `risk/021-exception-waiver-workflow` |
| 034 | `ready` → `in-progress` | `auth/034-oidc-rp-local-users`       |

Migration slots: 021 → `20260511000011_exceptions`, 034 → `20260511000012_users_sessions_api_keys` (may consume `_012`–`_015` if agent splits per-table). Spine touch: 034 only (OIDC libs into `go.mod` — `coreos/go-oidc/v3` + `golang.org/x/oauth2`). Shared touches all known-safe pattern: `httpserver.go` Mount-append · sqlc regen · CHANGELOG manual merge.

**Counts delta:** ready −2 · in-progress +2.

## Drift detected — 2026-05-11 (parallel batch 6 merged)

Three connector slices flipped to `merged`. **V1 connector roster is now complete** — 044 (GitHub) · 045 (Okta) · 046 (1Password) · 047 (osquery/Fleet) · 048 (Jira/Linear) · 049 (Manual/CSV/S3/SFTP) are all on main. No critical-path unlock — 007 (SOC 2 crosswalk · HITL) remains the bottleneck for the 010 → 012 → 016 → 020 chain.

| Row | Transition             | Evidence                                                                                                                                                                                                 |
| --- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 048 | `in-review` → `merged` | commit `78d916d` on main (gh#22 squashed 2026-05-11)                                                                                                                                                     |
| 047 | `in-review` → `merged` | commit `104a090` on main (gh#23 squashed 2026-05-11)                                                                                                                                                     |
| 049 | `in-review` → `merged` | commit `dd68fa2` on main (gh#24 squashed 2026-05-11; orchestrator closed out after agent stalled post-security-review · ed25519 runtime-key generation to satisfy both GitGuardian + detect-private-key) |

**Counts delta:** merged +3 · in-review −3. No new ready-set unblocks (047/048/049 are connector leaves).

## Drift detected — 2026-05-11 (parallel batch 6 claim-stake, archived)

Three connector slices flipped `ready` → `in-progress`. Final v1 connector roster — after this batch all 6 connectors (044/045/046/047/048/049) are on main.

| Row | Transition              | Branch                                       |
| --- | ----------------------- | -------------------------------------------- |
| 047 | `ready` → `in-progress` | `connectors/047-osquery-fleet-connector`     |
| 048 | `ready` → `in-progress` | `connectors/048-jira-linear-connector`       |
| 049 | `ready` → `in-progress` | `connectors/049-manual-upload-csv-connector` |

Migration slots: none (all three are stateless connectors reusing slice-014 schemas unchanged). Spine touch: none. Cleanest conflict surface of any batch — only shared file is `CHANGELOG.md`.

**Counts delta:** ready −3 · in-progress +3.

## Drift detected — 2026-05-11 (parallel batch 5 merged)

Three slices flipped to `merged`. First batch driven end-to-end by the new full-merge-cycle prompt.

| Row | Transition             | Evidence                                                                                                                                                                             |
| --- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 011 | `in-review` → `merged` | commit `15c89bb` on main (gh#20 squashed 2026-05-11; orchestrator closed out the agent's work + applied gofmt + prettier nits)                                                       |
| 026 | `in-review` → `merged` | commit `d6c8a5c` on main (gh#21 squashed 2026-05-11; orchestrator closed out the agent's work + patched slice 013's ingest test helper to TRUNCATE … CASCADE for new FK)             |
| 015 | `in-review` → `merged` | commit `24fe35e` on main (gh#19 squashed 2026-05-11; AC-6 TestAC6_RedactionAtIngestion was design-shaped failure — surfaced to human, then bounced to agent which diagnosed + fixed) |

**Counts delta:** merged +3 · in-review −3. No new ready-set unblocks (011 + 015 + 026 are all leaves of their clusters).

## Drift detected — 2026-05-11 (parallel batch 5 claim-stake, archived)

Three slices flipped `ready` → `in-progress` with worktrees + branches assigned:

| Row | Transition                  | Branch                                                 |
| --- | --------------------------- | ------------------------------------------------------ |
| 011 | `ready` → `in-progress`     | `control-as-code/011-manual-control-attestation`       |
| 015 | `ready` → `in-progress`     | `evidence-pipeline/015-nats-jetstream-ingestion-stage` |
| 015 | `in-progress` → `in-review` | gh#19 opened 2026-05-11                                |
| 026 | `ready` → `in-progress`     | `audit/026-sample-pull-primitives`                     |

Migration slots: 011 → none (reuses slice-014 schema), 015 → none (substrate swap), 026 → `20260511000010_audit_samples`. Spine touch: 015 only (NATS Go SDK in go.mod/go.sum). First batch driven by the full-merge-cycle prompt — orchestrator runs Step 5 merge queue + Step 6 final reconcile.

**Counts delta:** ready −3 · in-progress +3.

## Drift detected — 2026-05-11 (parallel batch 4 merged)

Three slices flipped to `merged`. Slice 009 unblocks slices 010 + 011 on the critical path.

| Row | Transition             | Evidence                                                                                                                                                                          |
| --- | ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 009 | `in-review` → `merged` | commit `8eeb184` on main (gh#16 squashed 2026-05-11; required orchestrator pgx-typing fix to slice-002's mustInsertControl test helper after bundle_id NOT NULL column was added) |
| 045 | `in-review` → `merged` | commit `998ac71` on main (gh#17 squashed 2026-05-11; orchestrator squashed branch history to clear GitGuardian flags from historical okta_secret_token literals)                  |
| 046 | `in-review` → `merged` | commit `7c07b9f` on main (gh#18 squashed 2026-05-11; orchestrator squashed branch history to clear GitGuardian flags from historical ops\_-prefixed test literals)                |

**Counts delta:** merged +3 · in-review −3 · ready +1 · not-ready −1. Slice 011 (manual control attestation) now has all deps satisfied (009 + 013 + 036) and transitions to `ready`. Slice 010 still waits on 007 (HITL SOC 2 crosswalk).

## Drift detected — 2026-05-11 (parallel batch 4 claim-stake, archived)

Three slices flipped `ready` → `in-progress` with worktrees + branches assigned:

| Row | Transition              | Branch                                      |
| --- | ----------------------- | ------------------------------------------- |
| 009 | `ready` → `in-progress` | `control-as-code/009-control-bundle-format` |
| 045 | `ready` → `in-progress` | `connectors/045-okta-connector`             |
| 046 | `ready` → `in-progress` | `connectors/046-1password-connector`        |

Migration slots: 009 → `20260511000009`, 045 → none, 046 → none.

**Counts delta:** ready −3 · in-progress +3.

## Drift detected — 2026-05-11 (parallel batch 3 merged)

Three slices flipped to `merged`. AC-6 PARTIAL gap from slice 013 is now closed (036 ships the storage destination).

| Row | Transition               | Evidence                                                                                                                                                                                                                                                                                |
| --- | ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 018 | `in-review` → `merged`   | commit `247e54f` on main (gh#13 squashed 2026-05-11)                                                                                                                                                                                                                                    |
| 044 | `in-review` → `merged`   | commit `6aacc2b` on main (gh#14 squashed 2026-05-11)                                                                                                                                                                                                                                    |
| 036 | `in-progress` → `merged` | commit `a8301ab` on main (gh#15 squashed 2026-05-11; orchestrator closed out the agent's work since the agent stalled twice before committing — three iterations of CI fixes were needed: bitnami/minio unpullable → docker-run startup step, mc image entrypoint, gofmt+errcheck nits) |

**Counts delta:** merged +3 · in-review −2 · in-progress −1.

## Drift detected — 2026-05-11 (slice 018 → in-review, archived)

Slice 018 (FrameworkScope predicate + intersection + four-state workflow) completed and opened for review:

| Row | Transition                  | PR    |
| --- | --------------------------- | ----- |
| 018 | `in-progress` → `in-review` | gh#13 |

**Counts delta:** in-progress −1 · in-review +1.

## Drift detected — 2026-05-11 (parallel batch 3 claim-stake, archived)

Three slices flipped `ready` → `in-progress` with worktrees + branches assigned:

| Row | Transition              | Branch                                   |
| --- | ----------------------- | ---------------------------------------- |
| 018 | `ready` → `in-progress` | `scope/018-framework-scope-intersection` |
| 036 | `ready` → `in-progress` | `infra/036-s3-artifact-store`            |
| 044 | `ready` → `in-progress` | `connectors/044-github-connector`        |

Migration slots: 018 → `20260511000007`, 036 → `20260511000008`, 044 → none (stateless connector).

**Counts delta:** ready −3 · in-progress +3.

## Drift detected — 2026-05-11 (parallel batch 2 merged)

Three slices flipped `in-review` → `merged` and ten previously-blocked slices unblocked:

| Row | Transition             | Evidence                                             |
| --- | ---------------------- | ---------------------------------------------------- |
| 013 | `in-review` → `merged` | commit `daecbe7` on main (gh#12 squashed 2026-05-11) |
| 019 | `in-review` → `merged` | commit `a0c5918` on main (gh#10 squashed 2026-05-11) |
| 024 | `in-review` → `merged` | commit `d3c24c7` on main (gh#11 squashed 2026-05-11) |
| 015 | `not-ready` → `ready`  | dep 013 `merged`                                     |
| 021 | `not-ready` → `ready`  | deps 019, 017 `merged`                               |
| 026 | `not-ready` → `ready`  | deps 013, 017 `merged`                               |
| 036 | `not-ready` → `ready`  | dep 013 `merged`                                     |
| 044 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |
| 045 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |
| 046 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |
| 047 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |
| 048 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |
| 049 | `not-ready` → `ready`  | deps 003, 013 `merged`                               |

**Counts delta:** merged +3 · in-review −3 · ready +10 · not-ready −10.

## Drift detected — 2026-05-11 (new slice added, archived)

Slice **050** (public release readiness + release automation) added to the backlog. Depends on 039 only, which is `merged`, so 050 starts as `ready`.

| Row | Transition      | Evidence                 |
| --- | --------------- | ------------------------ |
| 050 | (new) → `ready` | dep 039 already `merged` |

**Counts delta:** total +1 · ready +1.

## Drift detected — 2026-05-11 (parallel batch 2 claim-stake)

Three slices flipped `ready` → `in-progress` with worktrees + branches assigned:

| Row | Transition              | Branch                                            |
| --- | ----------------------- | ------------------------------------------------- |
| 013 | `ready` → `in-progress` | `evidence-pipeline/013-evidence-ledger-write-api` |
| 019 | `ready` → `in-progress` | `risk/019-risk-register-crud`                     |
| 024 | `ready` → `in-progress` | `vendor/024-vendor-lite-module`                   |

**Counts delta:** ready −3 · in-progress +3.

## Drift detected — 2026-05-11 (post-merge reconcile)

Reconcile against `git log main`:

| Row | Transition             | Evidence                                                              |
| --- | ---------------------- | --------------------------------------------------------------------- |
| 014 | `in-review` → `merged` | commit `44718c9` on main (gh#8 squashed 2026-05-11)                   |
| 017 | `in-review` → `merged` | commit `95819c2` on main (gh#9 squashed 2026-05-11)                   |
| 039 | `in-review` → `merged` | commit `8346784` on main (gh#7 squashed 2026-05-11)                   |
| 013 | `not-ready` → `ready`  | deps 002, 003, 014 all `merged`                                       |
| 018 | `not-ready` → `ready`  | dep 017 `merged` (open-q #19 flagged in Notes — gate for batch picks) |
| 024 | `not-ready` → `ready`  | deps 002, 017 `merged`                                                |

**Counts delta:** merged +3 · in-review −3 · ready +3 · not-ready −3.
**Newly ready:** 013, 018, 024.
**Newly blocked:** none.
**Stale work:** none flagged.

## Drift detected — 2026-05-11 (prior, archived)

Reconcile against `git log main` + `gh pr list` + `git worktree list` after parallel batch 1 reached `in-review`:

| Row     | Transition                                | Evidence                                    |
| ------- | ----------------------------------------- | ------------------------------------------- |
| 017     | `in-progress` → `in-review`               | PR gh#9 opened 2026-05-11T17:45:31Z         |
| 001–006 | `merged` (backfill PR + Started + Merged) | gh pr list --state merged                   |
| 014     | `in-review` (backfill Started)            | first unique commit on branch on 2026-05-11 |
| 039     | `in-review` (backfill Started)            | first unique commit on branch on 2026-05-11 |

## Counts

| Status        | Count  |
| ------------- | ------ |
| `merged`      | 53     |
| `in-review`   | 0      |
| `in-progress` | 3      |
| `ready`       | 4      |
| `blocked`     | 0      |
| `not-ready`   | 4      |
| **Total**     | **64** |

## Status enum

Legal values (use exactly these strings):

- `not-ready` — at least one dep is not yet `merged`
- `ready` — all deps merged; no one's started
- `blocked` — external blocker (open question, licensing decision, etc.); explain in Notes
- `in-progress` — branch exists, code being written
- `in-review` — PR open, awaiting approve+merge
- `merged` — squashed to main
- `abandoned` — explicitly dropped (rare; explain in Notes)

## Status table

| #   | Title                                                  | Status        | Branch                                               | PR    | Started    | Merged     | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| --- | ------------------------------------------------------ | ------------- | ---------------------------------------------------- | ----- | ---------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 001 | Monorepo skeleton + CI green build                     | `merged`      | spine/001-monorepo-skeleton                          | gh#1  | 2026-05-10 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 002 | Schema + migrations (6 primitives + FrameworkScope)    | `merged`      | spine/002-schema-migrations                          | gh#2  | 2026-05-10 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 003 | Evidence SDK: proto + Go push client + CLI             | `merged`      | spine/003-evidence-sdk-proto-push-client-cli         | gh#3  | 2026-05-10 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 004 | AWS connector (S3 encryption, end-to-end)              | `merged`      | spine/004-aws-connector-s3-encryption                | gh#4  | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 005 | Frontend bootstrap (Next.js + auth + SCF browser)      | `merged`      | spine/005-frontend-bootstrap                         | gh#5  | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 006 | SCF catalog importer + Framework/FrameworkVersion API  | `merged`      | catalog/006-scf-catalog-importer                     | gh#6  | 2026-05-11 | 2026-05-11 | open-q #01 cleared at merge                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 007 | SOC 2 v2017 (TSC) crosswalk loader                     | `merged`      | catalog/007-soc2-crosswalk-loader                    | gh#29 | 2026-05-12 | 2026-05-12 | HITL approved · 56 community_draft edges · unlocks 008, 010                                                                                                                                                                                                                                                                                                                                                                                            |
| 008 | UCF graph traversal query API                          | `merged`      | catalog/008-ucf-graph-traversal-api                  | gh#30 | 2026-05-13 | 2026-05-13 | 3 endpoints · two-hop JOIN · 5.89ms mean (34× under target) · 006 stub retired                                                                                                                                                                                                                                                                                                                                                                         |
| 009 | Control bundle format spec + parser + upload           | `merged`      | control-as-code/009-control-bundle-format            | gh#16 | 2026-05-11 | 2026-05-11 | unlocks 010, 011 critical path                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 010 | SCF-anchored control kit (50 SOC 2 controls)           | `merged`      | control-as-code/010-soc2-control-kit                 | gh#77 | 2026-05-13 | 2026-05-13 | 6/6 ACs · 3/3 P0 · 50 YAML bundles + coverage-check script · 43/43 TSC coverage (100%) · HITL signed off by Matt Goodrich ("010 looks good") · commit 1192b16 · unblocks 012, 037                                                                                                                                                                                                                                                                      |
| 011 | Manual control type + attestation flow                 | `merged`      | control-as-code/011-manual-control-attestation       | gh#20 | 2026-05-11 | 2026-05-11 | deps 009, 013, 036 all merged                                                                                                                                                                                                                                                                                                                                                                                                                          |
| 012 | Control state evaluation engine                        | `merged`      | controls/012-control-state-evaluation                | gh#89 | 2026-05-13 | 2026-05-13 | 7/7 ACs · 3/3 P0 · migration `_027` `control_evaluations` append-only ledger · `internal/eval` read-only ledger consumer · invariant 2 structurally enforced (one INSERT target, no evidence-write path) · IngestSubscriber + Scheduler · OPA sandbox reused · commit 2a07bdc · keystone — unblocked 016/020/030/041                                                                                                                                   |
| 013 | Evidence ledger write API + push endpoint              | `merged`      | evidence-pipeline/013-evidence-ledger-write-api      | gh#12 | 2026-05-11 | 2026-05-11 | AC-6 PARTIAL — S3 redirect awaits 036                                                                                                                                                                                                                                                                                                                                                                                                                  |
| 014 | Schema registry service (in-tree Go)                   | `merged`      | evidence-pipeline/014-schema-registry-service        | gh#8  | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 015 | NATS JetStream buffer + ingestion stage                | `merged`      | evidence-pipeline/015-nats-jetstream-ingestion-stage | gh#19 | 2026-05-11 | 2026-05-11 | dep 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 016 | Evidence freshness + drift detection                   | `merged`      | evidence/016-evidence-freshness-drift                | gh#94 | 2026-05-14 | 2026-05-14 | 6/6 ACs · 3/3 P0 · migration `_028` (`evidence_freshness` four-policy RLS + `control_drift_snapshots` append-only two-policy RLS) · drift = worst-cell rollup, stale-excluded, daily snapshots · reuses `eval.FreshnessMaxAge` (non-breaking exported wrapper) · decisions log committed · commit 6a34472                                                                                                                                              |
| 017 | Scope dimensions + applicability_expr + single-cell    | `merged`      | scope/017-scope-dimensions-applicability             | gh#9  | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 018 | FrameworkScope predicate + intersection compute        | `merged`      | scope/018-framework-scope-intersection               | gh#13 | 2026-05-11 | 2026-05-11 | implements ADR-0001                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| 019 | Risk CRUD + NIST 800-30 + 5x5 + ALE-band               | `merged`      | risk/019-risk-register-crud                          | gh#10 | 2026-05-11 | 2026-05-11 | open-q #4 resolved at merge                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 020 | Risk → control linkage + residual derivation           | `merged`      | risk/020-risk-control-linkage-residual               | gh#96 | 2026-05-14 | 2026-05-14 | 7/7 ACs · 3/3 P0 anti-criteria · migration `_029` (`risk_control_links` weight columns) reversible · residual = inherent × (1 − weighted_control_effectiveness) per canvas §6.2 · operational score reuses slice 012 `eval.Engine.Effectiveness` · `risk_residual_worker` durable consumer on `evidence.ingest` + EvaluateControl-first race fix · 36 tests pass (18 unit + 18 integration, real PG + NATS) · decisions log committed · commit 841647a |
| 021 | Exception/waiver workflow + auto-expiry                | `merged`      | risk/021-exception-waiver-workflow                   | gh#25 | 2026-05-11 | 2026-05-11 | AC-4 PARTIAL — eval-engine consumer is slice 020/012                                                                                                                                                                                                                                                                                                                                                                                                   |
| 022 | Policy library + 5 stock policies                      | `merged`      | policies/022-policy-library                          | gh#33 | 2026-05-13 | 2026-05-13 | HITL signed · 7/7 ACs · slot \_016 · chromedp spine touch · commit 3af9cb0                                                                                                                                                                                                                                                                                                                                                                             |
| 023 | Policy acknowledgment workflow                         | `merged`      | policies/023-policy-acknowledgment                   | gh#48 | 2026-05-13 | 2026-05-13 | 6/6 ACs · 3/3 P0 anti-criteria · slot \_017 · `policy.acknowledgment.v1` · commit 456d9e3                                                                                                                                                                                                                                                                                                                                                              |
| 024 | Vendor lite module                                     | `merged`      | vendor/024-vendor-lite-module                        | gh#11 | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 025 | Auditor role + scoped read-only access                 | `merged`      | auth/025-auditor-role-scoped-access                  | gh#67 | 2026-05-13 | 2026-05-13 | 6/6 ACs · 3/3 P0 anti-criteria · audit_notes + auditor_assignments + auditor.rego · query-layer enforcement on note visibility (slice 035 grc_engineer read collision) · unblocks 027, 029                                                                                                                                                                                                                                                             |
| 026 | Sample-pull primitives (Population + Sample)           | `merged`      | audit/026-sample-pull-primitives                     | gh#21 | 2026-05-11 | 2026-05-11 | deps 013, 017 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 027 | Walkthrough recording (annotated + hash/sign)          | `merged`      | audit/027-walkthrough-recording                      | gh#78 | 2026-05-13 | 2026-05-13 | 6/6 ACs · 3/3 P0 · migration `_025` (3 tables + four-policy RLS + append-only log) · ADR 0003 content-only-inputs hash · chromedp PDF reuse (no go.mod touch) · authz extends auditor + control_owner + grc_engineer rego · CodeQL TamperDetected refactor · unblocks 042                                                                                                                                                                              |
| 028 | AuditPeriod + freezing primitive                       | `merged`      | audit/028-audit-period-freezing                      | gh#58 | 2026-05-13 | 2026-05-13 | 7/7 ACs · 3/3 P0 anti-criteria · ADR 0003 (hash inputs content-only) · migration `_020` reversible · unblocks 025                                                                                                                                                                                                                                                                                                                                      |
| 029 | Audit Hub threaded comments                            | `merged`      | audit/029-audit-hub-comments                         | gh#71 | 2026-05-13 | 2026-05-13 | 6/6 ACs · 3/3 P0 · migrations `_023` (threading + append-only) + `_024` (notifications spine) · ListThreadForScope recursive CTE · in-app dispatch · OPA grc_engineer shared-thread allow · CodeQL CWE-681 front-loaded                                                                                                                                                                                                                                |
| 030 | OSCAL SSP + POA&M export pipeline                      | `ready`       | —                                                    | —     | —          | —          | **NEWLY UNBLOCKED** · deps 008, 012, 017, 018, 026, 028 all merged · JUDGMENT-type                                                                                                                                                                                                                                                                                                                                                                     |
| 031 | Monthly board brief (templated, no LLM)                | `ready`       | —                                                    | —     | —          | —          | **NEWLY UNBLOCKED** (batch 22) · deps 012/016/020 all merged                                                                                                                                                                                                                                                                                                                                                                                           |
| 032 | Quarterly board pack + investment-vs-coverage          | `not-ready`   | —                                                    | —     | —          | —          | waits on 031, 030                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 033 | Postgres RLS enforcement everywhere                    | `merged`      | auth/033-postgres-rls-enforcement                    | gh#27 | 2026-05-12 | 2026-05-12 | zero new migrations · P0 admincreds follow-up needed                                                                                                                                                                                                                                                                                                                                                                                                   |
| 034 | OIDC RP + local users                                  | `merged`      | auth/034-oidc-rp-local-users                         | gh#26 | 2026-05-11 | 2026-05-11 | unlocks 037 · ADR-0002 published                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 035 | RBAC roles + ABAC via OPA embedded                     | `merged`      | auth/035-rbac-abac-opa                               | gh#47 | 2026-05-13 | 2026-05-13 | 7/7 ACs · HITL signed · 5 roles + 10 Rego + decision audit log · OPA v1.16.2 · commit 1941a1c                                                                                                                                                                                                                                                                                                                                                          |
| 036 | S3 artifact store integration                          | `merged`      | infra/036-s3-artifact-store                          | gh#15 | 2026-05-11 | 2026-05-11 | closes 013 AC-6 PARTIAL gap                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 037 | docker-compose self-host bundle                        | `merged`      | infra/037-docker-compose-self-host                   | gh#88 | 2026-05-13 | 2026-05-13 | 7/7 ACs · 4/4 P0 · `deploy/docker/**` (compose + 4 Dockerfiles + bootstrap + .env.example) + justfile self-host recipes · option-B in-scope Go touch (`/health` route + `AttachAuthHandler` wiring + `ATLAS_BOOTSTRAP_TOKEN` + `bootstrap hash-password`) · decisions log committed · commit 42660e9 · unblocked 038                                                                                                                                   |
| 038 | Helm chart for K8s                                     | `ready`       | —                                                    | —     | —          | —          | **NEWLY UNBLOCKED** · dep 037 merged                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 039 | CLI binary distribution + release pipeline             | `merged`      | infra/039-cli-release-pipeline                       | gh#7  | 2026-05-11 | 2026-05-11 | —                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 040 | Program dashboard view                                 | `in-progress` | frontend/040-program-dashboard-view                  | —     | 2026-05-14 | —          | batch 23 · AFK · `web/**`-only · deps 005/012/016/020/024 merged                                                                                                                                                                                                                                                                                                                                                                                       |
| 041 | Control detail view + UCF mini-viz                     | `merged`      | frontend/041-control-detail-view                     | gh#93 | 2026-05-14 | 2026-05-14 | batch 22 · 6/7 ACs · 5/5 P0 · `/controls/[id]` per mockup · UCF mini-viz hand-rolled SVG · 4 BFF proxies · AC-4 PARTIAL (`GET /v1/evidence?control_id=` not on main — slice-060 placeholder pattern) · decisions log committed · commit 6db7395 · backend gaps → slice 064                                                                                                                                                                             |
| 042 | Audit workspace view (sample + walkthrough + comments) | `merged`      | frontend/042-audit-workspace-view                    | gh#80 | 2026-05-13 | 2026-05-13 | 7/7 ACs · 3/3 P0 · 32 files ~2.6k lines · BFF proxy + 12 components + Playwright E2E · period-bounded endpoints only (invariant 10) · annotation-draft-store for AC-7 · CodeQL js/xss-through-dom dismissed (React-escaped, false positive) · commit fe86f9c                                                                                                                                                                                           |
| 043 | Board pack preview/export view                         | `not-ready`   | —                                                    | —     | —          | —          | waits on 005, 032                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 044 | GitHub connector                                       | `merged`      | connectors/044-github-connector                      | gh#14 | 2026-05-11 | 2026-05-11 | first post-013 connector                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 045 | Okta connector                                         | `merged`      | connectors/045-okta-connector                        | gh#17 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 046 | 1Password connector                                    | `merged`      | connectors/046-1password-connector                   | gh#18 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 047 | osquery/Fleet endpoint connector                       | `merged`      | connectors/047-osquery-fleet-connector               | gh#23 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 048 | Jira/Linear ticket connector                           | `merged`      | connectors/048-jira-linear-connector                 | gh#22 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 049 | Manual upload / CSV / S3 / SFTP escape-hatch           | `merged`      | connectors/049-manual-upload-csv-connector           | gh#24 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 050 | Public release readiness + release automation          | `merged`      | infra/050-public-release-readiness                   | gh#34 | 2026-05-13 | 2026-05-13 | Apache 2.0 · 14/15 ACs + AC-7 closed via post-merge CoC inline · repo flipped public                                                                                                                                                                                                                                                                                                                                                                   |
| 051 | admincreds tenant derivation fix (P0 from slice 033)   | `merged`      | fix/051-admincreds-tenant-derivation                 | gh#28 | 2026-05-12 | 2026-05-12 | cross-tenant escalation closed · zero migrations · breaking API change                                                                                                                                                                                                                                                                                                                                                                                 |
| 052 | Schema + migrations for risk hierarchy + themes + DL   | `merged`      | risk/052-risk-hierarchy-schema                       | gh#31 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 8 new tables + ALTER risks · 4-policy RLS · slots \_014+\_015 · unlocks 053                                                                                                                                                                                                                                                                                                                                                                |
| 053 | Risk theme tagging + manual aggregation API            | `merged`      | risk/053-risk-theme-tagging                          | gh#32 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 17 files · severity max/wmax/sum · no migration · commit 25658dd                                                                                                                                                                                                                                                                                                                                                                           |
| 054 | Declarative aggregation rules engine                   | `merged`      | risk/054-aggregation-rules-engine                    | gh#81 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 6/6 P0 · migration \_026 (3 tables, 4-policy + append-only RLS) · YAML/JSON rule DSL · staged→active activation gate · custom_rego OPA sandbox (capabilities-restricted, no http.send) · cycle-prevention via rule_generated flag · first JUDGMENT-type slice merged without sign-off gate · commit c3ce306                                                                                                                                |
| 055 | Decision Log CRUD + linkage                            | `in-progress` | risk/055-decision-log                                | —     | 2026-05-14 | —          | batch 23 · AFK · no migration (builds on slice 052 schema) · deps 052/020/021 merged                                                                                                                                                                                                                                                                                                                                                                   |
| 056 | Hierarchical risk dashboard view                       | `not-ready`   | —                                                    | —     | —          | —          | waits on 005, 053, 054, 055                                                                                                                                                                                                                                                                                                                                                                                                                            |
| 057 | README screenshots + animated GIFs of core flows       | `not-ready`   | —                                                    | —     | —          | —          | waits on frontend views (040–043)                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 058 | User docs scaffold + 5 core pages                      | `ready`       | —                                                    | —     | —          | —          | deps 005 + 050 merged · JUDGMENT-type (docs authorship — Claude makes the call, decisions log, no sign-off gate)                                                                                                                                                                                                                                                                                                                                       |
| 059 | Per-tenant feature flags + capability toggles          | `merged`      | spine/059-feature-flags                              | gh#54 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 4/4 P0 anti-criteria · slot \_019 · `feature_flags` + `feature_flag_audit_log` tables · unlocks 060                                                                                                                                                                                                                                                                                                                                        |
| 060 | Admin settings UI (SSO · users · API keys · features)  | `merged`      | frontend/060-admin-settings-ui                       | gh#66 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 5/5 P0 · HITL signed off by Matt Goodrich 2026-05-13 ("60 looks good to me") · UI shells + BFF proxies + 5 admin pages + Playwright E2E · slice 062 backend wired · form save-wiring is a follow-up slice                                                                                                                                                                                                                                  |
| 061 | CI path-based filtering (docs-only PR fast-path)       | `merged`      | ci/061-path-filter                                   | gh#52 | 2026-05-13 | 2026-05-13 | 9/9 ACs · 4/4 P0 anti-criteria · dorny/paths-filter@v3 + stub-job pattern · saves ~80% billable on docs PRs                                                                                                                                                                                                                                                                                                                                            |
| 062 | Admin BFF backend endpoints (SSO + Users + audit-log)  | `merged`      | admin/062-admin-bff-backend-endpoints                | gh#70 | 2026-05-13 | 2026-05-13 | 10/10 ACs · 5/5 P0 anti-criteria · migration `_022` admin_audit_log_v view (UNION ALL across 7 audit-log tables) · 22 integration tests · SSRF-hardened OIDC preflight (Transport.DialContext IP re-check + redirect-disabled) · unblocks slice 060                                                                                                                                                                                                    |
| 063 | Enable `/admin/sso` form save (post-062 wire-up)       | `merged`      | frontend/063-admin-sso-form-enable                   | gh#76 | 2026-05-13 | 2026-05-13 | 9/9 ACs · 4/4 P0 · BFF proxy at `web/app/api/admin/sso/route.ts` · TanStack Query mutation + state machine · Playwright E2E extension with reload write-once check · slice 060 stopgap removed                                                                                                                                                                                                                                                         |
| 064 | Control-detail backend read endpoints                  | `in-progress` | controls/064-control-detail-backend-endpoints        | —     | 2026-05-14 | —          | batch 23 · AFK · no migration (read-only) · fills slice 041's 4 placeholders: `GET /v1/evidence?control_id=`, `GET /v1/controls/{id}/policies\|risks\|history` · deps 012/013/015/020/022 merged · 060→062 pattern                                                                                                                                                                                                                                     |

## Ready set right now

| #   | Title                             | Cluster | Est (d) | Notes                                                                                      |
| --- | --------------------------------- | ------- | ------- | ------------------------------------------------------------------------------------------ |
| 030 | OSCAL SSP + POA&M export pipeline | audit   | 4-5     | deps 008/012/017/018/026/028 merged · JUDGMENT-type · lands first Python (`oscal-bridge/`) |
| 031 | Monthly board brief (templated)   | board   | 2-3     | deps 012/016/020 merged · clean conflict-free pick, deferred from batch 23 (N=3 cap)       |
| 038 | Helm chart for K8s                | infra   | 2       | dep 037 merged · leaf slice · possible `justfile` spine touch                              |
| 058 | User docs scaffold + 5 core pages | docs    | 3       | deps 005 + 050 merged · JUDGMENT-type (docs authorship) · `justfile` spine touch           |

**Four slices ready** (030, 031, 038, 058) — 040/055/064 are in batch 23. Spine-touch conflicts: 030 (`pyproject.toml`), 038 + 058 (`justfile`) — at most one of these three per batch. 031 is fully conflict-free and pairs with anything.

## In-flight (3 worktrees building)

Batch 23 — created after the claim-stake PR merges:

- **040** — `frontend/040-program-dashboard-view` · `in-progress` since 2026-05-14 · AFK · `web/**`-only
- **055** — `risk/055-decision-log` · `in-progress` since 2026-05-14 · AFK · no migration
- **064** — `controls/064-control-detail-backend-endpoints` · `in-progress` since 2026-05-14 · AFK · no migration

Stale worktrees still on disk: `-007`, `-008`, `-009`, `-010`, `-011`, `-012`, `-013`, `-014`, `-015`, `-017`, `-018`, `-019`, `-021`, `-022`, `-023`, `-024`, `-025`, `-026`, `-027`, `-028`, `-029`, `-033`, `-034`, `-035`, `-036`, `-037`, `-039`, `-042`, `-044`, `-045`, `-046`, `-047`, `-048`, `-049`, `-050`, `-051`, `-052`, `-053`, `-054`, `-059`, `-060`, `-061`, `-062`, `-063`. Safe to `git worktree remove` whenever ready.

## Notes

- All six v1 spine slices (001–006) merged on 2026-05-11. Spine is complete.
- Parallel batch 1 (014 + 017 + 039) merged on 2026-05-11 in order 039 → 014 → 017.
- Open question **#01 SCF licensing** was cleared by the time slice 006 merged.
- Open question **#04 Risk methodology default** is **resolved** in slice 019's narrative (nist_800_30 + 5x5 + ALE-band locked in as default; FAIR pluggable for top-N risks).
- Open question **#13 solo-vs-multi-tenant** is **resolved** (2026-05-11): build multi-tenant from day one. The solo operator is a single-tenant deployment of the multi-tenant system. UI may hide tenant chrome when `tenant_count == 1`, but data model + authz never branch. Unblocks 033, 034, 037.
- Open question **#19 FrameworkScope UX** is **resolved** (2026-05-11) via [`docs/adr/0001-framework-scope-workflow.md`](../adr/0001-framework-scope-workflow.md): four-state lifecycle (`draft → review → approved → activated`), in-app attestation as primary approval evidence with optional file upload, any predicate edit re-approves (strict). Unblocks 018 (estimate bumped 1.5d → 2d for the workflow scope).
- Status changes should be committed directly to `main` as small `chore(status): NNN → <state>` commits — they're not feature work and don't need a feature branch.
