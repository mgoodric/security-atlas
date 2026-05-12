# v1 Slice Status

> Live tracker. Companion to [`_INDEX.md`](./_INDEX.md) (static backlog spec).
> Updated by `Plans/prompts/04-per-slice-template.md` (per-slice) and `Plans/prompts/05-parallel-batch.md` (parallel batch). Run `Plans/prompts/06-status-reconcile.md` when drift is suspected.

**Last reconciled:** 2026-05-11 (parallel batch 7 merged — 021, 034 → merged · 26/50 slices on main · slice 037 newly unblocked)

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
| `merged`      | 26     |
| `in-review`   | 0      |
| `in-progress` | 0      |
| `ready`       | 5      |
| `blocked`     | 0      |
| `not-ready`   | 19     |
| **Total**     | **50** |

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

| #   | Title                                                  | Status      | Branch                                               | PR    | Started    | Merged     | Notes                                                |
| --- | ------------------------------------------------------ | ----------- | ---------------------------------------------------- | ----- | ---------- | ---------- | ---------------------------------------------------- |
| 001 | Monorepo skeleton + CI green build                     | `merged`    | spine/001-monorepo-skeleton                          | gh#1  | 2026-05-10 | 2026-05-11 | —                                                    |
| 002 | Schema + migrations (6 primitives + FrameworkScope)    | `merged`    | spine/002-schema-migrations                          | gh#2  | 2026-05-10 | 2026-05-11 | —                                                    |
| 003 | Evidence SDK: proto + Go push client + CLI             | `merged`    | spine/003-evidence-sdk-proto-push-client-cli         | gh#3  | 2026-05-10 | 2026-05-11 | —                                                    |
| 004 | AWS connector (S3 encryption, end-to-end)              | `merged`    | spine/004-aws-connector-s3-encryption                | gh#4  | 2026-05-11 | 2026-05-11 | —                                                    |
| 005 | Frontend bootstrap (Next.js + auth + SCF browser)      | `merged`    | spine/005-frontend-bootstrap                         | gh#5  | 2026-05-11 | 2026-05-11 | —                                                    |
| 006 | SCF catalog importer + Framework/FrameworkVersion API  | `merged`    | catalog/006-scf-catalog-importer                     | gh#6  | 2026-05-11 | 2026-05-11 | open-q #01 cleared at merge                          |
| 007 | SOC 2 v2017 (TSC) crosswalk loader                     | `ready`     | —                                                    | —     | —          | —          | HITL on mapping spot-check                           |
| 008 | UCF graph traversal query API                          | `not-ready` | —                                                    | —     | —          | —          | waits on 007                                         |
| 009 | Control bundle format spec + parser + upload           | `merged`    | control-as-code/009-control-bundle-format            | gh#16 | 2026-05-11 | 2026-05-11 | unlocks 010, 011 critical path                       |
| 010 | SCF-anchored control kit (50 SOC 2 controls)           | `not-ready` | —                                                    | —     | —          | —          | waits on 009, 007 · HITL on accuracy                 |
| 011 | Manual control type + attestation flow                 | `merged`    | control-as-code/011-manual-control-attestation       | gh#20 | 2026-05-11 | 2026-05-11 | deps 009, 013, 036 all merged                        |
| 012 | Control state evaluation engine                        | `not-ready` | —                                                    | —     | —          | —          | waits on 010, 013, 017                               |
| 013 | Evidence ledger write API + push endpoint              | `merged`    | evidence-pipeline/013-evidence-ledger-write-api      | gh#12 | 2026-05-11 | 2026-05-11 | AC-6 PARTIAL — S3 redirect awaits 036                |
| 014 | Schema registry service (in-tree Go)                   | `merged`    | evidence-pipeline/014-schema-registry-service        | gh#8  | 2026-05-11 | 2026-05-11 | —                                                    |
| 015 | NATS JetStream buffer + ingestion stage                | `merged`    | evidence-pipeline/015-nats-jetstream-ingestion-stage | gh#19 | 2026-05-11 | 2026-05-11 | dep 013 merged                                       |
| 016 | Evidence freshness + drift detection                   | `not-ready` | —                                                    | —     | —          | —          | waits on 012                                         |
| 017 | Scope dimensions + applicability_expr + single-cell    | `merged`    | scope/017-scope-dimensions-applicability             | gh#9  | 2026-05-11 | 2026-05-11 | —                                                    |
| 018 | FrameworkScope predicate + intersection compute        | `merged`    | scope/018-framework-scope-intersection               | gh#13 | 2026-05-11 | 2026-05-11 | implements ADR-0001                                  |
| 019 | Risk CRUD + NIST 800-30 + 5x5 + ALE-band               | `merged`    | risk/019-risk-register-crud                          | gh#10 | 2026-05-11 | 2026-05-11 | open-q #4 resolved at merge                          |
| 020 | Risk → control linkage + residual derivation           | `not-ready` | —                                                    | —     | —          | —          | waits on 019, 012                                    |
| 021 | Exception/waiver workflow + auto-expiry                | `merged`    | risk/021-exception-waiver-workflow                   | gh#25 | 2026-05-11 | 2026-05-11 | AC-4 PARTIAL — eval-engine consumer is slice 020/012 |
| 022 | Policy library + 5 stock policies                      | `ready`     | —                                                    | —     | —          | —          | HITL on policy text                                  |
| 023 | Policy acknowledgment workflow                         | `not-ready` | —                                                    | —     | —          | —          | waits on 022, 034                                    |
| 024 | Vendor lite module                                     | `merged`    | vendor/024-vendor-lite-module                        | gh#11 | 2026-05-11 | 2026-05-11 | —                                                    |
| 025 | Auditor role + scoped read-only access                 | `not-ready` | —                                                    | —     | —          | —          | waits on 033, 035                                    |
| 026 | Sample-pull primitives (Population + Sample)           | `merged`    | audit/026-sample-pull-primitives                     | gh#21 | 2026-05-11 | 2026-05-11 | deps 013, 017 merged                                 |
| 027 | Walkthrough recording (annotated + hash/sign)          | `not-ready` | —                                                    | —     | —          | —          | waits on 025, 036                                    |
| 028 | AuditPeriod + freezing primitive                       | `not-ready` | —                                                    | —     | —          | —          | waits on 013, 016                                    |
| 029 | Audit Hub threaded comments                            | `not-ready` | —                                                    | —     | —          | —          | waits on 025                                         |
| 030 | OSCAL SSP + POA&M export pipeline                      | `not-ready` | —                                                    | —     | —          | —          | waits on 008, 012, 017, 018, 026, 028                |
| 031 | Monthly board brief (templated, no LLM)                | `not-ready` | —                                                    | —     | —          | —          | waits on 012, 016, 020                               |
| 032 | Quarterly board pack + investment-vs-coverage          | `not-ready` | —                                                    | —     | —          | —          | waits on 031, 030                                    |
| 033 | Postgres RLS enforcement everywhere                    | `ready`     | —                                                    | —     | —          | —          | open-q #13 resolved (multi-tenant v1)                |
| 034 | OIDC RP + local users                                  | `merged`    | auth/034-oidc-rp-local-users                         | gh#26 | 2026-05-11 | 2026-05-11 | unlocks 037 · ADR-0002 published                     |
| 035 | RBAC roles + ABAC via OPA embedded                     | `not-ready` | —                                                    | —     | —          | —          | waits on 033, 034 · HITL on roles                    |
| 036 | S3 artifact store integration                          | `merged`    | infra/036-s3-artifact-store                          | gh#15 | 2026-05-11 | 2026-05-11 | closes 013 AC-6 PARTIAL gap                          |
| 037 | docker-compose self-host bundle                        | `ready`     | —                                                    | —     | —          | —          | dep 034 merged                                       |
| 038 | Helm chart for K8s                                     | `not-ready` | —                                                    | —     | —          | —          | waits on 037                                         |
| 039 | CLI binary distribution + release pipeline             | `merged`    | infra/039-cli-release-pipeline                       | gh#7  | 2026-05-11 | 2026-05-11 | —                                                    |
| 040 | Program dashboard view                                 | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 012, 016, 020, 024                     |
| 041 | Control detail view + UCF mini-viz                     | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 008, 012                               |
| 042 | Audit workspace view (sample + walkthrough + comments) | `not-ready` | —                                                    | —     | —          | —          | waits on 025, 026, 027, 029                          |
| 043 | Board pack preview/export view                         | `not-ready` | —                                                    | —     | —          | —          | waits on 005, 032                                    |
| 044 | GitHub connector                                       | `merged`    | connectors/044-github-connector                      | gh#14 | 2026-05-11 | 2026-05-11 | first post-013 connector                             |
| 045 | Okta connector                                         | `merged`    | connectors/045-okta-connector                        | gh#17 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                 |
| 046 | 1Password connector                                    | `merged`    | connectors/046-1password-connector                   | gh#18 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                 |
| 047 | osquery/Fleet endpoint connector                       | `merged`    | connectors/047-osquery-fleet-connector               | gh#23 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                 |
| 048 | Jira/Linear ticket connector                           | `merged`    | connectors/048-jira-linear-connector                 | gh#22 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                 |
| 049 | Manual upload / CSV / S3 / SFTP escape-hatch           | `merged`    | connectors/049-manual-upload-csv-connector           | gh#24 | 2026-05-11 | 2026-05-11 | deps 003, 013 merged                                 |
| 050 | Public release readiness + release automation          | `ready`     | —                                                    | —     | —          | —          | HITL · dep 039 merged · open-q gates                 |

## Ready set right now

| #   | Title                                         | Cluster  | Est (d) | Notes                                                                          |
| --- | --------------------------------------------- | -------- | ------- | ------------------------------------------------------------------------------ |
| 007 | SOC 2 v2017 (TSC) crosswalk loader            | catalog  | 1.5     | HITL · critical path · unlocks 008, 010                                        |
| 022 | Policy library + 5 stock policies             | policies | 2       | HITL · unlocks 023 (with 034 now merged)                                       |
| 033 | Postgres RLS enforcement everywhere           | auth     | 2       | open-q #13 resolved; universal-conflict — solo run · unlocks 035 (with 034)    |
| 037 | docker-compose self-host bundle               | infra    | 2       | dep 034 merged · unlocks 038 (Helm) · open-q #13 resolved                      |
| 050 | Public release readiness + release automation | infra    | 3       | HITL · gated on open-q #1 (SCF), #3 (license), #15 (CSA); ships .github/+docs/ |

**Five slices ready** (007, 022, 033, 037, 050). After batch 7 the remaining AFK-clean ready set is even thinner — 007 + 022 + 050 are all HITL-gated; 033 is universal-conflict solo; 037 is now the only non-HITL standalone option but it's pure infra. Suggested next parallel-batch options:

1. **Solo 037 + HITL 022** — single AFK slice on 037 (docker-compose) while the HITL on 022 (policy text) waits for human review session. Lowest blast radius.
2. **Solo 033** — finally run the universal-conflict RLS-everywhere slice on its own, blocking all other work. Worth doing soon — it gates slice 035 (RBAC + ABAC via OPA) which is on the audit-flow critical path.
3. **HITL 007** — bring the SOC 2 crosswalk to a human spot-check session; this is the single biggest critical-path unlock (chain: 010 → 012 → 016 → 020 + 042).

## In-flight (0 worktrees building)

None. Batch 7 merged.

Stale worktrees still on disk from batches 1–7: `-009`, `-011`, `-013`, `-014`, `-015`, `-017`, `-018`, `-019`, `-021`, `-024`, `-026`, `-034`, `-036`, `-039`, `-044`, `-045`, `-046`, `-047`, `-048`, `-049`. Safe to `git worktree remove` whenever ready.

## Notes

- All six v1 spine slices (001–006) merged on 2026-05-11. Spine is complete.
- Parallel batch 1 (014 + 017 + 039) merged on 2026-05-11 in order 039 → 014 → 017.
- Open question **#01 SCF licensing** was cleared by the time slice 006 merged.
- Open question **#04 Risk methodology default** is **resolved** in slice 019's narrative (nist_800_30 + 5x5 + ALE-band locked in as default; FAIR pluggable for top-N risks).
- Open question **#13 solo-vs-multi-tenant** is **resolved** (2026-05-11): build multi-tenant from day one. The solo operator is a single-tenant deployment of the multi-tenant system. UI may hide tenant chrome when `tenant_count == 1`, but data model + authz never branch. Unblocks 033, 034, 037.
- Open question **#19 FrameworkScope UX** is **resolved** (2026-05-11) via [`docs/adr/0001-framework-scope-workflow.md`](../adr/0001-framework-scope-workflow.md): four-state lifecycle (`draft → review → approved → activated`), in-app attestation as primary approval evidence with optional file upload, any predicate edit re-approves (strict). Unblocks 018 (estimate bumped 1.5d → 2d for the workflow scope).
- Status changes should be committed directly to `main` as small `chore(status): NNN → <state>` commits — they're not feature work and don't need a feature branch.
