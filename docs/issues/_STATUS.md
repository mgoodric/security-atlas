# v1 Slice Status

> Live tracker. Companion to [`_INDEX.md`](./_INDEX.md) (static backlog spec).
> Updated by `Plans/prompts/04-per-slice-template.md` (per-slice) and `Plans/prompts/05-parallel-batch.md` (parallel batch). Run `Plans/prompts/06-status-reconcile.md` when drift is suspected.

**Last reconciled:** 2026-05-12 (batch 9 merged — slice 007 → merged · 29/51 on main · slices 008 + 010 newly unblocked)

## Drift detected — 2026-05-12 (batch 9 merged — slice 007 SOC 2 crosswalk)

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
| `merged`      | 29     |
| `in-review`   | 0      |
| `in-progress` | 0      |
| `ready`       | 5      |
| `blocked`     | 0      |
| `not-ready`   | 17     |
| **Total**     | **51** |

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

| #   | Title                                                  | Status      | Branch                                               | PR    | Started    | Merged     | Notes                                                                    |
| --- | ------------------------------------------------------ | ----------- | ---------------------------------------------------- | ----- | ---------- | ---------- | ------------------------------------------------------------------------ |
| 001 | Monorepo skeleton + CI green build                     | `merged`    | spine/001-monorepo-skeleton                          | gh#1  | 2026-05-10 | 2026-05-11 | —                                                                        |
| 002 | Schema + migrations (6 primitives + FrameworkScope)    | `merged`    | spine/002-schema-migrations                          | gh#2  | 2026-05-10 | 2026-05-11 | —                                                                        |
| 003 | Evidence SDK: proto + Go push client + CLI             | `merged`    | spine/003-evidence-sdk-proto-push-client-cli         | gh#3  | 2026-05-10 | 2026-05-11 | —                                                                        |
| 004 | AWS connector (S3 encryption, end-to-end)              | `merged`    | spine/004-aws-connector-s3-encryption                | gh#4  | 2026-05-11 | 2026-05-11 | —                                                                        |
| 005 | Frontend bootstrap (Next.js + auth + SCF browser)      | `merged`    | spine/005-frontend-bootstrap                         | gh#5  | 2026-05-11 | 2026-05-11 | —                                                                        |
| 006 | SCF catalog importer + Framework/FrameworkVersion API  | `merged`    | catalog/006-scf-catalog-importer                     | gh#6  | 2026-05-11 | 2026-05-11 | open-q #01 cleared at merge                                              |
| 007 | SOC 2 v2017 (TSC) crosswalk loader                     | `merged`    | catalog/007-soc2-crosswalk-loader                    | gh#29 | 2026-05-12 | 2026-05-12 | HITL approved · 56 community_draft edges · unlocks 008, 010              |
| 008 | UCF graph traversal query API                          | `ready`     | —                                                    | —     | —          | —          | dep 007 merged                                                           |
| 009 | Control bundle format spec + parser + upload           | `merged`    | control-as-code/009-control-bundle-format            | gh#16 | 2026-05-11 | 2026-05-11 | unlocks 010, 011 critical path                                           |
| 010 | SCF-anchored control kit (50 SOC 2 controls)           | `ready`     | —                                                    | —     | —          | —          | deps 009, 007 merged · HITL on 50-control accuracy                       |
| 011 | Manual control type + attestation flow                 | `merged`    | control-as-code/011-manual-control-attestation       | gh#20 | 2026-05-11 | 2026-05-11 | deps 009, 013, 036 all merged                                            |
| 012 | Control state evaluation engine                        | `not-ready` | —                                                    | —     | —          | —          | waits on 010, 013, 017                                                   |
| 013 | Evidence ledger write API + push endpoint              | `merged`    | evidence-pipeline/013-evidence-ledger-write-api      | gh#12 | 2026-05-11 | 2026-05-11 | AC-6 PARTIAL — S3 redirect awaits 036                                    |
| 014 | Schema registry service (in-tree Go)                   | `merged`    | evidence-pipeline/014-schema-registry-service        | gh#8  | 2026-05-11 | 2026-05-11 | —                                                                        |
| 015 | NATS JetStream buffer + ingestion stage                | `merged`    | evidence-pipeline/015-nats-jetstream-ingestion-stage | gh#19 | 2026-05-11 | 2026-05-11 | dep 013 merged                                                           |
| 016 | Evidence freshness + drift detection                   | `not-ready` | —                                                    | —     | —          | —          | waits on 012                                                             |
| 017 | Scope dimensions + applicability_expr + single-cell    | `merged`    | scope/017-scope-dimensions-applicability             | gh#9  | 2026-05-11 | 2026-05-11 | —                                                                        |
| 018 | FrameworkScope predicate + intersection compute        | `merged`    | scope/018-framework-scope-intersection               | gh#13 | 2026-05-11 | 2026-05-11 | implements ADR-0001                                                      |
| 019 | Risk CRUD + NIST 800-30 + 5x5 + ALE-band               | `merged`    | risk/019-risk-register-crud                          | gh#10 | 2026-05-11 | 2026-05-11 | open-q #4 resolved at merge                                              |
| 020 | Risk → control linkage + residual derivation           | `not-ready` | —                                                    | —     | —          | —          | waits on 019, 012                                                        |
| 021 | Exception/waiver workflow + auto-expiry                | `merged`    | risk/021-exception-waiver-workflow                   | gh#25 | 2026-05-11 | 2026-05-11 | AC-4 PARTIAL — eval-engine consumer is slice 020/012                     |
| 022 | Policy library + 5 stock policies                      | `ready`     | —                                                    | —     | —          | —          | HITL on policy text                                                      |
| 023 | Policy acknowledgment workflow                         | `not-ready` | —                                                    | —     | —          | —          | waits on 022, 034                                                        |
| 024 | Vendor lite module                                     | `merged`    | vendor/024-vendor-lite-module                        | gh#11 | 2026-05-11 | 2026-05-11 | —                                                                        |
| 025 | Auditor role + scoped read-only access                 | `not-ready` | —                                                    | —     | —          | —          | waits on 033, 035                                                        |
| 026 | Sample-pull primitives (Population + Sample)           | `merged`    | audit/026-sample-pull-primitives                     | gh#21 | 2026-05-11 | 2026-05-11 | deps 013, 017 merged                                                     |
| 027 | Walkthrough recording (annotated + hash/sign)          | `not-ready` | —                                                    | —     | —          | —          | waits on 025, 036                                                        |
| 028 | AuditPeriod + freezing primitive                       | `not-ready` | —                                                    | —     | —          | —          | waits on 013, 016                                                        |
| 029 | Audit Hub threaded comments                            | `not-ready` | —                                                    | —     | —          | —          | waits on 025                                                             |
| 030 | OSCAL SSP + POA&M export pipeline                      | `not-ready` | —                                                    | —     | —          | —          | waits on 008, 012, 017, 018, 026, 028                                    |
| 031 | Monthly board brief (templated, no LLM)                | `not-ready` | —                                                    | —     | —          | —          | waits on 012, 016, 020                                                   |
| 032 | Quarterly board pack + investment-vs-coverage          | `not-ready` | —                                                    | —     | —          | —          | waits on 031, 030                                                        |
| 033 | Postgres RLS enforcement everywhere                    | `merged`    | auth/033-postgres-rls-enforcement                    | gh#27 | 2026-05-12 | 2026-05-12 | zero new migrations · P0 admincreds follow-up needed                     |
| 034 | OIDC RP + local users                                  | `merged`    | auth/034-oidc-rp-local-users                         | gh#26 | 2026-05-11 | 2026-05-11 | unlocks 037 · ADR-0002 published                                         |
| 035 | RBAC roles + ABAC via OPA embedded                     | `ready`     | —                                                    | —     | —          | —          | deps 033, 034 merged · HITL on role design                               |
| 036 | S3 artifact store integration                          | `merged`    | infra/036-s3-artifact-store                          | gh#15 | 2026-05-11 | 2026-05-11 | closes 013 AC-6 PARTIAL gap                                              |
| 037 | docker-compose self-host bundle                        | `not-ready` | —                                                    | —     | —          | —          | waits on 010 (per slice file deps) · 034 merged but 010 still gates AC-4 |
| 038 | Helm chart for K8s                                     | `not-ready` | —                                                    | —     | —          | —          | waits on 037                                                             |
| 039 | CLI binary distribution + release pipeline             | `merged`    | infra/039-cli-release-pipeline                       | gh#7  | 2026-05-11 | 2026-05-11 | —                                                                        |
| 040 | Program dashboard view                                 | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 012, 016, 020, 024                                         |
| 041 | Control detail view + UCF mini-viz                     | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 008, 012                                                   |
| 042 | Audit workspace view (sample + walkthrough + comments) | `not-ready` | —                                                    | —     | —          | —          | waits on 025, 026, 027, 029                                              |
| 043 | Board pack preview/export view                         | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 032                                                        |
| 044 | GitHub connector                                       | `merged`    | connectors/044-github-connector                      | gh#14 | 2026-05-11 | 2026-05-11 | first post-013 connector                                                 |
| 045 | Okta connector                                         | `merged`    | connectors/045-okta-connector                        | gh#17 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                     |
| 046 | 1Password connector                                    | `merged`    | connectors/046-1password-connector                   | gh#18 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                     |
| 047 | osquery/Fleet endpoint connector                       | `merged`    | connectors/047-osquery-fleet-connector               | gh#23 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                     |
| 048 | Jira/Linear ticket connector                           | `merged`    | connectors/048-jira-linear-connector                 | gh#22 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                     |
| 049 | Manual upload / CSV / S3 / SFTP escape-hatch           | `merged`    | connectors/049-manual-upload-csv-connector           | gh#24 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                                     |
| 050 | Public release readiness + release automation          | `ready`     | —                                                    | —     | —          | —          | HITL · dep 039 merged · open-q gates                                     |
| 051 | admincreds tenant derivation fix (P0 from slice 033)   | `merged`    | fix/051-admincreds-tenant-derivation                 | gh#28 | 2026-05-12 | 2026-05-12 | cross-tenant escalation closed · zero migrations · breaking API change   |

## Ready set right now

| #   | Title                                         | Cluster  | Est (d) | Notes                                                                                   |
| --- | --------------------------------------------- | -------- | ------- | --------------------------------------------------------------------------------------- |
| 008 | UCF graph traversal query API                 | catalog  | 1.5     | **NEWLY UNBLOCKED · AFK-clean** · dep 007 merged · unlocks 030 (OSCAL export), 041 (UI) |
| 010 | SCF-anchored control kit (50 SOC 2 controls)  | controls | 2       | NEWLY UNBLOCKED · HITL on 50-control accuracy (machinery+draft pattern reusable)        |
| 022 | Policy library + 5 stock policies             | policies | 2       | HITL · unlocks 023 (with 034 merged)                                                    |
| 035 | RBAC roles + ABAC via OPA embedded            | auth     | 2       | HITL on role design · unlocks 025, 042 (audit-flow path)                                |
| 050 | Public release readiness + release automation | infra    | 3       | HITL · gated on open-q #1 (SCF), #3 (license), #15 (CSA); ships .github/+docs/          |

**Five slices ready** (008, 010, 022, 035, 050). **Slice 008 is the first AFK-clean ready slice since slice 037 was corrected back to not-ready in batch 8** — a graph-traversal API with no HITL component. 010 is HITL but the slice-007 machinery+draft+pair-review pattern proven this batch is directly reusable for 010's 50 SOC 2 controls. 035 + 022 also amenable to that pattern.

Next-batch options:

1. **AFK 008 (RECOMMENDED — restores AFK throughput)** — Cypher-ish graph queries over UCF graph + REST endpoints. No HITL. Unlocks slice 030 (OSCAL SSP/POA&M export) + slice 041 (Control detail view UCF mini-viz). 1.5d.
2. **HITL 010 session (machinery+draft pattern)** — port the slice-007 pair-review shape to the 50 SOC 2 stock controls (agent drafts 50 controls + the SCF anchor selection per slice 009's bundle format, user spot-checks low-confidence rows). Once 010 merges, 037 (docker-compose) AND 012 (control state eval) both unblock.
3. **HITL 035 / 022 sessions** — same pattern. Modest content review.
4. **Resolve open-q #1/#3/#15** for 050 (release readiness).

## In-flight (0 worktrees building)

None. Batch 9 merged.

Stale worktrees still on disk: `-007`, `-009`, `-011`, `-013`, `-014`, `-015`, `-017`, `-018`, `-019`, `-021`, `-024`, `-026`, `-033`, `-034`, `-036`, `-039`, `-044`, `-045`, `-046`, `-047`, `-048`, `-049`, `-051`. Safe to `git worktree remove` whenever ready.

## Notes

- All six v1 spine slices (001–006) merged on 2026-05-11. Spine is complete.
- Parallel batch 1 (014 + 017 + 039) merged on 2026-05-11 in order 039 → 014 → 017.
- Open question **#01 SCF licensing** was cleared by the time slice 006 merged.
- Open question **#04 Risk methodology default** is **resolved** in slice 019's narrative (nist_800_30 + 5x5 + ALE-band locked in as default; FAIR pluggable for top-N risks).
- Open question **#13 solo-vs-multi-tenant** is **resolved** (2026-05-11): build multi-tenant from day one. The solo operator is a single-tenant deployment of the multi-tenant system. UI may hide tenant chrome when `tenant_count == 1`, but data model + authz never branch. Unblocks 033, 034, 037.
- Open question **#19 FrameworkScope UX** is **resolved** (2026-05-11) via [`docs/adr/0001-framework-scope-workflow.md`](../adr/0001-framework-scope-workflow.md): four-state lifecycle (`draft → review → approved → activated`), in-app attestation as primary approval evidence with optional file upload, any predicate edit re-approves (strict). Unblocks 018 (estimate bumped 1.5d → 2d for the workflow scope).
- Status changes should be committed directly to `main` as small `chore(status): NNN → <state>` commits — they're not feature work and don't need a feature branch.
