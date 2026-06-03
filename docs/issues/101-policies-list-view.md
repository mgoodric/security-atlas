# 101 — /policies list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Implementation slice for `Plans/mockups/policies.html`. Today `/policies` 404s (audit F-4).

Two endpoints feed this list: `GET /v1/policies` (the policy library) and per-row `GET /v1/policies/{id}/ack-rate` (acknowledgment progress). The per-row fan-out needs care — if a tenant has 50+ policies, that's 50+ acks-rate calls per page load. Prefer extending the list endpoint with `?include=ack_rate` rather than client-side fan-out.

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/policies/page.tsx` server component renders `GET /v1/policies` as a table.
- [ ] AC-2: Columns per design doc §7: `title`, `version`, `status`, `owner_role`, `published_at`, ack progress (numerator/denominator + progress bar), `updated_at`.
- [ ] AC-3: Ack rate sourced via `GET /v1/policies?include=ack_rate` extension. If that extension doesn't exist, file as a backend follow-on slice rather than client-side fan-out (P0-A2). Document the chosen path in the decisions log.
- [ ] AC-4: Horizontal pill filter row: status (draft / published / retired) + owner role + framework.
- [ ] AC-5: Empty state per §2: "No policies published yet" + `Scaffold five foundational policies` primary CTA (true zero-state with a scaffold-wizard pathway).
- [ ] AC-6: Loading skeleton per §3 (3 shimmer rows).
- [ ] AC-7: Progress bars use shadcn/ui `<Progress>` primitive with semantic ARIA labels ("47 of 52 acknowledged · 90%").
- [ ] AC-8: Row click navigates to a per-policy detail page (placeholder).
- [ ] AC-9: Vitest unit tests for ack-rate formatting + progress-bar color (green ≥95%, amber 70-94%, red <70%).
- [ ] AC-10: Playwright spec `web/e2e/policies-list.spec.ts`.

## Constitutional invariants honored

- **Invariant 6:** tenant isolation via BFF.
- **AI-assist boundary:** pure render of ack-rate numbers; no auto-narration ("attestation lagging in Engineering").

## Canvas references

- `Plans/mockups/policies.html`
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §2, §3, §7, §8
- `internal/api/policies/handlers.go` (`policyWire`)
- `internal/api/policyacks/handlers.go` (`rateResponse`)
- Slice 098 (shared list-shell)

## Dependencies

- **093** — merged
- **098** — RECOMMENDED to land first (shared list-shell)
- **022** (policy library) — merged
- **023** (policy acknowledgment) — merged

## Anti-criteria (P0)

- **P0-A1:** Does NOT auto-narrate ack-rate trends.
- **P0-A2:** Does NOT do client-side per-row fan-out to `GET /v1/policies/{id}/ack-rate` — extend the list endpoint or file a backend slice.
- **P0-A3:** Does NOT invent columns; `policyWire` + `rateResponse` are authoritative.
- **P0-A4:** Does NOT bundle a policy-create UI — `Scaffold five foundational policies` CTA links to whichever existing scaffold flow exists (slice 022 likely has one) or links to a placeholder.
- **P0-A5:** Does NOT use vendor-prefixed tokens.

## Skill mix

- Next.js + TanStack Query list-view (shell from slice 098)
- Wire-shape binding + accessible progress-bar rendering
- Per-row backend extension judgment (extend vs. fan-out)

## Notes

- The progress-bar color thresholds (95%/70%) are derived from the SOC 2 CC1.4 norm where >90% acknowledgment is "compliant." Treat as defaults; capture in decisions log so the maintainer can tune post-deploy.
- If `GET /v1/policies?include=ack_rate` requires a backend extension, the spillover slice should ship before this one's PR opens (or this PR opens with a "WAITING ON backend" note).
