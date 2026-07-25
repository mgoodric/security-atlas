"""mkdocs hook — slice status page (publish, don't commit).

Runs at mkdocs build start. Renders the generated slice tracker into the
docs site source tree at `docs-site/docs/slice-status.md` so the published
docs surface always shows current program status — without committing a
derived `_STATUS.md` to a branch-protected `main`.

Mirrors the slice 140 `openapi_pipeline.py` pattern: a repo artifact rendered
into the docs tree as a derived, gitignored view at *build* time. The source
of truth stays git history + open PRs + branches + `docs/issues/_events.jsonl`,
projected by `scripts/gen-status.sh`.

Published view: open-PR state is intentionally NOT consulted (no `gh` auth
dependency in CI). Status is derived from git history + branches + the
committed event log. A slice with an open PR therefore shows via its branch
as `in-progress` rather than `in-review` — close enough for a published view,
and fully reproducible in CI with zero extra permissions.

Stdlib only — adds nothing to docs-site/requirements.txt. Invoked from
mkdocs.yml under the `hooks:` key. The hook never raises: a generator failure
degrades to a visible warning page so `mkdocs build --strict` still succeeds.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

# Computed once and cached for the rest of the build.
_REPO_ROOT = Path(__file__).resolve().parents[2]
_GEN = _REPO_ROOT / "scripts" / "gen-status.sh"
_DEST = _REPO_ROOT / "docs-site" / "docs" / "slice-status.md"


def on_pre_build(config, **kwargs):  # noqa: ARG001 (mkdocs hook signature)
    """Generate slice-status.md from scripts/gen-status.sh before the build."""
    env = dict(os.environ)
    env["ATLAS_OPEN_PRS_FILE"] = os.devnull  # publish view: no gh dependency

    try:
        result = subprocess.run(
            ["bash", str(_GEN), "--stdout", "--summary"],
            cwd=str(_REPO_ROOT),
            env=env,
            capture_output=True,
            timeout=120,
            check=True,
        )
        # Decode leniently — a stray byte in a slice title must not abort the build
        # (the generator also sanitizes, so this is belt-and-suspenders).
        markdown = result.stdout.decode("utf-8", errors="replace")
    except Exception as exc:  # noqa: BLE001 — never break the docs build
        markdown = (
            "# Slice status\n\n"
            '!!! warning "Status unavailable"\n'
            f"    The slice status could not be generated at build time: `{exc}`.\n"
        )

    _DEST.write_text(markdown, encoding="utf-8")
