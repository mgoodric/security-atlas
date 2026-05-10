# 041 — Control detail view + UCF mini-viz + STRM coverage table

**Cluster:** Frontend views
**Estimate:** 3d
**Type:** AFK

## Narrative

Build the control detail view per `Plans/mockups/control.html`. The page renders: control metadata, the SCF anchor pill, the UCF mini-viz (SVG graph showing control → SCF anchor → framework requirements with STRM types and strengths), the coverage-by-framework table, the evidence stream (recent records), freshness clock, effective scope per framework, linked policies, linked risks, audit log. Three navigation directions from this view: into the evidence stream, into the mappings inspector, into linked policies/risks. The slice delivers value because the UCF graph claim becomes tactile — users see exactly how one control satisfies many framework requirements.

## Acceptance criteria

- [ ] AC-1: `/controls/:id` route renders the full detail layout matching the mockup
- [ ] AC-2: Coverage-by-framework table reads from `/v1/controls/:id/coverage` (slice 008); STRM types + strengths visible per row
- [ ] AC-3: UCF mini-viz SVG renders the control → SCF anchor → framework requirements graph
- [ ] AC-4: Evidence stream paginates last 30 days from `/v1/evidence?control_id=...`
- [ ] AC-5: Freshness clock binds to slice 016 `valid_until`
- [ ] AC-6: Effective scope panel calls `/v1/controls/:id/effective-scope?framework=...` per framework
- [ ] AC-7: Out-of-scope framework requirements rendered as dashed/greyed rows (matching mockup)

## Constitutional invariants honored

- **Invariant 1 (one control, N satisfactions):** rendered visually in the UCF mini-viz
- **Invariant 5 (FrameworkScope intersection):** out-of-scope rows visibly distinct
- **Working norms:** uses shadcn/ui primitives

## Canvas references

- `Plans/mockups/control.html`
- `Plans/UCF_GRAPH_MODEL.md` §3 (worked example)

## Dependencies

- #005, #008, #012

## Anti-criteria (P0)

- Does NOT model framework-to-framework relationships in the viz (always via SCF anchor)
- Does NOT hide out-of-scope requirements — show them dashed/greyed
- Does NOT render fake mappings

## Skill mix (3–5)

- Next.js + shadcn/ui
- SVG-based graph viz (inline, not a heavy library)
- TanStack Query data composition
- Postgres-aware coverage math
- Visual design (port from mockup)
