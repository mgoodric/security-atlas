#!/usr/bin/env python3
"""WCAG 2.2 contrast measurement utility.

Computes the relative-luminance contrast ratio between a foreground color
(extracted from a mark PNG) and named background colors.

Usage:
    contrast.py mark.png
    contrast.py mark.png --fg-hex 4f46e5
    contrast.py mark.png --bg dark
    contrast.py mark.png --bg light

Output format (machine-parsable):
    fg=<hex> bg_dark=<ratio>:1 bg_light=<ratio>:1 dark_pass=<bool> light_pass=<bool>
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from PIL import Image

BG_DARK = (0x0A, 0x0A, 0x0A)
BG_LIGHT = (0xFA, 0xFA, 0xFA)
THRESHOLD = 4.5  # WCAG AA normal text


def srgb_to_linear(c: float) -> float:
    """Convert sRGB channel value (0..1) to linear-light."""
    if c <= 0.03928:
        return c / 12.92
    return ((c + 0.055) / 1.055) ** 2.4


def relative_luminance(rgb: tuple[int, int, int]) -> float:
    """WCAG 2.2 relative luminance."""
    r, g, b = (v / 255.0 for v in rgb)
    return 0.2126 * srgb_to_linear(r) + 0.7152 * srgb_to_linear(g) + 0.0722 * srgb_to_linear(b)


def contrast_ratio(fg: tuple[int, int, int], bg: tuple[int, int, int]) -> float:
    """Compute the WCAG contrast ratio."""
    l_fg = relative_luminance(fg)
    l_bg = relative_luminance(bg)
    lighter, darker = (l_fg, l_bg) if l_fg > l_bg else (l_bg, l_fg)
    return (lighter + 0.05) / (darker + 0.05)


def extract_dominant_non_transparent_color(image_path: Path) -> tuple[int, int, int]:
    """Find the dominant opaque color in the image.

    Strategy: convert to RGBA, drop transparent pixels (alpha < 200),
    quantize remaining opaque pixels to a 64-color palette, return the
    most-frequent color. If the image has no alpha channel, all pixels count.
    """
    img = Image.open(image_path).convert("RGBA")
    pixels = list(img.getdata())
    opaque = [(r, g, b) for r, g, b, a in pixels if a >= 200]
    if not opaque:
        # All transparent — fall back to all pixels regardless of alpha
        opaque = [(r, g, b) for r, g, b, _ in pixels]
    if not opaque:
        return (0, 0, 0)

    # Build a quantized RGB image from opaque pixels (1-row strip)
    strip = Image.new("RGB", (len(opaque), 1))
    strip.putdata(opaque)
    quantized = strip.quantize(colors=32, method=Image.Quantize.MEDIANCUT)
    palette = quantized.getpalette()[: 32 * 3]
    counts: dict[int, int] = {}
    for idx in quantized.getdata():
        counts[idx] = counts.get(idx, 0) + 1
    # Sort by frequency, but prefer non-near-white / non-near-black colors
    # if a near-neutral has only marginal frequency advantage. This biases the
    # measurement toward the actual brand mark color rather than the bg fill.
    sorted_idxs = sorted(counts.items(), key=lambda kv: -kv[1])
    best_idx = sorted_idxs[0][0]

    # If most-frequent is near-white or near-black, look for a more chromatic
    # color in the top 3.
    def is_near_neutral(rgb: tuple[int, int, int]) -> bool:
        r, g, b = rgb
        if r > 240 and g > 240 and b > 240:
            return True
        if r < 20 and g < 20 and b < 20:
            return True
        return False

    most_freq_rgb = (palette[best_idx * 3], palette[best_idx * 3 + 1], palette[best_idx * 3 + 2])
    if is_near_neutral(most_freq_rgb) and len(sorted_idxs) > 1:
        for idx, _count in sorted_idxs[1:5]:
            rgb = (palette[idx * 3], palette[idx * 3 + 1], palette[idx * 3 + 2])
            if not is_near_neutral(rgb):
                return rgb
    return most_freq_rgb


def hex_of(rgb: tuple[int, int, int]) -> str:
    return "{:02x}{:02x}{:02x}".format(*rgb)


def parse_hex(s: str) -> tuple[int, int, int]:
    s = s.lstrip("#")
    if len(s) != 6:
        raise ValueError(f"hex must be 6 chars, got {s!r}")
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("image", nargs="?", type=Path, help="image path")
    p.add_argument("--fg-hex", help="override foreground color (6-digit hex)")
    args = p.parse_args()

    if args.fg_hex:
        fg = parse_hex(args.fg_hex)
    else:
        if not args.image or not args.image.exists():
            print("error: image path required (or use --fg-hex)", file=sys.stderr)
            return 2
        fg = extract_dominant_non_transparent_color(args.image)

    r_dark = contrast_ratio(fg, BG_DARK)
    r_light = contrast_ratio(fg, BG_LIGHT)
    dark_pass = r_dark >= THRESHOLD
    light_pass = r_light >= THRESHOLD

    print(
        f"fg={hex_of(fg)} bg_dark={r_dark:.2f}:1 bg_light={r_light:.2f}:1 "
        f"dark_pass={str(dark_pass).lower()} light_pass={str(light_pass).lower()}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
