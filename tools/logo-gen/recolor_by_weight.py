#!/usr/bin/env python3
"""Render a multi-weight, multi-color SVG mark to light + dark PNG variants.

Built for candidate-04 v5 (slice 074, PR #180), which expresses its weight
hierarchy via per-stroke `stroke-width` + `stroke` attributes, plus a
separate fourth color slot for the node-dot fills. The light SVG is the
source-of-truth; the dark variant is produced by swapping the four palette
colors token-for-token before rasterizing.

Rationale: cand-04 layers three line weights AND a distinct node-dot tone,
each color-coded to a specific tier of the pastel/sky brand scale. PIL
ImageDraw cannot anti-alias stroked lines cleanly, and Flux is
non-deterministic on multi-hex prompts. Authoring as SVG and rasterizing
through Cairo gives us exact pixel/hex fidelity plus a vector source we
can ship alongside the PNGs.

v5 introduces a four-slot color hierarchy (heavy / medium / light / nodes,
each a distinct hex) where v3/v4 used a three-slot hierarchy that
collapsed nodes into the heavy color. The structural shift was a
deliberate response to the maintainer providing four pastel colors — v5
uses all four meaningfully rather than discarding one. The node-dot color
slot operates as an "anchor tone" visually distinct from the line tones,
reinforcing the "node = stable joint" semantic of the graph metaphor.

v5 also splits the palette per variant: the four user-supplied pastels run
verbatim on the DARK variant (where they all clear SC 1.4.11 against
#0a0a0a); the LIGHT variant uses a sky-scale dark-complement set
(sky-900 / sky-800 / sky-700) mirroring the line hierarchy slot-for-slot,
plus the literal user pastel #517891 for the node dots (the one
user-supplied color that clears SC 1.4.11 on BOTH backgrounds — its reuse
as the node color on both variants establishes a brand-family through-line
between the two renders).

See `docs/design/logo-candidates/candidate-04/notes.md` Iteration history
for v4 → v5 deltas.

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

The dark-variant swap is a per-color mapping defined inline (LIGHT_TO_DARK).
Edit that dict if the candidate's palette evolves.

Cairo dependency: requires libcairo on the system loader path. On macOS this
means `brew install cairo` plus `DYLD_LIBRARY_PATH=/opt/homebrew/opt/cairo/lib`
when invoking python (the venv at `.venv-svg/` is pre-provisioned with
cairosvg + pillow).
"""

from __future__ import annotations

import argparse
import io
import sys
from pathlib import Path

# Palette swap: light-variant tier  →  dark-variant tier
#
# v5 (current, 2026-05-15): four-slot hierarchy (heavy / medium / light /
# nodes) with user-supplied pastels verbatim on dark bg and sky-scale dark
# complements on light bg. The structural shift from three-slot (v4) to
# four-slot was a response to the maintainer providing four pastel colors
# — v5 uses all four meaningfully, splitting node dots into their own
# color slot rather than collapsing them into the heavy line color.
#
# Per-tier contrast on the v5 mapping (measured via tools/logo-gen/contrast.py):
#   Light bg #fafafa:  HEAVY 9.06:1   MEDIUM 7.25:1  LIGHT 5.68:1  NODES 4.53:1
#   Dark  bg #0a0a0a:  HEAVY 12.40:1  MEDIUM 9.23:1  LIGHT 8.51:1  NODES 4.19:1
# All eight clear SC 1.4.11 (3:1). All four light-variant colors also
# clear 4.5:1 (SC 1.4.3). Three of the four dark-variant colors clear
# 4.5:1; NODES sits at 4.19:1 on dark bg — passes SC 1.4.11 cleanly.
#
# NOTE the asymmetric mapping: #517891 (NODES) appears on BOTH sides of
# the swap because it is the one user-supplied pastel that clears SC
# 1.4.11 on both backgrounds. Reusing it as the node-dot tone on both
# variants establishes a brand-family through-line so the two variants
# read as the same mark rather than two unrelated marks.
LIGHT_TO_DARK = {
    "#0c4a6e": "#90D5FF",  # sky-900     →  pastel-sky    (heavy)
    "#075985": "#57B9FF",  # sky-800     →  pastel-blue   (medium)
    "#0369a1": "#77B1D4",  # sky-700     →  muted-blue    (light)
    "#517891": "#517891",  # blue-gray (literal user pastel) — preserved on both variants (nodes)
}

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

    Note: when the mapping is asymmetric (a source hex maps to itself, e.g.
    the v5 NODES color #517891), the no-op pair has no effect on the text
    but is retained in the mapping for documentation and to keep the
    light/dark variant specs aligned slot-for-slot.
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
