# 094 — Compliance calendar (cross-business view of upcoming audits + deadlines)

**Cluster:** Frontend / data aggregation
**Estimate:** 3d
**Type:** AFK

## Narrative

Audit periods, exception expirations, policy review cycles, and **periodic control reviews** (quarterly firewall rule reviews, quarterly access reviews, etc.) all have hard dates that matter to people outside the security/GRC team — engineering needs to know when sample collection windows open AND when their next firewall review is due, IT needs to see access-review deadlines in the same place they see audit milestones, legal needs to track exception expirations, finance plans for audit-fee timing, leadership wants visibility into the next quarter's compliance milestones. Today every one of those dates lives in its own page (\`/audit/[controlId]\`, the exceptions admin pages, a policies page that doesn't exist yet, control-detail pages buried per-control), with no aggregated view.

This slice ships a **compliance calendar**: one URL anyone in the tenant can hit to see what's coming up. Two views — agenda (default, next 90 days grouped by month) and month-grid (familiar calendar UI). Filterable by event type. Per-event link back to the canonical detail page (audit period, exception, policy, control). ICS export so engineers/legal/IT/finance can subscribe in their personal calendars and stop relying on email reminders.

**Scope discipline.** The calendar's value comes from being the _single place_ people check. Putting everything-with-a-date on it (every evidence freshness expiration, every framework version change) drowns the cross-business signal in operational noise. v1 ships exactly four event types — the ones with **scheduled, calendar-blockable, cross-business-relevant** dates:

1. **Audit periods** — sample collection deadline, walkthrough date, fieldwork window, report delivery
2. **Exception expirations** — exceptions granted via the GRC workflow have an end date; visibility on these 30/60/90 days out prevents quiet lapses
3. **Policy review cycles** — annual / biennial policy review dates; missing these breaks SOC 2 CC1.4
4. **Periodic control reviews** — controls with a defined cadence (quarterly firewall rule reviews, quarterly access reviews, monthly vendor reassessments, annual disaster recovery tests, etc.) — the activities that engineering / IT / business-process owners must perform on a fixed schedule to keep a control in a passing state

The distinction that defines what's IN vs OUT: events 1-4 are **scheduled business activities that someone has to block time for**. Out of scope are **ad-hoc operational events** that GRC staff watch in a daily ops dashboard — those don't belong on a cross-business calendar.

Out of scope for v1 (file as separate slices if value materializes): per-evidence-record freshness drift expirations (operational, not scheduled — every evidence record drifts on its own clock), framework-version supersession events, external regulator deadlines (HIPAA breach reporting clocks, GDPR DPIA deadlines).

## Acceptance criteria

### Backend — aggregation endpoint

- [ ] AC-1: New package \`internal/api/calendar/\` with handler \`GET /v1/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD&types=audit,exception,policy,control\`
- [ ] AC-2: Endpoint aggregates from four sources — \`audit_periods\` (audit events), \`exceptions\` (expiration dates), \`policies\` (next review date column; verify exists or add via migration in this slice), AND \`controls\` filtered to those with a defined review cadence (see AC-2b). Returns unified JSON event-list shape: \`{id, type, title, starts_at, ends_at|null, related_entity_id, related_entity_kind, summary, status, cadence|null}\` — \`cadence\` is populated only for \`type=control\` events.
- [ ] AC-2a: Verify the existing \`controls\` schema for a periodic-review cadence field (likely named \`review_cadence_days\`, \`review_frequency\`, or surfaced via the control bundle definition under \`controls/soc2/<id>/control.yaml\`). If the cadence is encoded in the bundle but not in the table, expose it via a derived view OR add a migration to surface it as a queryable column — flag the decision back to the maintainer rather than guessing the data shape.
- [ ] AC-2b: For controls with a defined cadence, compute \`next_due_at\` from \`last_evaluated_at + cadence\` (rolling-window cadence) OR from the next calendar-aligned period (fixed-quarter cadence like "Q1/Q2/Q3/Q4 of each year"). Both forms exist in real GRC programs; the cadence definition should specify which. Default to rolling-window unless the control bundle declares otherwise.
- [ ] AC-2c: A control with \`last_evaluated_at\` NULL (never evaluated) and a defined cadence emits a calendar event at \`now()\` with \`status: overdue\` — these are the highest-priority cross-business signal (a quarterly review that's never been done is the biggest red flag the calendar can surface).
- [ ] AC-3: RLS-enforced via tenant context (slice 033 pattern). Calendar is tenant-scoped — no cross-tenant leakage.
- [ ] AC-4: Default query window is 90 days forward from \`now()\` when \`from\`/\`to\` omitted. Maximum window is 366 days (one year) to bound the result size.
- [ ] AC-5: Pagination by date-range only — no \`offset\`/\`limit\` cursoring. If the result exceeds 500 events, return the first 500 ordered by \`starts_at\` plus a \`truncated: true\` flag and \`next_from\` suggestion (the date the caller should requery from).

### Backend — ICS export

- [ ] AC-6: \`GET /v1/calendar.ics?from=YYYY-MM-DD&to=YYYY-MM-DD&types=...\` returns an iCalendar 2.0 (RFC 5545) feed with \`VEVENT\` per row. Use a stable \`UID:\` per event (\`{type}-{id}@security-atlas.example\`) so calendar clients dedupe on re-subscribe.
- [ ] AC-7: \`X-WR-CALNAME:\` header reflects \`{tenant_display_name} compliance calendar\`. Cache-Control: \`private, max-age=300\` (5 min — calendar clients poll hourly typically; this allows reasonable freshness without re-rendering on every poll).
- [ ] AC-8: ICS feed requires the same auth as the JSON endpoint (bearer token in cookie OR ICS-specific URL token; see notes for implementing agent — ICS clients don't carry cookies, so a per-user opaque URL token is the standard pattern).

### Frontend — calendar page

- [ ] AC-9: New route \`/calendar\` under \`web/app/(authed)/calendar/page.tsx\` accessible to all signed-in users (RBAC: all roles, no admin gate — the whole point is cross-business visibility)
- [ ] AC-10: Default view: **agenda** (vertical list of events grouped by month header, next 90 days). Each row shows date + type-icon + title + linked entity. Empty state: "No compliance events in the next 90 days. Add an audit period or review your exception expirations to populate this view."
- [ ] AC-11: Toggle: **month-grid view** (standard 7-col × 5-row month calendar). Same events, different layout. Click on a date opens a popover listing that day's events. Month navigation via \`<\` \`>\` buttons.
- [ ] AC-12: Filter sidebar (collapses on mobile): event-type checkboxes (\`audit\`, \`exception\`, \`policy\`, \`control\`) — default all-checked. Filter state persists in URL query string for shareable filtered views (\`?types=audit,control\`).
- [ ] AC-13: Each event row/cell links to the canonical detail page — audit period → \`/audits/[id]\` (placeholder if /audits hasn't shipped yet — fall back to \`/audit/[controlId]\` if that's the closest existing page), exception → \`/admin/exceptions/[id]\` (placeholder), policy → \`/policies/[id]\` (placeholder), **control review → \`/controls/[id]\` (the existing detail page from slice 041)**.
- [ ] AC-13a: Control-cadence events render with the cadence in the row metadata (e.g. "quarterly review · last evaluated 87 days ago · due in 3 days"). Overdue control events (\`status: overdue\`) render with a red dot or equivalent visual urgency cue. Cross-business signal is most valuable when "you're behind on something" is unmissable.
- [ ] AC-14: "Subscribe in your calendar" link in the top-right exposes the per-user ICS URL. Click → copy-to-clipboard with a one-shot toast "URL copied. Paste into Google/Outlook/Apple Calendar's 'Add by URL' feature." Help-text tooltip explains the difference between import (one-shot) and subscribe (auto-refresh).

### Cross-business surface

- [ ] AC-15: New top-nav entry "Calendar" added to the shell, positioned between "Dashboard" and "Controls" (per slice 093's nav-order convention). Visible to every signed-in user regardless of role.
- [ ] AC-16: Dashboard's existing "Upcoming" panel (if present) links to \`/calendar\` for the full view. (Verify whether the dashboard has such a panel before adding the link — if not, skip this AC.)

### Tests

- [ ] AC-17: Go integration test against postgres + RLS asserting that an exception in tenant A does NOT appear in tenant B's calendar. Repeat the assertion for a control-cadence event in tenant A (controls table is also RLS-enforced; this proves the new aggregation path honors that).
- [ ] AC-17a: Go integration test for control-cadence math: seed a control with cadence=90d and \`last_evaluated_at = now() - 88d\`, assert the calendar surfaces it with \`status: due-soon\` and \`starts_at = last_evaluated_at + 90d\`. Repeat for the \`last_evaluated_at = NULL\` (never-evaluated) case and assert \`status: overdue\`.
- [ ] AC-18: Go integration test asserting the \`truncated: true\` flag and \`next_from\` cursor fire correctly at the 500-event threshold.
- [ ] AC-19: ICS feed validates against an iCalendar parser (use \`github.com/arran4/golang-ical\` or a stdlib-only minimal parser in the test) — no malformed VEVENT lines.
- [ ] AC-20: Playwright spec \`web/e2e/calendar.spec.ts\` asserts: agenda view renders, filter checkbox hides events, month-grid view renders, day-popover opens on click, ICS-copy button puts a URL on the clipboard.

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** AC-3 + AC-17 — calendar is tenant-scoped, RLS is the enforcer. Application code does not filter by tenant.
- **CLAUDE.md "design before implement":** slice 093 ships the missing-page mockups; this slice could land its own mockup as part of the work, OR cite an existing pattern. Note in implementation: if slice 093's design-decisions doc has landed, follow its empty-state + loading-skeleton patterns.

## Canvas references

- \`Plans/canvas/10-roadmap.md\` (verify "compliance calendar" is in-scope for v1 or a v1.x add — if explicitly deferred to phase 2, note that and ask before continuing)
- \`internal/api/auditperiods/handlers.go\` (existing audit-period read endpoints — calendar aggregates these)
- \`internal/api/exceptions/\` (exception expiration dates the calendar surfaces)
- \`internal/api/policies/\` (policy review-cycle dates the calendar surfaces; may need a migration to add a \`next_review_at\` column if one doesn't exist)
- \`internal/api/controls/\` + \`internal/api/controlstate/\` + \`internal/api/freshnessdrift/\` (control-cadence machinery — the existing freshness/state infrastructure tells you \`last_evaluated_at\`; cadence definitions are likely in the control bundle yaml under \`controls/soc2/\`)
- \`controls/soc2/<id>/control.yaml\` (verify how cadence is encoded for the SOC 2 stock control bundle — this is the reference schema the calendar reads against)
- Slice 093 (mockup design — extend the same nav/empty-state language)

## Dependencies

- #002 (merged) — base tables + tenancy plumbing
- #020 / #021 (audit periods + audit notes; verify both merged) — provides \`audit_periods\` table
- #011 (exceptions; merged) — provides \`exceptions\` table with expiration dates
- #022 (policy library; merged) — provides \`policies\` table; may need extension for review-cycle dates
- #009 (control bundle; merged) — provides \`controls\` table + the control-bundle yaml format where review cadence is encoded
- #012 (control evaluation engine; merged) — provides \`last_evaluated_at\` per control which the cadence math reads
- #033 (RLS enforcement; merged) — tenant isolation the calendar relies on
- #034 (auth + sessions; merged) — bearer cookie for the JSON endpoint; per-user URL token for the ICS endpoint
- #093 (mockup design; pending merge) — informs the empty-state + nav patterns

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT show **per-evidence-record** freshness drift expirations, or framework-version supersession events. v1 is exactly four event types (audit, exception, policy, control-cadence). The distinction between IN ("scheduled business activity") and OUT ("ad-hoc operational drift") is in the narrative — adding event types that don't meet the "someone blocks time on their calendar for this" test drowns the cross-business signal. Operational-drift dashboards belong elsewhere (e.g. a future GRC-staff ops view), not on the cross-business calendar.
- **P0-A2:** Does NOT add cross-tenant visibility. The calendar is tenant-scoped, RLS-enforced. A "public read-only calendar for prospective customers" is a phase-2 feature with its own design needs.
- **P0-A3:** Does NOT add write endpoints. The calendar is read-only — events are created/updated through their canonical detail pages (audit periods, exceptions, policies). The calendar aggregates; it doesn't own.
- **P0-A4:** Does NOT change the existing audit_periods / exceptions / policies tables' write paths or RLS policies. Read-only aggregation on top.
- **P0-A5:** Does NOT bundle the missing /audits, /exceptions, or /policies list views into this slice. Those are separate per-page slices (per slice 093's design half). The calendar's per-event links can point to placeholders that 404 with a friendly "page coming soon" message; replacing them with real links is automatic when the per-page slices land.
- **P0-A6:** Does NOT introduce a calendar-library dependency (FullCalendar, react-big-calendar, etc.) without a documented tradeoff. The agenda + month-grid views are simple enough to implement with hand-rolled Tailwind. If the implementing agent reaches for a library, surface as a design question and capture the tradeoff in the decisions log.
- **P0-A7:** Does NOT bake any tenant-specific event types into the schema. The handler reads from \`audit_periods\`/\`exceptions\`/\`policies\`/\`controls\` and emits a unified envelope — adding a new event type in a future slice means a new aggregation, not a new column.
- **P0-A9:** Does NOT modify the existing \`controls\` table beyond surfacing the cadence field if it isn't already queryable (AC-2a). The calendar READS control state; it does not own it. Cadence changes flow through the control bundle definitions, not via the calendar UI.
- **P0-A8:** Does NOT use vendor-prefixed tokens in test fixtures — neutral \`test-\*\` only.

## Skill mix (3–5)

- Multi-table aggregation in Go with RLS-aware pagination (4 sources: audit_periods + exceptions + policies + controls)
- Control-cadence math: rolling-window vs fixed-quarter-aligned next-due-at computation
- iCalendar (RFC 5545) format authoring + per-user URL-token auth
- Calendar UI patterns (agenda + month-grid) in Next.js without a heavy calendar library
- Tenant-isolation integration testing (slice 033 pattern, applied to the new \`controls\` aggregation path)
- Cross-business UX (the calendar is for non-GRC users; nav + empty-state copy + ICS-subscribe affordance + overdue-cue visual urgency reflect that)

## Notes for the implementing agent

- ICS auth is the tricky part. Calendar clients (Google Calendar, Apple Calendar, Outlook) fetch the URL with no cookies. Standard pattern: generate a per-user opaque token on first subscribe, store hashed in \`api_keys\` with a calendar-only scope, embed the token in the ICS URL (\`/v1/calendar.ics?token=<opaque>\`). Subscribed clients re-fetch every ~1hr; cache 5 min server-side. Revoke via a "rotate calendar URL" action in the user's settings.
- For the agenda view, the empty state matters a lot — most tenants will have <10 events in the next 90 days. Design the empty state to TEACH ("Add an audit period or review your exception expirations to populate this view") rather than just "No events."
- Verify the policies table has a \`next_review_at\` column (or similar) BEFORE designing the policy event aggregation. If not, the slice expands to include a migration to add it — flag back to the maintainer as a scope-expansion judgment call rather than silently adding the migration.
- For the control-cadence path (AC-2a / 2b / 2c): start by reading a real control bundle yaml under \`controls/soc2/\` to see how cadence is currently encoded (or not). The exact field name + format drives the entire backend design. If cadence is in the bundle but NOT in the \`controls\` table, the right play is probably a derived view that JOINs the bundle metadata against the control state — adding a column means migrating every existing control and risking a write-path regression. Surface the design decision back to the maintainer in the decisions log.
- For rolling-window vs fixed-quarter cadence: SOC 2 quarterly reviews are usually rolling-window (90d from last review), but some orgs lock them to calendar quarters (Q1/Q2/Q3/Q4). The control bundle definition should specify; if it doesn't, default to rolling-window and document the assumption in the decisions log so a future control bundle that wants fixed-quarter can be cleanly added.
- Surfaced 2026-05-15 — original ask was "a compliance calendar that notes upcoming audits"; expanded same day to include periodic control reviews ("quarterly firewall reviews, access reviews, etc.") after recognition that those are cross-business scheduled activities, not operational drift. Captured per the continuous-batch spillover-as-slice convention.
