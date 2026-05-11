# 03 — Execute Issue 001 (Monorepo Skeleton)

The first real build session. This is the **CLAUDE.md "When code begins" approval gate** — the prompt explicitly grants scaffolding authorization, which CLAUDE.md requires before any code lands.

## Prompt

```
Build docs/issues/001-monorepo-skeleton.md.

I approve scaffolding for this slice per CLAUDE.md "When code begins" step 1. This is the authorization gate.

Branch: spine/001-monorepo-skeleton (from main).

Honor all 6 acceptance criteria:
- AC-1: `just build` succeeds from a fresh clone in under 5 minutes
- AC-2: A PR with a trivial change triggers GitHub Actions; build + lint + test all green
- AC-3: `just install-hooks` installs pre-commit; bad-format Go file rejected before commit completes
- AC-4: `go.work` lists ≥ 1 module per cmd/ entry; `go mod tidy` clean
- AC-5: `npm install` at root installs web/ and sdk/typescript/ via workspaces
- AC-6: Root README explains the just task surface (build, test, lint, fmt, install-hooks)

Honor every anti-criterion (P0 — block merge if introduced):
- NO application code (no handlers, migrations, domain types)
- NO dependencies beyond build/lint/test infrastructure
- NO .env / secrets / credentials committed

Respect CLAUDE.md style: no emojis in code/docs/commits, Conventional Commits, Co-Authored-By trailer on AI-assisted commits.

Workflow:
1. Read CLAUDE.md "Planned repository layout" — create the directory tree exactly as specified
2. tdd where applicable (mostly config-shaped slice; minimal test surface)
3. simplify pass before opening PR
4. security-review check (no secrets, no overly broad CI permissions)
5. Open PR titled "feat(spine): monorepo skeleton + CI green build (#001)"
6. PR body must include: AC pass/fail table · files added (top-level dirs) · CI run URL · open questions surfaced

Use Algorithm mode (Standard or higher). Initialize a PRD (id: 001-monorepo-skeleton).
```

## What to expect back

- New branch `spine/001-monorepo-skeleton`
- The full directory tree from CLAUDE.md "Planned repository layout" (cmd/, internal/, pkg/, connectors/, sdk/, web/, oscal-bridge/, proto/, schemas/, migrations/, policies/, deploy/, etc.)
- `justfile`, `go.work`, `package.json`, `pyproject.toml` at root
- `.github/workflows/` for build + lint + test
- Root `README.md` documenting the `just` task surface
- A PR on github.com/mgoodric/security-atlas with the AC pass/fail table in the body

## Why this prompt is different from later slices

Issue 001 is the only slice with an explicit "**I approve scaffolding**" line. CLAUDE.md gates all initial scaffolding behind explicit user approval. After this slice merges, the gate is passed — subsequent slices use the per-slice template in `04-per-slice-template.md` without the approval clause.

## Verification before merging

- Clone fresh into `/tmp/`, run `just build` — should pass in < 5 min (AC-1)
- Make a trivial PR (e.g., add a line to README); confirm GH Actions runs green (AC-2)
- `just install-hooks` then try to commit a badly-formatted `.go` file — should reject (AC-3)
- `go mod tidy` should be a no-op (AC-4)
- `rm -rf node_modules && npm install` at root — `web/` and `sdk/typescript/` both resolve (AC-5)
- Read the README — task surface is documented (AC-6)

## Notes

- This slice ships no application code. If you find yourself writing handlers, migrations, or domain types, you've crossed into issue 002 (schema) — stop and merge 001 first.
- If a GH Actions job fails on a transient (network, cache miss), retry once. Don't paper over a real CI failure to claim AC-2.
- The Co-Authored-By trailer is required for AI-assisted commits per CLAUDE.md style. Use the standard form (`Co-Authored-By: Claude <noreply@anthropic.com>`).
