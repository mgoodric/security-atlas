#!/usr/bin/env python3
"""Batch-assemble all 10 logo candidates from raw inputs.

Inputs:
    /tmp/cand{01,02,03,04,05,09,10}-raw.png  (Flux/NanoBanana generated)

Outputs (per candidate):
    docs/design/logo-candidates/candidate-NN/mark-1024.png
    docs/design/logo-candidates/candidate-NN/mark-512.png
    docs/design/logo-candidates/candidate-NN/mark.svg  (where applicable)

This script is idempotent.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[2]
CANDIDATES_DIR = ROOT / "docs" / "design" / "logo-candidates"
FONTS_DIR = ROOT / "tools" / "logo-gen" / "fonts"
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


def trim_white(img: Image.Image, threshold: int = 240) -> Image.Image:
    """Treat near-white pixels as transparent, return cropped RGBA."""
    img = img.convert("RGBA")
    pixels = img.load()
    assert pixels is not None
    w, h = img.size
    for y in range(h):
        for x in range(w):
            r, g, b, _a = pixels[x, y]
            if r >= threshold and g >= threshold and b >= threshold:
                pixels[x, y] = (r, g, b, 0)
    bbox = img.getbbox()
    if bbox is None:
        return img
    return img.crop(bbox)


def write_png(img: Image.Image, path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    img.save(path, "PNG", optimize=True)


def composit_text_below(
    raw_path: Path,
    text: str,
    text_color: str,
    weight: str,
    out_path: Path,
    mark_height: int = 580,
    gap: int = 60,
    font_size: int = 130,
) -> None:
    """Mark on top center, text composited below."""
    raw = Image.open(raw_path)
    mark = trim_white(raw)
    scale = mark_height / mark.height
    new_w = int(mark.width * scale)
    new_h = int(mark.height * scale)
    if new_w > 700:
        scale = 700 / mark.width
        new_w = 700
        new_h = int(mark.height * scale)
    mark = mark.resize((new_w, new_h), Image.Resampling.LANCZOS)

    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    mark_x = (CANVAS - new_w) // 2
    mark_y = (CANVAS - new_h - gap - font_size) // 2
    if mark_y < 30:
        mark_y = 30
    canvas.paste(mark, (mark_x, mark_y), mark)

    font = get_font(weight, font_size)
    bbox = font.getbbox(text)
    tw = bbox[2] - bbox[0]
    draw = ImageDraw.Draw(canvas)
    tx = (CANVAS - tw) // 2 - bbox[0]
    ty = mark_y + new_h + gap
    draw.text((tx, ty), text, font=font, fill=hex_to_rgba(text_color))
    write_png(canvas, out_path)


def composit_text_right(
    raw_path: Path,
    text: str,
    text_color: str,
    weight: str,
    out_path: Path,
    mark_height: int = 480,
    font_size: int = 180,
) -> None:
    """Mark on left, text composited to the right."""
    raw = Image.open(raw_path)
    mark = trim_white(raw)
    scale = mark_height / mark.height
    new_w = int(mark.width * scale)
    new_h = int(mark.height * scale)
    mark = mark.resize((new_w, new_h), Image.Resampling.LANCZOS)

    font = get_font(weight, font_size)
    bbox = font.getbbox(text)
    tw = bbox[2] - bbox[0]
    th = bbox[3] - bbox[1]
    gap = 60
    total_w = new_w + gap + tw

    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    start_x = (CANVAS - total_w) // 2
    mark_x = start_x
    mark_y = (CANVAS - new_h) // 2
    canvas.paste(mark, (mark_x, mark_y), mark)

    draw = ImageDraw.Draw(canvas)
    tx = mark_x + new_w + gap - bbox[0]
    ty = (CANVAS - th) // 2 - bbox[1]
    draw.text((tx, ty), text, font=font, fill=hex_to_rgba(text_color))
    write_png(canvas, out_path)


def mark_only(raw_path: Path, out_path: Path) -> None:
    """Trim white, recenter on transparent 1024x1024."""
    raw = Image.open(raw_path)
    mark = trim_white(raw)
    # Fit into 880px square keeping aspect, centered
    target = 880
    scale = min(target / mark.width, target / mark.height)
    new_w = int(mark.width * scale)
    new_h = int(mark.height * scale)
    mark = mark.resize((new_w, new_h), Image.Resampling.LANCZOS)

    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    mx = (CANVAS - new_w) // 2
    my = (CANVAS - new_h) // 2
    canvas.paste(mark, (mx, my), mark)
    write_png(canvas, out_path)


def pure_monogram(text: str, color: str, weight: str, out_path: Path, font_size: int = 680) -> None:
    """Pure-composit monogram (e.g. 'SA')."""
    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    font = get_font(weight, font_size)
    bbox = font.getbbox(text)
    tw = bbox[2] - bbox[0]
    th = bbox[3] - bbox[1]
    draw = ImageDraw.Draw(canvas)
    tx = (CANVAS - tw) // 2 - bbox[0]
    ty = (CANVAS - th) // 2 - bbox[1]
    draw.text((tx, ty), text, font=font, fill=hex_to_rgba(color))
    write_png(canvas, out_path)


def pure_wordmark(
    text: str,
    color: str,
    accent_color: str | None,
    weight: str,
    out_path: Path,
    font_size: int = 130,
) -> None:
    """Pure-composit wordmark with optional accent underline."""
    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    font = get_font(weight, font_size)
    bbox = font.getbbox(text)
    tw = bbox[2] - bbox[0]
    th = bbox[3] - bbox[1]
    draw = ImageDraw.Draw(canvas)
    tx = (CANVAS - tw) // 2 - bbox[0]
    ty = (CANVAS - th) // 2 - bbox[1] - 24
    draw.text((tx, ty), text, font=font, fill=hex_to_rgba(color))
    if accent_color:
        underline_y = ty + bbox[3] + 28
        underline_x0 = tx + bbox[0]
        underline_x1 = tx + bbox[2]
        draw.rectangle(
            [underline_x0, underline_y, underline_x1, underline_y + 10],
            fill=hex_to_rgba(accent_color),
        )
    write_png(canvas, out_path)


def pure_wordmark_with_anchor_dot(
    text_parts: list[str],
    color: str,
    accent_color: str,
    weight: str,
    out_path: Path,
    font_size: int = 110,
) -> None:
    """'security · atlas' with the middle dot replaced by a small anchor node.

    Composit: text-part-1, gap, anchor-dot-glyph (3 connected nodes), gap, text-part-2.
    """
    canvas = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    font = get_font(weight, font_size)
    left, right = text_parts
    lbb = font.getbbox(left)
    rbb = font.getbbox(right)
    lw = lbb[2] - lbb[0]
    rw = rbb[2] - rbb[0]
    th = max(lbb[3] - lbb[1], rbb[3] - rbb[1])
    glyph_w = 70
    gap = 36
    total_w = lw + gap + glyph_w + gap + rw

    start_x = (CANVAS - total_w) // 2
    base_y = (CANVAS - th) // 2 - min(lbb[1], rbb[1])

    draw = ImageDraw.Draw(canvas)
    # Left text
    draw.text((start_x - lbb[0], base_y), left, font=font, fill=hex_to_rgba(color))
    # Anchor glyph: 3 small connected nodes in a triangle (mini graph)
    glyph_cx = start_x + lw + gap + glyph_w // 2
    glyph_cy = base_y + th // 2 + min(lbb[1], rbb[1])
    node_r = 12
    node_color = hex_to_rgba(accent_color)
    line_color = hex_to_rgba(accent_color)
    # Triangle of nodes
    n1 = (glyph_cx, glyph_cy - 24)
    n2 = (glyph_cx - 22, glyph_cy + 16)
    n3 = (glyph_cx + 22, glyph_cy + 16)
    for n in (n1, n2, n3):
        draw.ellipse([n[0] - node_r, n[1] - node_r, n[0] + node_r, n[1] + node_r], fill=node_color)
    draw.line([n1, n2], fill=line_color, width=4)
    draw.line([n2, n3], fill=line_color, width=4)
    draw.line([n1, n3], fill=line_color, width=4)
    # Right text
    rx = start_x + lw + gap + glyph_w + gap - rbb[0]
    draw.text((rx, base_y), right, font=font, fill=hex_to_rgba(color))
    write_png(canvas, out_path)


def resize_to_512(in_1024: Path, out_512: Path) -> None:
    img = Image.open(in_1024).convert("RGBA")
    out = img.resize((512, 512), Image.Resampling.LANCZOS)
    out.save(out_512, "PNG", optimize=True)


# ---------------- SVG generators (text + simple shapes only) ----------------


def write_svg_monogram(text: str, color: str, out_path: Path) -> None:
    """SVG monogram using web-font reference + fallback."""
    svg = f"""<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
  <title>security-atlas {text} monogram</title>
  <style>
    @font-face {{
      font-family: "Inter";
      font-weight: 900;
      src: url("https://rsms.me/inter/font-files/Inter-Black.woff2") format("woff2");
    }}
    .mark {{
      font-family: "Inter", "Helvetica Neue", Helvetica, Arial, sans-serif;
      font-weight: 900;
      font-size: 680px;
      letter-spacing: -38px;
      fill: {color};
    }}
  </style>
  <text x="512" y="700" text-anchor="middle" class="mark">{text}</text>
</svg>
"""
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(svg, encoding="utf-8")


def write_svg_wordmark(text: str, color: str, accent_color: str | None, out_path: Path) -> None:
    """SVG wordmark with optional accent underline."""
    accent = ""
    if accent_color:
        accent = f'  <rect x="180" y="600" width="664" height="14" fill="{accent_color}"/>\n'
    svg = f"""<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
  <title>security-atlas wordmark</title>
  <style>
    @font-face {{
      font-family: "Inter";
      font-weight: 700;
      src: url("https://rsms.me/inter/font-files/Inter-Bold.woff2") format("woff2");
    }}
    .word {{
      font-family: "Inter", "Helvetica Neue", Helvetica, Arial, sans-serif;
      font-weight: 700;
      font-size: 130px;
      fill: {color};
      letter-spacing: -3px;
    }}
  </style>
  <text x="512" y="560" text-anchor="middle" class="word">{text}</text>
{accent}</svg>
"""
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(svg, encoding="utf-8")


def write_svg_wordmark_with_anchor_dot(
    left: str, right: str, color: str, accent_color: str, out_path: Path
) -> None:
    """SVG: left text + small anchor-graph glyph + right text."""
    svg = f"""<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">
  <title>security-atlas dot-graph wordmark</title>
  <style>
    @font-face {{
      font-family: "Inter";
      font-weight: 700;
      src: url("https://rsms.me/inter/font-files/Inter-Bold.woff2") format("woff2");
    }}
    .word {{
      font-family: "Inter", "Helvetica Neue", Helvetica, Arial, sans-serif;
      font-weight: 700;
      font-size: 140px;
      fill: {color};
      letter-spacing: -4px;
    }}
  </style>
  <text x="392" y="555" text-anchor="end" class="word">{left}</text>
  <g stroke="{accent_color}" stroke-width="5" fill="{accent_color}">
    <line x1="490" y1="492" x2="466" y2="536"/>
    <line x1="490" y1="492" x2="514" y2="536"/>
    <line x1="466" y1="536" x2="514" y2="536"/>
    <circle cx="490" cy="492" r="14"/>
    <circle cx="466" cy="536" r="14"/>
    <circle cx="514" cy="536" r="14"/>
  </g>
  <text x="608" y="555" text-anchor="start" class="word">{right}</text>
</svg>
"""
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(svg, encoding="utf-8")


# ---------------- Driver ----------------


def assemble() -> None:
    raw_dir = Path("/tmp")

    # Candidate 01 — Cartographic contour + "atlas" wordmark below
    print("[01] cartographic contour + atlas wordmark")
    composit_text_below(
        raw_path=raw_dir / "cand01-raw.png",
        text="atlas",
        text_color="#4f46e5",
        weight="Bold",
        out_path=CANDIDATES_DIR / "candidate-01" / "mark-1024.png",
        mark_height=520,
        gap=70,
        font_size=140,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-01" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-01" / "mark-512.png",
    )

    # Candidate 02 — Control-graph nodes, mark-only
    print("[02] control-graph nodes (mark-only)")
    mark_only(
        raw_path=raw_dir / "cand02-raw.png",
        out_path=CANDIDATES_DIR / "candidate-02" / "mark-1024.png",
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-02" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-02" / "mark-512.png",
    )

    # Candidate 03 — Spine-and-branches + "atlas" wordmark to the right
    print("[03] spine-and-branches + atlas wordmark right")
    composit_text_right(
        raw_path=raw_dir / "cand03-raw.png",
        text="atlas",
        text_color="#059669",
        weight="Bold",
        out_path=CANDIDATES_DIR / "candidate-03" / "mark-1024.png",
        mark_height=520,
        font_size=170,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-03" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-03" / "mark-512.png",
    )

    # Candidate 04 — Lattice mesh A, mark-only
    print("[04] lattice mesh A (mark-only)")
    mark_only(
        raw_path=raw_dir / "cand04-raw.png",
        out_path=CANDIDATES_DIR / "candidate-04" / "mark-1024.png",
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-04" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-04" / "mark-512.png",
    )

    # Candidate 05 — Anchor+lattice + "SA" initials
    print("[05] anchor+lattice + SA initials")
    composit_text_below(
        raw_path=raw_dir / "cand05-raw.png",
        text="SA",
        text_color="#1f2937",
        weight="Black",
        out_path=CANDIDATES_DIR / "candidate-05" / "mark-1024.png",
        mark_height=480,
        gap=60,
        font_size=170,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-05" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-05" / "mark-512.png",
    )

    # Candidate 06 — Pure SA monogram (no image-model)
    print("[06] SA monogram (pure composit)")
    pure_monogram(
        text="SA",
        color="#0f172a",
        weight="Black",
        out_path=CANDIDATES_DIR / "candidate-06" / "mark-1024.png",
        font_size=680,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-06" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-06" / "mark-512.png",
    )
    write_svg_monogram("SA", "#0f172a", CANDIDATES_DIR / "candidate-06" / "mark.svg")

    # Candidate 07 — Wordmark "security-atlas" + indigo accent underline
    print("[07] wordmark security-atlas + accent underline")
    pure_wordmark(
        text="security-atlas",
        color="#334155",
        accent_color="#6366f1",
        weight="Bold",
        out_path=CANDIDATES_DIR / "candidate-07" / "mark-1024.png",
        font_size=130,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-07" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-07" / "mark-512.png",
    )
    write_svg_wordmark(
        "security-atlas",
        "#334155",
        "#6366f1",
        CANDIDATES_DIR / "candidate-07" / "mark.svg",
    )

    # Candidate 08 — "security · atlas" with anchor-graph glyph instead of dot
    print("[08] security + anchor-graph + atlas")
    pure_wordmark_with_anchor_dot(
        text_parts=["security", "atlas"],
        color="#0f172a",
        accent_color="#0891b2",
        weight="Bold",
        out_path=CANDIDATES_DIR / "candidate-08" / "mark-1024.png",
        font_size=110,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-08" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-08" / "mark-512.png",
    )
    write_svg_wordmark_with_anchor_dot(
        "security",
        "atlas",
        "#0f172a",
        "#0891b2",
        CANDIDATES_DIR / "candidate-08" / "mark.svg",
    )

    # Candidate 09 — Hex control-cell, mark-only
    print("[09] hex control-cell (mark-only)")
    mark_only(
        raw_path=raw_dir / "cand09-raw.png",
        out_path=CANDIDATES_DIR / "candidate-09" / "mark-1024.png",
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-09" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-09" / "mark-512.png",
    )

    # Candidate 10 — A from control-graph + "atlas" wordmark below
    print("[10] A-graph + atlas wordmark")
    composit_text_below(
        raw_path=raw_dir / "cand10-raw.png",
        text="atlas",
        text_color="#7c3aed",
        weight="Bold",
        out_path=CANDIDATES_DIR / "candidate-10" / "mark-1024.png",
        mark_height=550,
        gap=70,
        font_size=140,
    )
    resize_to_512(
        CANDIDATES_DIR / "candidate-10" / "mark-1024.png",
        CANDIDATES_DIR / "candidate-10" / "mark-512.png",
    )

    print("done.")


if __name__ == "__main__":
    assemble()
    sys.exit(0)
