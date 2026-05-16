# Candidate 08 — security · atlas with anchor-graph glyph

**Concept:** Two-word treatment "security · atlas" where the middle separator is replaced with a small 3-node anchor-graph glyph (triangle of connected nodes in cyan). The glyph IS a miniature control graph — the project's core thesis compressed into a single 70-pixel mark sitting between the two halves of the name. Reads as "security [graph that anchors] atlas".

**Color treatment:**

- Mark color(s): text `#0f172a` slate-900 + glyph `#0891b2` cyan-600 (light-bg canonical); text `#e2e8f0` slate-200 + glyph `#22d3ee` cyan-400 (dark-bg variant)
- Background neutrality: dual-variant

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 1.11:1 — FAIL (dominant color is text slate-900)
- `mark-1024.png` against `#fafafa` (light): 17.10:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 16.06:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.18:1 — FAIL

**Wordmark provenance:** composited (font: Inter Bold 700, license: SIL OFL, source: https://github.com/rsms/inter v4.0). Text reads "security" + anchor-graph glyph + "atlas".

**AI provenance:**

- Model: none — pure composit (PIL + Inter Bold + geometric primitives for the 3-node glyph)
- Model version: n/a
- Generation timestamp: 2026-05-16T01:18Z
- License of output: Inter SIL OFL + mechanical PIL composit; Apache-2.0 compatible without restriction.

**Prompt (verbatim):**

```
(no image-model prompt — pure composit. Glyph is 3 cyan circles connected by 3 cyan lines forming a triangle; geometry from compose_logo.py)
```

**Strengths:** Conveys both the project name AND the control-graph thesis in one mark. The glyph at the center IS a graph — semantically perfect. The glyph alone (without text) works as a 16px favicon. Vector-native (`mark.svg` ships). Cyan accent differentiates from the indigo-leaning mockups.
**Weaknesses:** Same horizontal-aspect issue as candidate 07 — doesn't fit a square favicon as a whole. The glyph at small sizes (16-32px) may read as a generic triangle rather than a graph. The two-word treatment with the middle dot-replacement is a typographic trick some viewers will find precious.
