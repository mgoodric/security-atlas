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

### Combining columns (ISO 27001)

Some frameworks span more than one workbook column. Repeat
`--framework-column` to import them as one framework. A column given as
`HEADER=PREFIX` has `PREFIX` prepended to every requirement code read from it
(a code that already starts with the prefix is left alone). Framework metadata
that is not passed explicitly is taken from the first column.

ISO/IEC 27001:2022 is the case this exists for. SCF puts only the
management-system clauses (4-10) under `ISO 27001 2022`; the Annex A controls
are mapped under `ISO 27002 2022` as bare `5.1`-`8.34`. Importing either column
alone gives an ISO 27001 with no Annex A controls or one with no clauses. The
preset below imports both and writes the Annex A codes as `A.5.1`-`A.8.34`,
the same codes and slug as the shipped `data/crosswalks/iso27001-2022.yaml`:

```sh
atlas-cli catalog import-crosswalk ./secure-controls-framework-scf-2026-3.xlsx \
  --framework-column "ISO 27001 2022" \
  --framework-column "ISO 27002 2022=A." \
  --framework-slug iso27001 \
  --framework-name "ISO/IEC 27001:2022" \
  --framework-version 2022
```

**Do not run this preset unchanged on a database that already holds
`iso27001-2022.yaml`.** It targets the same framework slug and version
(`iso27001` / `2022`). The workbook carries IDs only, so every requirement it
writes has `title` set to its code and an empty body, and the import overwrites
the curated Annex A titles and bodies from the YAML. To compare the two side by
side, give the workbook import its own version, for example
`--framework-version 2022-scf-2026.3`.

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
