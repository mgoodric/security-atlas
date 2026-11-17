# Issue #2264 — OSCAL catalog and profile imports declare their SCF release

Follows #1771 (#2218), which made crosswalk and OSCAL component-definition
imports name the SCF release their codes belong to. The OSCAL catalog and
profile imports still matched control ids against whichever SCF release
was current. They now declare a release the same way the component import
does:

| Import                     | Where the release comes from                                                              |
| -------------------------- | ----------------------------------------------------------------------------------------- |
| OSCAL catalog              | `atlas-oscal import-catalog --scf-release`, or an `scf-release` prop                      |
| OSCAL profile              | `atlas-oscal import-profile --scf-release`, or an `scf-release` prop on the entry profile |
| OSCAL component-definition | unchanged from #1771                                                                      |

With no release declared, both match in the current release as before and
report a deprecation warning (the expand step). #1774 is the contract step.

- `detection_tier_actual`: none
- `detection_tier_target`: none

No bug surfaced during the work.

## D1 — One resolver for the three OSCAL importers

**Decision.** The component import's declaration reader and anchor loader
move to a new package, `internal/oscal/scfbind`. `Declared(flag, doc, root)`
reads the flag and the `scf-release` prop under the document's top-level
key (`catalog`, `profile`, `component-definition`). `Bind` picks the release
through `scfrelease.Lookup` and loads its codes. Each importer's
`ErrSCFReleaseConflict` is `scfbind.ErrConflict`, so `errors.Is` holds across
all three.

**Why.** The three imports match control ids the same way. Two more copies
of the reader and loader would drift. The component import's behaviour is
unchanged: its #1771 integration tests pass untouched. The one visible
change there is the conflict error's prefix, which now reads
`oscal import:` instead of `componentimport:`.

## D2 — A prop naming an unloaded release falls back; the flag does not

**Decision.** For catalog and profile imports, a release named only by the
document's `scf-release` prop that is not loaded matches in the current
release and reports `scfbind.PropReleaseNotLoadedWarning`. The warning names
the release and the remedy: load it with `atlas-cli catalog import-scf`, or
pass `--scf-release` to refuse the import instead. A `--scf-release` that
is not loaded is `scfrelease.ErrUnknownRelease`, including one that agrees
with the prop.

**Why.** The ticket asks for the #1771 D5 fallback. D5 drew the line between
the document's statement (a workbook's sheet name), which falls back, and
the operator's (`--scf-release`), which stays strict. A metadata prop is the
document's statement, so it takes the same side. The component import keeps
its #1771 rule that a prop is strict: changing it is out of this ticket's
scope, and `scfbind.Policy.PropFallsBack` keeps the two explicit rather
than implied.

## D3 — For a profile, only the entry profile declares

**Decision.** The release comes from the entry profile's metadata.
Intermediate profiles and supplied catalogs are not read for a prop.

**Why.** The control ids persisted are the entry profile's resolved
baseline, and the entry profile is the document being imported (its hash is
the provenance hash). Reading props from inputs to resolution would raise
questions the ticket does not ask, such as what two catalogs naming two
releases mean.

## D4 — The release is recorded in the report and audit row, not per control

**Decision.** `Report.SCFRelease`, the `--json` output and the success
audit row's `detail.scf_release` carry the release matched in.
`imported_catalog_controls` gains no `scf_release` column.

**Why.** #1771 D3 added the column to `imported_component_claims` because a
claim can be re-mapped later by an operator. An imported catalog or profile
control is matched once, at import, and every control in one import shares
the release, so the import's audit row already says which release its
`scf_anchor_id` values belong to. A per-row column needs a migration that
the acceptance criteria do not call for; if a remap surface for imported
catalog controls lands, it is the place to add one.
