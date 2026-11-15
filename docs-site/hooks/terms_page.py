"""mkdocs hook — #2144 Terms page.

Generates `docs-site/docs/terms.md` from the user-facing term list at
`web/help/terms.json` at build start, so the app's <Term> tooltips and the
docs site's Terms page are published from one file. The generated page is
gitignored; edit the JSON, not the page.

Each entry's heading carries the term id as its anchor (`{#residual-risk}`,
via the `attr_list` extension), which is the anchor the app's tooltip links
to (`web/lib/help/terms.ts` `termHref`). A term that names a developer
GLOSSARY.md entry links to it on GitHub.

Standard library only, like `openapi_pipeline.py`.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
_SRC_TERMS = _REPO_ROOT / "web" / "help" / "terms.json"
_DEST_PAGE = _REPO_ROOT / "docs-site" / "docs" / "terms.md"
_GLOSSARY_URL = "https://github.com/mgoodric/security-atlas/blob/main/"


def on_pre_build(config: dict[str, Any], **kwargs: Any) -> None:
    """mkdocs hook entry: writes the Terms page before files are collected.

    Args:
        config: mkdocs build config dict (unused but required by hook
            signature).
        **kwargs: forward-compat for future mkdocs hook args.
    """
    if not _SRC_TERMS.exists():
        raise FileNotFoundError(f"#2144 terms page: expected term list at {_SRC_TERMS}")
    terms = json.loads(_SRC_TERMS.read_text(encoding="utf-8"))["terms"]
    _DEST_PAGE.write_text(render_terms_page(terms), encoding="utf-8")


def render_terms_page(terms: list[dict[str, str]]) -> str:
    """Return the Terms page Markdown for ``terms``, sorted by name."""
    lines = [
        "# Terms",
        "",
        "<!-- Generated from web/help/terms.json by docs-site/hooks/terms_page.py."
        " Edit the JSON, not this page. -->",
        "",
        "Plain-language definitions of the terms security-atlas underlines in the"
        " app. Hover over, focus or tap an underlined term to see the short"
        " definition; it links here for the full one.",
        "",
    ]
    for term in sorted(terms, key=lambda t: t["name"].lower()):
        lines += [
            f"## {term['name']} {{#{term['id']}}}",
            "",
            f"**In short:** {term['short']}",
            "",
            term["long"],
            "",
        ]
        if term.get("glossary"):
            lines += [
                f"Developer definition: [GLOSSARY.md]({_GLOSSARY_URL}{term['glossary']}).",
                "",
            ]
    return "\n".join(lines)
