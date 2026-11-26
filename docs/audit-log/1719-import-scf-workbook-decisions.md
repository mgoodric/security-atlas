# Issue 1719: `import-scf` reads the official SCF workbook: decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/1719

- **`detection_tier_actual`: `manual_review`.** Found during a first-run adopter setup (2026-11-12): no path loaded the catalog from SCF's own download.
- **`detection_tier_target`: `manual_review`.** This was a missing input format, not a regression a test could have caught.

## The gap

`atlas-cli catalog import-scf` accepted only the project's JSON shape. `import-crosswalk` already read SCF's `.xlsx`, so an adopter could load mappings from the workbook but had to write a converter for the controls those mappings point at.

## Decisions

### D1: read the workbook, not the OSCAL catalog. **Confidence: HIGH.**

The ticket allows either. The workbook is what an adopter downloads, and it's the file `import-crosswalk` and the scfseed pin (issue #1717) already use. A single source file for catalog and crosswalks keeps both on one release. OSCAL catalogs already have their own route, `atlas-oscal import-catalog`. OSCAL support for `import-scf` is left for a later change if someone asks for it.

### D2: share sheet detection through a new `internal/scfworkbook` package. **Confidence: HIGH.**

Both importers must pick the same `SCF <release>` sheet. The rule (match `SCF <yyyy>.<n>` by name, else the first `SCF ...` sheet with an `SCF #` column) moved out of `internal/api/soc2import` into `internal/scfworkbook`, along with the header and cell helpers. `soc2import` calls it, so the crosswalk importer's behaviour is unchanged. The sheet-order bug the ticket mentions had already been fixed in `soc2import`; this change makes the catalog importer use the same fix.

### D3: family is the `SCF #` prefix, not the `SCF Domain` text. **Confidence: MEDIUM.**

The JSON catalog and every row seeded from it use the ID prefix (`IAC` for `IAC-06`). Using the domain name ("Identification & Authentication") would put rows from the two loaders into different families. The `SCF Domain` column is used only when an ID has no `-` prefix.

### D4: release version comes from the sheet name; release date is a flag. **Confidence: HIGH.**

The control sheet is named for the release (`SCF 2026.3`), so that is the version, and `--release-version` overrides it. The workbook carries no release date, so `--release-date` records one when the operator has it. Both flags are refused for a JSON catalog, which carries its own.

### D5: description column matched exactly. **Confidence: HIGH.**

The prefix match the crosswalk importer uses would let `Secure Controls Framework (SCF)` hit sibling columns. The catalog loader matches `SCF #`, `SCF Control` and `Secure Controls Framework (SCF) Control Description` exactly, after whitespace normalization.

## Verification against the published file

`secure-controls-framework-scf-2026-3.xlsx` from the SCF GitHub repository, sha256 `5a89bf2d…7df697` (the #1717 pin), imported into a scratch Postgres 16 with all migrations applied: 1,591 controls in 34 families, and `unchanged=1591` on re-run. `import-crosswalk --framework-column "ISO 27001 2022"` on the same file still yields 148 requirements and 316 edges.
