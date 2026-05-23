# Slice 204 — UI parity audit fleet · aggregate report

**Date**: 2026-05-23
**Target**: `https://atlas-edge.home.gmoney.sh` (atlas-edge live deployment on commit `4974e7a` — main HEAD after slices 206 + 208 + 209 + 210 + 211 + 212 collectively resolved the v1.14.0 500-error class)
**Auditor**: slice 204 fleet (11 parallel per-page audit Agents, dispatched in three waves of 4 + 4 + 3)
**Aggregate output**: 11 per-page PRs · 49 spillover slices · 11 per-page audit logs

## Headline findings

- **v1.14.0 500-error class is RESOLVED**. Every page audited returned HTTP 200; no panel-level 403 surfaces remained (the dashboard agent explicitly confirmed all six panel endpoints return clean 200s with honest empty-states).
- **Findings are predominantly chrome + affordance gaps**, not data-bound lies. The page set is honest about what it has — the gaps are about what it SHOULD show but doesn't (breadcrumbs, audit-in-progress pills, command-K search bars, sidebar item counts).
- **Highest-density honesty gap**: `disabled` buttons / CTAs without tooltips or affordance signposting. Pattern appears on `/audits` (#217 OSCAL export), `/evidence` (#233 Push evidence), `/policies` (#241 Ack report + New policy), `/risks` (#247 New risk), `/controls` (#225 New control). Five separate findings on the same underlying anti-pattern; the maintainer may choose to bundle into one cross-cutting "disabled-CTA-affordance" slice.
- **Highest-impact single finding**: #253 (control-detail "endpoint not on main yet" empty-states). Five user-facing surfaces understate platform completeness — the backends shipped (slice 106 evidence list, per-control history/policies/risks endpoints) but the UI still renders the placeholder copy. HIGH severity per the auditor.
- **Mockup-stale findings** (mockup references features that don't ship): 9 of 49 findings are this class. Concentrated on `/audits` (#216 Sample size column), `/board-packs` (#220 coverage-trend chart), `/policies` (#240 365-day disclosure), `/risks` (#245 above-appetite tally), `/index` (#258 stale "design only" badges + #259 missing nav tiles), `/questionnaire` (#264 Excel column-mapping review). Cleanest fix is mockup-side, not code-side.

## Per-PR summary

| PR                                                          | Page              | Spillovers  | Density  | High-severity findings                                                             |
| ----------------------------------------------------------- | ----------------- | ----------- | -------- | ---------------------------------------------------------------------------------- |
| [#517](https://github.com/mgoodric/security-atlas/pull/517) | `/audits`         | 213-217 (5) | moderate | —                                                                                  |
| [#518](https://github.com/mgoodric/security-atlas/pull/518) | `/board-packs`    | 218-222 (5) | moderate | —                                                                                  |
| [#519](https://github.com/mgoodric/security-atlas/pull/519) | `/dashboard`      | 228-232 (5) | moderate | — (500-class confirmed resolved)                                                   |
| [#520](https://github.com/mgoodric/security-atlas/pull/520) | `/controls`       | 223-227 (5) | moderate | —                                                                                  |
| [#521](https://github.com/mgoodric/security-atlas/pull/521) | `/evidence`       | 233-237 (5) | moderate | —                                                                                  |
| [#522](https://github.com/mgoodric/security-atlas/pull/522) | `/policies`       | 238-242 (5) | moderate | —                                                                                  |
| [#523](https://github.com/mgoodric/security-atlas/pull/523) | `/risks`          | 243-247 (5) | moderate | —                                                                                  |
| [#524](https://github.com/mgoodric/security-atlas/pull/524) | `/settings`       | 248-252 (5) | moderate | — (11 slice-154 duplicates correctly skipped)                                      |
| [#525](https://github.com/mgoodric/security-atlas/pull/525) | `/` (index)       | 258-259 (2) | light    | — (lighter scope acknowledged)                                                     |
| [#526](https://github.com/mgoodric/security-atlas/pull/526) | `/questionnaires` | 263-264 (2) | light    | #263 HIGH (page entirely missing; slice 155 backend shipped but frontend deferred) |
| [#527](https://github.com/mgoodric/security-atlas/pull/527) | `/controls/{id}`  | 253-257 (5) | moderate | #253 HIGH (stale empty-states understate completeness)                             |

## Full spillover catalog

Sortable by page · finding category · severity · spillover slice. Categories: (i) layout / chrome parity · (ii) broken interactions · (iii) data-bound surfaces that lie · (iv) mockup-stale text.

| Slice | Page           | Category                | Severity | Title                                                                                                           |
| ----- | -------------- | ----------------------- | -------- | --------------------------------------------------------------------------------------------------------------- |
| #213  | audits         | (i) layout              | medium   | Audits page header chrome parity gap (breadcrumb + in-progress badge + global search + user avatar)             |
| #214  | audits         | (i) layout              | medium   | Sidebar item counts parity gap (Controls "82", Risks "3" badges)                                                |
| #215  | audits         | (i) layout              | low      | Audits page title status tally missing                                                                          |
| #216  | audits         | (iv) mockup-stale       | low      | Audits mockup "Sample size" column stale                                                                        |
| #217  | audits         | (ii) broken-interaction | low      | "Export OSCAL bundle" button permanently disabled on /audits                                                    |
| #218  | board-pack     | (i) layout              | low      | UI honesty: board-pack detail breadcrumb chain missing                                                          |
| #219  | board-pack     | (iii) data-bound        | medium   | UI honesty: board-pack header "Author" cell hardcoded to em-dash                                                |
| #220  | board-pack     | (iv) mockup-stale       | low      | Mockup update: board-pack coverage trend is scalar-only in v1                                                   |
| #221  | board-pack     | (i) layout              | medium   | Board-pack section divergence: vendor-burndown (mockup) vs open-findings (live)                                 |
| #222  | board-pack     | (i) layout              | low      | Board-pack posture coverage-definition caption missing                                                          |
| #223  | controls       | (i) layout              | medium   | UI honesty: controls top bar omits breadcrumb, search, audit banner, avatar                                     |
| #224  | controls       | (i) layout              | medium   | Add Scope filter pill to /controls list                                                                         |
| #225  | controls       | (ii) broken-interaction | medium   | UI honesty: "New control" button on /controls is silently disabled                                              |
| #226  | controls       | (i) layout              | medium   | Add Frameworks-per-row column to /controls list                                                                 |
| #227  | controls       | (i) layout              | low      | Add pagination to /controls list                                                                                |
| #228  | dashboard      | (i) layout              | medium   | UI honesty: dashboard global command-K search bar missing from topbar                                           |
| #229  | dashboard      | (i) layout              | medium   | UI honesty: dashboard header lacks tenant + snapshot-freshness subtitle                                         |
| #230  | dashboard      | (i) layout              | medium   | UI honesty: dashboard "Export" and "New board report" header actions missing                                    |
| #231  | dashboard      | (iv) mockup-stale       | low      | UI parity: dashboard mockup-stale "SOC 2 Type II · Q2 2026 in progress" topbar status pill                      |
| #232  | dashboard      | (i) layout              | low      | UI honesty: dashboard activity-feed "View full activity ledger" footer link missing                             |
| #233  | evidence       | (ii) broken-interaction | medium   | UI honesty: /evidence "Push evidence" CTA is disabled with no affordance                                        |
| #234  | evidence       | (i) layout              | medium   | UI honesty: /evidence filter row missing three pills (Source, Scope, Since)                                     |
| #235  | evidence       | (i) layout              | medium   | UI honesty: /evidence header missing audit-period banner + global search                                        |
| #236  | evidence       | (iii) data-bound        | low      | UI honesty: /evidence record-count meta lacks ledger-total context                                              |
| #237  | evidence       | (ii) broken-interaction | medium   | UI honesty: /evidence table missing pagination footer (cursor unwired)                                          |
| #238  | policies       | (i) layout              | medium   | Policies list: missing "Linked control" and "Ack status" filter pills                                           |
| #239  | policies       | (iii) data-bound        | medium   | Policies list: header missing inline "N published · M draft · K retired" counts                                 |
| #240  | policies       | (iv) mockup-stale       | low      | Policies list: missing pagination footer + "365-day acknowledgment window" disclosure                           |
| #241  | policies       | (ii) broken-interaction | medium   | Policies list: "Acknowledgment report" + "New policy" buttons render disabled                                   |
| #242  | policies       | (ii) broken-interaction | high     | Policies empty-state: "Scaffold five foundational policies" CTA redirects to unrelated admin page               |
| #243  | risks          | (i) layout              | medium   | UI honesty: risks top bar omits breadcrumb, search, audit banner, avatar                                        |
| #244  | risks          | (i) layout              | medium   | Risks list: extend filter pills to Category, Methodology, Org unit                                              |
| #245  | risks          | (iv) mockup-stale       | low      | Risks mockup-stale: "N above appetite" subtitle has no v1 backend concept                                       |
| #246  | risks          | (i) layout              | low      | Risks list: pagination control absent from footer                                                               |
| #247  | risks          | (ii) broken-interaction | medium   | Risks list: header "New risk" button is silently disabled, /risks/new is a real route                           |
| #248  | settings       | (i) layout              | low      | Settings page lacks page-specific `<title>` metadata                                                            |
| #249  | settings       | (i) layout              | medium   | Settings admin variants flicker between non-admin → admin on first paint                                        |
| #250  | settings       | (iii) data-bound        | medium   | Settings Profile section surfaces credential-bearer artifacts as user identity                                  |
| #251  | settings       | (ii) broken-interaction | medium   | Settings Notifications section returns error for credential-bearer JWTs                                         |
| #252  | settings       | (i) layout              | low      | Settings admin cross-link renders ASCII "->" instead of Unicode "→"                                             |
| #253  | controls/{id}  | (iv) mockup-stale       | **high** | UI honesty: control-detail "endpoint not on main yet" empty-states are stale                                    |
| #254  | controls/{id}  | (i) layout              | medium   | Control-detail tab strip absent (Overview / Evidence / Mappings / Effective scope / Policies / Risks / History) |
| #255  | controls/{id}  | (i) layout              | medium   | Control-detail header action buttons + "last evaluated" timestamp missing                                       |
| #256  | controls/{id}  | (i)+(iii)               | medium   | Coverage column in /controls/{id} coverage table missing                                                        |
| #257  | controls/{id}  | (i) layout              | low      | UI honesty: control-detail top bar chrome parity                                                                |
| #258  | / (index)      | (iv) mockup-stale       | low      | Mockup index: 6 "design only — implementation pending" badges are stale                                         |
| #259  | / (index)      | (iv) mockup-stale       | low      | Mockup index: missing tiles for Calendar / Metrics / Vendors / Board Packs / Catalog · SCF / Admin              |
| #263  | questionnaires | (i) layout              | **high** | UI honesty: questionnaire frontend page missing (mockup + backend exist; no live route)                         |
| #264  | questionnaires | (iv) mockup-stale       | low      | MOCKUP-STALE: questionnaire Excel column-mapping review UI                                                      |

## Cross-cutting patterns

These patterns appear across multiple page audits and may justify single "umbrella" slices rather than per-page fixes:

1. **Disabled-CTA-without-affordance** (5 findings: #217, #225, #233, #241, #247) — every disabled button SHOULD carry a tooltip explaining why. The pattern is uniform; one cross-cutting design slice may resolve them all.

2. **Top-bar chrome parity** (6 findings: #213 audits, #223 controls, #228 dashboard, #229 dashboard, #235 evidence, #243 risks, #257 control-detail) — every page is missing some combination of breadcrumb / global ⌘K search / audit-in-progress pill / user avatar. The chrome is rendered by the shared `(authed)` layout; one cross-cutting slice resolves these.

3. **Pagination footer** (4 findings: #227, #237, #240, #246) — list pages all lack pagination affordances even when the backend cursor exists. One cross-cutting "list-pagination harness" slice would unify the fix.

4. **Page-title tally / header counts** (3 findings: #215, #229, #239) — list pages should show inline status counts in headers. One cross-cutting slice.

5. **Filter-pill set completeness** (4 findings: #224, #234, #238, #244) — list pages systematically render fewer filter pills than the mockup or backend supports.

## Next-step recommendations

1. **Maintainer triage**: prioritize the 5 cross-cutting patterns above. Each could merge into a single mid-sized slice (~1-2d) that resolves 4-6 spillovers.
2. **High-severity individual fixes**: #253 (control-detail stale empty-states) and #263 (questionnaire frontend missing) deserve their own slice work.
3. **Mockup-edit-only slices**: #216, #220, #240, #245, #258, #259, #264 — these are cleanest as mockup updates, not code changes. Batch into one mockup-refresh slice.
4. **Settings cluster (#248-252)**: small / low-priority; defer or batch.

## What was NOT audited

Per slice 204 §"Scope discipline":

- No fix attempts made — all 49 findings are spillover slices
- No backend code inspected beyond what was needed to verify a finding's class (e.g. checking if a backend endpoint exists)
- No mobile / responsive audit
- No cross-tenant / multi-tenant UX edge cases
- No visual design critique (color / typography / spacing nits)
- No re-running of slice 178's heuristic harness
- v1.14.0 500-error class diagnostic was NOT performed (separate concern; the class is already resolved on main per the dashboard audit's finding)

## What the audit confirmed (positive findings)

- Slice 185 (risk-row-click) shipped correctly — verified live on `/risks`
- Slice 100 (controls list) shipped correctly — confirmed sidebar drops `/risks/hierarchy`
- Slice 178 (audit harness) `makeReadOnly(page)` pattern was used by every fleet agent
- Slice 154 (settings audit) dedup process worked — settings agent correctly skipped 11 already-filed findings
- Slice 075 + 176 (theme-aware logo) renders correctly on `/login` and authed routes
- Slice 091 (root-redirect contract) met both authed and unauthed
- Slice 209 + 210 + 211 (local-credential sign-in path) end-to-end functional — every audit agent successfully signed in with the bootstrap user JWT
