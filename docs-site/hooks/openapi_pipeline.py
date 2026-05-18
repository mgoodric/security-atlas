"""mkdocs hook — slice 140 OpenAPI spec pipeline.

Runs at mkdocs build start. Two responsibilities:

1. **Copy** the canonical `docs/openapi.yaml` (committed at the repo
   root) into the docs site source tree at `docs-site/docs/openapi.yaml`
   so mkdocs can serve it as a static asset alongside the Redoc page.

2. **Filter** the copied spec to strip every operation whose
   `x-internal: true` extension is set. This is the slice 140 P0-A3
   load-bearing mitigation against information disclosure — operator-
   only endpoints (`/health`, `/metrics`, `/v1/version`,
   `/v1/install-state`) MUST NOT render in the public Redoc UI.

The filtering happens at *build time*, not at *run time*. The source
spec (`docs/openapi.yaml` at the repo root) is the single source of
truth (slice 140 P0-A8); this hook produces a derived public render.

The hook uses only the standard library — no PyYAML — so it doesn't
add to the docs-site `requirements.txt`. It does a line-by-line
transform over the YAML (the generator emits a stable shape per
operation; safe to filter that way without a full parse).

Specifically: walks the `paths:` section, and for any operation whose
indented body contains `x-internal: true`, drops that operation block
from the output. If the resulting path has zero operations left, drops
the path key too.

Invoked from mkdocs.yml under the `hooks:` key.
"""

from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Any


# Computed once and cached for the rest of the build.
_REPO_ROOT = Path(__file__).resolve().parents[2]
_SRC_SPEC = _REPO_ROOT / "docs" / "openapi.yaml"
_DEST_SPEC = _REPO_ROOT / "docs-site" / "docs" / "openapi.yaml"


def on_pre_build(config: dict[str, Any], **kwargs: Any) -> None:
    """mkdocs hook entry: runs once at build start.

    Copies the source spec into the docs tree and filters out
    operations marked `x-internal: true`. Idempotent — overwrites the
    destination on every build.

    Args:
        config: mkdocs build config dict (unused but required by hook
            signature).
        **kwargs: forward-compat for future mkdocs hook args.
    """
    if not _SRC_SPEC.exists():
        raise FileNotFoundError(
            f"slice 140 openapi pipeline: expected canonical spec at {_SRC_SPEC}; "
            "run `just openapi-generate` to produce it."
        )

    _DEST_SPEC.parent.mkdir(parents=True, exist_ok=True)

    filtered = _filter_internal_operations(_SRC_SPEC.read_text(encoding="utf-8"))
    _DEST_SPEC.write_text(filtered, encoding="utf-8")


def _filter_internal_operations(yaml_text: str) -> str:
    """Return ``yaml_text`` with `x-internal: true` operations stripped.

    The generator emits one operation per HTTP method under each path,
    with this shape (4-space indent under the path; 6-space indent
    under the method; etc.):

        paths:
          /health:
            get:
              summary: GET /health
              tags: [system]
              operationId: get-health
              x-internal: true
              responses:
                ...

    The filter walks the text line-by-line tracking path / operation
    boundaries. When a method block contains the literal
    ``      x-internal: true`` line at the operation-attribute indent,
    the entire method block (from the method line to the next sibling
    method / path / top-level key) is omitted.

    If a path ends up with zero operations after filtering, the path
    key is also omitted so the public render is clean (no empty stubs).
    """
    lines = yaml_text.splitlines(keepends=True)
    out: list[str] = []

    # Walk line-by-line emitting two-pass-style: collect operation
    # blocks, decide internal-or-not, emit selectively.
    i = 0
    in_paths = False
    while i < len(lines):
        line = lines[i]
        stripped = line.rstrip("\n").rstrip("\r")

        # Detect paths section start / end.
        if stripped == "paths:":
            in_paths = True
            out.append(line)
            i += 1
            continue
        if in_paths and stripped and not line.startswith(" "):
            # Sibling top-level key (e.g. "components:") — end paths.
            in_paths = False

        if not in_paths:
            out.append(line)
            i += 1
            continue

        # Inside paths. A path key looks like "  /foo:" at 2-space
        # indent. A method block opens at 4-space indent. We collect
        # the path header + every method block under it, then emit
        # only those that aren't x-internal.
        if line.startswith("  ") and not line.startswith("    ") and stripped.endswith(":"):
            path_header = line
            i += 1
            # Collect method blocks for this path.
            method_blocks: list[list[str]] = []
            while i < len(lines):
                peek = lines[i]
                if peek.startswith("    ") and not peek.startswith("      ") and peek.rstrip("\n").endswith(":"):
                    # New method block.
                    block = [peek]
                    i += 1
                    # Consume the body until we hit the next method or
                    # the next path or EOF.
                    while i < len(lines):
                        body = lines[i]
                        if body.startswith("    ") and not body.startswith("      ") and body.rstrip("\n").endswith(":"):
                            break
                        if body.startswith("  ") and not body.startswith("    ") and body.rstrip("\n").endswith(":"):
                            break
                        if body.strip() and not body.startswith(" "):
                            break
                        block.append(body)
                        i += 1
                    method_blocks.append(block)
                    continue
                break
            # Filter out any method block carrying `x-internal: true`.
            public_blocks = [
                block for block in method_blocks
                if not any("x-internal: true" in ln for ln in block)
            ]
            if public_blocks:
                out.append(path_header)
                for block in public_blocks:
                    out.extend(block)
            # else: drop the path entirely (no public methods left)
            continue

        out.append(line)
        i += 1

    return "".join(out)
