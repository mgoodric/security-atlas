# 040 — Program dashboard view

**Cluster:** Frontend views
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Build the program dashboard view per `Plans/mockups/dashboard.html`. Real data bound via TanStack Query to: framework posture (from slice 008), top risks aging (from slice 020), recent drift (from slice 016), upcoming items (from slice 021 + 022 + 024), evidence freshness summary (from slice 016), and the recent activity feed (from slice 015). Layout uses shadcn/ui primitives — port the design language from the HTML mockup, but render via React components. Time-to-paint under 1 second on cached data. The slice delivers value because the home screen the primary persona opens every morning is real and live.

## Acceptance criteria

- [ ] AC-1: `/dashboard` route renders the full dashboard layout matching the mockup
- [ ] AC-2: Framework tiles bind to real data per framework; trend arrows reflect actual deltas
- [ ] AC-3: Top risks aging table renders from `/v1/risks?treatment=mitigate&sort=residual,age`
- [ ] AC-4: Recent drift panel binds to `/v1/controls/drift?since=7d`
- [ ] AC-5: Upcoming items reads from exception expiration + policy ack + vendor review + audit period
- [ ] AC-6: Activity feed paginates with infinite scroll; backed by NATS-driven event stream archive
- [ ] AC-7: All panels gracefully degrade if any backing API is slow (skeleton loaders); errors surfaced with retry

## Constitutional invariants honored

- **Replacement-grade criterion 7:** dashboard is what the primary persona sees first; quality of first-impression matters
- **Working norms:** mockups are reference, not production — uses shadcn/ui primitives, not raw Tailwind

## Canvas references

- `Plans/mockups/dashboard.html`
- `Plans/canvas/07-metrics.md` §7.1, §7.5

## Dependencies

- #005, #012, #015, #016, #020, #021, #023, #024

## Anti-criteria (P0)

- Does NOT render fake data anywhere (every panel binds to real API)
- Does NOT block the page on a single slow API
- Does NOT copy HTML mockup verbatim — port via shadcn/ui

## Skill mix (3–5)

- Next.js 15 + shadcn/ui
- TanStack Query (suspense + error boundaries)
- Data binding to multiple parallel APIs
- Tailwind 4 layout
- Performance budget enforcement
