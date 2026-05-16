#!/usr/bin/env python3
"""Render a multi-weight, multi-color SVG mark to light + dark PNG variants.

Built for candidate-04 v4 (slice 074, PR #180), which expresses its weight
hierarchy via per-stroke `stroke-width` + `stroke` attributes. The light SVG
is the source-of-truth; the dark variant is produced by swapping the indigo
brand-palette colors token-for-token before rasterizing.

Rationale: cand-04 layers three line weights, each color-coded to a
specific tier of the indigo brand scale. PIL ImageDraw cannot anti-alias
stroked lines cleanly, and Flux is non-deterministic on multi-hex prompts.
Authoring as SVG and rasterizing through Cairo gives us exact pixel/hex
fidelity plus a vector source we can ship alongside the PNGs.

v4 widens the palette spread within the indigo family for visibly-distinct
tiers, and corrects the topology so every line endpoint terminates at an
explicit node circle. See `docs/design/logo-candidates/candidate-04/notes.md`
Iteration history for v3 → v4 deltas.

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

# Palette swap: indigo brand-scale → its dark-bg counterpart.
# Light-variant tier  →  dark-variant tier
#
# v4 (current, 2026-05-15): wider value spread within the indigo family so
# the three weight tiers are visibly distinct at a glance. Adopts the
# correct WCAG 2.2 SC 1.4.11 Non-text Contrast standard (≥3:1) for logo
# marks; previously the script targeted the over-conservative text-contrast
# 4.5:1 floor, which clustered the three tiers on the value scale.
#
# Per-tier contrast on the v4 mapping (measured via tools/logo-gen/contrast.py):
#   Light bg #fafafa:  HEAVY 15.32:1  MEDIUM 6.02:1  LIGHT 4.28:1
#   Dark  bg #0a0a0a:  HEAVY 16.07:1  MEDIUM  9.93:1  LIGHT 6.64:1
# All six clear SC 1.4.11 (3:1). HEAVY + MEDIUM also clear 4.5:1 on both bgs;
# LIGHT clears 4.5:1 on dark bg (6.64:1) and sits just under on light bg
# (4.28:1) — still passes the logo-mark accessibility floor.
LIGHT_TO_DARK = {
    "#1e1b4b": "#e0e7ff",  # indigo-950  →  indigo-100   (heavy / nodes)
    "#4f46e5": "#a5b4fc",  # indigo-600  →  indigo-300   (medium)
    "#6366f1": "#818cf8",  # indigo-500  →  indigo-400   (light / detail)
}

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
