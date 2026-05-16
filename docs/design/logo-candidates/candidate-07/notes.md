# Candidate 07 — Wordmark with accent underline

**Concept:** Inter Bold wordmark spelling out the full project name "security-atlas" in slate-700, underscored with a thin indigo accent line. The accent line is the only non-typographic element and acts as a subtle brand-mark hook — viewers register the underline before they consciously read the word. Slate + indigo aligns with the existing shadcn mockup palette (`Plans/mockups/*.html`).

**Color treatment:**

- Mark color(s): wordmark `#334155` slate-700 + accent `#6366f1` indigo (light-bg canonical); wordmark `#e2e8f0` slate-200 + accent `#a5b4fc` indigo-300 (dark-bg variant)
- Background neutrality: dual-variant

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 1.91:1 — FAIL (dominant color is wordmark slate)
- `mark-1024.png` against `#fafafa` (light): 9.92:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 16.06:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.18:1 — FAIL

**Wordmark provenance:** composited (font: Inter Bold 700, license: SIL OFL, source: https://github.com/rsms/inter v4.0). Text reads "security-atlas".

**AI provenance:**

- Model: none — pure composit (PIL + Inter Bold TTF + accent rectangle)
- Model version: n/a
- Generation timestamp: 2026-05-16T01:18Z
- License of output: Inter SIL OFL + mechanical PIL composit; Apache-2.0 compatible without restriction.

**Prompt (verbatim):**

```
(no image-model prompt — pure typographic composit with accent rectangle)
```

**Strengths:** Conveys the project name in full — useful when the brand is unknown. Vector-native (`mark.svg` ships). Color palette already matches the deployed mockups (indigo `#6366f1` is canonical there). Accent underline is removable for monochrome contexts (favicon, watermark) without losing identity.
**Weaknesses:** Wide horizontal aspect (the word "security-atlas" with a 130px font barely fits the 1024 canvas square) — doesn't work as a square favicon. Pure wordmark = no symbol. Would need a paired symbol-mark for icon contexts. Same generic-SaaS-wordmark risk as candidate 06.
