#!/usr/bin/env python3
"""Composit helper: mark PNG + Inter text → final 1024×1024 PNG.

Also handles pure-composit candidates (SVG monogram / wordmark).

Usage:
    compose_logo.py text-below --mark /path/mark.png --text "atlas" --out /path/out.png \
        --text-color "#4f46e5" --font Bold
    compose_logo.py text-right --mark /path/mark.png --text "atlas" --out /path/out.png
    compose_logo.py monogram --text "SA" --out /path/out.png --color "#1f2937" --font Black
    compose_logo.py wordmark --text "security-atlas" --out /path/out.png \
        --color "#334155" --accent "#6366f1"

All outputs: 1024x1024 PNG, transparent background unless --bg specified.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

FONTS_DIR = Path(__file__).parent / "fonts"
FONT_BOLD = FONTS_DIR / "Inter-Bold.ttf"
FONT_BLACK = FONTS_DIR / "Inter-Black.ttf"
CANVAS = 1024


def hex_to_rgb(s: str) -> tuple[int, int, int]:
    s = s.lstrip("#")
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def hex_to_rgba(s: str, alpha: int = 255) -> tuple[int, int, int, int]:
    r, g, b = hex_to_rgb(s)
    return (r, g, b, alpha)


def get_font(weight: str, size: int) -> ImageFont.FreeTypeFont:
    path = FONT_BLACK if weight == "Black" else FONT_BOLD
    return ImageFont.truetype(str(path), size)


def make_canvas(bg: str | None = None) -> Image.Image:
    if bg:
        return Image.new("RGBA", (CANVAS, CANVAS), hex_to_rgba(bg))
    return Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))


def trim_transparent(img: Image.Image) -> Image.Image:
    """Crop the image to its non-transparent bounding box."""
    if img.mode != "RGBA":
        img = img.convert("RGBA")
    bbox = img.getbbox()
    if bbox is None:
        return img
    return img.crop(bbox)


def measure_text(text: str, font: ImageFont.FreeTypeFont) -> tuple[int, int]:
    bbox = font.getbbox(text)
    return bbox[2] - bbox[0], bbox[3] - bbox[1]


def cmd_text_below(args) -> int:
    """Mark centered upper, text below."""
    canvas = make_canvas(args.bg)
    mark = trim_transparent(Image.open(args.mark).convert("RGBA"))
    # Scale mark to ~620px tall in upper region
    mark_target_h = 620
    scale = mark_target_h / mark.height
    new_w = int(mark.width * scale)
    new_h = int(mark.height * scale)
    if new_w > 700:
        scale = 700 / mark.width
        new_w = 700
        new_h = int(mark.height * scale)
    mark = mark.resize((new_w, new_h), Image.Resampling.LANCZOS)
    mark_x = (CANVAS - new_w) // 2
    mark_y = 70
    canvas.paste(mark, (mark_x, mark_y), mark)

    # Text at the bottom
    font = get_font(args.font, 130)
    tw, th = measure_text(args.text, font)
    draw = ImageDraw.Draw(canvas)
    tx = (CANVAS - tw) // 2
    ty = mark_y + new_h + 50
    draw.text((tx, ty), args.text, font=font, fill=hex_to_rgba(args.text_color))

    canvas.save(args.out, "PNG", optimize=True)
    return 0


def cmd_text_right(args) -> int:
    """Mark left, text right."""
    canvas = make_canvas(args.bg)
    mark = trim_transparent(Image.open(args.mark).convert("RGBA"))
    mark_target_h = 500
    scale = mark_target_h / mark.height
    new_w = int(mark.width * scale)
    new_h = int(mark.height * scale)
    mark = mark.resize((new_w, new_h), Image.Resampling.LANCZOS)

    font = get_font(args.font, 180)
    tw, th = measure_text(args.text, font)
    gap = 60
    total_w = new_w + gap + tw

    start_x = (CANVAS - total_w) // 2
    mark_x = start_x
    mark_y = (CANVAS - new_h) // 2
    canvas.paste(mark, (mark_x, mark_y), mark)

    draw = ImageDraw.Draw(canvas)
    tx = mark_x + new_w + gap
    ty = (CANVAS - th) // 2 - font.getbbox(args.text)[1] // 2
    draw.text((tx, ty), args.text, font=font, fill=hex_to_rgba(args.text_color))

    canvas.save(args.out, "PNG", optimize=True)
    return 0


def cmd_monogram(args) -> int:
    """Pure-composit SA monogram — no mark image."""
    canvas = make_canvas(args.bg)
    font = get_font(args.font, 720)
    draw = ImageDraw.Draw(canvas)
    tw, th = measure_text(args.text, font)
    bbox = font.getbbox(args.text)
    tx = (CANVAS - tw) // 2 - bbox[0]
    ty = (CANVAS - th) // 2 - bbox[1]
    draw.text((tx, ty), args.text, font=font, fill=hex_to_rgba(args.color))
    canvas.save(args.out, "PNG", optimize=True)
    return 0


def cmd_wordmark(args) -> int:
    """Pure-composit wordmark with optional accent dot/underline."""
    canvas = make_canvas(args.bg)
    font = get_font(args.font, 150)
    draw = ImageDraw.Draw(canvas)
    tw, th = measure_text(args.text, font)
    bbox = font.getbbox(args.text)
    tx = (CANVAS - tw) // 2 - bbox[0]
    ty = (CANVAS - th) // 2 - bbox[1] - 20
    draw.text((tx, ty), args.text, font=font, fill=hex_to_rgba(args.color))

    # Accent: thin underline beneath the wordmark
    if args.accent:
        underline_y = ty + bbox[3] + 30
        underline_x0 = tx + bbox[0]
        underline_x1 = tx + bbox[2]
        draw.rectangle(
            [underline_x0, underline_y, underline_x1, underline_y + 8],
            fill=hex_to_rgba(args.accent),
        )

    canvas.save(args.out, "PNG", optimize=True)
    return 0


def cmd_resize(args) -> int:
    """Resize a 1024 PNG down to a target size with PNG optimize."""
    img = Image.open(args.input).convert("RGBA")
    out = img.resize((args.size, args.size), Image.Resampling.LANCZOS)
    out.save(args.out, "PNG", optimize=True)
    return 0


def main() -> int:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)

    tb = sub.add_parser("text-below")
    tb.add_argument("--mark", type=Path, required=True)
    tb.add_argument("--text", required=True)
    tb.add_argument("--out", type=Path, required=True)
    tb.add_argument("--text-color", default="#0f172a")
    tb.add_argument("--font", choices=["Bold", "Black"], default="Bold")
    tb.add_argument("--bg", default=None)
    tb.set_defaults(func=cmd_text_below)

    tr = sub.add_parser("text-right")
    tr.add_argument("--mark", type=Path, required=True)
    tr.add_argument("--text", required=True)
    tr.add_argument("--out", type=Path, required=True)
    tr.add_argument("--text-color", default="#0f172a")
    tr.add_argument("--font", choices=["Bold", "Black"], default="Bold")
    tr.add_argument("--bg", default=None)
    tr.set_defaults(func=cmd_text_right)

    mo = sub.add_parser("monogram")
    mo.add_argument("--text", required=True)
    mo.add_argument("--out", type=Path, required=True)
    mo.add_argument("--color", default="#0f172a")
    mo.add_argument("--font", choices=["Bold", "Black"], default="Black")
    mo.add_argument("--bg", default=None)
    mo.set_defaults(func=cmd_monogram)

    wm = sub.add_parser("wordmark")
    wm.add_argument("--text", required=True)
    wm.add_argument("--out", type=Path, required=True)
    wm.add_argument("--color", default="#0f172a")
    wm.add_argument("--accent", default=None)
    wm.add_argument("--font", choices=["Bold", "Black"], default="Bold")
    wm.add_argument("--bg", default=None)
    wm.set_defaults(func=cmd_wordmark)

    rs = sub.add_parser("resize")
    rs.add_argument("--input", type=Path, required=True)
    rs.add_argument("--out", type=Path, required=True)
    rs.add_argument("--size", type=int, required=True)
    rs.set_defaults(func=cmd_resize)

    args = p.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
