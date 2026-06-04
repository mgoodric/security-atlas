# 154 — Settings page audit + parity check against mockup

**Cluster:** Frontend / Quality
**Estimate:** 0.5d (diagnose-heavy)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced 2026-05-18 from the comprehensive front-end-to-back-end gap audit. Settings page (`web/app/(authed)/settings/page.tsx` — slice 103) was not verified against the mockup or operator-tested for parity in the v1.10.0 deployment report. Status: UNKNOWN.

**What this slice ships:**

- Audit `web/app/(authed)/settings/page.tsx` against `Plans/mockups/settings.html`.
- Verify each settings section renders correctly + persists changes via working backend endpoints.
- Identify gaps; file fix slices OR resolve in-place if scope is small.
- Smoke-test on fresh-install Unraid deployment.

**Likely sections to verify:**

- Profile (slice 108 `/v1/me`)
- Notification preferences (slice 108 `/v1/me/preferences`)
- Active sessions (slice 108 `/v1/me/sessions`)
- Tenant rename (slice 144, pending) — placeholder check
- Theme / appearance (if applicable)
- API key management (slice 034 `/v1/admin/credentials`)
- SSO config (slice 035 `/v1/admin/sso` — admin-gated subsection)

## Acceptance criteria

- [ ] AC-1: Read `Plans/mockups/settings.html` + enumerate expected sections.
- [ ] AC-2: Read `web/app/(authed)/settings/page.tsx` + compare.
- [ ] AC-3: For each section: verify it renders + verify the bound endpoint works on a fresh install.
- [ ] AC-4: Document findings in decisions log `docs/audit-log/154-settings-page-audit-decisions.md`.
- [ ] AC-5: Resolve gaps either in-place (if 1-2 small fixes) OR file follow-up slices.
- [ ] AC-6: Playwright e2e expanded to cover every settings section.
- [ ] AC-7: CHANGELOG entry: "Settings page parity verified against mockup (#154)".

## Dependencies

- **#103** Settings page UI (merged).
- **#108** `/v1/me` profile (merged) — multiple sections bind to it.

## Anti-criteria (P0 — block merge)

- **P0-SET-1** Audit findings recorded in decisions log; not just verbal.
- **P0-SET-2** Any gap that requires >1 hour fix is FILED as a separate slice; this slice doesn't blow up scope.
- **P0-SET-3** NO scope creep into adding new settings sections beyond what the mockup defines.

## Notes for the implementing agent

Diagnose-shaped slice. Engineer at pickup spends most time reading + comparing; minimal coding unless gaps are tiny.

Provenance: filed 2026-05-18 from comprehensive front-end gap audit.
