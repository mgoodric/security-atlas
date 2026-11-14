# #1730 — Workbook requirement titles and frozen-version re-imports

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1730 (adopter finding, 2026-11-12)
**Code:** `internal/api/soc2import/titles.go`, `internal/api/soc2import/import.go`, `data/crosswalks/embed.go`
**Date:** 2026-11-13

## Detection-tier classification

- `detection_tier_actual`: `production`
- `detection_tier_target`: `integration`

An adopter found both defects on a first-run setup. No integration test
re-imported a workbook into a version that already existed. That test is
the one that would have caught the trigger rollback. The new
`TestImport_FrozenVersionKeepsTitlesAndAddsEdges` fails against the old code
with the trigger's own error and passes with the fix.

## D1 — The bundled crosswalks are embedded in the binary

The workbook loader looks up titles by framework slug, so it needs the
bundled YAML at runtime. `atlas-cli` runs from the bootstrap image, where
`data/crosswalks/` is at `/repo`, but also as a bare binary. A
`//go:embed *.yaml` package next to the files (`data/crosswalks`, the
`catalogs/metrics` pattern) works in both places and stays in step with the
files. The bootstrap script still imports the SOC 2 crosswalk by path. Its
`*.yaml` glob doesn't match the new `embed.go`.

## D2 — The bundled match is on slug only, not slug and version

A workbook's version (`2026.3`, `2022`) seldom matches the bundled file's
(`2017`). A criterion's code and meaning are stable across the versions the
SCF maps, so a title keyed on slug and code is correct, and keying on the
version as well would find nothing in practice.

## D3 — The code-only gate lives in the loader, not the CLI

The ticket requires that "writes nothing" holds whichever way the import
is called, so `LoadSCFWorkbook` returns `CodeOnlyTitlesError` itself and
`SCFWorkbookOptions.AllowCodeTitles` is the opt-out. Existing loader tests
for fixture slugs with no bundled crosswalk now set `AllowCodeTitles`. They
test sheet and column selection, not titles.

## D4 — A point of focus takes its parent's title and no body

The parent's body describes the criterion, not the point of focus, so
copying it would claim text the point of focus doesn't have. If the parent
has no title source, the point of focus counts as code-only too: a
parent's bare code is no better than its own. #1731 rolls points of focus
up into their parent. Once it lands, this rule only matters for the OLIR
path, if at all.

## D5 — "Frozen" mirrors the trigger's status set

`isFrozenVersionStatus` uses the same `current`/`legacy`/`withdrawn` set as
`enforce_framework_requirement_immutability`. It reads the status after the
version upsert, which is what the trigger sees. A unit test fails if a new
enum value (a future draft stage) isn't classified. Differing requirements
are counted in `RequirementsDiffered`, not in `RequirementsUnchanged`, so the
summary still shows that the incoming file disagreed.

## D6 — YAML imports get the frozen-version behaviour too

Step 4 of the ticket says "frozen versions are never updated", which covers
every import path. A YAML re-import with an edited title used to fail on the
trigger. It now keeps the stored text and reports the code. No existing test
relied on the old failure.
