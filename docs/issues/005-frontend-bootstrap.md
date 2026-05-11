# 005 — Frontend bootstrap (Next.js shell + auth shell + SCF browsing page)

**Cluster:** Spine
**Estimate:** 2d
**Type:** AFK

## Narrative

Scaffold the Next.js 15 App Router project under `web/` with shadcn/ui + Tailwind 4 + TanStack Query. Wire the OIDC sign-in flow (development mode using a local user; full OIDC routes from slice 034). Land the application shell — top bar, sidebar nav (per dashboard mockup), session context. Build one real page (the SCF anchor browser) that calls slice 008's UCF query API and renders the SCF anchor list with framework requirement mappings. This is a tracer bullet for the frontend: real data on day one, not stub.

## Acceptance criteria

- [ ] AC-1: `cd web && npm run dev` starts; visiting `http://localhost:3000` renders the app shell
- [ ] AC-2: Sign-in page accepts a local user; session establishes; user lands on the dashboard route
- [ ] AC-3: The SCF browser page (`/catalog/scf`) fetches from `GET /v1/anchors` via TanStack Query and renders the anchor list
- [ ] AC-4: Clicking an anchor shows the framework requirements that map to it (via `GET /v1/anchors/:id/requirements`)
- [ ] AC-5: shadcn/ui components used for primitives (Button, Card, Table, NavigationMenu); no inline custom CSS
- [ ] AC-6: `npm run build` produces a production bundle without errors

## Constitutional invariants honored

- **Working norms:** mockups in `Plans/mockups/` are reference, not production code — this slice transitions to shadcn/ui React per CLAUDE.md tech-stack lock

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Next.js + shadcn/ui + Tailwind 4 + TanStack Query locked
- `Plans/mockups/dashboard.html` — visual reference for top bar + sidebar nav
- `CLAUDE.md` — "When code begins" step 5

## Dependencies

- #001, #008, #034

## Anti-criteria (P0)

- Does NOT proceed without a real API to call (no stubbed data on this slice)
- Does NOT re-create the HTML mockups verbatim — port the design language but use shadcn/ui primitives
- Does NOT add state-management beyond TanStack Query and React's built-ins in this slice

## Skill mix (3–5)

- Next.js 15 App Router
- shadcn/ui + Tailwind 4
- TanStack Query
- TypeScript strict mode
- OIDC client (dev mode shim)
