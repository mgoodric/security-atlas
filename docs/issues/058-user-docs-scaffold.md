# 058 — User docs scaffold + 5 core pages

**Cluster:** Infra
**Estimate:** 3d
**Type:** JUDGMENT

## Narrative

Build the public-facing documentation site for security-atlas. Resolves the canvas open question on docs generator: **mkdocs Material** is the choice — lighter than Docusaurus, fewer JS dependencies, common in OSS Python/Go projects, ships with built-in dark mode and excellent search.

Five core pages ship in this slice:

1. **Intro** — what is security-atlas, who it's for, what it isn't.
2. **Install** — self-host quickstart (docker-compose path from slice 037), prerequisites, first-boot config.
3. **Framework setup** — how to load the SCF catalog + a framework version (SOC 2 first), what crosswalk looks like.
4. **First audit** — walkthrough from creating an AuditPeriod → generating samples → recording walkthroughs → OSCAL SSP export.
5. **Board reporting** — generating the monthly brief and quarterly pack, what the board sees.

The slice also adds a **per-PR docs gate** to the ship-gate checklist: any PR touching user-facing surfaces (frontend views, public API, CLI commands, configuration) must update the relevant docs page or explicitly note "no doc change needed" in the PR body. Enforcement is via PR template + reviewer discipline, not a CI hard-block (avoid merge friction for genuinely no-doc PRs).

## Acceptance criteria

- [ ] AC-1: `docs-site/` directory present with mkdocs.yml configured: theme = `material`, palette = light+dark, navigation collapsible, search enabled, edit-on-github links wired.
- [ ] AC-2: `mkdocs build --strict` succeeds locally and in CI — `--strict` catches broken internal links and missing nav entries.
- [ ] AC-3: Five core pages present at `docs-site/docs/`:
  - `index.md` — Intro (what, who, not)
  - `install.md` — Self-host install (docker-compose quickstart)
  - `framework-setup.md` — SCF + SOC 2 crosswalk
  - `first-audit.md` — End-to-end audit workflow walkthrough
  - `board-reporting.md` — Monthly brief + quarterly pack
- [ ] AC-4: Each page includes: a "What you'll learn" summary, body content, "Next steps" link to the related page, and a "Was this helpful?" footer (GitHub Discussions link, no JS analytics).
- [ ] AC-5: `.github/workflows/docs-publish.yml` builds + deploys to GitHub Pages on every release tag. PR builds run `mkdocs build --strict` and post a preview URL via the GitHub Pages preview action.
- [ ] AC-6: `.github/PULL_REQUEST_TEMPLATE.md` (created in slice 050 AC-8) gets a new section: "Docs impact" with two options — "Updated docs pages: <list>" or "No doc change needed because: <reason>". Reviewer discipline; not CI-enforced.
- [ ] AC-7: `ship-gate` skill's checklist updated to include "Docs page updated or explicitly no-change-needed" as a gate item (advisory, not blocking).
- [ ] AC-8: Resolves canvas open question `Docs site generator` — `Plans/canvas/11-open-questions.md` updated to mark this question resolved with the mkdocs Material decision and link to this slice.
- [ ] AC-9: Verification: a fresh contributor can clone the repo, run `just docs-serve`, and view all 5 pages locally within 60 seconds. Deployed site loads at `<owner>.github.io/security-atlas/` after the first release tag.
- [ ] AC-10: Embedded screenshots from slice 057 are referenced in the relevant pages (hero on `index.md`, dashboard on `framework-setup.md`, audit workspace on `first-audit.md`, board pack on `board-reporting.md`) — visual continuity between README and docs.

## Constitutional invariants honored

- **Working norms — Markdown over prose** (CLAUDE.md) — docs pages use tables, lists, short paragraphs; no walls of text
- **Working norms — Cite sources** (CLAUDE.md) — install instructions reference specific versions; framework setup cites SCF + NIST IR 8477; board reporting cites canvas §7 + §10
- **AI-assist boundary (product, unchanged):** the constitutional boundary governs _audit-binding artifacts at runtime_ — questionnaire answers, SSP narratives, board-report sections. User-docs pages are not audit-binding artifacts; they are dev-process content authored under the `JUDGMENT`-slice model (Claude authors, writes a decisions log, the maintainer iterates post-publish from the revisit list). The two are distinct — see `Plans/prompts/04-per-slice-template.md` "Slice types".

## Canvas references

- `Plans/canvas/10-roadmap.md §10.1` — v1 acceptance criteria informs which features are documented (and which are deferred)
- `Plans/canvas/11-open-questions.md` — `Docs site generator (mkdocs Material vs Docusaurus)` question resolved by this slice
- `Plans/canvas/08-audit-workflow.md` — first-audit.md page maps to canvas section content

## Dependencies

- **005** (frontend bootstrap) — docs reference UI views that need to exist
- **050** (public release readiness) — license, README, and security policy must be in place before docs site goes public; PR template exists for AC-6 modification

## Anti-criteria (P0)

- Do NOT block PR merges on docs updates via CI — advisory gate only (AC-7). Hard-gating creates merge friction that erodes the discipline rather than reinforcing it.
- Do NOT use a docs generator that requires a Node toolchain just for docs builds — mkdocs Material is Python-only and matches the repo's existing toolchain (uv).
- Do NOT publish docs that contradict the canvas — the canvas is the architectural source of truth; docs are user-facing reads of that truth.
- Do NOT include the maintainer's name in docs page authorship (cross-references slice 050 sanitization).
- Do NOT auto-generate "What's New" / "Latest Releases" pages from release-please without a human-edit pass — release notes are user-facing communication.

## Skill mix (3–5)

- `tdd` (AC-9 verification: fresh-contributor workflow tested as integration)
- `engineering-advanced-skills:codebase-onboarding` (the `install.md` and `framework-setup.md` pages are essentially codified onboarding paths)
- `engineering-advanced-skills:runbook-generator` (per-page structure follows runbook conventions: prereqs, steps, verification, troubleshooting)
- `engineering-advanced-skills:changelog-generator` (release-notes integration with the docs site)
- `simplify` (each page's prose should be ruthlessly compressed — users skim, not read)

## Notes for the implementing session

- mkdocs Material has built-in syntax highlighting, admonitions, code tabs, and content tabs — use them; don't reinvent. The `mkdocs-material` package handles dark mode out of the box.
- Use the `mkdocs-git-revision-date-localized-plugin` so pages show last-updated date — feeds the "is this stale?" trust signal users look for.
- Reserve `docs-site/docs/api/` for auto-generated API docs (future slice; not in v1 scope) — don't manually author API references in v1.
- After this slice merges, every future feature slice should grow an AC: "User-facing change → relevant docs page updated." Codify this in the per-slice template's quality gates if it becomes friction.
