# 058 — user docs scaffold — decisions log

Slice 058 is `Type: JUDGMENT` — docs authorship. This log records the
subjective build-time judgment calls made while landing the mkdocs
Material scaffold plus the five core pages, in the JUDGMENT-slice format
(Decisions made · Revisit once in use · Confidence per decision) so the
maintainer can re-evaluate them once the docs site is in real use. It
does NOT block merge.

## Decisions made

### 1. mkdocs Material is the docs generator — and how it is invoked

**Chosen: mkdocs Material**, invoked in isolation via
`uv tool run --with-requirements docs-site/requirements.txt --from
mkdocs-material mkdocs ...`. The slice's narrative + AC-8 pre-committed
to the generator choice; this decision records the **invocation
shape** and the **pinning posture**.

The repo already uses `uv` as its canonical Python toolchain
(`pyproject.toml` + `uv.lock`; `oscal-bridge/` is the precedent in
`tool.uv.workspace`). Two invocation shapes were considered:

- **(A)** Add `mkdocs-material` as a `[tool.uv]` dev dependency of the
  monorepo root or `oscal-bridge`. Rejected: docs is not the
  oscal-bridge's concern; adding it to the monorepo root mixes a docs
  build dependency into the application's locked dependency graph.
- **(B)** A separate `docs-site/requirements.txt` invoked through
  `uv tool run --with-requirements`. **Chosen.** `uv tool run` creates
  an isolated, cached environment per requirement set; the docs build
  cannot pollute the monorepo's `uv.lock`, and the dependency pins are
  a self-contained `docs-site/requirements.txt` that's trivial to bump.

Pinned versions:

- `mkdocs-material==9.5.39` — most recent stable as of 2026-05.
- `mkdocs-git-revision-date-localized-plugin==1.2.6` — supplies the
  "Last updated" trust-signal date on every page.

**Confidence: high.** The invocation shape is the closest precedent to
how the repo already runs Python tooling (`uv tool run ruff`,
`uv tool run pytest` patterns in CI). No new toolchain.

### 2. `docs-site/` is the docs root — not `docs/` proper

**Chosen:** `docs-site/` rather than putting mkdocs pages alongside the
existing `docs/` (`docs/adr/`, `docs/audit-log/`, `docs/issues/`,
`docs/getting-started/first-evidence.md`, etc.).

The existing `docs/` is a **mixed contributor-facing surface** —
ADRs, audit logs, issue tracker companion files, getting-started prose,
ship-readiness records. Aiming mkdocs at `docs/` directly would force
either (a) a `docs_dir`-then-`include`-pattern dance to ignore the
contributor-only subtrees, or (b) renaming half the repo's internal
docs. Both are friction. A separate `docs-site/` tree gives the public
docs site a clean root, leaves the internal docs untouched, and makes
`mkdocs build --strict`'s "unrecognized files" guard meaningful.

The trade: a contributor refreshing `docs/getting-started/first-evidence.md`
must also check `docs-site/docs/install.md` if the install workflow
changed. The PR template's new "Docs impact" section (AC-6) is the
mitigation.

**Confidence: medium-high.** The split is right for v1; the long-term
choice is between this shape and "promote the internal docs into the
public site". That choice can be made later from data — for now, ship
the public site and learn what's missing.

### 3. AC-7 — ship-gate skill + PR template durable surface

**The finding:** the ship-gate skill physically lives in
`~/.claude/plugins/cache/claude-code-skills/engineering-advanced-skills/2.4.4/skills/ship-gate/`
— a global plugin cache, not a repo-versioned artifact. An edit to its
`references/checks.md` is non-durable: the next plugin update
overwrites the cache.

**Two-part chosen resolution:**

- **(a)** Added `DOCS-01` (advisory) to the global plugin's
  `references/checks.md` under a new `## DOCS: User Documentation`
  section. This makes AC-7 literally hold in this session — a
  ship-gate run on this repo will now include the docs-page check.
- **(b)** The repo-durable enforcement is the new "Docs impact"
  section in `.github/PULL_REQUEST_TEMPLATE.md` (AC-6). That section
  forces the PR author to pick exactly one of two options: "Updated
  docs pages: <list>" OR "No doc change needed because: <reason>". The
  reviewer sees the choice on every PR.

The constitutional question — "does the slice make a CI hard-block out
of docs?" — answers cleanly: no. Anti-criterion P0 ("Do NOT block PR
merges on docs updates via CI") is honoured. The advisory gate operates
at the reviewer-discipline layer (PR template) plus the optional
ship-gate scan; merge-friction is zero.

**Confidence: high** on (a) (additive, minimal, format matches the
existing OBS section). **Medium** on (b) — reviewer discipline drifts
over time, but the PR template is the canonical artifact for that
discipline and is itself reviewed when it changes.

### 4. AC-10 — slice-057 screenshots referenced as TODO placeholders

**Pre-cleared by the calling prompt and the slice narrative.** Slice
057 (README screenshots + animated GIFs) is `not-ready`, waiting on
slice 043. The screenshots that AC-10 wants to embed do not exist on
disk yet.

**Chosen:** each of `index.md`, `framework-setup.md`, `first-audit.md`,
and `board-reporting.md` carries an HTML comment of the form

```md
<!-- TODO(slice-057): hero screenshot from
     docs/images/hero-dashboard.png once slice 057 merges. -->
```

at the head of the page. The placeholder is invisible in the rendered
site, does not break `mkdocs build --strict`, and gives the eventual
slice-057 (or post-057) follow-up an obvious search target
(`rg "TODO(slice-057)"`).

**Confidence: high.** Same forward-reference pattern the calling prompt
endorsed; explicitly captured in "Revisit once in use" below.

### 5. Page authorship sourcing — canvas + existing repo artifacts, not imagination

Each page sources its claims from a specific canvas section + a
specific repo artifact:

| Page                 | Primary canvas source                                   | Repo artifact source                                                           |
| -------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `index.md`           | canvas §1 (vision) + §3 (UCF)                           | `CLAUDE.md` constitutional invariants                                          |
| `install.md`         | canvas §10.1 (Self-host row)                            | `docs/getting-started/first-evidence.md` phases 1–2 + `.env.example` env table |
| `framework-setup.md` | canvas §3 + §3.2 (STRM)                                 | `CONTEXT.md` coverage section (slice 008) + slice 006 importer pattern         |
| `first-audit.md`     | canvas §8.1–8.5 (audit workflow)                        | `CONTEXT.md` AuditPeriod section (slice 028) verbatim terminology              |
| `board-reporting.md` | canvas §10.1 (board reporting row) + AI-assist boundary | slice 031 + 032 monthly-brief / quarterly-pack issue docs                      |

Wherever a page mentions a CLI command, an HTTP endpoint, or a
configuration key, the shape is taken from the canonical source — never
invented. Where a feature is `v1` vs `v2/v3`, the page says so honestly.

**Confidence: high.** This is the entire reason CONTEXT.md exists.

### 6. Edit-on-GitHub link wiring — `edit_uri: edit/main/docs-site/docs/`

**Chosen:** the standard GitHub edit-URL shape, scoped to the
`docs-site/docs/` subtree. This makes every "Edit this page" link
resolve to e.g.
`https://github.com/mgoodric/security-atlas/edit/main/docs-site/docs/install.md`.

`repo_url` is set explicitly to `https://github.com/mgoodric/security-atlas`
so mkdocs Material can render the GitHub-icon repo link in the header.

**Confidence: high.** This is the mkdocs Material reference pattern.

### 7. GitHub Pages deploy — release-tag-triggered, not main-push-triggered

**Chosen:** the deploy job in `.github/workflows/docs-publish.yml` runs
ONLY on tag push matching `v*.*.*`; the build job runs on every PR and
every push to `main` (for early failure signal) but uploads the Pages
artifact only on tag push.

The reasoning is the same as why
`.github/workflows/container-publish.yml` and the GoReleaser pipeline
are tag-triggered: the published documentation tracks releases, not the
tip of `main`. A user clicking the docs link expects to see the docs
that match the latest released version, not whatever shipped 20 minutes
ago.

A future slice can add a `latest` channel by switching the trigger to
`main` if user demand surfaces. The cost of changing this is
trivial — one workflow file.

**Confidence: high.** Matches the existing release-pipeline cadence;
explicitly stated in the workflow comments.

### 8. Permissions — least-privilege deploy job, read-only workflow default

**Chosen:** `permissions: contents: read` at the workflow level (the
narrowest possible default); the `deploy` job widens to exactly
`contents: read, pages: write, id-token: write` — the minimum set
`actions/deploy-pages@v4` requires. The `build` job inherits the
workflow-level `contents: read` only.

This is the pattern recommended by the GitHub Pages official action
and matches the slice's security-review note ("review that GITHUB_TOKEN
is least-privilege"). No long-lived secrets are referenced.

**Confidence: high.**

### 9. `git-revision-date-localized` plugin — `fetch-depth: 0` in CI

**Chosen:** the docs-publish workflow's checkout step explicitly sets
`fetch-depth: 0`. The plugin reads each page's last-commit timestamp
from git history; on a shallow clone the plugin either errors under
`--strict` or falls back to the build date silently. The trade is a
slightly slower checkout (~5–10 s for this repo's history) for the
"Last updated" trust signal on every page — easily worth it for docs.

**Confidence: high.**

### 10. No `_INDEX.md` modification — slice doc captured this; verified

Per the parent prompt + the slice's spillover policy: `_INDEX.md` is
NOT modified by this slice. The slice doc is `docs/issues/058-user-docs-scaffold.md`
on its own — appending a row to `_INDEX.md` would be scope creep into
batch orchestration territory. The slice status flip lives in
`_STATUS.md` (the live tracker), which is the correct surface.

**Confidence: high.**

### 11. CHANGELOG entry — under [Unreleased] / Added, single bullet

**Chosen:** one consolidated bullet rather than per-file entries.
Matches the existing CHANGELOG.md pattern (the slice-038 Helm-chart
entry under `## [Unreleased]` / `### Added` is the closest precedent —
single bullet, multi-line, describes the slice as a single delivery).

**Confidence: high.**

## Revisit once in use

- **Decision 1 (mkdocs Material + uv invocation):** if `uv tool run`
  starts to feel like overhead in CI (the plugin install runs on every
  PR), consider caching the uv tool environment or pinning a single
  Docker image. Current cost is ~30 s on a warm runner — acceptable.
- **Decision 2 (`docs-site/` separate from `docs/`):** revisit when
  the public docs site has 15+ pages or when a contributor refreshing
  `docs/getting-started/first-evidence.md` repeatedly forgets to
  cross-update `docs-site/docs/install.md`. The merge candidate would
  promote `docs/getting-started/*` into `docs-site/docs/` and rewrite
  the in-repo links.
- **Decision 3 (ship-gate global edit):** the durable answer is to move
  the docs check into a repo-local ship-gate companion (e.g.,
  `docs/SHIP_GATE_CHECKS.md`) that the global ship-gate skill discovers
  via a `.shipgate.yml` convention. That convention does not exist
  yet; once the project ships one, port `DOCS-01` into the repo.
- **Decision 4 (slice-057 TODO placeholders):** once slice 057 merges,
  a follow-up slice (or a single docs-only PR) replaces each
  `<!-- TODO(slice-057): ... -->` comment with the actual `<img>` or
  `<picture>` element pointing at the captured asset. `rg "TODO(slice-057)"`
  surfaces the four call sites.
- **Decision 7 (release-tag-only deploy):** if users start complaining
  that the published docs lag behind a feature they're consuming off
  `main`, add a `main` → `latest/` channel by promoting the build
  artifact on `main` push and adjusting the `actions/deploy-pages`
  step to a per-branch path. Not v1 work.
- **GitHub Pages enable:** Pages must be enabled in the repo settings
  (Settings → Pages → Source: GitHub Actions) before the first
  release-tag deploy can land. This is a one-time maintainer step that
  is NOT automated by this slice — recorded for the post-merge
  follow-up checklist.
- **`docs/SELF_HOSTING.md` cross-link sync:** the production-host
  prose lives at `docs/SELF_HOSTING.md` in the repo, and `install.md`
  links out to it. If the file moves or splits, update the link in
  `docs-site/docs/install.md`.

## Confidence per decision

| #   | Decision                                            | Confidence            |
| --- | --------------------------------------------------- | --------------------- |
| 1   | mkdocs Material + `uv tool run` invocation          | high                  |
| 2   | `docs-site/` as a separate tree from `docs/`        | medium-high           |
| 3   | ship-gate global edit + PR template durable surface | high (a) / medium (b) |
| 4   | slice-057 screenshots as TODO placeholders          | high                  |
| 5   | Page authorship sourced from canvas + CONTEXT.md    | high                  |
| 6   | `edit_uri: edit/main/docs-site/docs/`               | high                  |
| 7   | Release-tag-only Pages deploy                       | high                  |
| 8   | Least-privilege workflow + per-job permissions      | high                  |
| 9   | `fetch-depth: 0` for git-revision-date plugin       | high                  |
| 10  | No `_INDEX.md` modification                         | high                  |
| 11  | Single consolidated CHANGELOG bullet                | high                  |
