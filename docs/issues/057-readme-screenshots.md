# 057 — README screenshots + animated GIFs of core flows

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Embed real screenshots and short animated GIFs of the running app into the public README so first-time visitors get an immediate sense of what security-atlas looks like and what it does. Depends on the frontend views (040–043) being merged because the screenshots need actual UI to capture.

Five visual assets land:

1. **Hero screenshot** — program dashboard view (slice 040). The "what does the dashboard look like?" answer.
2. **Control detail** — slice 041's view, showing the UCF mini-viz and per-framework coverage.
3. **Audit workspace** — slice 042's view, demonstrating the auditor-facing surface.
4. **Board pack preview** — slice 043's view, the v1 binary success test artifact.
5. **One animated GIF** — a 5–15 second loop of a representative flow (e.g., creating a control, then watching evidence land + the control auto-evaluate).

Capture via Playwright headless with a consistent viewport (1440×900). Stored as lossless PNG (≤ 500 KB each) and an optimized GIF (≤ 5 MB). README embeds them between the badge row and the install section.

## Acceptance criteria

- [ ] AC-1: Five visual assets present at `docs/images/`: `hero-dashboard.png`, `control-detail.png`, `audit-workspace.png`, `board-pack-preview.png`, `flow-create-control.gif`.
- [ ] AC-2: All PNG screenshots captured at 1440×900 viewport from a Playwright script committed at `scripts/capture-readme-screenshots.ts`. Re-runnable; deterministic seed data (no PII, no real tenant references).
- [ ] AC-3: GIF generated via `ffmpeg` from a Playwright screen-recording session; resolution 1280×720; ≤ 5 MB; ≤ 15 seconds.
- [ ] AC-4: README updated: hero screenshot directly below the 4-badge row (slice 050 AC-6a); other screenshots integrated into a "Screenshots" section between the value prop and the install section; GIF placed in a "What it looks like in motion" subsection.
- [ ] AC-5: All images use the `<picture>` element with light + dark theme variants — dark theme screenshots saved as `*-dark.png`. README references both via `media="prefers-color-scheme: dark"` and `media="prefers-color-scheme: light"`.
- [ ] AC-6: Captured UI shows seeded demo data only (no real tenant names, no real Matt/maintainer references). Demo data committed at `fixtures/readme-demo/`.
- [ ] AC-7: `scripts/capture-readme-screenshots.ts` is documented in `CONTRIBUTING.md` so a contributor refreshing the screenshots after UI changes knows the workflow.
- [ ] AC-8: CI does NOT block on screenshot freshness (deliberately) — screenshots are version-controlled artifacts updated on demand, not on every PR. A `just refresh-screenshots` target exists for the workflow.

## Constitutional invariants honored

- **Working norms — Style** (CLAUDE.md "No emojis in code, docs, commits") — README copy around the screenshots stays emoji-free; screenshots are visual, not decorative
- **AI-assist boundary** — captured screenshots show real running UI from real code paths; nothing AI-generated or mocked

## Canvas references

- `Plans/canvas/10-roadmap.md §10.1` — v1 binary success test artifact (board pack) shows up directly in the README via the board-pack-preview screenshot
- `Plans/mockups/` — original HTML mockups served as the design reference; the screenshots demonstrate the actual built UI matches the design intent

## Dependencies

- **040** (program dashboard view) — hero screenshot needs this view live
- **041** (control detail view + UCF mini-viz) — control-detail screenshot needs this view live
- **042** (audit workspace view) — audit-workspace screenshot needs this view live
- **043** (board pack preview view) — board-pack-preview screenshot needs this view live

## Anti-criteria (P0)

- Do NOT use mocked-up images or design-tool exports — must be screenshots of the actually-running app.
- Do NOT embed seeded data that contains the maintainer's name or real tenant references (cross-references slice 050 sanitization rules).
- Do NOT exceed 5 MB total weight for all images combined — README load time matters.
- Do NOT add screenshot freshness gates to CI — these are intentionally on-demand artifacts; gating on freshness creates merge friction without commensurate value.
- Do NOT include screenshots that show ephemeral overlays (tooltips mid-fade, toast notifications, loading spinners) — clean idle states only.

## Skill mix (3–5)

- `engineering-advanced-skills:full-page-screenshot` (Playwright headless capture pipeline)
- `engineering-advanced-skills:browser-automation` (deterministic seeded-data setup before capture)
- `simplify` (the README integration is content-only — keep the surrounding copy tight)
- `security-review` (verify no secrets / PII / real tenant data leaks into the captured frames)
