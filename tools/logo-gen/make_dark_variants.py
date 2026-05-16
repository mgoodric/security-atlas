#!/usr/bin/env python3
"""Generate dark-bg variants of each candidate by lightening the mark color.

Strategy: for each candidate's `mark-1024.png` (which is optimized for light bg),
produce `mark-1024-dark.png` and `mark-512-dark.png` that pass WCAG against #0a0a0a.

For image-model raster candidates (01-05, 09, 10): recolor non-transparent pixels
to a chosen "dark-bg-safe" hue (typically a 50-60% lighter shade of the original).
For composit candidates (06, 07, 08): re-composit with a light foreground color.

The mapping is per-candidate to preserve visual identity.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[2]
CANDIDATES = ROOT / "docs" / "design" / "logo-candidates"


# Mapping: candidate_id -> (light_mark_color_hex_for_reference, dark_variant_target_color)
# Light variant uses the original brand color; dark variant uses a lighter tint.
DARK_VARIANT_COLOR = {
    "01": "#a5b4fc",  # indigo-300 — lifts indigo onto dark
    "02": "#5eead4",  # teal-300 — lifts teal
    "03": "#6ee7b7",  # emerald-300 — lifts emerald
    "04": "#fdba74",  # orange-300 — lifts amber
    "05": "#cbd5e1",  # slate-300 — lifts slate
    "06": "#f8fafc",  # slate-50 — flip near-black to near-white
    "07": "#e2e8f0",  # slate-200 — flip slate
    "08": "#e2e8f0",  # slate-200 — flip slate
    "09": "#fda4af",  # rose-300 — lifts rose; the surrounding cells become slate-300
    "10": "#c4b5fd",  # violet-300 — lifts violet
}


def hex_to_rgb(s: str) -> tuple[int, int, int]:
    s = s.lstrip("#")
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def recolor_preserving_alpha(src_path: Path, dst_path: Path, new_color: str) -> None:
    """Replace all non-transparent pixel colors with `new_color`, preserve alpha.

    This works well for marks that are silhouettes / monochrome.
    For multi-color marks (#09 has both rose and slate), this collapses both
    to one color — that's an acceptable visual simplification for the dark variant.
    """
    img = Image.open(src_path).convert("RGBA")
    r_new, g_new, b_new = hex_to_rgb(new_color)
    pixels = img.load()
    assert pixels is not None
    w, h = img.size
    for y in range(h):
        for x in range(w):
            r, g, b, a = pixels[x, y]
            if a > 0:
                # Preserve the per-pixel alpha (for anti-aliased edges) and luminance shading
                # by blending: keep the original luminance, swap the chroma.
                # Simpler: just replace with target color at full opacity weighted by alpha.
                pixels[x, y] = (r_new, g_new, b_new, a)
    img.save(dst_path, "PNG", optimize=True)


def recolor_multi_aware(src_path: Path, dst_path: Path, rose_to: str, slate_to: str) -> None:
    """For candidate 09 specifically: preserve the rose center vs slate frame split."""
    img = Image.open(src_path).convert("RGBA")
    rr, rg, rb = hex_to_rgb(rose_to)
    sr, sg, sb = hex_to_rgb(slate_to)
    pixels = img.load()
    assert pixels is not None
    w, h = img.size
    for y in range(h):
        for x in range(w):
            r, g, b, a = pixels[x, y]
            if a == 0:
                continue
            # If red dominates → rose region; otherwise slate region
            if r > g + 30 and r > b + 30:
                pixels[x, y] = (rr, rg, rb, a)
            else:
                pixels[x, y] = (sr, sg, sb, a)
    img.save(dst_path, "PNG", optimize=True)


def resize_to_512(src: Path, dst: Path) -> None:
    img = Image.open(src).convert("RGBA")
    img.resize((512, 512), Image.Resampling.LANCZOS).save(dst, "PNG", optimize=True)


def main() -> int:
    for n in ["01", "02", "03", "04", "05", "06", "07", "08", "09", "10"]:
        cdir = CANDIDATES / f"candidate-{n}"
        src = cdir / "mark-1024.png"
        dst_1024 = cdir / "mark-1024-dark.png"
        dst_512 = cdir / "mark-512-dark.png"
        if not src.exists():
            print(f"[{n}] SKIP — source missing: {src}")
            continue

        if n == "09":
            # Preserve the rose vs slate split
            recolor_multi_aware(src, dst_1024, rose_to="#fda4af", slate_to="#cbd5e1")
        else:
            recolor_preserving_alpha(src, dst_1024, DARK_VARIANT_COLOR[n])
        resize_to_512(dst_1024, dst_512)
        print(f"[{n}] wrote {dst_1024.name} + {dst_512.name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
