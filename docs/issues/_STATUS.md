# v1 Slice Status — GENERATED

> **GENERATED FILE — do not edit by hand.** Produced by `scripts/gen-status.sh`
> (`just status`). State is derived from git history + open PRs + branches +
> `_events.jsonl`. To change a slice's state: merge a PR, push a branch, or append
> an event with `just event <slice> <state> [note]` — never edit this file.
>
> Precedence: (git-merged or a `merged` event) > in-review > in-progress > other events > ready.

**Generated:** 2026-06-12 · **Total slices:** 624

## Counts

| State | Count |
| --- | --- |
| merged | 550 |
| in-progress | 1 |
| ready | 71 |
| not-ready | 2 |

## Ready set

095 112 113 114 115 118 134 228 230 232 272 323 330 336 338 339 354 355 356 357 358 368 384 415 418 419 420 434 435 436 440 442 446 450 452 453 468 483 484 499 500 501 502 504 505 506 507 517 518 528 536 537 541 544 545 546 567 651 658 676 694 695 696 697 698 699 700 701 702 703 733

## In-flight

| Slice | State | PR / Branch |
| --- | --- | --- |
| 448 | in-progress | feat/448-bulk-ops-saved-views |

## All slices

| Slice | Title | State | PR | Merged | Ref/Note |
| --- | --- | --- | --- | --- | --- |
| 001 | Monorepo skeleton + CI green build | merged |  |  | v1 backlog — slice-071 audit |
| 002 | Schema + migrations for six primitives + FrameworkScope + ten... | merged |  |  | v1 backlog — slice-071 audit |
| 003 | Evidence SDK: proto + Go push client + CLI | merged |  |  | v1 backlog — slice-071 audit |
| 004 | AWS connector (S3 encryption evidence_kind, end-to-end) | merged |  |  | v1 backlog — slice-071 audit |
| 005 | Frontend bootstrap (Next.js shell + auth shell + SCF browsing... | merged |  |  | v1 backlog — slice-071 audit |
| 006 | SCF catalog importer + Framework / FrameworkVersion API | merged |  |  | v1 backlog — slice-071 audit |
| 007 | SOC 2 v2017 (TSC) crosswalk loader | merged |  |  | v1 backlog — slice-071 audit |
| 008 | UCF graph traversal query API | merged |  |  | v1 backlog — slice-071 audit |
| 009 | Control bundle format spec + parser + upload API | merged |  |  | v1 backlog — slice-071 audit |
| 010 | SCF-anchored control kit (50 SOC 2 controls bundled) | merged |  |  | v1 backlog — slice-071 audit |
| 011 | Manual control type + attestation/upload flow | merged |  |  | v1 backlog — slice-071 audit |
| 012 | Control state evaluation engine | merged |  |  | v1 backlog — slice-071 audit |
| 013 | Evidence ledger write API + push endpoint | merged |  |  | v1 backlog — slice-071 audit |
| 014 | Schema registry service (in-tree Go service) | merged |  |  | v1 backlog — slice-071 audit |
| 015 | NATS JetStream evidence buffer + ingestion stage | merged |  |  | v1 backlog — slice-071 audit |
| 016 | Evidence freshness + drift detection + stale flagging | merged |  |  | v1 backlog — slice-071 audit |
| 017 | Scope dimensions + scope cell + applicability_expr engine + d... | merged |  |  | v1 backlog — slice-071 audit |
| 018 | FrameworkScope predicate + intersection compute + four-state ... | merged |  |  | v1 backlog — slice-071 audit |
| 019 | Risk CRUD + NIST 800-30 default + 5x5 + ALE-band + methodolog... | merged |  |  | v1 backlog — slice-071 audit |
| 020 | Risk → control linkage + residual risk derivation | merged |  |  | v1 backlog — slice-071 audit |
| 021 | Exception / waiver workflow with auto-expiry + expiration cal... | merged |  |  | v1 backlog — slice-071 audit |
| 022 | Policy entity + version control + 5 stock policies bundled | merged |  |  | v1 backlog — slice-071 audit |
| 023 | Policy acknowledgment workflow + role-required attestation | merged |  |  | v1 backlog — slice-071 audit |
| 024 | Vendor lite module | merged |  |  | v1 backlog — slice-071 audit |
| 025 | Auditor role + scoped read-only access | merged |  |  | v1 backlog — slice-071 audit |
| 026 | Sample-pull primitives (Population + Sample with deterministi... | merged |  |  | v1 backlog — slice-071 audit |
| 027 | Walkthrough recording: annotated capture + transcript + hash/... | merged |  |  | v1 backlog — slice-071 audit |
| 028 | AuditPeriod + freezing primitive (evidence horizon shift) | merged |  |  | v1 backlog — slice-071 audit |
| 029 | Audit Hub threaded comments | merged |  |  | v1 backlog — slice-071 audit |
| 030 | OSCAL SSP + POA&M export pipeline | merged |  |  | v1 backlog — slice-071 audit |
| 031 | Monthly board brief (templated, no LLM) | merged |  |  | v1 backlog — slice-071 audit |
| 032 | Quarterly board pack with templated narrative + investment-vs... | merged |  |  | v1 backlog — slice-071 audit |
| 033 | Postgres RLS enforcement on every tenant-scoped table + tenan... | merged |  |  | v1 backlog — slice-071 audit |
| 034 | OIDC RP for SSO + local users for solo deployments | merged |  |  | v1 backlog — slice-071 audit |
| 035 | RBAC roles (5) + ABAC via OPA embedded library | merged |  |  | v1 backlog — slice-071 audit |
| 036 | S3 artifact store integration (per-tenant prefixes + tenant-s... | merged |  |  | v1 backlog — slice-071 audit |
| 037 | docker-compose self-host bundle | merged |  |  | v1 backlog — slice-071 audit |
| 038 | Helm chart for K8s deployment | merged |  |  | v1 backlog — slice-071 audit |
| 039 | CLI binary distribution + release pipeline | merged |  |  | v1 backlog — slice-071 audit |
| 040 | Program dashboard view | merged |  |  | v1 backlog — slice-071 audit |
| 041 | Control detail view + UCF mini-viz + STRM coverage table | merged |  |  | v1 backlog — slice-071 audit |
| 042 | Audit workspace view (sample-pull + walkthrough + comments) +... | merged |  |  | v1 backlog — slice-071 audit |
| 043 | Board pack preview/export view | merged |  |  | v1 backlog — slice-071 audit |
| 044 | GitHub connector | merged |  |  | v1 backlog — slice-071 audit |
| 045 | Okta connector | merged |  |  | v1 backlog — slice-071 audit |
| 046 | 1Password connector | merged |  |  | v1 backlog — slice-071 audit |
| 047 | osquery / Fleet endpoint connector | merged |  |  | v1 backlog — slice-071 audit |
| 048 | Jira / Linear ticket evidence connector | merged |  |  | v1 backlog — slice-071 audit |
| 049 | Manual upload / CSV / S3 / SFTP escape-hatch connector | merged |  |  | v1 backlog — slice-071 audit |
| 050 | Public release readiness + release automation | merged |  |  | v1 backlog — slice-071 audit |
| 051 | admincreds Issue/List derive tenant from credential, not requ... | merged |  |  | v1 backlog — slice-071 audit |
| 052 | Schema + migrations for risk hierarchy + themes + Decision Log | merged |  |  | v1 backlog — slice-071 audit |
| 053 | Risk theme tagging + manual aggregation API | merged |  |  | v1 backlog — slice-071 audit |
| 054 | Declarative aggregation rules engine | merged |  |  | v1 backlog — slice-071 audit |
| 055 | Decision Log CRUD + linkage | merged |  |  | v1 backlog — slice-071 audit |
| 056 | Hierarchical risk dashboard view | merged |  |  | v1 backlog — slice-071 audit |
| 057 | README screenshots + animated GIFs of core flows | merged |  |  | v1 backlog — slice-071 audit |
| 058 | User docs scaffold + 5 core pages | merged |  |  | v1 backlog — slice-071 audit |
| 059 | Per-tenant feature flags + capability toggles | merged | #54 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 060 | Admin settings UI (SSO · users · API keys · feature flags ... | merged | #66 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 061 | CI path-based filtering (skip expensive jobs for docs-only ch... | merged | #52 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 062 | Admin BFF backend endpoints (SSO + Users + Unified audit log) | merged | #70 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 063 | Enable `/admin/sso` form save (post-slice-062 wire-up) | merged | #76 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 064 | Control-detail backend read endpoints | merged | #102 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 065 | self-host bundle P0 fixes (slice 037 follow-up) | merged | #115 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 066 | Dashboard backend read endpoints | merged | #109 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 067 | Risk-hierarchy backend read endpoints | merged | #113 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 068 | Schema-registry evidence_kind identifier fix | merged | #125 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 069 | verification suite: Playwright runner wiring + frontend unit ... | merged | #132 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 070 | Onboarding walkthroughs (showboat-generated) | merged | #200 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 071 | Repo cleanup audit + in-place updates | merged | #197 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 072 | Version string surfaced in the UI | merged | #148 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 073 | First-time login UX + bootstrap-token discoverability | merged | #149 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 074 | Logo design candidates (Media:Art, human approval pending) | merged | #180 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 075 | Logo integration (post-approval of slice 074) | merged | #189 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 076 | Metrics catalog + cascade + observation store | merged | #203 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 077 | Dependabot `deps` commit prefix + dedicated release-please se... | merged | #147 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 078 | Unblock `npm run lint` after ESLint 10 + eslint-plugin-react ... | merged | #194 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 079 | Quarantine `Frontend · Playwright e2e` until the seed-data h... | merged | #164 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 080 | Fix release-tag infrastructure (GoReleaser + mkdocs publish) | merged | #166 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 081 | Pre-push hook + post-status-flip pre-commit re-run guidance | merged | #165 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 082 | Playwright e2e seed-data harness | merged | #253 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 083 | Pre-push hook: add `npm run lint -w web` once slice 078 lands | merged | #209 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 084 | cosign v3 + goreleaser-action@v7 migration | merged | #425 | 2026-05-20 | f15839a3 |
| 085 | Security audit Q2 2026 | merged | #168 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 086 | Fix open redirect on signIn `from` parameter | merged | #172 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 087 | Security HTTP headers middleware | merged | #171 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 088 | CLI `http.Client` explicit timeout | merged | #173 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 089 | Dependency vulnerability scanning (govulncheck + npm audit + ... | merged | #177 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 090 | Bump `govulncheck` pin for Go 1.26 toolchain compatibility | merged | #192 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 091 | Root-route redirect (replace stock create-next-app template) | merged | #210 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 092 | Version display end-to-end fix (publish-arg + middleware exem... | merged | #208 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 093 | Mockups for missing top-level pages (controls / evidence / ri... | merged | #215 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 094 | Compliance calendar (cross-business view of upcoming audits +... | merged | #218 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 095 | Re-upgrade ESLint to 10.x once `eslint-plugin-react` ships co... | ready |  |  |  |
| 096 | Repo cleanup deletion candidates (follow-on to slice 071) | merged | #205 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 097 | Metrics dashboard + cascade-tree visualization (follow-on to ... | merged | #214 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 098 | /controls list view (per slice 093 mockup) | merged | #223 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 099 | /evidence list view (per slice 093 mockup) | merged | #232 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 100 | /risks list view (per slice 093 mockup) | merged | #226 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 101 | /policies list view (per slice 093 mockup) | merged | #233 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 102 | /audits list view (per slice 093 mockup) | merged | #227 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 103 | /settings page (per slice 093 mockup) | merged | #238 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 104 | `GET /v1/anchors?include=state` extension for `/controls` lis... | merged | #228 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 105 | Risk-create UI for the /risks empty-state CTA | merged | #231 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 106 | `GET /v1/evidence` backend extension (spillover from 099) | merged | #240 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 107 | `GET /v1/policies?include=ack_rate` extension for `/policies`... | merged | #239 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 108 | `/v1/me/*` profile, preferences, and sessions endpoints | merged | #246 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 109 | Pin the sqlc toolchain version so `sqlc generate` is reproduc... | merged | #243 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 110 | BFF forwards atlas_session cookie alongside bearer for /v1/me... | merged | #249 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 111 | Enable full assertions in `dashboard.spec.ts` (post-082 stabi... | merged | #111 | 2026-05-21 | 8c3b3551 |
| 112 | Extend `control-detail.sql` to FULL coverage + enable asserti... | ready |  |  |  |
| 113 | Extend `audit-workspace.sql` to FULL coverage + enable assert... | ready |  |  |  |
| 114 | Extend `risk-hierarchy.sql` to FULL coverage + enable asserti... | ready |  |  |  |
| 115 | Extend `admin-bootstrap.sql` to FULL coverage + enable assert... | ready |  |  |  |
| 116 | Promote `Frontend · Playwright e2e` to required-checks in br... | merged | #494 | 2026-05-22 | 963e60e8 |
| 117 | Adopt StepSecurity Harden-Runner (audit mode → block mode) | merged | #262 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 118 | Promote StepSecurity Harden-Runner to block mode | ready |  |  |  |
| 119 | Fix recurring `port 3000 already in use` flake in `Frontend ... | merged | #259 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 120 | Audit and remove phantom (unused) dependencies across all man... | merged | #264 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 121 | Add OTel SDK to atlas (traces + metrics + Go runtime telemetry) | merged | #269 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 122 | Make slice 082's `seedFromFixture()` harness idempotent on `a... | merged | #265 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 123 | Investigate + fix 4 e2e specs unmasked by slice 119's port-30... | merged | #286 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 124 | Unified audit-log aggregation API (read-only across 9 per-dom... | merged | #267 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 125 | Frontend `/audit-log` page (consumes slice 124's unified aggr... | merged | #276 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 126 | External audit-log sink (tamper-evident retention outside the... | merged | #277 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 127 | Branch-protection drift fix + recurring drift-detect CI job | merged | #285 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 128 | SHA-pin every GitHub Action across all workflows (+ CI guard ... | merged | #288 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 129 | Extend slice-124 `/v1/admin/audit-log/unified` with `actor_name` | merged | #282 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 130 | Extend `/api/admin/me` BFF + `/v1/admin/credentials` backend ... | merged | #281 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 131 | Fix slice 029 integration tests' `SET LOCAL $1` syntax error | merged | #484 | 2026-05-22 | 29ab44d4 |
| 132 | README refresh with fresh screenshots (v1.10.0+ baseline) | merged | #296 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 133 | mkdocs user docs content refresh (slice 058 follow-on) | merged | #389 | 2026-05-20 | b64cb069 |
| 134 | Refresh slice-070 onboarding walkthroughs against current mai... | ready |  |  |  |
| 135 | Data-export library + audit-log export (reference implementat... | merged | #297 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 136 | Risk register data export (CSV / JSON / XLSX) | merged | #378 | 2026-05-19 | c80bec31 |
| 137 | Controls UCF graph data export (CSV / JSON / XLSX) | merged | #384 | 2026-05-19 | 4300cde2 |
| 138 | Ledger entities export (evidence + policies + exceptions + sa... | merged | #387 | 2026-05-19 | a171a0aa |
| 139 | Audit periods + vendors data export (CSV / JSON / XLSX) | merged | #379 | 2026-05-19 | 3522c644 |
| 140 | OpenAPI 3.1 spec + Redoc UI + drift-detect CI guard | merged | #300 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 141 | Multi-tenant login + tenant picker + persistent header switcher | merged | #458 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 142 | super_admin role: full schema + management surface (slice 141... | merged | #480 | 2026-05-21 | ea674f67 |
| 143 | Create-tenant flow (super_admin-gated) | merged | #485 | 2026-05-22 | 73326954 |
| 144 | Rename-tenant flow (per-tenant admin or super_admin) | merged | #462 | 2026-05-21 | dd2e8762 |
| 145 | Data-export hardening: payload_json redaction + per-tenant co... | merged | #375 | 2026-05-19 | 637b8dce |
| 146 | Fix BFF cookie regression in production-build standalone | merged | #327 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 147 | Dashboard panels still render "endpoint does not exist" place... | merged | #309 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 148 | Calendar page fails to load events despite slice 094 merge | merged | #310 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 149 | Audits page "Create audit period" button redirects to /admin ... | merged | #315 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 150 | Empty-set robustness audit: list endpoints return 500 on fres... | merged | #316 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 151 | Risk creation form missing control-link UI (slice 105 incompl... | merged | #324 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 152 | Control detail 404 on fresh install (seed SOC 2 kit OR friend... | merged | #323 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 153 | Logo not rendering in header + login screen on production-bui... | merged | #330 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 154 | Settings page audit + parity check against mockup | merged | #338 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 155 | Questionnaire feature: design + build (CAIQ / SIG / HECVAT re... | merged | #433 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 156 | Dashboard read endpoints likely have the same OPA admit omiss... | merged | #319 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 157 | Dashboard: re-point upcoming-panel to /v1/upcoming + top-risk... | merged | #320 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 158 | branch-protection drift: real permission fix (PR #311 follow-on) | merged | #336 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 159 | Resolve sqlc-toolchain CI binary drift (slice 109 follow-on) | merged | #347 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 160 | Add missing fixtures/e2e/control-detail-empty.sql (slice 152 ... | merged | #342 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 161 | Diagnose + fix auth-open-redirect.spec.ts drift (slice 086 fo... | merged | #343 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 162 | Active sessions wire shape — augment with user_agent, ip_ad... | merged | #346 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 163 | Settings API tokens — Rotate action | merged | #351 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 164 | Settings Playwright e2e — seed fixture + un-comment AC bodies | merged | #354 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 165 | Diagnose + fix 11 settings.spec.ts AC failures (slice 164 fol... | merged | #360 | 2026-05-19 | e725893c |
| 166 | Production bug: /settings crashes when an api_keys row has em... | merged | #362 | 2026-05-19 | e76e5cf9 |
| 167 | Logo redesign + replace existing assets across all usages | merged | #367 | 2026-05-19 | 516e0436 |
| 168 | Diagnose + fix remaining 4 settings.spec.ts AC failures (slic... | merged | #368 | 2026-05-19 | 9f70f08b |
| 169 | Apply slice 166 null-safe allowed_kinds helper to admin/api-k... | merged | #364 | 2026-05-19 | 632eeb73 |
| 170 | Settings theme picker doesn't restore from localStorage after... | merged | #370 | 2026-05-19 | 2c89eb35 |
| 171 | Settings spec AC-3 notifications PATCH never fires (slice 168... | merged | #372 | 2026-05-19 | 9d01de27 |
| 172 | MCP server foundation + read-only tools | merged | #382 | 2026-05-19 | 95dd94d9 |
| 173 | MCP server write tools (create / update operations) | merged | #396 | 2026-05-20 | 0de83f62 |
| 174 | UCF anchor catalog export (nested / two-sheet) | merged | #410 | 2026-05-20 | 79eb8295 |
| 175 | Control bundle history export (lineage including superseded v... | merged | #495 | 2026-05-22 | fac14500 |
| 176 | Logo variant follows app theme + README/docs asset refresh | merged | #394 | 2026-05-20 | 6eb07017 |
| 177 | Exceptions list-page UI surface | merged | #393 | 2026-05-20 | ee0163ac |
| 178 | UI honesty audit harness + first-pass audit against v1 mockup... | merged | #414 | 2026-05-20 | 69ce7df0 |
| 179 | `schema-removal-age` CI check (enforce 90-day deprecation win... | merged | #406 | 2026-05-20 | f06c323f |
| 180 | Privacy-module foundation (audit-log `subject_module` + sibli... | merged | #413 | 2026-05-20 | 125205b7 |
| 181 | Open-governance pre-commitments (GOVERNANCE.md + funding sign... | merged | #409 | 2026-05-20 | 67092db6 |
| 182 | Board-narrative AI-assist foundation (CLAUDE.md expansion + t... | merged | #405 | 2026-05-20 | 6622e8ee |
| 183 | UI honesty: calendar dead-link family + dashboard mockup refresh | merged | #422 | 2026-05-20 | f40fa6f9 |
| 184 | UI honesty: audits row-click 404 (per-period detail placeholder) | merged | #417 | 2026-05-20 | e058f251 |
| 185 | UI honesty: risks row-click routes to hierarchy, not detail | merged | #418 | 2026-05-20 | c509ca8c |
| 186 | UI honesty: sidebar "Admin" entry shown to non-admin users | merged | #421 | 2026-05-20 | 471f7796 |
| 187 | OAuth Authorization Server scaffolding (JWT signing + JWKS + ... | merged | #432 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 188 | OAuth `/oauth/token` endpoint + RFC 8693 token exchange | merged | #442 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 189 | OAuth `/oauth/authorize` + PKCE + frontend OAuth client integ... | merged | #447 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 190 | JWT validation middleware on `/v1/*` + R2 eviction + `/oauth/... | merged | #190 | 2026-05-21 | 3df5df16 |
| 191 | SDK migration to `client_credentials` × 4 languages + CLI de... | merged | #454 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 192 | Multi-tenant switch via OAuth token-exchange + frontend heade... | merged | #192 | 2026-05-21 | b0b5280a |
| 193 | Diagnose + fix dashboard.spec.ts AC-5 upcoming-row Playwright... | merged | #193 | 2026-05-21 | de1b20ab |
| 194 | CI path filter should include `fixtures/**` so fixture-only P... | merged | #194 | 2026-05-21 | 4159fb8a |
| 195 | Java SDK OAuth client_credentials helper | merged | #457 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 196 | Bootstrap container OAuth migration (atlas-bootstrap → clie... | merged | #465 | 2026-05-21 | 3b8f0f18 |
| 197 | Complete slice 034 bearer-middleware retirement (test-fixture... | merged | #471 | 2026-05-21 | 00a682c8 |
| 198 | OIDC first-install bootstrap (closes slice 192 AC-11/AC-12) | merged | #477 | 2026-05-21 | 12a6219f |
| 199 | Cross-tab BroadcastChannel sync for tenant-switcher | merged | #461 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 200 | Fix self-host bundle external-mode postgres init regression | merged | #468 | 2026-05-21 | 293a661d |
| 201 | Migrate Playwright e2e fixtures from slice 034 bearer to JWT-... | merged | #474 | 2026-05-21 | d4bb38fc |
| 202 | Self-host bundled mode: atlas service starts before migration... | merged | #490 | 2026-05-22 | bd8c7974 |
| 203 | Dark-mode stylesheet wiring (theme selection actually themes ... | merged | #499 | 2026-05-22 | d7a0b10d |
| 204 | Comprehensive page-by-page UI parity audit (per-page agent fl... | merged | #528 | 2026-05-23 | ced0a8df |
| 205 | Comprehensive demo seed dataset (showcase tenant + evidence a... | merged | #500 | 2026-05-22 | 072f9a7e |
| 206 | BFF auth cookie migration from `sa_session_token` to `atlas_jwt` | merged | #503 | 2026-05-22 | be6a5cc3 |
| 207 | Edge deploy channel: per-commit images + Watchtower-driven `a... | merged | #506 | 2026-05-22 | bf0aa197 |
| 208 | Next.js rewrites for `/v1/*`, `/health`, `/metrics` | merged | #510 | 2026-05-22 | 3967ff95 |
| 209 | Local-credential AS: email/password → atlas_jwt (no externa... | merged | #512 | 2026-05-22 | d35dae97 |
| 210 | `/v1/install-state` returns bootstrap tenant_id (close slice ... | merged | #514 | 2026-05-22 | eac25533 |
| 211 | Bootstrap seed grants user_roles + super_admins (close slice ... | merged | #515 | 2026-05-23 | a1d689b0 |
| 212 | Self-host bundled e2e asserts bootstrap user can sign in + re... | merged | #516 | 2026-05-23 | 4974e7ac |
| 213 | Audits page header chrome parity gap (breadcrumb + in-progres... | merged | #583 | 2026-05-23 | 1b7074b1 |
| 214 | Sidebar item counts parity gap (Controls "82", Risks "3" badges) | merged | #587 | 2026-05-23 | 1001f4de |
| 215 | Audits page title status tally missing ("1 in progress · 4 f... | merged | #542 | 2026-05-23 | e61d3154 |
| 216 | Audits mockup "Sample size" column stale (no backing data; de... | merged | #533 | 2026-05-23 | 7770adf3 |
| 217 | "Export OSCAL bundle" button permanently disabled on /audits ... | merged | #217 | 2026-05-23 | 381784bf |
| 218 | UI honesty: board-pack detail breadcrumb chain missing | merged | #582 | 2026-05-23 | ceef32f2 |
| 219 | UI honesty: board-pack header "Author" cell hardcoded to em-dash | merged | #552 | 2026-05-23 | f57c8c1b |
| 220 | Mockup update: board-pack coverage trend is scalar-only in v1 | merged | #532 | 2026-05-23 | ffee1c9f |
| 221 | Board-pack section divergence: vendor-burndown (mockup) vs op... | merged | #586 | 2026-05-23 | 4634060c |
| 222 | Board-pack posture coverage-definition caption missing | merged | #547 | 2026-05-23 | 88f37669 |
| 223 | UI honesty: controls top bar omits breadcrumb, search, audit ... | merged | #603 | 2026-05-24 | cf7c0be0 |
| 224 | Add Scope filter pill to /controls list | merged | #224 | 2026-05-23 | e2df15d0 |
| 225 | UI honesty: "New control" button on /controls is silently dis... | merged | #561 | 2026-05-23 | ef3c4a28 |
| 226 | Add Frameworks-per-row column to /controls list | merged | #553 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 227 | Add pagination to /controls list | merged | #576 | 2026-05-23 | 58422721 |
| 228 | UI honesty: dashboard global command-K search bar missing fro... | ready |  |  |  |
| 229 | UI honesty: dashboard header lacks tenant + snapshot-freshnes... | merged | #592 | 2026-05-23 | 6c8fa4eb |
| 230 | UI honesty: dashboard "Export" and "New board report" header ... | ready |  |  |  |
| 231 | UI parity: dashboard mockup-stale "SOC 2 Type II · Q2 2026 i... | merged | #537 | 2026-05-23 | a5bbf03a |
| 232 | UI honesty: dashboard activity-feed "View full activity ledge... | ready |  |  |  |
| 233 | UI honesty: /evidence "Push evidence" CTA is disabled with no... | merged | #558 | 2026-05-23 | ebf7502a |
| 234 | UI honesty: /evidence filter row missing three pills (Source,... | merged | #568 | 2026-05-23 | 1bea0cd4 |
| 235 | UI honesty: /evidence header missing audit-period banner + gl... | merged | #606 | 2026-05-24 | 0351f05c |
| 236 | UI honesty: /evidence record-count meta lacks ledger-total co... | merged | #562 | 2026-05-23 | ef1291a4 |
| 237 | UI honesty: /evidence table missing pagination footer (cursor... | merged | #578 | 2026-05-23 | 7576852d |
| 238 | Policies list: missing "Linked control" and "Ack status" filt... | merged | #566 | 2026-05-23 | d4d90810 |
| 239 | Policies list: header missing inline "N published · M draft ... | merged | #548 | 2026-05-23 | 628a5ffe |
| 240 | Policies list: missing pagination footer + "365-day acknowled... | merged | #577 | 2026-05-23 | cdc8dfaf |
| 241 | Policies list: "Acknowledgment report" + "New policy" buttons... | merged | #572 | 2026-05-23 | 0dd23ed0 |
| 242 | Policies empty-state: "Scaffold five foundational policies" C... | merged | #242 | 2026-05-23 | f676ee36 |
| 243 | UI honesty: risks top bar omits breadcrumb, search, audit ban... | merged | #610 | 2026-05-24 | a4950b76 |
| 244 | Risks list: extend filter pills to Category, Methodology, Org... | merged | #567 | 2026-05-23 | 4c8435ce |
| 245 | Risks mockup-stale: "N above appetite" subtitle has no v1 bac... | merged | #534 | 2026-05-23 | 472a17f1 |
| 246 | Risks list: pagination control absent from footer | merged | #571 | 2026-05-23 | 399e0e12 |
| 247 | Risks list: header "New risk" button is silently disabled, /r... | merged | #556 | 2026-05-23 | bf665f02 |
| 248 | Settings page lacks page-specific `<title>` metadata | merged | #551 | 2026-05-23 | 79eef15c |
| 249 | Settings admin variants flicker between non-admin → admin o... | merged | #581 | 2026-05-23 | 98872a4d |
| 250 | Settings Profile section surfaces credential-bearer artifacts... | merged | #588 | 2026-05-23 | 0a4762d9 |
| 251 | Settings Notifications section returns error for credential-b... | merged | #251 | 2026-05-23 | c5dfc87a |
| 252 | Settings admin cross-link renders ASCII "->" instead of Unico... | merged | #546 | 2026-05-23 | 0800df71 |
| 253 | UI honesty: control-detail "endpoint not on main yet" empty-s... | merged | #602 | 2026-05-24 | 2591cbbb |
| 254 | Control-detail tab strip (Overview / Evidence / Mappings / Ef... | merged | #615 | 2026-05-24 | 3b273d0b |
| 255 | Control-detail header action buttons + "last evaluated" times... | merged | #612 | 2026-05-24 | bb34d8a6 |
| 256 | Coverage column in /controls/{id} coverage table (strength ×... | merged | #607 | 2026-05-24 | da03fd24 |
| 257 | UI honesty: control-detail top bar chrome parity (tenant brea... | merged | #611 | 2026-05-24 | 66a426a6 |
| 258 | Mockup index: 6 "design only — implementation pending" badg... | merged | #539 | 2026-05-23 | 03b9e34e |
| 259 | Mockup index: missing tiles for Calendar / Metrics / Vendors ... | merged | #591 | 2026-05-23 | 575d1bf6 |
| 263 | UI honesty: questionnaire frontend page (Stages A + C; Stage ... | merged | #625 | 2026-05-24 | 2f850df7 |
| 264 | MOCKUP-STALE: questionnaire Excel column-mapping review UI | merged | #538 | 2026-05-23 | 9a51b405 |
| 268 | Unified `/v1/search` endpoint (aggregates controls + risks + ... | merged | #593 | 2026-05-23 | d9d8e69b |
| 269 | Dashboard snapshot export endpoint (JSON / CSV / XLSX) | merged | #599 | 2026-05-23 | 418caabf |
| 270 | Non-admin activity-ledger surface (`/activity`) | merged | #598 | 2026-05-23 | 1ee7242b |
| 271 | Shared-shell breadcrumb (`<tenant> › <page>`) | merged |  |  | pre-convention merge; reconciled 2026-06-03 (batch 184); #1266 backfill miss (not in history Status table) |
| 272 | Global search box (`⌘K` modal) in shared shell | ready |  |  |  |
| 273 | Board-pack: vendor-burndown section | merged | #616 | 2026-05-24 | 418763b8 |
| 274 | Settings spec AC-9 token-row flake: deterministic-fail invest... | merged | #597 | 2026-05-23 | 235c41d6 |
| 275 | Slice 254 control-detail-tabs.spec.ts e2e assertions racy | merged | #620 | 2026-05-24 | dcfe0523 |
| 276 | Slice 254 control-detail-tabs.spec.ts e2e deep-investigation | merged | #647 | 2026-05-25 | b1abf63b |
| 277 | Mobile-responsive baseline (viewport meta + per-page audit + ... | merged | #646 | 2026-05-25 | 2e885889 |
| 278 | Demo-seed UI button (edge-only via `ATLAS_ENABLE_DEMO_SEED`) | merged | #652 | 2026-05-25 | ed08f0dd |
| 279 | Coverage audit + targeted lift of 5 highest-leverage packages | merged | #279 | 2026-05-25 | 3bad23a1 |
| 280 | Remove bearer-paste card from /login (no users yet; no backwa... | merged | #642 | 2026-05-25 | 8bb1f202 |
| 281 | Mobile-aware list-table collapse (`<ListTable>` → card-stac... | merged | #661 | 2026-05-25 | 16222d6c |
| 282 | Coverage lift — `internal/eval` to 70%+ | merged | #674 | 2026-05-25 | d002c3dd |
| 283 | Coverage lift — `internal/board` to 70%+ | merged | #678 | 2026-05-25 | a701cd89 |
| 284 | Coverage lift — `internal/scope` to 70%+ | merged | #668 | 2026-05-25 | d7a5c1a1 |
| 285 | Coverage lift — `internal/oscal` to 70%+ | merged | #667 | 2026-05-25 | 6ab5c2f5 |
| 286 | Coverage lift — `internal/observability/otel` to 70%+ | merged | #662 | 2026-05-25 | 25eba879 |
| 287 | Coverage lift — `internal/vendor` to 70%+ | merged | #666 | 2026-05-25 | c179d36e |
| 288 | Coverage lift — `internal/audit/walkthrough` to 70%+ | merged | #682 | 2026-05-25 | abf81c8b |
| 289 | Coverage lift — `internal/artifact` to 70%+ | merged | #663 | 2026-05-25 | 3681c13d |
| 290 | Coverage lift — `internal/api/controldetail` to 70%+ | merged | #679 | 2026-05-25 | eea0bf2e |
| 291 | Coverage lift — `internal/api/controls` to 70%+ | merged | #687 | 2026-05-26 | 78ce3e4b |
| 292 | Coverage lift — `internal/api/oscalexport` to 70%+ | merged | #693 | 2026-05-26 | ed1d57a2 |
| 293 | Coverage lift — `internal/api/metrics` to 70%+ | merged | #689 | 2026-05-26 | 640f1c3c |
| 294 | Coverage lift — `internal/metrics/eval` to 70%+ | merged | #688 | 2026-05-26 | 569d24b2 |
| 295 | Coverage lift — `internal/metrics/scheduler` to 70%+ | merged | #684 | 2026-05-26 | 6e1f4da0 |
| 296 | Coverage lift — `internal/catalog/metrics` to 70%+ | merged | #672 | 2026-05-25 | 98bb47fa |
| 297 | Coverage lift — `internal/policy/seed` to 70%+ | merged | #673 | 2026-05-25 | de53fa63 |
| 298 | Coverage lift — `connectors/aws/internal/awsauth` to 70%+ | merged | #677 | 2026-05-25 | 793d9b68 |
| 299 | Coverage lift — `connectors/aws/cmd/aws-connector` to 70%+ | merged | #683 | 2026-05-25 | a5f6b17c |
| 300 | Coverage lift — `connectors/jira/cmd/atlas-jira` to 70%+ | merged | #697 | 2026-05-26 | e5697ba9 |
| 301 | Coverage lift — `connectors/github/cmd/atlas-github` to 70%+ | merged | #699 | 2026-05-26 | 5131f95f |
| 302 | Coverage lift — `connectors/okta/cmd/atlas-okta` to 70%+ | merged | #694 | 2026-05-26 | f1918618 |
| 303 | Coverage lift — `connectors/osquery/cmd/atlas-osquery` to 70%+ | merged | #692 | 2026-05-26 | 8d3dc905 |
| 304 | Mobile-baseline e2e: page.route mock for /controls /risks /ev... | merged |  |  | pre-convention merge; reconciled 2026-06-03 (batch 184); #1266 backfill miss (not in history Status table) |
| 305 | Coverage lift (round 2) — `connectors/aws/cmd/aws-connector... | merged | #698 | 2026-05-26 | b9868ede |
| 306 | Coverage lift — `connectors/1password/cmd/atlas-1password` ... | merged | #704 | 2026-05-26 | 380ea2c1 |
| 307 | Coverage lift — `connectors/manual/cmd/atlas-manual` to 70%+ | merged | #705 | 2026-05-26 | d84c7c02 |
| 308 | Coverage lift (round 2) — `connectors/github/cmd/atlas-gith... | merged | #710 | 2026-05-26 | 824a3af2 |
| 309 | Coverage lift (round 2) — `connectors/okta/cmd/atlas-okta` ... | merged | #709 | 2026-05-26 | 9a5b867b |
| 310 | Coverage lift — `internal/api/soc2import` to 70%+ | merged | #708 | 2026-05-26 | cb19570d |
| 311 | Coverage lift — `internal/auth/bearer` to 70%+ | merged | #703 | 2026-05-26 | 75d18a73 |
| 312 | Coverage audit (round 3) + targeted lift of new gaps | merged | #714 | 2026-05-27 | d742ae25 |
| 313 | Coverage lift — admin HTTP handlers (5 packages, integratio... | merged | #313 | 2026-05-27 | 2ed6babe |
| 314 | Coverage lift — `internal/api/oauth` to 70%+ | merged | #909 | 2026-05-30 | 63eb82f6 |
| 315 | Coverage lift — auth-substrate-v2 small packages (4 packages) | merged | #721 | 2026-05-27 | d2e74d76 |
| 316 | Coverage lift — HTTP handler integration-enrollment (calend... | merged | #316 | 2026-05-27 | e4d0608c |
| 317 | Coverage lift — MCP write-proposals stack (2 packages) | merged | #725 | 2026-05-27 | e5c21ec2 |
| 318 | Coverage lift — audit ledger plumbing (3 packages) | merged | #318 | 2026-05-27 | 1874ee78 |
| 319 | Coverage lift — `internal/questionnaire` engine to 70%+ | merged | #319 | 2026-05-27 | c5e2cf0c |
| 320 | Coverage lift — `internal/demoseed` to 70%+ | merged | #753 | 2026-05-27 | 3e0c0ea3 |
| 321 | Coverage lift — `pkg/sdk-go` to 70%+ | merged | #321 | 2026-05-27 | 291e49b1 |
| 322 | /admin/demo "Reseed demo dataset" button click produces no vi... | merged | #719 | 2026-05-27 | 7e4732a3 |
| 323 | README refresh: current release + accurate slice count + fres... | ready |  |  |  |
| 324 | Evidence SDK docs alignment: push-only wire reality + connect... | merged | #752 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 325 | OAuth grants landing map: per-grant reference docs for `inter... | merged | #746 | 2026-05-27 | a9f705bf |
| 326 | Legacy bearer 410-Gone deprecation responder retirement | merged | #754 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 327 | Security audit via voltagent-qa-sec:security-auditor | merged | #811 | 2026-05-28 | 17b73591 |
| 328 | Comprehensive code review via voltagent-qa-sec:code-reviewer | merged | #821 | 2026-05-28 | 3b81923c |
| 329 | Compliance meta-audit via voltagent-qa-sec:compliance-auditor | merged | #827 | 2026-05-28 | bc3f3f27 |
| 330 | Privacy audit (GDPR + CCPA) via voltagent-qa-sec:gdpr-ccpa-co... | ready |  |  |  |
| 331 | Accessibility audit (WCAG 2.1 AA) via voltagent-qa-sec:access... | merged | #785 | 2026-05-28 | 6bb54ea6 |
| 332 | Performance audit (evidence pipeline + UCF + frontend) via vo... | merged | #845 | 2026-05-28 | e13b58e8 |
| 333 | QA strategy gap analysis via voltagent-qa-sec:qa-expert | merged | #776 | 2026-05-28 | b68f589c |
| 334 | Test framework review via voltagent-qa-sec:test-automator | merged | #770 | 2026-05-27 | 37052f08 |
| 335 | Chaos experiment design via voltagent-qa-sec:chaos-engineer | merged | #782 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 336 | UX flow validation via voltagent-qa-sec:ui-ux-tester | ready |  |  |  |
| 337 | AI-writing tone audit via voltagent-qa-sec:ai-writing-auditor | merged | #764 | 2026-05-27 | c27db8a4 |
| 338 | Penetration test against atlas-edge.home.gmoney.sh via voltag... | ready |  |  |  |
| 339 | OpenAPI spec drift: 12 OAuth endpoints not enumerated in `doc... | ready |  |  |  |
| 340 | Investigate + re-enable chromedp `TestRender_ProducesRealPDF`... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 341 | Apply chromedp WSURLReadTimeout fix to remaining four PDF ren... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 342 | Vision canvas tone rewrite (banned phrase + em-dash saturation) | merged | #861 | 2026-05-29 | a85499bd |
| 343 | Tone polish round 1 (low-density bundle from slice 337 audit) | merged | #767 | 2026-05-27 | 87d72889 |
| 344 | CLAUDE.md tone discipline expansion (additions from slice 337... | merged | #868 | 2026-05-29 | 7e014299 |
| 345 | CI integration-job enrolment-discovery primitive | merged | #873 | 2026-05-29 | 10230d18 |
| 346 | CI yaml: extract inline slice-history commentary | merged | #788 | 2026-05-28 | a4e0d923 |
| 347 | vitest coverage ratchet | merged | #773 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 348 | Test framework polish round 1 | merged | #794 | 2026-05-28 | 58fa94eb |
| 349 | Evaluate adding a contract-test tier for BFF↔atlas wire shape | merged | #879 | 2026-05-29 | 30c501ad |
| 350 | Branch-coverage floor for security-critical packages | merged | #779 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 351 | e2e critical multi-tenant flow gap audit + spec fill | merged | #875 | 2026-05-29 | b95383ed |
| 352 | Flake budget formalization + dashboard | merged | #791 | 2026-05-28 | 621ddef3 |
| 353 | QA strategy tactical round 1 | merged | #885 | 2026-05-29 | 31a0ed58 |
| 354 | Chaos experiment execution: DB connection-pool exhaustion | ready |  |  |  |
| 355 | Chaos experiment execution: NATS JetStream consumer lag spike | ready |  |  |  |
| 356 | Chaos experiment execution: data-tier outage chaos round 1 | ready |  |  |  |
| 357 | Chaos experiment execution: auth-substrate chaos round 1 | ready |  |  |  |
| 358 | Chaos experiment execution: schema-registry unavailable | ready |  |  |  |
| 359 | A11y skip-link to `<main>` in authed layout | merged | #798 | 2026-05-28 | 28975bd5 |
| 360 | A11y light-mode `--muted-foreground` contrast lift | merged | #869 | 2026-05-29 | 7671991b |
| 361 | A11y global-search combobox ARIA wiring | merged | #804 | 2026-05-28 | be9ef102 |
| 362 | A11y in-progress audit pill dark-mode contrast | merged | #801 | 2026-05-28 | 44e9a014 |
| 363 | A11y admin forms: raw input + error association | merged | #807 | 2026-05-28 | 4da5ed9e |
| 364 | Strip `atlas` Prometheus namespace from OTel Collector; enabl... | merged | #795 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 365 | OIDC ID-token nonce generation + validation | merged | #814 | 2026-05-28 | ed24c6ec |
| 366 | JWT signing key rotation end-to-end | merged | #863 | 2026-05-29 | 405a375f |
| 367 | Error detail leakage audit + cleanup across `internal/api/` | merged | #817 | 2026-05-28 | 87777f19 |
| 368 | OSCAL export bundle signing: ed25519 → cosign | ready |  |  |  |
| 369 | Consolidate `writeJSON` / `writeError` into shared `internal/... | merged | #874 | 2026-05-29 | 99ad0a9b |
| 370 | Split `web/lib/api.ts` god-file into per-domain `web/lib/api/... | merged | #889 | 2026-05-29 | 8954d33c |
| 371 | Auth-substrate clock injection (sessions + apikeystore + jwtmw) | merged | #824 | 2026-05-28 | 151fb504 |
| 372 | Incident response plan (governance document) | merged | #830 | 2026-05-28 | 38de6363 |
| 373 | Business continuity / disaster recovery plan (governance docu... | merged | #833 | 2026-05-28 | a84da085 |
| 374 | GitHub org access review cadence (governance document) | merged | #836 | 2026-05-28 | 257853ea |
| 375 | Data retention and disposal policy (governance document) | merged | #839 | 2026-05-28 | 20e1b2ab |
| 376 | Project asset inventory (governance document) | merged | #842 | 2026-05-28 | c9258075 |
| 377 | Cache `rego.PreparedEvalQuery` in eval engine (close slice 33... | merged | #848 | 2026-05-29 | 3237e077 |
| 378 | Hot-reload authz bundle without server restart (close slice 3... | merged | #851 | 2026-05-29 | 32134ed7 |
| 379 | Eliminate double `protojson.Marshal` on ingest redaction path... | merged | #859 | 2026-05-29 | f2bfc5d0 |
| 380 | Dashboard Server Component fan-out + parallel data fetch (clo... | merged | #862 | 2026-05-29 | 8e353f14 |
| 381 | Perf cleanup round 1 (bundle of slice 332 Low findings) | merged | #870 | 2026-05-29 | 9145cd5a |
| 382 | Enforce STATUS row convention: orchestrator-only edits + CI l... | merged | #853 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 383 | Pre-push `go mod tidy` drift check (catch direct-import-not-p... | merged | #854 |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 384 | ActionPlan primitive: schema + CRUD + risk/control linkage | ready |  |  |  |
| 385 | metrics seeder: empty `source_slices` becomes SQL NULL, viola... | merged | #864 | 2026-05-29 | 549f0a80 |
| 386 | metrics_catalog seed aborts on empty source_slices (NULL viol... | merged | #865 | 2026-05-29 | cac076a3 |
| 387 | CI harness for the production-build standalone Playwright specs | merged | #896 | 2026-05-29 | a497e20e |
| 388 | Board-pack export end-to-end Playwright spec | merged | #883 | 2026-05-29 | 259454e5 |
| 389 | Multi-tenant JWT harness + real-RLS cross-tenant-leak e2e spec | merged | #880 | 2026-05-29 | a5d274a9 |
| 390 | Drain the integration-job enrolment backlog (38 packages) | merged |  |  | merged via #933 (closes slice 390, integration-enrolment drain batch 8); #1266 backfill miss |
| 391 | Wire `duphelper-lint` into CI as a hard-failure step | merged | #878 | 2026-05-29 | b324adcd |
| 392 | Roll out the golden-file contract-test tier to high-traffic B... | merged | #884 | 2026-05-29 | 7d817d5c |
| 393 | Wire slice-353 QA-tactical scripts into CI | merged | #888 | 2026-05-29 | 9aa2452f |
| 394 | Teach the `/e2e/` `route.fulfill` mocks to load from the reco... | merged | #939 | 2026-05-30 | 8fd89bb4 |
| 395 | Migrate `@/lib/api` import sites to per-domain paths (slice 3... | merged | #892 | 2026-05-29 | 5f1e2169 |
| 396 | Retire the `web/lib/api.ts` barrel shim (slice 370 Phase 3) | merged | #905 | 2026-05-30 | 1e4585c9 |
| 397 | Rename `SESSION_COOKIE` → `ATLAS_JWT_COOKIE` symbol (slice ... | merged | #899 | 2026-05-30 | de51303c |
| 398 | Fix pre-existing `tsc --noEmit` errors in three web test files | merged | #895 | 2026-05-29 | 5c36c52c |
| 399 | Re-shape bff-cookie-production-build.spec.ts for the prod-bui... | merged | #902 | 2026-05-30 | 560e1738 |
| 400 | OSCAL signing: cosign/Sigstore decision spike + ADR (no code) | merged | #956 | 2026-06-03 | ae14ea4d |
| 401 | Integration-enrolment drain · batch 1 (security-critical aut... | merged | #912 | 2026-05-30 | 112801ce |
| 402 | Integration-enrolment drain · batch 2 (admin creds surface) | merged | #915 | 2026-05-30 | cf2fb8a1 |
| 403 | Integration-enrolment drain · batch 3 (admin users aggregati... | merged | #918 | 2026-05-30 | afa262ea |
| 404 | Integration-enrolment drain · batch 4 (api domain handlers A) | merged | #921 | 2026-05-30 | cbdbf50a |
| 405 | Integration-enrolment drain · batch 5 (api domain handlers B) | merged | #924 | 2026-05-30 | 600d39ff |
| 406 | Integration-enrolment drain · batch 6 (auth substrate + keys... | merged | #927 | 2026-05-30 | 6f7f7163 |
| 407 | Integration-enrolment drain · batch 7 (freshness drift family) | merged | #930 | 2026-05-30 | bb71ab51 |
| 408 | Integration-enrolment drain · batch 8 (catalog oscal policy ... | merged | #933 | 2026-05-30 | bb2ce517 |
| 409 | Contract-tier rollout: dashboard + high-traffic e2e routes (u... | merged | #936 | 2026-05-30 | 85be428c |
| 410 | Contract-tier rollout: dashboard top-risks panel (GET /v1/risks) | merged | #942 | 2026-05-30 | ec3718fd |
| 411 | Contract-tier rollout: controls-detail + audit-workspace routes | merged | #958 | 2026-06-03 | cfaa30a9 |
| 412 | Contract-tier rollout: controls-detail + audit-workspace LONG... | merged |  | 2026-06-11 | 74013361 |
| 413 | OSCAL bundle signing Phase 1: cosign-kms + retained embedded-... | merged | #961 | 2026-06-03 | f777ff07 |
| 414 | OSCAL bundle signing Phase 2: cosign-keyless + Fulcio + Rekor... | merged |  | 2026-06-12 | 89cca3e1 |
| 415 | Adopt GitHub merge queue to kill the update-branch re-CI cascade | ready |  |  |  |
| 416 | Pin golangci-lint version (drop `version: latest`) | merged | #973 | 2026-06-03 | baf06f64 |
| 417 | Shard the `-p 1` integration job (Phase A serial / Phase B ma... | merged | #417 | 2026-06-04 | e662bed4 |
| 418 | Extract the 4× duplicated Playwright/Postgres stack bring-up... | ready |  |  |  |
| 419 | Promote long-stable advisory CI checks to required (or formal... | ready |  |  |  |
| 420 | Fix the flake-budget "flake" definition to capture rerun-pass... | ready |  |  |  |
| 421 | Parser fuzz harnesses for untrusted-input surfaces | merged | #985 | 2026-06-04 | 9d8e4179 |
| 422 | Lift `internal/api/oauth` coverage toward the 90% security-cr... | merged | #975 | 2026-06-03 | 3e6eae8e |
| 423 | End-to-end test for the OSCAL signed-export download chain | merged | #981 | 2026-06-04 | 7298f546 |
| 424 | End-to-end test for the vendor-review workflow | merged |  | 2026-06-11 | bfbd482d |
| 425 | Real cloud-KMS round-trip integration test for `internal/osca... | merged |  | 2026-06-11 | 2ada94e9 |
| 426 | Targeted coverage-lift round: decisions / policies / me / fre... | merged | #1006 | 2026-06-04 | 9dfb08b8 |
| 427 | Publish the auditor-facing "verify a signed OSCAL export" pag... | merged | #974 | 2026-06-03 | 2dcb70be |
| 428 | ADRs for the four load-bearing canvas invariants without a de... | merged |  |  | ADRs 0011-0014 merged via #989 (2026-06-04); #1266 backfill miss (file says merged) |
| 429 | README parity for the AWS connector and the Go/TypeScript SDKs | merged | #979 | 2026-06-04 | 74ca43be |
| 430 | Consolidated environment-variable / configuration reference page | merged | #984 | 2026-06-04 | 07c2c25c |
| 431 | External-IdP / OIDC operator setup guide | merged | #994 | 2026-06-04 | bdca8df7 |
| 432 | Operator backup/restore + upgrade runbooks, surfaced in the d... | merged | #1001 | 2026-06-04 | 9537f74c |
| 433 | gitignore the `.understand-anything/` local-analysis cache | merged | #978 | 2026-06-03 | 1508501c |
| 434 | One-time audit + reconcile of the stale `not-ready` `_STATUS.... | ready |  |  |  |
| 435 | Shared integration-test DB/tenant harness package (`internal/... | ready |  |  |  |
| 436 | Split the three oversized hand-written god-files | ready |  |  |  |
| 437 | Archive `Plans/mockups/` iteration-1 HTML out of the active tree | merged | #980 | 2026-06-04 | 7ce4cc6f |
| 438 | ISO 27001:2022 crosswalk loader (2nd framework — proves the... | merged | #1011 | 2026-06-04 | af5aec7f |
| 439 | Evidence-staleness digest + alerting (honest, named-interval) | merged |  | 2026-06-11 | a9ec52b7 |
| 440 | Board-narrative AI v0 — one numbered section, end-to-end | ready |  |  |  |
| 441 | Questionnaire AI-answer suggestion v0 (cited drafts, one-clic... | merged |  | 2026-06-12 | bf191829 |
| 442 | GCP connector (highest-demand missing cloud) | ready |  |  |  |
| 443 | Slack connector | merged |  | 2026-06-11 | 8e866181 |
| 444 | AI gap-explanation v0 (plain-language, cited, non-binding) | merged |  | 2026-06-11 | 6bcc6942 |
| 445 | Email/SMTP notification channel (delivery substrate) | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 446 | Disclosure / breach-notification workflow decision spike + AD... | ready |  |  |  |
| 447 | PCI DSS v4.0 crosswalk loader (3rd framework; scope-reduction... | merged | #1017 | 2026-06-05 | 69078c86 |
| 448 | Bulk operations + saved filter-views (operator ergonomics, co... | in-progress |  |  | feat/448-bulk-ops-saved-views |
| 449 | OPA 1.4 → 1.17 embedded-engine upgrade (13 minors on the au... | merged | #1319 | 2026-06-11 | 05bb92a3 |
| 450 | vitest 4 + @vitest/coverage-v8 4 paired migration | ready |  |  |  |
| 451 | SLSA provenance + SBOM for binary / CLI / SDK releases | merged | #990 | 2026-06-04 | 750d0eca |
| 452 | Node version alignment: @types/node + CI + runtime + engines ... | ready |  |  |  |
| 453 | TypeScript 6 migration | ready |  |  |  |
| 454 | go-otel observability group bump (13 modules) | merged | #986 | 2026-06-04 | b839f98a |
| 455 | OIDC-identity-strategy decision spike (unblocks cosign keyles... | merged |  | 2026-06-11 | a5bc0ea9 |
| 456 | `internal/api/oauth` residual coverage: audit-write + signer-... | merged | #1022 | 2026-06-05 | 02c926f9 |
| 457 | Browser download surface for the OSCAL signed-export bundle | merged |  | 2026-06-11 | a04fd240 |
| 458 | pre-commit guard against committing machine-local analysis ca... | merged | #1005 | 2026-06-04 | e3877b29 |
| 459 | Sweep code-comment `Plans/mockups/` provenance citations to t... | merged | #1010 | 2026-06-04 | 4833a7bd |
| 460 | `NATS_URL` is read by the server but absent from `.env.example` | merged | #460 | 2026-06-04 | a528e279 |
| 461 | integration suite SCF-seed state coupling when run outside CI... | merged | #991 | 2026-06-04 | 4d0c4a41 |
| 462 | admindemo integration suite leaks a `demo` tenant when an ear... | merged | #995 | 2026-06-04 | 56af1e61 |
| 463 | `demoseed.Seeder.Teardown` leaves the `tenants` row behind on... | merged | #1000 | 2026-06-04 | 03554f2d |
| 464 | `atlas evidence verify` CLI: ledger-wide integrity walk (+ SE... | merged | #1027 | 2026-06-06 | c3d08cbc |
| 465 | `TRUST_FORWARDED_HEADERS` is server-read but not plumbed thro... | merged | #1004 | 2026-06-04 | 7b54bec7 |
| 466 | `TRUSTED_PROXY_CIDRS` allowlist as the structural fix for X-F... | merged | #1016 | 2026-06-04 | 4d54e0e8 |
| 467 | ISO 27001:2022 full Annex A coverage completion | merged |  | 2026-06-11 | 594c4696 |
| 468 | Server-backed bulk-assign-owner + saved filter-views (controls) | ready |  |  |  |
| 469 | Sweep `web/app/**` stale `Plans/mockups/` citations to the ar... | merged | #1015 | 2026-06-04 | cbd26d51 |
| 470 | header-overwriting reverse-proxy container in the e2e seed ha... | merged | #1021 | 2026-06-06 | 4c5496df |
| 471 | Role-scoped control-implementation checklist generator v0 (ci... | merged |  | 2026-06-12 | 9c500311 |
| 472 | `internal/api/oauth` coverage: device-approval flow + DBUserR... | merged | #1026 | 2026-06-06 | f6cd0eec |
| 473 | Idempotent migrate-on-upgrade for the self-host deploy stack ... | merged | #1042 | 2026-06-06 | 8d5da2c0 |
| 474 | align the ingest evidence hash so the ledger-wide verify walk... | merged | #1160 | 2026-06-08 | 90b1416a |
| 475 | Board/questionnaire PDF render must degrade to 503 (not 500/h... | merged | #1030 | 2026-06-06 | 37155f96 |
| 476 | Make seeded demo data reachable by the operator who loads it | merged | #1098 | 2026-06-07 | 967d9afc |
| 477 | Slice 477 — Walkthrough PDF render must degrade to 503 (not... | merged | #1041 | 2026-06-06 | efcd4a74 |
| 478 | Super-admin user↔tenant↔role assignment API (incl. self-a... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 479 | Admin user-management UI: assign users to tenants + roles (in... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 480 | NIST CSF 2.0 crosswalk loader (4th framework via the generic ... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 481 | HIPAA Security Rule crosswalk loader (catalog-only; not the c... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 482 | Coverage-strength rollup + mapping-confidence visualization | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 483 | Crosswalk-mapping verified-tier governance (community_draft ... | ready |  |  |  |
| 484 | Framework-versioning capability (multiple live versions + mig... | ready |  |  |  |
| 486 | Azure connector (Entra ID + Storage) — cloud parity with AW... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 487 | Kubernetes connector (RBAC + workload security config) | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 488 | Monitoring connectors (Datadog + Grafana) — logging/alertin... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 489 | PagerDuty connector (incident-response evidence) | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 490 | MDM connectors (Jamf + Intune) — endpoint posture evidence | merged | #1095 | 2026-06-07 | 190b038e |
| 491 | HRIS connectors (Rippling + BambooHR) — joiner/mover/leaver... | merged | #1105 | 2026-06-07 | f3abca7e |
| 492 | OSCAL import: catalog / profile / component-definition ingestion | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 493 | SSP export: real control-implementation narratives (not the s... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 494 | Assessment-Results export: wire the drawn sample evidence + w... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 495 | Control-as-code: evaluate SQL + JSON-path evidence queries (n... | merged | #1043 | 2026-06-06 | f3f74bf4 |
| 496 | Control-bundle test runner: fixture evidence + expected pass/... | merged | #1108 | 2026-06-07 | 6da529b6 |
| 498 | Shared local-inference (`internal/llm`) client foundation + `... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 499 | Cloud-LLM opt-in per-tenant routing + visible "routes to {pro... | ready |  |  |  |
| 500 | pgvector semantic-retrieval grounding for AI-assist drafts (c... | ready |  |  |  |
| 501 | Board-narrative full multi-section + numeric-claim verificati... | ready |  |  |  |
| 502 | AI evidence-summarization v0 (cited, non-binding, control-det... | ready |  |  |  |
| 504 | Privacy v0: right-to-erasure (tombstone) implementation again... | ready |  |  |  |
| 505 | Privacy v0: data-subject-access-request (DSAR) export workflow | ready |  |  |  |
| 506 | Privacy v0: Records of Processing Activities (RoPA, GDPR Art.... | ready |  |  |  |
| 507 | Breach-notification workflow implementation (security → pri... | ready |  |  |  |
| 508 | SCIM 2.0 user-lifecycle provisioning (deprovisioning on IdP o... | merged |  | 2026-06-12 | f9969f4e |
| 509 | IdP group-to-role mapping (claims/SCIM-group -> atlas role as... | merged |  | 2026-06-12 | 2a6297a7 |
| 510 | Automated backup + scheduled restore-verification (operationa... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 511 | OSCAL profile import (resolve import / merge / modify directi... | merged | #1109 | 2026-06-07 | 57f085cf |
| 512 | OSCAL component-definition import (vendor control-implementat... | merged | #1115 | 2026-06-07 | 3c3c62e3 |
| 513 | Correct the AI-assist-boundary canonical-adopter reference (Q... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 514 | NIST CSF 2.0 full Subcategory coverage | merged | #1100 | 2026-06-07 | e19faf7a |
| 515 | NIST CSF 2.0 Profile / Tier assessment workflow | merged | #1168 | 2026-06-08 | 25f7a8b9 |
| 516 | HIPAA Security Rule full coverage (incl. §164.314 + §164.316) | merged | #1104 | 2026-06-07 | 06000d08 |
| 517 | HIPAA covered-entity workflow (phase-3; deferred §10.3) | ready |  |  |  |
| 518 | HIPAA FrameworkScope ePHI-environment example | ready |  |  |  |
| 519 | Azure connector: AKS workload configuration evidence | merged | #1133 | 2026-06-08 | 64bbcb79 |
| 520 | Azure connector: NSG / firewall rule evidence | merged | #1143 | 2026-06-08 | e91f9ed6 |
| 521 | Azure connector: Key-Vault access-policy / RBAC evidence | merged | #1146 | 2026-06-08 | a8838f2e |
| 522 | Azure connector: event-driven (subscribe) profile via Event G... | merged | #1237 | 2026-06-10 | afa1726e |
| 523 | Kubernetes connector: NetworkPolicy coverage evidence | merged | #1151 | 2026-06-08 | 62c2c11b |
| 524 | Kubernetes connector: Pod-Security-Standards admission-config... | merged | #1186 | 2026-06-09 | 03c6ccb8 |
| 525 | Kubernetes connector: Secret-inventory (metadata-only) evidence | merged | #1210 | 2026-06-09 | 1006afa9 |
| 526 | Kubernetes connector: watch-based event-driven profile (audit... | merged | #1240 | 2026-06-10 | 9bfbcf66 |
| 527 | Admin user-assign dialog: user + tenant dropdowns (replace ra... | merged |  |  | gen-status backfill: pre-convention merge; authority _STATUS_HISTORY.md (git slice-NNN detection gap) |
| 528 | Admin user-assign dialog: searchable combobox for large user/... | ready |  |  |  |
| 533 | Datadog connector: Cloud-SIEM / Security-Monitoring rule evid... | merged | #1171 | 2026-06-08 | d50594a3 |
| 534 | Grafana connector: SAML / RBAC config evidence | merged | #1183 | 2026-06-08 | 8cb2f9fa |
| 535 | Monitoring connectors: alert-firing-history (event-driven) pr... | merged | #1216 | 2026-06-09 | 4bb176d8 |
| 536 | Crosswalk-review / conflict editing UI (STRM mapping review) | ready |  |  |  |
| 537 | Cross-framework coverage-strength comparison matrix | ready |  |  |  |
| 538 | PagerDuty connector: postmortem / retrospective evidence | merged | #1179 | 2026-06-08 | d853e0d5 |
| 539 | PagerDuty connector: responder-performance metrics | merged | #1201 | 2026-06-09 | e5210384 |
| 540 | PagerDuty connector: event-driven (webhook) profile | merged | #1228 | 2026-06-10 | 1185791c |
| 541 | Wire the staleness digest (slice 439) to email delivery | ready |  |  |  |
| 542 | Per-notification-kind email preferences | merged | #1099 | 2026-06-07 | 0f3dd94c |
| 543 | Additional notification channels (Slack / webhook) | merged | #1110 | 2026-06-07 | f7d1b2c8 |
| 544 | Point-in-time recovery (WAL archiving / PITR) for self-host | ready |  |  |  |
| 545 | Helm/K8s-native backup integration (Velero / CloudNativePG) | ready |  |  |  |
| 546 | Cross-region backup replication (off-region durability) | ready |  |  |  |
| 549 | Edge/self-host upgrade runbook: "pull the compose, not just t... | merged | #1094 | 2026-06-07 | 1fed76e8 |
| 555 | MDM connectors: software-inventory evidence (Jamf + Intune) | merged | #1120 | 2026-06-07 | 4ebc39df |
| 556 | MDM connectors: configuration-profile detail evidence (Jamf +... | merged | #1125 | 2026-06-07 | 0059979c |
| 557 | MDM connectors: event-driven (webhook) profile (Jamf + Intune) | merged | #1234 | 2026-06-10 | 708712e2 |
| 566 | Per-kind email opt-out for the unmapped notification kinds | merged | #1103 | 2026-06-07 | 1ae25343 |
| 567 | Re-point palette-bound HIPAA low-confidence rows at finer ful... | ready |  |  |  |
| 571 | HRIS connectors: manager-hierarchy evidence (Rippling + Bambo... | merged | #1175 | 2026-06-08 | 241aa821 |
| 573 | HRIS connectors: event-driven termination-webhook profile (Ri... | merged | #1222 | 2026-06-09 | 0ff20ff8 |
| 574 | Control-bundle upload test-gate: "tests must pass to upload" | merged | #1130 | 2026-06-07 | 39ed4ef4 |
| 578 | OSCAL chained profile-over-profile resolution | merged | #1124 | 2026-06-07 | 4d30be12 |
| 582 | Notification-channel digest scheduler (fan-out driver) | merged | #1114 | 2026-06-07 | bf2163a5 |
| 583 | Per-kind filtering for Slack + webhook channels | merged | #1119 | 2026-06-07 | 5fc6c7ad |
| 584 | Settings UI toggles for Slack + webhook notification channels | merged | #1113 | 2026-06-07 | e4c50068 |
| 585 | Disabled "channel not configured" state for delivery toggles | merged | #1118 | 2026-06-07 | ba13da85 |
| 589 | Vendor-claim read API + operator accept/reject disposition (c... | merged | #1147 | 2026-06-08 | 21f66cba |
| 590 | MDM connectors: software-inventory cursor pagination (Jamf + ... | merged | #1128 | 2026-06-07 | 83bc731c |
| 594 | Settings UI for Slack + webhook per-kind notification toggles | merged | #1123 | 2026-06-07 | be32ca6c |
| 595 | MDM connectors: configuration-profile per-setting enrichment ... | merged | #1189 | 2026-06-09 | 8ba8c8f8 |
| 599 | OSCAL resolved-chain provenance read surface | merged | #1129 | 2026-06-07 | c4167dee |
| 608 | Per-tenant control-bundle upload test-gate policy (`require_b... | merged | #1134 | 2026-06-08 | 4974fe06 |
| 613 | Web settings toggle for the per-tenant control-bundle gate po... | merged | #1141 | 2026-06-08 | b0f41c87 |
| 614 | Azure connector: Azure Firewall rule-collection evidence | merged | #1164 | 2026-06-08 | 1706ccbe |
| 615 | Azure connector: Key-Vault RBAC role-assignment enumeration | merged | #1154 | 2026-06-08 | a6a173de |
| 619 | Accepted vendor claim → OSCAL SSP control-implementation ev... | merged | #1150 | 2026-06-08 | 91442368 |
| 620 | Map an unmapped vendor claim to an SCF anchor (operator mappi... | merged | #1155 | 2026-06-08 | 3d690943 |
| 621 | Kubernetes connector: cursor pagination across all reads | merged | #1195 | 2026-06-09 | 77c7ca14 |
| 622 | Kubernetes connector: CNI-native NetworkPolicy (Cilium / Cali... | merged | #1204 | 2026-06-09 | 65e3eb03 |
| 623 | Azure Key-Vault: roleAssignments cursor pagination for large ... | merged | #1192 | 2026-06-09 | 0884916d |
| 631 | CI guard: block merge when a required integration shard is RE... | merged | #1163 | 2026-06-08 | 7fee2bd2 |
| 633 | Fix slice 474 ingest/verify canonical-scope hash round-trip d... | merged | #1160 | 2026-06-08 | 90b1416a |
| 634 | Azure connector: Azure Firewall rule-collection-group cursor ... | merged | #1167 | 2026-06-08 | 877f0173 |
| 635 | Seed the THR (threat-detection) SCF anchor for the SIEM-rule ... | merged | #1174 | 2026-06-08 | 7c179b1a |
| 636 | Datadog connector: Cloud-SIEM signal-history (event-driven) p... | merged | #1207 | 2026-06-09 | 49e54067 |
| 641 | Import the full SCF Threat-Management (THR) domain + reconcil... | merged | #1178 | 2026-06-08 | cc8ffb0c |
| 646 | Map the finer SCF THR controls into the framework crosswalks | merged | #1182 | 2026-06-08 | a9c63b70 |
| 651 | Map THR-05 / THR-06 / THR-07 into framework crosswalks (insid... | ready |  |  |  |
| 652 | Kubernetes connector: admission-webhook + policy-engine evide... | merged | #1213 | 2026-06-09 | 5d2c7351 |
| 653 | Kubernetes connector: cursor pagination for the PSS collector | merged | #1198 | 2026-06-09 | dd804bbb |
| 654 | Validate schema `x-default-scf-anchors` against the bundled c... | merged | #1219 | 2026-06-09 | 5119d941 |
| 655 | BambooHR webhook multi-employee fan-out | merged | #1225 | 2026-06-10 | 181ec7e2 |
| 656 | Cross-connector shared webhook-receiver abstraction (refactor) | merged | #1231 | 2026-06-10 | cce247d5 |
| 657 | webhookrecv: first-class shared validation-handshake hook | merged | #1243 | 2026-06-10 | 77e8a0e8 |
| 658 | Azure connector: Event-Grid subscription / Activity-Log diagn... | ready |  |  |  |
| 659 | OSCAL "Vendor Claims" (component-definitions) list returns 500 | merged | #1261 | 2026-06-10 | 18d986cc |
| 660 | Feature-flag state does not gate exposed nav/routes (OSCAL, B... | merged | #1264 | 2026-06-10 | cdb65621 |
| 661 | Global search returns no results for controls / SCF anchors | merged | #1252 | 2026-06-10 | 078746fb |
| 662 | Board pack §05 (Vendor burndown) not rendered; raw key leaks... | merged | #1258 | 2026-06-10 | 07b24d13 |
| 663 | Risk creation is a dead end on a fresh tenant (mitigate requi... | merged |  | 2026-06-11 | 7a5384cb |
| 664 | Vendor "Review burndown" shows 100% on-time with 0 vendors (d... | merged |  | 2026-06-10 | ea8c06b4 |
| 665 | Board pack "Generate draft" gives no feedback when quarter-en... | merged |  | 2026-06-10 | a263b530 |
| 666 | Controls page count is inconsistent (header "53 of 53" vs foo... | merged |  | 2026-06-11 | 0a3e82fa |
| 667 | Dashboard "Recent activity" filter chips are inert; placehold... | merged |  | 2026-06-11 | 878c9129 |
| 668 | Calendar month view does not highlight "today" | merged |  | 2026-06-11 | 10b655a9 |
| 669 | Activity ledger is dominated by internal read-telemetry (low ... | merged |  | 2026-06-11 | 4e26fd21 |
| 670 | Pre-GA copy & metadata pass (titles, breadcrumbs, raw IDs, ty... | merged |  | 2026-06-11 | b0a6ba5a |
| 671 | Seeded demo tenant shows no evaluated control state / zero me... | merged | #1249 | 2026-06-10 | 2894827a |
| 672 | Policy detail link 404s — `/policies/{id}` route does not e... | merged | #1255 | 2026-06-10 | d8a8763c |
| 673 | Board Packs list fails to load in seeded tenant — `/api/boa... | merged | #1258 | 2026-06-10 | 07b24d13 |
| 674 | Dashboard/breadcrumb shows "Default Tenant" while the active ... | merged |  | 2026-06-11 | f850f1dd |
| 675 | Calendar agenda missing audit-period / vendor-review / policy... | merged |  | 2026-06-11 | 6be54edf |
| 676 | Pervasive 503s on Next.js RSC prefetch/navigation requests | ready |  |  |  |
| 677 | Metrics correctness pass (freshness contradiction, count labe... | merged |  | 2026-06-11 | 5ac10361 |
| 678 | Demo seed completeness: org_units, questionnaires, ack-role u... | merged |  | 2026-06-11 | 01f4038d |
| 679 | Vendor UX/data: name/domain spacing, missing Delete control, ... | merged |  | 2026-06-11 | a490e17a |
| 680 | Data-quality + scoring clarity: audit-period labels, residual... | merged |  | 2026-06-11 | d9ca9533 |
| 681 | Risk register UX: column sorting + per-risk detail; sidebar "... | merged |  | 2026-06-11 | 35dca30d |
| 682 | Demo seed: anchor controls to SCF + give the demo framework r... | merged |  | 2026-06-11 | 76344c22 |
| 683 | OSCAL component-definitions edge migration-lag (deploy-note /... | not-ready | #1261 |  | OSCAL edge migration-lag deploy-note; blocked on maintainer edge access |
| 684 | Risks page count is inconsistent (header "N of M risks" vs fo... | merged |  | 2026-06-11 | 6f5289ca |
| 685 | Wire dashboard "Recent activity" filter chips to a real kind ... | not-ready |  |  | wire dashboard activity chips — blocked on backend endpoint filter support (spillover from 667) |
| 686 | Read-only vendor detail page with review history | merged |  | 2026-06-11 | 84e6386c |
| 687 | Contract-tier rollout: controls-detail + audit-workspace REMA... | merged |  | 2026-06-11 | fb9b03b4 |
| 688 | vendor_reviews ledger (per-review history surface) | merged |  | 2026-06-11 | 7514a084 |
| 689 | Contract-tier rollout: audit-workspace read tail (populations... | merged |  | 2026-06-11 | 4020b1a4 |
| 690 | Contract-tier rollout: audit-workspace read-tail remainder (s... | merged |  | 2026-06-11 | 035a8f60 |
| 691 | Vendor detail does not refresh after recording a review | merged |  | 2026-06-11 | b600c061 |
| 692 | Contract-tier rollout: controldetail attest-form + per-contro... | merged |  | 2026-06-11 | 1a4d3fa1 |
| 693 | CI pipeline efficiency + safety hardening (Tier 1) | merged | #1301 | 2026-06-11 | 3d11343c |
| 694 | Docker layer caching on the trivy-image job | ready |  |  |  |
| 695 | Share the prebuilt atlas binary across jobs (build once) | ready |  |  |  |
| 696 | Share the `.next` frontend build + standardize on `npm ci` | ready |  |  |  |
| 697 | Cache uv/pip in the Python CI jobs | ready |  |  |  |
| 698 | De-duplicate the precommit CI job's language hooks | ready |  |  |  |
| 699 | PR-scope (or demote) the three advisory bot comments | ready |  |  |  |
| 700 | Move the Trivy image scan off the PR hot-path to a nightly ma... | ready |  |  |  |
| 701 | Collapse the ~21 stub jobs behind a promoted merge-gate | ready |  |  |  |
| 702 | container-publish edge-build efficiency | ready |  |  |  |
| 703 | Main-canary: run a single representative leg on docs-only pushes | ready |  |  |  |
| 704 | Contract-tier rollout: tenant-wide `/v1/evidence` ledger wind... | merged |  | 2026-06-11 | f882950d |
| 732 | Calendar/dashboard exception event labels show the raw contro... | merged |  | 2026-06-11 | 9159b6a5 |
| 733 | Live group-role derivation wiring + SCIM /Groups REST resource | ready |  |  |  |
