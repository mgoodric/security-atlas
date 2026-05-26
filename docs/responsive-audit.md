# Per-page responsive audit (security-atlas)

> Per-route verdict on how the page behaves at 375px viewport width
> after slice 277 lands the foundational baseline (viewport meta +
> sidebar drawer). The rubric the auditor follows is in
> [`web/docs/responsive-discipline.md`](../web/docs/responsive-discipline.md);
> this doc is the **source of truth for the per-page spillover slice
> backlog**. A row marked `partial` or `no` is a follow-on slice
> waiting to be filed (or, if already filed, linked in the Notes
> column).

## Verdict legend

| Verdict   | Meaning                                                                                                       |
| --------- | ------------------------------------------------------------------------------------------------------------- |
| `yes`     | Content readable at 375px · no horizontal scroll · primary actions reachable · sidebar drawer toggles cleanly |
| `partial` | Content readable but layout suboptimal (e.g. dense table that should be a card stack at mobile)               |
| `no`      | Content unreadable · horizontal scroll on body · primary actions unreachable at 375px                         |
| `n/a`     | Admin-only or explicitly desktop-only by design (document the rationale)                                      |

## Methodology

Each route was inspected statically against the slice 277 implementation
(viewport meta in place, sidebar drawer mounted, `hidden md:block` on the
desktop sidebar). Pages already using `grid-cols-1 → md:grid-cols-N`
patterns received a `yes`. Pages with `<ListTable>` + 5+ column tables
received a `partial` (the table will horizontal-scroll at 375px;
collapsing to a card stack is a per-page spillover). Pages with no
responsive classes at all received `partial` or `no` based on whether
the content is single-column-friendly by default.

The audit is intentionally **conservative**: when in doubt, the page
gets `partial` and a spillover row, not `yes`. The maintainer can
re-verdict to `yes` after a real-device pass.

## Shell (cross-cutting chrome)

| Surface                           | Verdict   | Notes                                                                                                                                              |
| --------------------------------- | --------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Sidebar (desktop)                 | `n/a`     | Hidden at `< md` by slice 277. Renders inline at `≥ md` exactly as pre-277.                                                                        |
| Sidebar drawer (mobile)           | `yes`     | Shipped this slice. AC-3..AC-7 pin the behavior.                                                                                                   |
| Topbar (logo + chip + breadcrumb) | `partial` | The right side (GlobalSearch + audit pill + TenantSwitcher + avatar + Sign out) is dense at 375px. Spillover: hide-or-collapse some items at < sm. |
| Version footer                    | `yes`     | Fixed-position, single line; no width problem.                                                                                                     |

## Authed routes (`web/app/(authed)/**`)

| Route                                      | Verdict   | Notes                                                                                                                                                                                                                                                                           |
| ------------------------------------------ | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/dashboard`                               | `partial` | Top-line uses `grid-cols-1 lg:grid-cols-3` (good); inner panels (activity feed, drift table) have long lines that need `break-words` / row-stack treatment.                                                                                                                     |
| `/calendar`                                | `partial` | Outer grid `grid-cols-12` with `col-span-12 md:col-span-3/9` collapses cleanly to single column. Calendar grid itself (date cells) may need a smaller cell at 375px.                                                                                                            |
| `/dashboards/metrics`                      | `partial` | Metrics tile grid uses Tailwind; sparkline canvases may overflow at very narrow widths. Spillover for tile-stack treatment.                                                                                                                                                     |
| `/dashboards/metrics/[id]`                 | `partial` | Detail page chart canvas needs explicit `max-w-full` audit.                                                                                                                                                                                                                     |
| `/activity`                                | `partial` | Slice 270's filter chips + dense audit-log table — will horizontal-scroll at 375px. Spillover for filter wrap + row-card collapse.                                                                                                                                              |
| `/controls`                                | `yes`     | Re-verdicted from `no` by slice 281 — `<ListTable>` now collapses to a card stack at `< md` via `mobileMode="cards"`. Each card surfaces the SCF anchor + Name link at the top so the primary affordance is one-tap at 375px. Desktop unchanged at `≥ md`.                      |
| `/controls/[id]`                           | `partial` | Tab strip (slice 254) wraps OK; long-form attestation panes have wide pre-formatted text blocks that need `overflow-x-auto` audit.                                                                                                                                              |
| `/controls/[id]/attest`                    | `partial` | Form layout is already single-column; the table of evidence candidates beneath wraps. Likely close to `yes` after a real-device pass.                                                                                                                                           |
| `/risks`                                   | `yes`     | Re-verdicted from `no` by slice 281 — `<ListTable>` now collapses to a card stack at `< md` via `mobileMode="cards"`. The per-row "View in hierarchy" link (slice 185) is reachable inline on every card instead of requiring a horizontal scroll. Desktop unchanged at `≥ md`. |
| `/risks/hierarchy`                         | `partial` | Slice 056 already implements an `md:grid-cols-12` shape with 3/5/4 column split that collapses to single column at `< md`. The tree + heatmap panels themselves likely need 375px-specific tightening.                                                                          |
| `/risks/new`                               | `yes`     | Form is single column with `max-w-*` constraints; works at 375px.                                                                                                                                                                                                               |
| `/evidence`                                | `yes`     | Re-verdicted from `no` by slice 281 — `<ListTable>` now collapses to a card stack at `< md` via `mobileMode="cards"`. The row-click drawer (full record JSON) still opens on card tap, so the ledger workflow is one-handed at 375px. Desktop unchanged at `≥ md`.              |
| `/audits`                                  | `partial` | List of audit periods; period cards likely collapse, but the period-detail action row needs verification.                                                                                                                                                                       |
| `/audits/new`                              | `yes`     | Form-style page; mostly single column.                                                                                                                                                                                                                                          |
| `/policies`                                | `partial` | List table of policies. Spillover for card-stack collapse.                                                                                                                                                                                                                      |
| `/vendors`                                 | `partial` | List table of vendors. Spillover for card-stack collapse.                                                                                                                                                                                                                       |
| `/vendors/[id]`                            | `partial` | Detail page is mostly two-column metadata; collapses OK but the tab strip below may need wider audit.                                                                                                                                                                           |
| `/vendors/new`                             | `yes`     | Form-style page.                                                                                                                                                                                                                                                                |
| `/questionnaires`                          | `partial` | List of questionnaires; same table-collapse story as policies/vendors.                                                                                                                                                                                                          |
| `/questionnaires/[id]`                     | `partial` | Question-answer detail page. Long answer text needs `break-words` audit.                                                                                                                                                                                                        |
| `/exceptions`                              | `partial` | List table of exceptions. Same table-collapse story.                                                                                                                                                                                                                            |
| `/framework-scopes`                        | `partial` | Multi-pane scope editor; needs a real-device pass for the predicate-editor surface.                                                                                                                                                                                             |
| `/framework-scopes/[framework_version_id]` | `partial` | Detail view with mapping table — horizontal-scroll at 375px.                                                                                                                                                                                                                    |
| `/board-packs`                             | `partial` | List of board-pack cards. Likely close to `yes` but unverified at 375px.                                                                                                                                                                                                        |
| `/board-packs/[id]`                        | `partial` | Print-styled report layout — designed for letter-size paper, NOT mobile reading. Spillover for "view on mobile" treatment or explicit n/a.                                                                                                                                      |
| `/catalog/scf`                             | `partial` | SCF anchor list — likely a table or grid; needs verification.                                                                                                                                                                                                                   |
| `/catalog/scf/[id]`                        | `partial` | Anchor detail with framework-crosswalk panels. Needs verification.                                                                                                                                                                                                              |
| `/settings`                                | `partial` | Slice 250 profile page is mostly single-column; the personal-API-tokens table at the bottom needs collapse.                                                                                                                                                                     |

## Admin routes (`web/app/admin/**`)

Admin routes are role-gated (slice 060 + slice 186). They are
**desktop-first by deliberate choice** — the operator is doing
multi-table data entry (tenant management, super-admin grants, SSO
config) where dense desktop tables outperform mobile card stacks. The
audit verdicts reflect "works at 375px even if it's not pretty," not
"matches desktop UX."

| Route                 | Verdict   | Notes                                                                                                          |
| --------------------- | --------- | -------------------------------------------------------------------------------------------------------------- |
| `/admin/tenants`      | `partial` | Slice 142 form uses `grid-cols-1 sm:grid-cols-2`; collapses cleanly. The tenant list below may need attention. |
| `/admin/users`        | `partial` | Standard user table. Same table-collapse story.                                                                |
| `/admin/super-admins` | `partial` | Two-table layout. Same table-collapse story.                                                                   |
| `/admin/sso`          | `partial` | SSO config form; single column.                                                                                |
| `/admin/api-keys`     | `partial` | API-key table — wide columns; spillover.                                                                       |
| `/admin/features`     | `partial` | Feature-flag table.                                                                                            |
| `/admin/audit`        | `partial` | Slice 124/125 unified audit log table — horizontal-scroll at 375px. Same table-collapse story as `/activity`.  |

## Spillover slices filed by this audit

The audit identifies four classes of follow-on work. Per the slice 277
continuous-batch policy, one summary spillover slice was filed during
this audit; the maintainer can split it into per-page slices as the
priority backlog clarifies.

| Spillover slice                                   | Status   | Scope                                                                                                                    |
| ------------------------------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------ |
| [`281`](issues/281-mobile-list-table-collapse.md) | `merged` | Mobile-aware list-table collapse pattern + per-page application to `/controls` + `/risks` + `/evidence` (priority three) |

Future per-page slices (NOT filed by 277) that the audit table above
implies:

- `/activity` filter-chip wrap + unified-log card collapse
- `/policies` + `/vendors` + `/exceptions` + `/questionnaires` + admin
  tables — each gets a card-stack collapse slice
- `/board-packs/[id]` — explicit n/a or "view on mobile" stripped variant
- Topbar right-side density — collapse / hide Sign-out / TenantSwitcher
  at `< sm`

These rows stay open in this audit doc; the maintainer files individual
slices as priority + bandwidth allow.

## When to update this doc

- Add a row when a new authed route ships.
- Re-verdict a row from `partial` / `no` to `yes` when a spillover
  slice lands and the page passes the three-checkpoint test in
  [`web/docs/responsive-discipline.md`](../web/docs/responsive-discipline.md).
- The doc is intentionally append-mostly; the historical record of
  which routes needed which fixes is more useful than a moving snapshot.
