# Candidate 01 — Cartographic contour

**Concept:** Concentric topographic contour lines forming a soft asymmetric ridge shape, evoking the "atlas-as-map-of-controls" anchor. The contours read as elevation lines on a topographic map — territory rendered as a system of layers, which mirrors the platform's mapping of one control to many framework satisfactions through SCF anchors.

**Color treatment:**

- Mark color(s): `#4f46e5` indigo (light-bg canonical); `#a5b4fc` indigo-300 (dark-bg variant)
- Background neutrality: dual-variant — ship `mark-1024.png` on light surfaces, `mark-1024-dark.png` on dark

**Contrast measurement (WCAG 2.2):**

- `mark-1024.png` against `#0a0a0a` (dark): 3.15:1 — FAIL
- `mark-1024.png` against `#fafafa` (light): 6.02:1 — PASS
- `mark-1024-dark.png` against `#0a0a0a` (dark): 9.93:1 — PASS
- `mark-1024-dark.png` against `#fafafa` (light): 1.91:1 — FAIL

**Wordmark provenance:** composited (font: Inter Bold, license: SIL OFL, source: https://github.com/rsms/inter releases v4.0). Text reads "atlas" below the mark.

**AI provenance:**

- Model: flux-1.1-pro (Replicate `black-forest-labs/flux-1.1-pro`)
- Model version: pinned by Replicate (latest as of 2026-05-15)
- Generation timestamp: 2026-05-16T01:13Z
- License of output: Replicate Flux 1.1 Pro output is commercial-use-OK per the model's TOS; Apache-2.0 compatible for redistribution.

**Prompt (verbatim):**

```
Minimalist abstract logo mark. Topographic contour lines: 6-8 concentric closed curves of varying thickness arranged like elevation rings on a topographic map, forming a soft asymmetric organic shape that suggests a ridge or hill. The contours are evenly spaced thin lines. Indigo color #4f46e5 lines on pure white background. Flat 2D vector style, no shading. Centered, generous whitespace. No anchor, no nautical imagery, no shield, no padlock. No text, no letters, no words.
```

**Strengths:** Unmistakably cartographic; directly evokes the "atlas" name. The fingerprint-like ridge geometry is distinctive and uncommon in GRC tooling. Indigo aligns with existing mockup palette (`#6366f1`).
**Weaknesses:** The shape is somewhat organic / fingerprinty rather than geometric, which may not telegraph "infrastructure platform" at first glance. Raster only — no clean vector trace (`mark.svg` not provided).
