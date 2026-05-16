# Candidate 06 — Typographic SA monogram

**Concept:** Pure-typographic monogram. Two interlocking Inter Black letterforms set tight (negative letter-spacing) so the counterforms of "S" and "A" create visual tension. The wordmark IS the mark — strongest hand-shake with the maintainer's "docs lean text-heavy" reality (slice 074 narrative). Mark and wordmark are the same artifact.

**Color treatment:**

- Mark color(s): `#0f172a` near-black slate-900 (light-bg canonical); `#f8fafc` slate-50 (dark-bg variant)
- Background neutrality: dual-variant — both ship; pick by surface

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 1.11:1 — FAIL
- `mark-1024.png` against `#fafafa` (light): 17.10:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 18.92:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.00:1 — FAIL

**Wordmark provenance:** composited (font: Inter Black 900, license: SIL OFL, source: https://github.com/rsms/inter v4.0). The mark IS the wordmark — "SA".

**AI provenance:**

- Model: none — pure composit (PIL + Inter Black TTF)
- Model version: n/a
- Generation timestamp: 2026-05-16T01:18Z
- License of output: Inter is SIL OFL; PIL composition is mechanical — no model rights apply. Apache-2.0 compatible without restriction.

**Prompt (verbatim):**

```
(no image-model prompt — pure typographic composit)
```

**Strengths:** Zero ambiguity, zero risk of unintended visual reads (no cross, no padlock, no animal-shape pareidolia). Vector-native (`mark.svg` ships). Strongest contrast of any candidate (17:1 / 18.9:1). Iconographic OS-folder-icon legibility at 16px favicon scale. Easy color theming downstream.
**Weaknesses:** Zero narrative. Says nothing about graph / pipeline / atlas / map — relies entirely on the project name carrying meaning. Many SaaS wordmarks look like this; little differentiation from generic dev tooling.
