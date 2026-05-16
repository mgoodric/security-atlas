# 12 — UI fill-in design decisions (slice 093)

> Decisions captured during slice 093 so the six follow-up implementation slices
> (`/controls`, `/evidence`, `/risks`, `/policies`, `/audits`, `/settings`) don't
> re-litigate them. The artifacts live at `Plans/mockups/{controls,evidence,risks,policies,audits,settings}.html`.

---

## 1. Top-nav order (the v2 canonical order)

**Decision (slice 093, extended 2026-05-16 post-094 / post-097):**

Dashboard · **Calendar · Metrics** · Controls · Evidence · Risks · _Risk hierarchy_ · Audits · Policies · Vendors · Board Packs · **Catalog · SCF** · Settings · Admin.

**Post-093 additions (recorded here per audit `Plans/canvas/13-ui-mockup-audit-2026-05-16.md` F-2):**

- **Calendar** (slice 094) — cross-business compliance calendar. Sits immediately after Dashboard per slice 094 decision D8: shortest scanning path for the cross-business at-a-glance cluster.
- **Metrics** (slice 097) — board cascade metrics dashboard. Clusters with Dashboard + Calendar as the cross-business "at-a-glance" group.
- **Catalog · SCF** — reference content (~1,400 SCF anchors). Sits after Vendors + Board Packs as a reference top-level; not in the core-5 cluster because it's not per-tenant data.
- _Risk hierarchy_ stays italicized in the list because it's a **transitional** entry: slice 101 (`/risks` list view) is expected to remove it from the sidebar and instead link to it from the `/risks` page header per §5. Until slice 101 lands, removing it would orphan the org-tree view.

**Rationale:**

- **Dashboard first** — the home screen a returning user expects. Matches every other GRC tool the primary user has seen (Vanta, Drata, Hyperproof).
- **Controls / Evidence / Risks / Audits / Policies** — these are the five core platform primitives the user touches every week. Ordered by frequency of access for the solo security leader persona:
  - _Controls_ is opened most (drift triage, pre-audit walk-through).
  - _Evidence_ is opened second-most (verifying a connector pushed what it should).
  - _Risks_ is opened weekly (treatment progress, residual review).
  - _Audits_ is opened in bursts (period setup, sample review, freeze).
  - _Policies_ is opened least often (publish, ack-rate review) but is first-class for SOC 2.
- **Vendors** — adjacent program work, less frequently touched by the solo leader than the core five.
- **Board Packs** — quarterly cadence. Belongs in the nav for discoverability but appears below the weekly items.
- **Settings** — user-facing personal preferences. Sits near the bottom because users open it once a quarter at most.
- **Admin** — tenant-wide configuration. Last in the nav because most signed-in users will never click it (only the admin role has access).

**Where this lives:** the canonical sidebar is duplicated in every mockup that has a sidebar
(`dashboard.html` + the six new mockups). The order MUST stay byte-identical across files — a
shared partial isn't possible because mockups are deliberately self-contained Tailwind-via-CDN
HTML with no build step (per `Plans/canvas/09-tech-stack.md`'s "Tailwind via CDN" choice). The
implementing agent for the six follow-up slices is expected to fold this nav into a single
`web/components/AppShell.tsx` partial when the React port lands.

**Mockups without a sidebar** (`control.html`, `board-pack.html`, `questionnaire.html`) keep
their breadcrumb-only top-bar pattern — they're focused views, and the user is expected to have
already navigated in via the sidebar or a hyperlink from the dashboard. AC-9 of slice 093 is
satisfied for these pages by leaving them as-is, since they have no sidebar to update. The
implementing agents for the four iteration-1 deep workflows will pick a single shell-vs-no-shell
convention when those pages graduate from mockup to code (deferred to the per-page slices, not
in scope for 093).

---

## 2. Empty-state pattern

**Standard:** centered illustration (16px-line heroicon, slate-300) + one-sentence cause +
one-sentence next-step + one primary CTA button (sometimes paired with a secondary "clear
filters" link).

```
[ icon ]
{One sentence: what the user is seeing.}
{One sentence: what they can do about it.}
[ Primary CTA ]   ( · Secondary link · )
```

**Why centered + minimal:** the user almost always reaches an empty state because they
over-filtered or because they're brand-new to a feature. We don't want a dense "do these 8
things" panel — we want one obvious next click. If the empty state needs more guidance, that
guidance lives in the docs site (see slice 058), linked from the secondary "learn more" link
on a per-page basis.

**Variants by page:**

- **controls** — "No controls match these filters" → `Clear filters` button (filter-induced empty, not zero-state).
- **evidence** — "No records match" → `Clear filters` + `Set up a connector →` link (true zero-state on first install needs the connector path).
- **risks** — "No risks logged yet" → `Add first risk` (true zero-state, since most installs start with zero).
- **policies** — "No policies published yet" → `Scaffold five foundational policies` (true zero-state with a scaffold-wizard pathway).
- **audits** — "No audit periods yet" → `Create audit period`.
- **settings** — no empty state required (always populated by the OIDC-synced profile).

**Anti-pattern explicitly rejected:** Lorem-ipsum "your data goes here" placeholders. Every
empty state names the specific primitive (controls, records, risks, policies, periods) and the
specific path back to a populated state.

---

## 3. Loading-skeleton pattern

**Standard:** 3 shimmer rows that mirror the table's column widths. The skeleton row uses
`animate-pulse` and a slate-200 fill; the column widths are chosen to roughly approximate the
real content so the perceived layout shift on load is minimal.

```
┌────────────────────────────────────────────────────┐
│ [▓▓▓▓ header bar]                                  │
├────────────────────────────────────────────────────┤
│ [▓▓ id]  [▓▓▓▓▓▓▓▓▓▓ title]  [▓▓ status] [▓▓ ts] │
│ [▓▓ id]  [▓▓▓▓▓▓▓▓▓▓ title]  [▓▓ status] [▓▓ ts] │
│ [▓▓ id]  [▓▓▓▓▓▓▓▓▓▓ title]  [▓▓ status] [▓▓ ts] │
└────────────────────────────────────────────────────┘
```

**Why 3 rows:** enough to show "this is a list, it's loading", not so many that the skeleton
itself becomes a distraction. TanStack Query's default 250ms keepPreviousData window absorbs
most refetches without ever showing the skeleton — the skeleton is for true first-load only.

**Anti-pattern explicitly rejected:** generic centered spinners. They tell the user nothing
about what's loading and discard the layout cue the skeleton provides.

---

## 4. /settings scope: user-facing only (admin lives at /admin)

**Decision:** `/settings` shows ONLY user-facing preferences:

- **Profile** — display name, time zone, OIDC subject (read-only).
- **Appearance** — theme (light / dark / system).
- **Notifications** — per-event in-app + email toggles (audit-period assignment, policy ack due, risk review overdue, control drift).
- **API tokens** — personal credentials for `security-atlas evidence push` CLI use. List shows last-4, scope predicate, allowed kinds, issued/last-used. Plaintext shown once on issue, never re-displayed.
- **Active sessions** — currently signed-in browsers; sign out one or all.

**Tenant-wide settings stay at `/admin`:**

- Tenant identity, brand, SSO config (admin SSO at `/admin/sso`)
- Credentials for connectors, scope predicates, kinds (already at `/admin/credentials`)
- User management, roles (at `/admin/users`)
- Audit log access (at `/admin/audit-log`)
- Org units, scopes, frameworks-in-scope, framework scope predicates

**Why split:** the solo-security-leader persona will spend 90% of their settings time on
tenant-wide concerns (those live at /admin). The 10% personal stuff (theme, token issuance for
their personal CLI) is a different mental model — it's about THEM, not the tenant. Mashing them
together leads to permission confusion ("can a non-admin see this page?") and makes the page
too long for a user who just wants to flip dark mode. The cross-link at the top of /settings
("Tenant administration → /admin") handles the navigation case.

**Open question deferred:** whether /settings is accessible without the admin role. v1
implementation will let any signed-in user open /settings (their own profile + tokens), so no
new auth surface is needed. The admin cross-link is hidden for non-admins.

---

## 5. /risks vs /risks/hierarchy co-existence

**Decision:** `/risks` is the canonical flat list (table). `/risks/hierarchy` (already shipped
in slice 056) is a specialized view for org-tree navigation.

**Rationale:**

- The flat list is what a user expects to see when they click "Risks" in the nav. It's how every other GRC tool shows risks.
- The hierarchy is genuinely useful for orgs with org_units configured (slice 052+053+056), but it's a power-user view, not the default.
- Both views read the same `riskWire` shape — no data-model fork. The hierarchy just imposes a tree layout over the flat list via the `org_unit_id` and parent_risk_id linkages.

**UX bridge:** `/risks` shows a `Hierarchy view →` link in the page header. `/risks/hierarchy`
should add a reciprocal `List view →` link when its slice gets a refresh (out of scope for 093
per P0-A3). Bookmarks land on `/risks` (the list); deep-links to a specific risk land on
`/risks/{id}` regardless of which view the user came from.

---

## 6. /audits vs /audit/[controlId] co-existence

**Decision:** `/audits` (plural) is the new period index. `/audit/[controlId]` (singular,
already shipped in slice 042) remains the per-control walk-through for an auditor.

**Rationale:**

- The plural `/audits` indexes `audit_periods` — the lifecycle artifact (created, in-progress,
  frozen, closed). This is what the user looks at when planning a Q-end freeze or starting a
  new period.
- The singular `/audit/[controlId]` is the auditor's deep-link for one control inside an
  open or frozen period — sample population, evidence walk, sign-off. Built for the auditor
  role, not the security leader.
- The two pages serve disjoint user goals; collapsing them would force the auditor through a
  list-view they don't need, and force the security leader to drill through individual
  controls to find the period.

**No URL collision:** Next.js routes `/audits` and `/audit/[id]` independently — different
top-level segments, different files (`web/app/audits/page.tsx` vs `web/app/audit/[id]/page.tsx`).
The trailing 's' is the disambiguator.

**Naming convention going forward:** plurals are list-views over a primitive; singulars are
per-entity workspaces. The same pattern applies to `/risks` (list) + `/risks/hierarchy`
(workspace) + future `/risks/[id]` (per-risk detail).

---

## 7. Data-model fidelity (P0-A5 enforcement)

Every column in every mockup is derived from a verified backend wire shape. The mappings:

| Mockup   | Wire shape source                                                                                           | Columns                                                                                                               |
| -------- | ----------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| controls | `internal/api/anchors/handlers.go` (`anchorWire`) + `internal/api/controlstate/handlers.go` (`stateWire`)   | scf_id · name · family · result · freshness_status + freshness_class · last_observed_at                               |
| evidence | `internal/api/evidence/http.go` (`recordWire` + `receiptWire`)                                              | observed_at · evidence_kind · control_id · result · source_attribution · scope · hash                                 |
| risks    | `internal/api/risks/handlers.go` (`riskWire`)                                                               | id · title · category · treatment · treatment_owner · residual_score · severity · review_due_at                       |
| policies | `internal/api/policies/handlers.go` (`policyWire`) + `internal/api/policyacks/handlers.go` (`rateResponse`) | title · version · status · owner_role · published_at · numerator/denominator · updated_at                             |
| audits   | `internal/api/auditperiods/handlers.go` (`periodWire`)                                                      | name · framework_version_id · period_start..period_end · status · frozen_at + frozen_by · created_by                  |
| settings | `internal/api/me/{notifications,audit_period}.go` + `internal/api/admincreds/http.go` (`ListItem`)          | display_name · email · tenant_role · timezone · notification toggles · token last4/scope/kinds/issued_at/last_used_at |

**If a follow-up implementation slice wants a column NOT on this list, the slice author MUST
either (a) verify the field already exists on the wire shape and update this table, or (b) file
a separate backend slice to add it.** The mockup is not authoritative over the backend; the
backend is authoritative.

---

## 8. Filter pattern

All six list-views use the same filter row pattern:

- Horizontal pill row above the table (not a left filter sidebar)
- Each filter is a `<select>` styled to look like a chip — label + value in one element
- Active filter set count is shown to the right (e.g. "Showing 7 of 47")
- "Clear filters" is exposed only on the empty-state CTA (no always-visible clear button to reduce chrome)

**Why horizontal vs sidebar:** the slice notes called this out — six list-views, keep them
simple. A left filter sidebar is appropriate for catalog-browse pages with deeply faceted
search (think `/catalog/scf`). For these six pages, the user wants 3–6 filters at most, and
the horizontal row keeps the table the focal element.

---

## Next steps — six follow-up implementation slices

This slice merges as the design phase. The six implementation slices are drafted as `ready`
backlog items immediately after merge:

- **`/controls` list view** — Next.js page at `web/app/controls/page.tsx` consuming
  `GET /v1/anchors` + `GET /v1/controls/{id}/state` joined. Table + filter row + empty state +
  loading skeleton per this design. Estimated 1–2d.
- **`/evidence` list view** — `web/app/evidence/page.tsx` consuming a new
  `GET /v1/evidence?control_id=&kind=&result=&since=` (which already exists for the dashboard
  activity feed at a different shape — verify and extend if needed). Estimated 1–2d.
- **`/risks` list view** — `web/app/risks/page.tsx` consuming the existing `GET /v1/risks`.
  Estimated 1d.
- **`/policies` list view** — `web/app/policies/page.tsx` consuming `GET /v1/policies` +
  `GET /v1/policies/{id}/ack-rate` per row. Estimated 1–2d (per-row fan-out needs care; may
  warrant a `GET /v1/policies?include=ack_rate` extension).
- **`/audits` list view** — `web/app/audits/page.tsx` consuming `GET /v1/audit-periods`.
  Estimated 1d.
- **`/settings` page** — `web/app/settings/page.tsx` consuming `GET /v1/me`, `GET /v1/me/notifications`,
  `GET /v1/admin/credentials` (scoped to the calling user, which is the existing shape).
  Settings mutations land via PATCH per section. Estimated 2d.

Slot numbers are intentionally not assigned here — they'll be assigned when the slices are
written (post-merge of 093), so they pick up the next available numbers in `_INDEX.md`. Per
the continuous-batch policy, the loop will pick these up automatically once their
`_STATUS.md` rows land as `ready`.

Total estimated wall-clock for the UI fill-in: ~8d across the six slices, runnable in
batches-of-three for ~3–4 calendar days of throughput.
