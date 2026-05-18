# 148 — Calendar page fails to load events despite slice 094 merge

**Cluster:** Backend / Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0 Unraid deployment:

> "Calendar I see 'Failed to load calendar events. Refresh to try again.'"

Slice 094 (Compliance calendar — `merged` 2026-05-16, PR gh#218) shipped the frontend page (`web/app/(authed)/calendar/page.tsx`) but the backend aggregation endpoints (`GET /v1/calendar` for the page data + `GET /v1/calendar.ics` for ICS subscriptions) appear to not have shipped — operator sees the page chrome but no events render.

**What this slice ships:**

- Audit slice 094's actual shipped state: did the backend endpoints land? If yes, why do they fail? If no, build them per the slice 094 spec.
- Implement `GET /v1/calendar` aggregating events from `audit_periods`, `exceptions`, `policies` (next review dates), `controls` (with cadence). Per slice 094 AC-1 through AC-7.
- Implement `GET /v1/calendar.ics` for subscription URL (RFC 5545 ICS format).
- Frontend page binds to working endpoint; empty-install renders empty-state UI (not "Failed to load").

## Acceptance criteria

- [ ] AC-1: Audit slice 094's shipped backend code; document gap in decisions log.
- [ ] AC-2: Implement `GET /v1/calendar?from=<date>&to=<date>` returning JSON event list aggregated across 4 source types.
- [ ] AC-3: Implement `GET /v1/calendar.ics?token=<one-shot>` returning RFC 5545 ICS format (slice 094 AC-7 calendar-subscription pattern).
- [ ] AC-4: Empty-install integration test: GET /v1/calendar returns `{events: []}` with 200, not 500.
- [ ] AC-5: Frontend "Failed to load" branch only fires on actual network error; empty list renders empty-state UI.
- [ ] AC-6: Tenant isolation integration test: events from Tenant A do not surface in Tenant B's calendar.
- [ ] AC-7: ICS subscription auth: token-based (one-shot bearer in URL) — accepted-risk per slice 094 design; document in decisions log.
- [ ] AC-8: Playwright e2e asserts calendar page loads + renders events on seeded install.
- [ ] AC-9: CHANGELOG entry: "Calendar backend aggregation endpoint shipped (#148; slice 094 follow-on)".

## Dependencies

- **#094** Compliance calendar (merged) — frontend shipped; backend gap.
- **#028** Audit period freezing (merged) — audit_periods source.
- **#022** Policy library (merged) — policies source.
- **#021** Exceptions (merged) — exceptions source.

## Anti-criteria (P0 — block merge)

- **P0-CAL-1** Empty install MUST return 200 with empty array, NOT 500 or "Failed to load".
- **P0-CAL-2** Tenant isolation enforced via RLS on every source-table query.
- **P0-CAL-3** ICS token surface inherits slice 094 design (one-shot, accepted-risk).
- **P0-CAL-4** NO scope creep into calendar UI redesign.

## Notes for the implementing agent

Operator hits this on every Unraid deployment. Triage HIGH for the next release. Slice 094's spec is the source of truth — engineer at pickup re-reads it to confirm what should have shipped vs what did.

Provenance: filed 2026-05-18 from operator v1.10.0 report.
