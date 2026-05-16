# Candidate 09 — Hexagonal control-cell

**Concept:** Seven hexagonal cells in a honeycomb arrangement — six surrounding one. The center cell is filled solid rose; the six surrounding cells are dark slate outlines. The metaphor is direct: the central highlighted hex is "your control", and the surrounding hexes are "all the framework satisfactions that depend on it" — exactly the canvas §3 / §5 thesis (one control, N framework satisfactions). Reads as both an architectural floor plan and a biological cell.

**Color treatment:**

- Mark color(s): center `#e11d48` rose-600 + frame `#0f172a` slate-900 (light-bg canonical); center `#fda4af` rose-300 + frame `#cbd5e1` slate-300 (dark-bg variant)
- Background neutrality: dual-variant

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 1.11:1 — FAIL (dominant detected color is frame slate-900)
- `mark-1024.png` against `#fafafa` (light): 17.10:1 — PASS (frame); rose center is 4.50:1 also-PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 13.33:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.42:1 — FAIL

**Wordmark provenance:** none — mark-only.

**AI provenance:**

- Model: flux-1.1-pro (Replicate `black-forest-labs/flux-1.1-pro`)
- Model version: pinned by Replicate (latest as of 2026-05-15)
- Generation timestamp: 2026-05-16T01:15Z
- License of output: Flux 1.1 Pro commercial-use-OK per TOS; Apache-2.0 compatible. (Direction was originally specified for GPT-Image-1 but rerouted to Flux because OPENAI_API_KEY was not configured; substitution documented in the decisions log.)

**Prompt (verbatim):**

```
Minimalist abstract logo mark. Hexagonal lattice grid of 7 hexagonal cells — one central hex surrounded by 6 hexes — each hex is a clean thin outline. The center hex is filled solid in rose red #e11d48 while the 6 surrounding hexes are filled in slate gray #475569 outline only. The composition has the precision of an architectural floor plan. Pure white background. Flat 2D vector style, no shading, no gradients. Centered, generous whitespace. No text, no letters, no words, no labels.
```

**Strengths:** Strongest "one control + N satisfactions" semantic match in the entire slate. Hexagonal arrangement reads as both technical (lattice mesh, honeycomb) and architectural (cellular floor plan). The single highlighted center cell creates visual focus — eye is drawn to the rose first, then explores the surrounding hexes. Works at favicon scale.
**Weaknesses:** Hexagons have heavy semantic baggage in tech branding (associated with blockchain, crypto, web3, several SaaS platforms). May feel "of-its-era" rather than timeless. Rose-red center is the only red in the entire slate — strong color, but red carries connotations (errors, alerts) that may conflict with brand voice.
