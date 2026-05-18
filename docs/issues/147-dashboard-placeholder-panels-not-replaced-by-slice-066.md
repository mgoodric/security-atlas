# 147 — Dashboard panels still render "endpoint does not exist" placeholders despite slice 066 merge

**Cluster:** Frontend / Backend
**Estimate:** 0.5-1d (diagnose-heavy)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0 Unraid deployment.

Dashboard renders **two** "endpoint does not exist on main yet" placeholders even though slice 066 (Dashboard backend read endpoints — `merged` 2026-05-14, PR gh#109) was supposed to ship those endpoints:

> "On dashboard for instance I see 'This panel binds to GET /v1/frameworks/posture, which does not exist on main yet'"
> "Under recent activity I see 'This panel binds to GET /v1/activity, which does not exist on main yet'"

**Hypothesis:** slice 066 shipped frontend SHELL updates (panel components with TanStack Query wiring) but the backend endpoints `/v1/frameworks/posture` and `/v1/activity` either (a) didn't ship, (b) ship under different paths, OR (c) ship but return errors the frontend treats as "doesn't exist".

**What this slice ships:**

- Diagnose where slice 066 actually landed: read `internal/api/dashboard/` package + grep `httpserver.go` for the endpoint registrations. Verify the endpoints exist + work against a fresh install.
- If endpoints are missing → ship them (the slice 066 doc describes the intended shape).
- If endpoints exist under different paths → update the frontend TanStack Query keys to hit them.
- If endpoints exist but error on empty → make them return empty-result-set gracefully (overlaps with slice 150).
- Remove the "endpoint does not exist" placeholder copy from the frontend panel components; replace with actual data binding.

## Acceptance criteria

- [ ] AC-1: Confirm via code grep whether `/v1/frameworks/posture` is registered in httpserver.go.
- [ ] AC-2: Confirm via code grep whether `/v1/activity` is registered.
- [ ] AC-3: If missing, build the endpoints per slice 066 spec (composite per-framework posture; activity feed read model).
- [ ] AC-4: Frontend dashboard panel components stop rendering placeholder copy; bind to actual endpoint responses.
- [ ] AC-5: Empty-install integration test: dashboard loads cleanly with 0 frameworks + 0 activity events — no placeholders, no 500s.
- [ ] AC-6: Playwright e2e asserts dashboard renders without placeholder copy after slice 066 + this slice's fixes land.
- [ ] AC-7: Decisions log records what slice 066 actually delivered vs what its spec described (audit trail of the discrepancy).
- [ ] AC-8: CHANGELOG entry: "Dashboard 'frameworks posture' + 'recent activity' panels now render real data (#147; slice 066 follow-on)".

## Dependencies

- **#066** Dashboard backend endpoints (merged) — verify what it actually shipped.
- **#040** Dashboard UI shell (merged) — the panel components live here.
- **#150** Empty-set robustness audit — if endpoints exist but 500 on empty, depends on the same fix shape.

## Anti-criteria (P0 — block merge)

- **P0-DASH-1** Frontend placeholder copy MUST be removed; no "endpoint does not exist on main yet" string anywhere in the dashboard code path.
- **P0-DASH-2** Empty-install path renders empty-state UI (no rows, friendly message), NOT 500 or placeholder.
- **P0-DASH-3** NO scope creep into other dashboard panels (drift, metrics handled by other slices).

## Notes for the implementing agent

Surfaced from operator v1.10.0 report. Slice 066 was marked merged but the operator-facing behavior says otherwise — root cause likely either (a) slice 066 shipped backend stubs that the frontend wasn't updated to consume, OR (b) slice 066's frontend AC list omitted the placeholder-removal step. Either way, this slice closes the loop.

Provenance: filed 2026-05-18 from operator v1.10.0 report during the comprehensive front-end-to-back-end gap audit.
