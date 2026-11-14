# Issue #1771 — Crosswalk and OSCAL component imports declare their SCF release

Part of #1718 (release-aware SCF anchors), after the resolver in #1770. Each
import that turns an SCF code into an anchor now names the SCF release the
code belongs to, and binds within that release only:

| Import                     | Where the release comes from                                   |
| -------------------------- | -------------------------------------------------------------- |
| Crosswalk YAML             | top-level `scf_release`                                        |
| Wide SCF workbook          | the `SCF <release>` control sheet's name                       |
| SCF OLIR STRM workbook     | `atlas-cli catalog import-crosswalk --scf-release`             |
| OSCAL component-definition | `atlas-oscal --scf-release`, or an `scf-release` metadata prop |

A declared release that is not loaded is `scfrelease.ErrUnknownRelease`. A
crosswalk code missing from its declared release is
`scfrelease.ErrCodeNotInRelease`. A declared release never falls back to the
current one. With no release declared, both imports bind in the current
release exactly as before and report a deprecation warning, until #1774
makes the declaration required.

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The bug found during the work: three existing workbook integration tests
(`TestImport_SCFWorkbookCreatesCommunityDraftEdges`,
`TestImport_WorkbookPointsOfFocusRollUp`,
`TestImport_FrozenVersionKeepsTitlesAndAddsEdges`) build workbooks with an
`SCF 2026.2` or `SCF 2026.3` sheet against a database that holds only the
sample catalog. Their sheets now declare a release that is not loaded, so the
import is refused, which is the intended contract. They test edges and titles,
not releases, so they now bind to the loaded sample release through
`bindToLoadedSCF`; the sheet binding has its own test.

## D1 — OLIR STRM workbooks declare their release by flag

**Decision.** An OLIR STRM workbook takes its release from
`--scf-release`. Without the flag it declares none and falls back with the
warning.

**Why.** The ticket says both workbook shapes derive the release from the
`SCF <release>` sheet name. An OLIR workbook has no such sheet: the loader
reads a workbook that has one as the wide shape (#1916). Guessing the release
from the OLIR "General Information" sheet would rest on a field layout no
fixture in this repo shows. The flag is explicit, and the same rule (the sheet
wins; a flag that contradicts it is refused) covers both shapes. The flag
also fills an undeclared YAML crosswalk's release, and is refused when it
contradicts a declared one.

## D2 — An OSCAL claim whose control id is not in the release stays unmapped

**Decision.** For a component-definition, an unknown release fails the import
(`ErrUnknownRelease`), but a control id that is not in the declared release
does not. The claim imports with `scf_anchor_id` NULL, flagged for operator
mapping, as it did before.

**Why.** A vendor's component-definition targets its own catalog's control ids
(`AC-2`, `IA-2(1)`). Only some of them coincide with SCF codes, and slice 512
imports the rest unmapped rather than dropping them. Turning a non-matching id
into an error would refuse almost every real vendor document. The named
error applies to the crosswalk, where every `scf_anchor` must be an SCF code.

## D3 — Claims record the release they were bound in

**Decision.** `imported_component_claims` gains `scf_release TEXT NULL`
(migration `20261028000000`). The importer writes the release label on every
mapped claim, including the current release's label when none was declared.
The operator re-map (`MapScfAnchor`) validates the code in the current release
and records that release. A CHECK keeps `scf_release` NULL whenever
`scf_anchor_id` is.

**Why.** `scf_anchor_id` is a free-form code, not an anchor uuid (slice 512).
Without the release beside it, a claim bound to release A reads as release B's
meaning the moment B is current, which is the silent re-binding this spec
removes. Existing rows are not backfilled: the release they were bound in is
not knowable after the fact.

The claim read endpoints do not expose `scf_release` yet; nothing in this
ticket asks for it, and the remap queue (#1775) is where readers of a claim's
release arrive.

## D4 — The document prop is read on the Go side

**Decision.** The `scf-release` prop is read from
`component-definition.metadata.props` by decoding the inbound JSON in Go,
matching on the prop name regardless of `ns`. Two props that disagree, or a
flag that disagrees with the prop, are `ErrSCFReleaseConflict`, before the
bridge is called.

**Why.** The bridge's projection carries no document props, and adding one
means a proto change and a Python change for a single string. The decoding
only reads a field; nothing is dereferenced, so the slice 512 boundary that
the bridge never follows an `href` is untouched.
