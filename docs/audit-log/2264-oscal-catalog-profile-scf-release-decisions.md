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

- `detection_tier_actual`: manual_review
- `detection_tier_target`: manual_review

The bug found during the work: the first version let an unloaded prop
release fall back to the current release for catalog and profile imports
(see D2). PR review caught it; the integration tests now expect
`ErrUnknownRelease`.

## D1 — One resolver for the three OSCAL importers

**Decision.** The component import's declaration reader and anchor loader
move to a new package, `internal/oscal/scfbind`. `Declared(importer, flag,
doc, root)` reads the flag and the `scf-release` prop under the document's
top-level key (`catalog`, `profile`, `component-definition`), and prefixes
a conflict error with the importer's name. `Bind` picks the release through
`scfrelease.Lookup` and loads its codes. Each importer's
`ErrSCFReleaseConflict` is `scfbind.ErrConflict`, so `errors.Is` holds across
all three.

**Why.** The three imports match control ids the same way, and a prop means
the same thing whichever importer reads it. Two more copies of the reader and
loader would drift. The component import's behaviour is unchanged, including
its conflict error text (`componentimport: conflicting SCF release
declarations: ...`); its #1771 integration tests pass untouched.

## D2 — A declared release that is not loaded fails, flag or prop

**Decision.** For catalog and profile imports, as for component imports, a
release declared by `--scf-release` or by the document's `scf-release` prop
that is not loaded is `scfrelease.ErrUnknownRelease`, and nothing is
persisted. Neither falls back to the current release.

**Why.** The ticket asks to follow the #1771 D5 fallback. D5 falls back
only for a release _inferred_ from a workbook's `SCF <release>` sheet name,
which no one wrote down as a declaration. Every explicit declaration stays
strict: `--scf-release` and a YAML crosswalk's `scf_release` alike. An OSCAL
`scf-release` prop is written into the document on purpose, so it is an
explicit declaration, which is why #1771 made the component import's prop
strict. Applying D5 here therefore means no fallback, and the shared
resolver treats the prop the same for all three importers. The first version
of this PR read D5 the other way and let a prop fall back for catalog and
profile imports only; review caught it.

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
