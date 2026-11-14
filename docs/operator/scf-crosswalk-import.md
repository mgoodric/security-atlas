# SCF Workbook Crosswalk Import

`atlas-cli catalog import-crosswalk` accepts the project's YAML crosswalk files
and SCF-format `.xlsx` files downloaded by the operator. The importer never
downloads SCF content itself.

## Download Source

Download the current Secure Controls Framework workbook from SCF's download
page:

https://securecontrolsframework.com/free-content/scf-download

SCF states that the spreadsheet is the primary free XLSX download and that the
canonical no-registration copy is hosted on GitHub. As of the 2026.2 workbook,
the file is named:

`secure-controls-framework-scf-2026-2.xlsx`

## Verified 2026.2 Format

The 2026.2 workbook was inspected before this importer was added.

The full workbook contains:

- `Focal Documents`: metadata for framework mapping columns. Important headers:
  `SCF Column Header`, `Focal Document Identifier (FDI)`, `Source`, and
  `Focal Document Name (FDN)`.
- `SCF 2026.2`: one wide control catalog sheet. Important headers:
  `SCF #` for the SCF anchor and one column per mapped law, regulation, or
  framework. Framework cells contain newline-separated requirement IDs.

Example framework headers in `SCF 2026.2` include `AICPA TSC 2017:2022 (used
for SOC 2)`, `ISO 27001 2022`, `NIST CSF 2.0`, and `PCI DSS 4.0.1`.

The wide full workbook records ID mappings but does not encode a per-cell STRM
relationship or strength. Imports from this format are therefore written as
`source_attribution=community_draft` with conservative draft STRM values
(`intersects_with`, `0.5`) so maintainers can review and promote them through
the crosswalk tier workflow.

SCF's OLIR STRM workbooks use a different two-sheet shape. The mapping sheet
contains `Focal Document Element`, `Relationship`, `Reference Document Element`,
and `Strength of Relationship` columns. When this shape is supplied, the
importer uses the workbook's explicit relationship and strength values.

## What Each Source Carries

A crosswalk edge has two parts: which SCF anchor a requirement maps to, and the
NIST IR 8477 STRM detail of that mapping (relationship type and strength). The
sources differ in how much of that detail they publish, so the same pair can
arrive with different detail depending on the file you import.

| Source                                     | Anchor IDs | Relationship type  | Strength  | `source_attribution` on import |
| ------------------------------------------ | ---------- | ------------------ | --------- | ------------------------------ |
| Full SCF workbook (`SCF <version>` sheet)  | Yes        | No, defaulted      | No, `0.5` | `community_draft`              |
| SCF OLIR STRM workbook                     | Yes        | Yes, from the file | Yes       | `community_draft`              |
| Project YAML crosswalk (`data/crosswalks`) | Yes        | Yes, per mapping   | Yes       | As declared in the file        |

What this means in practice:

- **Full workbook edges are placeholders for the STRM detail.** The workbook
  lists only requirement IDs per SCF control, so every edge from it lands as
  `intersects_with` with strength `0.5`. These values are not SCF's
  assessment; they are the importer's conservative default. The edge's
  rationale names the sheet, row and column it came from and says it is
  pending STRM review.
- **The OLIR STRM workbook is the source of real relationship detail.** SCF
  publishes its STRM relationship types and strengths in separate STRM files,
  not in the main workbook. At the time of writing those are free as PDFs or
  as a paid Excel bundle; only the Excel (`.xlsx`) shape is importable. When
  you have one, import it (see [Import](#import)) and its explicit
  `Relationship` and `Strength of Relationship` values replace the
  workbook's defaults for the same (requirement, anchor) pairs, because both
  imports are `community_draft` and a same-source import updates the edge.
- **"Official" is provenance, not trust.** No XLSX import is written as
  `scf_official`, including the OLIR STRM workbook: the importer cannot verify
  that a local file is an unmodified SCF publication. `scf_official` is only
  set by a YAML crosswalk that declares it, and none of the bundled
  `data/crosswalks/*.yaml` files does today. Separately from provenance, every
  imported edge starts at the draft trust tier; promoting it to reviewed or
  verified is a human step (ADR 0018).

If you need STRM-accurate relationships for audit work, import the OLIR STRM
workbook for that framework, or review and edit the workbook-derived edges
before promoting them. Do not read a workbook edge's `intersects_with` /
`0.5` as SCF's judgement of that mapping.

## Import

For the full SCF workbook, pass the local `.xlsx` file and the framework column
to ingest:

```sh
atlas-cli catalog import-crosswalk ./secure-controls-framework-scf-2026-2.xlsx \
  --framework-column "NIST CSF 2.0" \
  --framework-slug nist-csf \
  --framework-version 2.0
```

If the workbook has exactly one framework mapping column, the column is selected
automatically. Real SCF full workbooks contain many framework columns, so
`--framework-column` is normally required.

For an SCF OLIR STRM workbook, no framework column is needed:

```sh
atlas-cli catalog import-crosswalk ./scf-olir-strm-nist-800-53-r5-1-1.xlsx \
  --framework-slug nist-800-53 \
  --framework-name "NIST SP 800-53 R5.1.1" \
  --framework-issuer NIST
```

All XLSX imports land at `source_attribution=community_draft`. The database
trust tier for new edges remains the catalog default draft tier; promotion to
reviewed or verified is a human governance step.

## Provenance Precedence

An import never lowers an existing edge's provenance. Precedence is
`scf_official` > `community_draft` > `org_internal`. A row whose source ranks
below the existing edge for the same (requirement, anchor) pair is skipped,
and the edge keeps its attribution, rationale, relationship, strength and
tier. Because XLSX imports land as `community_draft`, they skip every pair that
already has an `scf_official` edge. Each skip is printed:

```text
edge skipped CC6.1->IAC-01: existing source_attribution=scf_official outranks incoming community_draft
```

A same-source or stronger-source row updates the edge as before, but never its
trust tier. If a stronger source lands on an edge whose tier is `rejected`, the
edge stays out of coverage, and the import prints one line per edge so a
reviewer can act on it:

```text
edge upgraded onto rejected tier CC6.1->IAC-01: source_attribution community_draft -> scf_official; mapping_tier stays rejected
```
