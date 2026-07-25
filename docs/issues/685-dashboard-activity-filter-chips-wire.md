# 685 — Wire dashboard "Recent activity" filter chips to a real kind filter

**Cluster:** Dashboard
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (chip taxonomy / endpoint filter shape)
**Status:** `not-ready` — depends on widening the dashboard `/v1/activity` endpoint beyond the evidence branch (no such slice on `main` yet).

## Narrative

Slice 667 **removed** the inert All / Evidence / Controls / Approvals chips from the
dashboard "Recent activity" card. The chips had no handler and bound to nothing: the
dashboard `/v1/activity` endpoint (`internal/api/dashboard/handler.go::Activity` +
`store.go::ActivityFeed`) reads **only** the evidence branch (`ListEvidenceActivity`) and
takes only `cursor` + `limit` — no `kind`/`source` filter param. With only evidence events
available, "Controls" and "Approvals" chips would always render an empty feed.

This slice re-introduces the chips **wired to real filtering**, once the endpoint can serve
more than the evidence branch.

## Threat model

Read-only view; tenant-scoped via RLS. A `?kind=` filter must not let a caller widen beyond
their tenant's evidence (RLS already enforces tenant scope; the filter is a presentation
narrowing within already-authorized rows). No new mutation surface.

## Acceptance criteria

- [ ] **AC-1.** The dashboard activity endpoint accepts a kind/source filter param and the
      store reads beyond the evidence branch (controls, approvals, etc.).
- [ ] **AC-2.** The dashboard "Recent activity" card renders filter chips that each filter
      the feed and reflect active state (aria-pressed/aria-current + visual).
- [ ] **AC-3.** The chip kind taxonomy is **consistent with slice 669's** model (the
      `decision`/`read` deny-list + business-event kinds) so the dashboard card and the
      standalone `/activity` view share one vocabulary.
- [ ] **AC-4.** A Playwright e2e asserts each chip narrows the feed and the active chip
      reflects state; a contract/unit test pins the endpoint's filter behavior.
- [ ] **AC-5.** JUDGMENT (decisions log): record the chip taxonomy and the
      endpoint-filter shape chosen.

## Anti-criteria

- Does NOT re-introduce inert chips (slice 667's anti-criterion stands — wire or omit).
- Does NOT diverge from slice 669's kind taxonomy.

## Dependencies

- A backend slice widening the dashboard `/v1/activity` source beyond the evidence branch
  and adding the filter param (`internal/api/dashboard/handler.go` + `store.go`). Not on
  `main` yet — this slice is `not-ready` until that lands.
- Slice 669 (activity-feed kind taxonomy) — merged; this slice reuses its model.

## Notes

Surfaced during slice 667, captured as follow-up per continuous-batch policy. See the
slice 667 decisions log (`docs/audit-log/667-dashboard-activity-chips-decisions.md`) D1 for
the hide-now / wire-later rationale.
