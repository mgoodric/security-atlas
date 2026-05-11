# v1 Slice Status

> Live tracker. Companion to [`_INDEX.md`](./_INDEX.md) (static backlog spec).
> Updated by `Plans/prompts/04-per-slice-template.md` (per-slice) and `Plans/prompts/05-parallel-batch.md` (parallel batch). Run `Plans/prompts/06-status-reconcile.md` when drift is suspected.

**Last reconciled:** 2026-05-11 (new slice 050 added — public release readiness)

## Drift detected — 2026-05-11 (new slice added)

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
| `merged`      | 9      |
| `in-review`   | 0      |
| `in-progress` | 3      |
| `ready`       | 7      |
| `blocked`     | 0      |
| `not-ready`   | 31     |
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

| #   | Title                                                  | Status      | Branch                                          | PR    | Started    | Merged     | Notes                                  |
| --- | ------------------------------------------------------ | ----------- | ----------------------------------------------- | ----- | ---------- | ---------- | -------------------------------------- |
| 001 | Monorepo skeleton + CI green build                     | `merged`    | spine/001-monorepo-skeleton                     | gh#1  | 2026-05-10 | 2026-05-11 | —                                      |
| 002 | Schema + migrations (6 primitives + FrameworkScope)    | `merged`    | spine/002-schema-migrations                     | gh#2  | 2026-05-10 | 2026-05-11 | —                                      |
| 003 | Evidence SDK: proto + Go push client + CLI             | `merged`    | spine/003-evidence-sdk-proto-push-client-cli    | gh#3  | 2026-05-10 | 2026-05-11 | —                                      |
| 004 | AWS connector (S3 encryption, end-to-end)              | `merged`    | spine/004-aws-connector-s3-encryption           | gh#4  | 2026-05-11 | 2026-05-11 | —                                      |
| 005 | Frontend bootstrap (Next.js + auth + SCF browser)      | `merged`    | spine/005-frontend-bootstrap                    | gh#5  | 2026-05-11 | 2026-05-11 | —                                      |
| 006 | SCF catalog importer + Framework/FrameworkVersion API  | `merged`    | catalog/006-scf-catalog-importer                | gh#6  | 2026-05-11 | 2026-05-11 | open-q #01 cleared at merge            |
| 007 | SOC 2 v2017 (TSC) crosswalk loader                     | `ready`     | —                                               | —     | —          | —          | HITL on mapping spot-check             |
| 008 | UCF graph traversal query API                          | `not-ready` | —                                               | —     | —          | —          | waits on 007                           |
| 009 | Control bundle format spec + parser + upload           | `ready`     | —                                               | —     | —          | —          | —                                      |
| 010 | SCF-anchored control kit (50 SOC 2 controls)           | `not-ready` | —                                               | —     | —          | —          | waits on 009, 007 · HITL on accuracy   |
| 011 | Manual control type + attestation flow                 | `not-ready` | —                                               | —     | —          | —          | waits on 009, 013, 036                 |
| 012 | Control state evaluation engine                        | `not-ready` | —                                               | —     | —          | —          | waits on 010, 013, 017                 |
| 013 | Evidence ledger write API + push endpoint              | `in-review` | evidence-pipeline/013-evidence-ledger-write-api | gh#12 | 2026-05-11 | —          | critical path; consumes slice 014 hook |
| 014 | Schema registry service (in-tree Go)                   | `merged`    | evidence-pipeline/014-schema-registry-service   | gh#8  | 2026-05-11 | 2026-05-11 | —                                      |
| 015 | NATS JetStream buffer + ingestion stage                | `not-ready` | —                                               | —     | —          | —          | waits on 013                           |
| 016 | Evidence freshness + drift detection                   | `not-ready` | —                                               | —     | —          | —          | waits on 012                           |
| 017 | Scope dimensions + applicability_expr + single-cell    | `merged`    | scope/017-scope-dimensions-applicability        | gh#9  | 2026-05-11 | 2026-05-11 | —                                      |
| 018 | FrameworkScope predicate + intersection compute        | `ready`     | —                                               | —     | —          | —          | open-q #19 FrameworkScope UX — gate    |
| 019 | Risk CRUD + NIST 800-30 + 5x5 + ALE-band               | `in-review` | risk/019-risk-register-crud                     | gh#10 | 2026-05-11 | —          | open-q #4 resolved (nist_800_30 lock)  |
| 020 | Risk → control linkage + residual derivation           | `not-ready` | —                                               | —     | —          | —          | waits on 019, 012                      |
| 021 | Exception/waiver workflow + auto-expiry                | `not-ready` | —                                               | —     | —          | —          | waits on 019, 017                      |
| 022 | Policy library + 5 stock policies                      | `ready`     | —                                               | —     | —          | —          | HITL on policy text                    |
| 023 | Policy acknowledgment workflow                         | `not-ready` | —                                               | —     | —          | —          | waits on 022, 034                      |
| 024 | Vendor lite module                                     | `in-review` | vendor/024-vendor-lite-module                   | gh#11 | 2026-05-11 | —          | —                                      |
| 025 | Auditor role + scoped read-only access                 | `not-ready` | —                                               | —     | —          | —          | waits on 033, 035                      |
| 026 | Sample-pull primitives (Population + Sample)           | `not-ready` | —                                               | —     | —          | —          | waits on 013, 017                      |
| 027 | Walkthrough recording (annotated + hash/sign)          | `not-ready` | —                                               | —     | —          | —          | waits on 025, 036                      |
| 028 | AuditPeriod + freezing primitive                       | `not-ready` | —                                               | —     | —          | —          | waits on 013, 016                      |
| 029 | Audit Hub threaded comments                            | `not-ready` | —                                               | —     | —          | —          | waits on 025                           |
| 030 | OSCAL SSP + POA&M export pipeline                      | `not-ready` | —                                               | —     | —          | —          | waits on 008, 012, 017, 018, 026, 028  |
| 031 | Monthly board brief (templated, no LLM)                | `not-ready` | —                                               | —     | —          | —          | waits on 012, 016, 020                 |
| 032 | Quarterly board pack + investment-vs-coverage          | `not-ready` | —                                               | —     | —          | —          | waits on 031, 030                      |
| 033 | Postgres RLS enforcement everywhere                    | `ready`     | —                                               | —     | —          | —          | open-q #13 affects UX                  |
| 034 | OIDC RP + local users                                  | `ready`     | —                                               | —     | —          | —          | open-q #13 affects UX                  |
| 035 | RBAC roles + ABAC via OPA embedded                     | `not-ready` | —                                               | —     | —          | —          | waits on 033, 034 · HITL on roles      |
| 036 | S3 artifact store integration                          | `not-ready` | —                                               | —     | —          | —          | waits on 013                           |
| 037 | docker-compose self-host bundle                        | `not-ready` | —                                               | —     | —          | —          | waits on 002, 013, 034 · open-q #13    |
| 038 | Helm chart for K8s                                     | `not-ready` | —                                               | —     | —          | —          | waits on 037                           |
| 039 | CLI binary distribution + release pipeline             | `merged`    | infra/039-cli-release-pipeline                  | gh#7  | 2026-05-11 | 2026-05-11 | —                                      |
| 040 | Program dashboard view                                 | `not-ready` | —                                               | —     | —          | —          | waits on 005, 012, 016, 020, 024       |
| 041 | Control detail view + UCF mini-viz                     | `not-ready` | —                                               | —     | —          | —          | waits on 005, 008, 012                 |
| 042 | Audit workspace view (sample + walkthrough + comments) | `not-ready` | —                                               | —     | —          | —          | waits on 025, 026, 027, 029            |
| 043 | Board pack preview/export view                         | `not-ready` | —                                               | —     | —          | —          | waits on 005, 032                      |
| 044 | GitHub connector                                       | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 045 | Okta connector                                         | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 046 | 1Password connector                                    | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 047 | osquery/Fleet endpoint connector                       | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 048 | Jira/Linear ticket connector                           | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 049 | Manual upload / CSV / S3 / SFTP escape-hatch           | `not-ready` | —                                               | —     | —          | —          | waits on 003, 013                      |
| 050 | Public release readiness + release automation          | `ready`     | —                                               | —     | —          | —          | HITL · dep 039 merged · open-q gates   |

## Ready set right now

| #   | Title                                           | Cluster           | Est (d) | Notes                                                                                                                                                           |
| --- | ----------------------------------------------- | ----------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 007 | SOC 2 v2017 (TSC) crosswalk loader              | catalog           | 1.5     | HITL                                                                                                                                                            |
| 009 | Control bundle format spec + parser + upload    | control-as-code   | 2       | —                                                                                                                                                               |
| 013 | Evidence ledger write API + push endpoint       | evidence-pipeline | 3       | critical path; uses slice 014 hook                                                                                                                              |
| 018 | FrameworkScope predicate + intersection compute | scope             | 1.5     | open-q #19 unresolved — pick-gate                                                                                                                               |
| 019 | Risk CRUD + NIST 800-30 + 5x5 + ALE-band        | risk              | 2       | open-q #4 resolved (nist_800_30 lock)                                                                                                                           |
| 022 | Policy library + 5 stock policies               | policies          | 2       | HITL                                                                                                                                                            |
| 024 | Vendor lite module                              | vendor            | 1.5     | —                                                                                                                                                               |
| 033 | Postgres RLS enforcement everywhere             | auth              | 2       | open-q #13; universal-conflict — solo                                                                                                                           |
| 034 | OIDC RP + local users                           | auth              | 1.5     | open-q #13 — solo                                                                                                                                               |
| 050 | Public release readiness + release automation   | infra             | 3       | HITL · license/persona/sanitize judgment · safe to batch with anything (no code touches outside .github/, docs/, deploy/, LICENSE, README, Plans/canvas/ prose) |

**Ten slices ready** (counts table reflects the post-batch-2 truth: 013/019/024 are `in-progress`, so the _runnable_ ready set is 7). Suggested parallel-batch trio: **013 + 019 + 024** (evidence-pipeline + risk + vendor — zero spine touches, three pre-allocated migration slots, all AFK, no open-q gates). **Slice 050 is safely batchable with feature slices** because its file scope (`.github/`, `docs/`, `deploy/`, `LICENSE`, `README.md`, `Plans/canvas/` prose) doesn't conflict with feature work touching `internal/`, `cmd/`, `migrations/`, etc.

## In-flight (3 worktrees building)

- **013** — `evidence-pipeline/013-evidence-ledger-write-api` · `in-progress` since 2026-05-11
- **019** — `risk/019-risk-register-crud` · `in-progress` since 2026-05-11
- **024** — `vendor/024-vendor-lite-module` · `in-progress` since 2026-05-11

Migration slots: 013 → `20260511000004`, 019 → `20260511000005`, 024 → `20260511000006`.

## Notes

- All six v1 spine slices (001–006) merged on 2026-05-11. Spine is complete.
- Parallel batch 1 (014 + 017 + 039) merged on 2026-05-11 in order 039 → 014 → 017.
- Open question **#01 SCF licensing** was cleared by the time slice 006 merged.
- Open question **#04 Risk methodology default** is **resolved** in slice 019's narrative (nist_800_30 + 5x5 + ALE-band locked in as default; FAIR pluggable for top-N risks).
- Open question **#13 solo-vs-multi-tenant** affects slices 033, 034, 037. Worth resolving before merging 033 or 034 — batch-pick gate.
- Open question **#19 FrameworkScope UX** affects slice 018; now-blocking since 017 has merged — batch-pick gate.
- Status changes should be committed directly to `main` as small `chore(status): NNN → <state>` commits — they're not feature work and don't need a feature branch.
