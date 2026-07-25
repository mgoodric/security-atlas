# 697 — Cache uv/pip in the Python CI jobs

**Cluster:** CI / Infra
**Estimate:** XS
**Type:** AFK
**Status:** `ready`
**Priority:** P3
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2).

## Narrative

Two Python jobs in `.github/workflows/ci.yml` set up their environment with no dependency
cache:

- `oscal-bridge` uses `astral-sh/setup-uv` without `enable-cache: true`.
- `lint-python` does a bare `pip install ruff==0.7.0` with no pip cache.

Both re-download on every code PR. Add `enable-cache: true` + a `cache-dependency-glob:
oscal-bridge/uv.lock` to the oscal-bridge setup-uv, and either run ruff via
`uv tool run ruff@0.7.0` (the pattern already used elsewhere in the file) or add `cache: pip`
to lint-python's setup-python. Estimated saving ~20–40s per code PR.

## Acceptance criteria

- [ ] **AC-1.** `oscal-bridge`'s `setup-uv` enables caching keyed on `oscal-bridge/uv.lock`.
- [ ] **AC-2.** `lint-python` caches its ruff install (pip cache or `uv tool run`).
- [ ] **AC-3.** Both jobs still run the same lint/test commands and stay green.
- [ ] **AC-4.** Any new action reference is SHA-pinned (`actions-pin-check` passes).

## Anti-criteria

- Does NOT change ruff's version pin or rule set.
- Does NOT change the oscal-bridge test surface.

## Dependencies

- Independent.

## Notes

Source: slice 693 audit Finding 1.3.
</content>
