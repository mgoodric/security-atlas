#!/usr/bin/env python3
"""Render a multi-color SVG mark to light + dark PNG variants.

Built for candidate-04 v6 (slice 074, PR #180), which expresses its
visual hierarchy via per-line color positioned along a temperature
gradient (warm pinks at the apex, cream/mint at mid-A, sky/blue at the
foundation). The light SVG is the source-of-truth; the dark variant is
produced by swapping the eight palette colors token-for-token before
rasterizing.

Rationale: candidate-04 has gone through five color iterations:

  v3 / v4 — 3-slot indigo hierarchy (heavy / medium / light) keyed to
            stroke-width tiers. Nodes shared the heavy color slot.
  v5      — 4-slot pastel/sky hierarchy (heavy / medium / light /
            nodes). Same stroke-width tiers as v4; nodes split into
            their own color slot. Split palette per variant:
            user-supplied pastels verbatim on dark, sky-scale dark
            complements on light.
  v6      — 8-slot wide-spectrum hierarchy (8 line colors + 1 node
            color). NEW: color is no longer keyed to stroke-width.
            v6 uses uniform stroke (sw=6) across all 16 lines, and
            color is assigned by line MIDPOINT y-coordinate
            (positional temperature gradient: warm top → cool bottom).
            Sister L/R lines share a color to preserve symmetry. This
            is a structural shift — the tool name "recolor_by_weight"
            is now historical; v6 recolors by position, not by weight.

The token-swap rendering pipeline is unchanged across all versions:
read source SVG (light variant), apply LIGHT_TO_DARK mapping to
produce dark variant text, rasterize both at 1024 and 512 px through
cairosvg, optimize through PIL.

v6 palette: the maintainer asked for a wider-ranging palette
(#f2a2b3 #f9c3c3 #f7d4c0 #f9e6c1 #d1e7e0 #a0d1e8 #7ab8e1 #4b8db5).
Seven of the eight clear SC 1.4.11 only on a DARK background; on light
they sit between 1.18:1 and 2.06:1. Only #4b8db5 clears at 3.48:1 on
light. The v5 split-palette pattern is therefore preserved: pastels
verbatim on the dark variant; Tailwind 700-800 dark complements
(rose-800, pink-700, orange-800, amber-700, emerald-800, sky-800,
sky-700, blue-800) on the light variant, mirroring the temperature
gradient slot-for-slot.

v6 node dots: uniform #4b8db5 (dark) / #1e40af (light) — the deepest
tone in each variant's palette. This DEPARTS from v5's "shared #517891
on both variants" brand-through-line, because no color in the new
8-pastel palette clears SC 1.4.11 on both backgrounds. The tradeoff is
documented in notes.md Iteration history v6.

See `docs/design/logo-candidates/candidate-04/notes.md` Iteration
history for v5 → v6 deltas.

Usage:
    DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib \
        python3 tools/logo-gen/recolor_by_weight.py \
            --svg docs/design/logo-candidates/candidate-04/mark.svg \
            --out-dir docs/design/logo-candidates/candidate-04

Outputs (overwritten in place):
    mark-1024.png        light-bg canonical
    mark-512.png         light-bg web-optimized
    mark-1024-dark.png   dark-bg canonical
    mark-512-dark.png    dark-bg web-optimized

The dark-variant swap is a per-color mapping defined inline
(LIGHT_TO_DARK). Edit that dict if the candidate's palette evolves.

Cairo dependency: requires libcairo on the system loader path. On macOS
this means `brew install cairo` plus
`DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib` when invoking python
(the venv at `.venv-svg/` is pre-provisioned with cairosvg + pillow).
"""

from __future__ import annotations

import argparse
import io
import sys
from pathlib import Path

# Palette swap: light-variant tier  →  dark-variant tier
#
# v6 (current, 2026-05-15): 8-slot wide-spectrum temperature gradient
# (8 line colors + 1 node color = 9 hex pairs total). Color is assigned
# by line midpoint y-coordinate (positional gradient: warm top → cool
# bottom), not by stroke-width as in v3-v5. All 16 lines use uniform
# sw=6. Sister L/R lines share a color so the gradient reads as
# left-right symmetric rather than confetti.
#
# Mapping pairs each light-variant dark complement to its dark-variant
# maintainer pastel, in matching color families:
#
# Per-tier contrast on the v6 mapping (measured via tools/logo-gen/contrast.py):
#   Light bg #fafafa:
#     rose-800    7.68:1   pink-700    5.78:1   orange-800   7.00:1
#     amber-700   6.56:1   emerald-800 7.36:1   sky-800      7.25:1
#     sky-700     5.68:1   blue-800    8.36:1
#   Dark bg #0a0a0a:
#     pink        9.99:1   pale pink  12.84:1   peach       14.27:1
#     cream      16.14:1   mint       15.29:1   pale sky    12.05:1
#     med sky     9.21:1   deep blue   5.45:1
# All 16 clear SC 1.4.11 (3:1). All 16 also clear 4.5:1 (SC 1.4.3) —
# v6 is fully 4.5:1-compliant on both variants, an improvement over v5
# (whose NODES color sat at 4.19:1 on dark bg).
#
# Node-dot color is uniform per variant: #1e40af (light) / #4b8db5
# (dark) — the deepest blue in each palette. Anchors all 14 nodes
# consistently. v5's "shared #517891 on both variants" brand-through-
# line is NOT preserved in v6, because no color in the v6 palette
# clears SC 1.4.11 on both backgrounds.
LIGHT_TO_DARK = {
    # 8 line colors (positional gradient, warm top → cool bottom):
    "#9f1239": "#f2a2b3",  # rose-800     →  pink           (upper: apex roof)
    "#be185d": "#f9c3c3",  # pink-700     →  pale pink      (upper: apex tangent)
    "#9a3412": "#f7d4c0",  # orange-800   →  peach          (upper: outrigger braces)
    "#854d0e": "#f9e6c1",  # amber-700    →  cream          (middle: A legs)
    "#065f46": "#d1e7e0",  # emerald-800  →  mint           (middle: crossbars)
    "#075985": "#a0d1e8",  # sky-800      →  pale sky       (middle: inner diagonals)
    "#0369a1": "#7ab8e1",  # sky-700      →  medium sky     (lower: CROSS_MID→base)
    "#1e40af": "#4b8db5",  # blue-800     →  deep blue      (lower: base + braces + ALL NODES)
}

# v5 (prior, retained for traceability — 4-slot pastel/sky hierarchy
# keyed to stroke-width tiers, with a separate NODES slot. v6 abandoned
# the weight-keyed pattern in favor of 8-color positional gradient).
# LIGHT_TO_DARK_V5 = {
#     "#0c4a6e": "#90D5FF",  # sky-900     →  pastel-sky    (heavy)
#     "#075985": "#57B9FF",  # sky-800     →  pastel-blue   (medium)
#     "#0369a1": "#77B1D4",  # sky-700     →  muted-blue    (light)
#     "#517891": "#517891",  # blue-gray (literal user pastel) — preserved on both variants (nodes)
# }

# v4 (prior, retained for traceability — three-slot indigo hierarchy with
# nodes sharing the heavy color slot; replaced when maintainer asked for
# the pastel palette and the four-slot structure emerged as the response):
# LIGHT_TO_DARK_V4 = {
#     "#1e1b4b": "#e0e7ff",  # indigo-950  →  indigo-100   (heavy / nodes)
#     "#4f46e5": "#a5b4fc",  # indigo-600  →  indigo-300   (medium)
#     "#6366f1": "#818cf8",  # indigo-500  →  indigo-400   (light / detail)
# }

# v3 (prior, retained for traceability — three indigo tiers clustered near
# the dark/light extremes; tiers were hard to distinguish at a glance):
# LIGHT_TO_DARK_V3 = {
#     "#312e81": "#c7d2fe",  # indigo-900  →  indigo-200
#     "#4338ca": "#a5b4fc",  # indigo-700  →  indigo-300
#     "#4f46e5": "#818cf8",  # indigo-600  →  indigo-400
# }


def swap_palette(svg_text: str, mapping: dict[str, str]) -> str:
    """Token-swap hex colors in SVG source.

    Case-insensitive on the SOURCE hex, but emits the mapping value verbatim.
    Operates on raw text — fine for our hand-authored SVG where colors only
    appear in `stroke="#..."` / `fill="#..."` attributes.

    Note: when the mapping is asymmetric (a source hex maps to itself, as
    was the case for v5 NODES color #517891), the no-op pair has no effect
    on the text but is retained in the mapping for documentation. v6 has no
    such no-op pairs — all 8 mappings are distinct on both sides.
    """
    out = svg_text
    for src, dst in mapping.items():
        out = out.replace(src, dst)
        out = out.replace(src.upper(), dst)
    return out


def render(svg_bytes: bytes, size: int) -> bytes:
    """Rasterize an SVG byte-string to a PNG of `size`x`size` pixels."""
    import cairosvg  # local import: cairo dlopen happens here

    buf = io.BytesIO()
    cairosvg.svg2png(
        bytestring=svg_bytes,
        write_to=buf,
        output_width=size,
        output_height=size,
    )
    return buf.getvalue()


def optimize_png(png_bytes: bytes) -> bytes:
    """Re-encode a PNG through PIL with optimize=True to shrink file size."""
    from PIL import Image

    img = Image.open(io.BytesIO(png_bytes)).convert("RGBA")
    out = io.BytesIO()
    img.save(out, "PNG", optimize=True)
    return out.getvalue()


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--svg", type=Path, required=True, help="source light-variant SVG")
    p.add_argument(
        "--out-dir",
        type=Path,
        required=True,
        help="output directory (writes 4 PNGs: 1024/512 × light/dark)",
    )
    args = p.parse_args()

    if not args.svg.exists():
        print(f"error: svg not found: {args.svg}", file=sys.stderr)
        return 2
    args.out_dir.mkdir(parents=True, exist_ok=True)

    light_svg = args.svg.read_text(encoding="utf-8")
    dark_svg = swap_palette(light_svg, LIGHT_TO_DARK)

    targets = [
        (light_svg.encode("utf-8"), 1024, args.out_dir / "mark-1024.png"),
        (light_svg.encode("utf-8"), 512, args.out_dir / "mark-512.png"),
        (dark_svg.encode("utf-8"), 1024, args.out_dir / "mark-1024-dark.png"),
        (dark_svg.encode("utf-8"), 512, args.out_dir / "mark-512-dark.png"),
    ]

    for svg_bytes, size, out_path in targets:
        raw = render(svg_bytes, size)
        optimized = optimize_png(raw)
        out_path.write_bytes(optimized)
        kb = len(optimized) / 1024
        print(f"wrote {out_path.name}  {size}x{size}  {kb:.1f} KB")

    return 0


if __name__ == "__main__":
    sys.exit(main())
