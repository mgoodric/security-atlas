# 555 — Documentation freshness audit

**Issue:** OPENENGINE-555
**Date:** 2026-07-28
**Auditor:** OE-555 fire (engineer-as-collaborator)
**Product under audit:** release **1.18.0** (`.release-please-manifest.json`, tag `v1.18.0`)
**Scope:** the user-facing, operator, and developer doc surfaces — README, `docs-site/docs/**`, `docs/SELF_HOSTING.md`, `cmd/*/README.md`, top-level `*.md`, `CLAUDE.md` / `CONTEXT.md`, plus a contradiction-only read of `Plans/canvas/**` and `docs/adr/**`
**Disposition:** clear mechanical fixes applied in this PR. Design-intent contradictions and one shipped-code defect are flagged, not edited.

---

## Methodology

A doc claim only counts as verified if it was checked against a ground-truth
artifact in the tree, not against another doc:

| Claim class                    | Ground truth used                                                                 |
| ------------------------------ | --------------------------------------------------------------------------------- |
| Release / version strings      | `.release-please-manifest.json`, `git tag`, `web/package.json`, `go.mod`          |
| `/v1` endpoint names           | route literals under `internal/api/**` (~293 routes) + `just openapi-drift-check` |
| `atlas-cli` command signatures | the cobra `Use:` / `Flags()` declarations under `cmd/atlas-cli/`                  |
| `atlas-oscal` signatures       | `cmd/atlas-oscal/main.go` flag sets + its `usage()` text                          |
| `just` recipe names            | `just --summary`, diffed against every `just <recipe>` cited in docs              |
| Config / env-var names         | `deploy/docker/.env.example` + `just config-reference-drift-check`                |
| MCP tool surface               | `internal/mcp/testdata/tools.golden.json`                                         |
| Release binary names           | `.goreleaser.yaml` `builds:`                                                      |
| Observability behavior         | `internal/observability/otel/otel.go`, `deploy/observability/`                    |
| Internal links                 | script walk of every Markdown link, code fences stripped                          |
| Rendered-site integrity        | `just docs-build` (`mkdocs build --strict`)                                       |

Two rules from the prior docs audit (PR #1359) were respected throughout:
`docs/audit-log/**`, `docs/issues/**`, `_STATUS.md`, `_STATUS_HISTORY.md` and
`_events.jsonl` are archival by design and were not touched; the archived
mockups under `Plans/_archive/mockups/` are frozen and mockup-vs-`web/`
divergence was not treated as fileable.

---

## Inventory and per-surface verdict

| Surface                                              | Describes                                   | Verdict                                                        |
| ---------------------------------------------------- | ------------------------------------------- | -------------------------------------------------------------- |
| `README.md`                                          | Project front door, quickstart, feature map | **Fixed** — quickstart did not run as written (F-1)            |
| `docs-site/docs/` (40 pages)                         | The mkdocs user + operator guide            | **Fixed** — fabricated commands, a wrong route, 3 orphan pages |
| `docs-site/mkdocs.yml`                               | Site nav                                    | **Fixed** — 3 shipped pages were unreachable (F-5)             |
| `docs/SELF_HOSTING.md`                               | Single-VM self-host runbook                 | **Fixed** — observability + tag-pinning claims (F-6, F-7)      |
| `cmd/README.md`                                      | Binary build-target map                     | **Fixed** — stale table, wrong binary names (F-2)              |
| `cmd/atlas-mcp/README.md`                            | MCP server                                  | **Fixed** — tool counts stale after the read-surface widening  |
| `CLAUDE.md`                                          | Tech-stack table + invariants               | **Fixed** — frontend versions, CLI name, observability row     |
| `CONTEXT.md`                                         | Domain model notes                          | **Fixed** — one ADR link resolved from the wrong base          |
| `CONTRIBUTING.md`                                    | Contributor workflow                        | **Clean** — every cited `just` recipe and link resolves        |
| `GOVERNANCE.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md` | Project governance                          | **Clean** — no version or command claims to drift              |
| `docs/walkthroughs/**`                               | Generated walkthroughs (repo copy)          | **Fixed** — stale slice-doc and ADR filenames                  |
| `Plans/canvas/**`                                    | Design intent (design SoR)                  | **Read-only** — no contradiction against shipped code found    |
| `docs/adr/**`                                        | Decision records                            | **Read-only** — one numbering collision flagged (G-1)          |

---

## Fixes applied

**F-1 — the README quickstart did not run as written.** Three separate
faults in one block: `just build-go` is `go build ./...`, a compile check that
writes no binary, so `./bin/atlas` never existed; `cmd/atlas` has no `serve`
subcommand (`main()` runs the server directly, recognizing only
`--version`/`-v`/`version`); and `DATABASE_URL_APP` is required or the HTTP
server refuses to start (`cmd/atlas/main.go:781`). The `evidence push` flags
were also wrong — `--evidence-kind` and a bare `--payload` do not exist; the
real required set is `--kind --control --scope --observed-at --result
--payload --idempotency-key --actor-id`, and the CLI speaks gRPC on `:50051`,
not HTTP. Replaced with a sequence that works, plus an explicit note that
`build-go` is a compile check.

**F-2 — `cmd/README.md` described a repo that no longer exists.** The table
listed four binaries; six ship (`atlas-openapi` and `scripts` were missing).
It named the CLI `security-atlas`, which is the _server_ binary — per
`.goreleaser.yaml` the CLI is `security-atlas-cli`. It also closed on "slice
001 ships hello-world `main.go` files", true at slice 001 and false for ~1,500
slices since. Corrected, and the release note now says exactly which two
targets goreleaser builds rather than singling out `atlas-mcp`.

**F-3 — fabricated CLI commands across five docs-site pages.** Six code
blocks invoked `just atlas-cli <verb>`. There is no `atlas-cli` recipe in the
justfile, and none of `board-brief generate`, `board-pack generate`,
`walkthrough record`, `framework-import`, `catalog import` or
`oscal-export --period` exists on any binary. These were
documented-but-unshipped. Each was replaced with the surface that actually
ships: `/v1/board-briefs` and the generate → approve-per-section → publish
flow for board packs, `/v1/walkthroughs` + `:finalize`, `just import-soc2` /
`catalog import-crosswalk` for crosswalks, `atlas-oscal import-catalog` for
OSCAL catalogs, and `oscal-export --tenant-id --period-id --out`.

**F-4 — one wrong endpoint shape.** `first-audit.md` documented
`POST /v1/audit-periods/<id>:freeze`. The registered route is
`/v1/audit-periods/{id}/freeze` — a copy-paste of the colon-verb style used
by `:finalize` and `:push` elsewhere. The freeze call would have 404'd.

**F-5 — three shipped pages were unreachable in the rendered site.**
`tenant-membership.md`, `migration/oauth.md` and the new `cli.md` existed
under `docs-site/docs/` but appeared in no `nav:` entry, so the site rendered
them with no path to reach them. All three are now in the nav; the site now
has zero orphaned pages.

**F-6 — the observability claim overstated what ships.** README-adjacent docs
and the `CLAUDE.md` tech-stack row claimed "traces + metrics + logs, exported
by default" with a bundle at `deploy/docker/observability-compose.yml`. In
fact: OTEL is a **no-op** unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set
(`otel.go` returns no-op providers and logs "disabled"); only traces and
metrics are wired — the collector config states the logs pipeline is
"intentionally absent"; and the bundle lives at
`deploy/observability/docker-compose.yml`, shipping OTel Collector +
Prometheus + Tempo while expecting a pre-existing Grafana and Loki. Both docs
now say this.

**F-7 — stale pre-1.0 tag-pinning examples.** The image-pinning tables in
`docs-site/docs/upgrade.md` and `docs/SELF_HOSTING.md` recommended `:0.3` for
production and used `0.3.x` / `0.4` / `:0.3.5` throughout. On a 1.18.0
product this reads as if the project were eight minors behind. Retargeted to
the shipped series (`:1.18`, `:1.18.0`, `1.19`); the semantics of the table are
unchanged.

**F-8 — MCP tool counts stale after the read surface widened.**
`cmd/atlas-mcp/README.md` said "six read-only tools … eleven tools total" and
described write tools as future work gated on a pending slice.
`tools.golden.json` holds fifteen: ten read (the six original plus
`list_policies`, `list_vendors`, `list_exceptions`, `list_action_plans`) and
five write (`push_evidence`, `create_risk`, `update_control_state`,
`update_risk_treatment`, `confirm_write`). Counts and the soak note corrected;
the experimental status is unchanged because it is still accurate.

**F-9 — broken and stale cross-references.** `CONTEXT.md` linked
`../adr/0003-…` from the repo root, which resolves outside the repo. Four
walkthrough pages (both the `docs/` and `docs-site/` copies) pointed at
`0003-audit-period-freeze-hash.md` and at five `docs/issues/` slice docs under
names none of them carry (e.g. `012-eval-engine.md` for
`012-control-state-evaluation.md`). All retargeted to real files.

**F-10 — no CLI reference existed.** `security-atlas-cli` registers fourteen
top-level command groups (`root.go:124-137`) and was documented only in
scattered per-topic snippets. Added
`docs-site/docs/cli.md`: global flags and their env vars, the full command
tree, and per-group flag detail for `evidence`, `credentials`, `catalog`,
`controls`, `features`, `keys`, `oscal`, `demo` and `login`. Every flag,
default and env var in it was read off the cobra declarations
(`--page-size` 1000, `--timeout` 15m, `--scale` 0.1–5.0,
`ATLAS_HTTP_ENDPOINT` defaulting to `http://localhost:8080`, and the
`ATLAS_ENABLE_DEMO_SEED` exact-lowercase-`true` gate).

---

## Verification

| Check                                                  | Result                                                        |
| ------------------------------------------------------ | ------------------------------------------------------------- |
| `just docs-build` (`mkdocs build --strict`)            | Passes, no warnings (18.3s)                                   |
| Internal links, top-level `*.md` + `docs-site/docs/**` | 47 files scanned, **0 broken**                                |
| Internal links, `docs/` + `cmd/**` READMEs             | 13 files scanned, **0 broken**                                |
| Orphaned docs-site pages                               | 0 (was 3)                                                     |
| `just config-reference-drift-check`                    | No drift — 51 variables, 26 active + 25 opt-in match the page |
| `just openapi-drift-check`                             | No drift — 293 routes, spec matches generator output          |
| `just <recipe>` cited in docs vs `just --summary`      | Every cited recipe exists                                     |
| `atlas-cli` subcommands cited in docs                  | All 27 resolve to real commands                               |

---

## Flagged — not edited

**G-1 — ADR slot 0003 is double-occupied.** `0003-audit-period-freeze-hash-inputs.md`
and `0003-oauth-authorization-server.md` both hold slot 0003, so "ADR-0003" is
ambiguous in prose and eight docs cite one or the other by full filename.
This is _known_ — ADRs 0007, 0010, 0016 and 0017 each carry an explicit slot
note declining to touch it. Renumbering breaks inbound links including from
`CLAUDE.md` and the docs site, so which one moves (and whether a stub is left
behind) is a maintainer call, not a docs-pass fix. Left as-is; raised so the
decision is on the record rather than re-deferred per ADR.

**G-2 — no design-intent contradiction found.** `Plans/canvas/**` was read for
statements contradicted by shipped code and none surfaced; the canvas holds no
version or command claims to drift, which is why the tech-stack drift landed in
`CLAUDE.md` instead. Nothing in the canvas was rewritten.

**G-3 — the "Push evidence" CTA in `web/` links to a route the app does not
serve (shipped defect, filed separately).** `web/app/(authed)/evidence/push-cta.ts`
sets `PUSH_CTA_HREF = "/docs/primitives/evidence#pushing-evidence-from-your-own-tools"`,
rendered on the `/evidence` page as a same-origin `<a target="_blank">` in two
places (`page.tsx:631`, `page.tsx:665`). The web app has no `/docs` route —
`web/app/docs/` does not exist and no docs base URL is configured — so both
links open a 404. The docs page itself is fine: the heading is live at
`docs-site/docs/primitives/evidence.md:97`. The doc is right and the link is
wrong, so per this audit's charter it was not "fixed" by editing the doc.
`push-cta.test.ts:47` asserts `startsWith("/docs/")`, so the fix must update
the test alongside the href. Filed as a bug; the correct target is the
published docs-site URL.

---

## Coverage limits

Highest-traffic surfaces were prioritized per the issue's guidance. Not
audited in this pass, and worth a follow-up if the long tail matters:
`docs/operator/**`, `docs/runbooks/**`, `docs/architecture/**`,
`docs/spec/**` and `docs/getting-started/**` (repo-internal docs, absent from
the rendered site), and external-link liveness beyond the sampled set. No
finding in this pass suggested those surfaces are unusually stale — they were
simply out of the time box.
