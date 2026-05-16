# Candidate 10 — Stylized A from control-graph

**Concept:** A network of ~10 connected nodes whose silhouette traces the shape of the letter A — two diagonal "legs" meeting at a top node, with a horizontal crossbar formed by the middle nodes. The mark is BOTH a graph AND a letter, with neither dominating. Hands-on rendition of the prompt's intent. "atlas" composited below as Inter Bold.

**Color treatment:**

- Mark color(s): `#7c3aed` violet-600 (light-bg canonical); `#c4b5fd` violet-300 (dark-bg variant)
- Background neutrality: dual-variant

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 3.47:1 — FAIL
- `mark-1024.png` against `#fafafa` (light): 5.46:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 10.72:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.77:1 — FAIL

**Wordmark provenance:** composited (font: Inter Bold 700, license: SIL OFL, source: https://github.com/rsms/inter v4.0). Text reads "atlas" below the mark.

**AI provenance:**

- Model: flux-1.1-pro (Replicate `black-forest-labs/flux-1.1-pro`)
- Model version: pinned by Replicate (latest as of 2026-05-15)
- Generation timestamp: 2026-05-16T01:16Z
- License of output: Flux 1.1 Pro commercial-use-OK per TOS; Apache-2.0 compatible. (Direction originally specified for GPT-Image-1 but rerouted to Flux because OPENAI_API_KEY was not configured; substitution documented in the decisions log.)

**Prompt (verbatim):**

```
Minimalist abstract logo mark. A stylized capital letter A formed entirely by abstract graph/network lines: thin straight line segments connect 8-10 small filled circular nodes such that the overall silhouette traces the shape of the letter A — two diagonal legs meeting at a point at the top with a horizontal crossbar. The composition reads as 'A made of a control graph'. Deep violet #7c3aed nodes with thin lines on pure white background. Flat 2D vector style. Centered, generous whitespace. No literal alphabet typography rendered — only the A shape suggested by the network geometry. No text, no words, no labels.
```

**Strengths:** Uniquely double-decodable — reads as both letter and graph. Strong "atlas" symbology (the A) plus strong "control graph" symbology (the nodes) in one mark. Violet differentiates clearly from the existing indigo/teal/emerald palette in the rest of the slate.
**Weaknesses:** The mark's interior is busy — at favicon scale the individual lines blur into a triangle mass. The flux-generated graph has some redundant cross-lines that don't add semantic value (visible as the thin internal X-pattern). Raster only (no SVG).
