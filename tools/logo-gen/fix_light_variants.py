#!/usr/bin/env python3
"""For candidates 02, 03, 09 whose canonical light-bg variant fails WCAG on #fafafa,
darken the mark color so it passes ≥4.5:1.

Recolor strategy mirrors make_dark_variants.py but in reverse (darken to deeper tone).
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[2]
CANDIDATES = ROOT / "docs" / "design" / "logo-candidates"


def hex_to_rgb(s: str) -> tuple[int, int, int]:
    s = s.lstrip("#")
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def recolor_preserving_alpha(src_path: Path, dst_path: Path, new_color: str) -> None:
    img = Image.open(src_path).convert("RGBA")
    r_new, g_new, b_new = hex_to_rgb(new_color)
    pixels = img.load()
    assert pixels is not None
    w, h = img.size
    for y in range(h):
        for x in range(w):
            r, g, b, a = pixels[x, y]
            if a > 0:
                pixels[x, y] = (r_new, g_new, b_new, a)
    img.save(dst_path, "PNG", optimize=True)


def recolor_multi_aware(src_path: Path, dst_path: Path, rose_to: str, slate_to: str) -> None:
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
            if r > g + 30 and r > b + 30:
                pixels[x, y] = (rr, rg, rb, a)
            else:
                pixels[x, y] = (sr, sg, sb, a)
    img.save(dst_path, "PNG", optimize=True)


def resize_to_512(src: Path, dst: Path) -> None:
    img = Image.open(src).convert("RGBA")
    img.resize((512, 512), Image.Resampling.LANCZOS).save(dst, "PNG", optimize=True)


def main() -> int:
    # Candidate 02: teal — darken to teal-800 #115e59 (passes light bg)
    src = CANDIDATES / "candidate-02" / "mark-1024.png"
    recolor_preserving_alpha(src, src, "#115e59")
    resize_to_512(src, CANDIDATES / "candidate-02" / "mark-512.png")
    print("[02] recolored canonical to teal-800 #115e59")

    # Candidate 03: emerald — darken to emerald-800 #065f46 (passes light bg)
    src = CANDIDATES / "candidate-03" / "mark-1024.png"
    recolor_preserving_alpha(src, src, "#065f46")
    resize_to_512(src, CANDIDATES / "candidate-03" / "mark-512.png")
    print("[03] recolored canonical to emerald-800 #065f46")

    # Candidate 09: split — rose stays #e11d48 (4.50:1 light, barely passes),
    # slate frame darkens to slate-700 #334155 (1.91:1 light — FAIL, but center cell
    # carries the brand color). The dominant pixel detector returns slate so the test
    # registers as fail. Adjust: darken slate to slate-900 #0f172a (17.10:1 light PASS).
    src = CANDIDATES / "candidate-09" / "mark-1024.png"
    recolor_multi_aware(src, src, rose_to="#e11d48", slate_to="#0f172a")
    resize_to_512(src, CANDIDATES / "candidate-09" / "mark-512.png")
    print("[09] recolored to rose center on near-black hex frame")
    return 0


if __name__ == "__main__":
    sys.exit(main())
