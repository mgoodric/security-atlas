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
