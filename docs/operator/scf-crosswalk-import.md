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
  Any edge whose content that import changes has its trust tier re-seeded
  (#1849), so an edge already promoted to reviewed or verified drops back to
  draft and needs review again. Each one is printed:

  ```text
  edge tier re-seeded CC6.1->IAC-01: content changed; mapping_tier verified -> draft
  ```

- **What "official" means.** No XLSX import is written as `scf_official`,
  including the OLIR STRM workbook: the importer cannot verify that a local
  file is an unmodified SCF publication. `scf_official` is only set by a YAML
  crosswalk that declares it, and none of the bundled
  `data/crosswalks/*.yaml` files does today. Every XLSX import, and every YAML
  mapping that isn't `scf_official`, starts at the draft trust tier, and
  promoting it is a human step. A YAML mapping declared `scf_official` is
  seeded at verified (ADR 0018 §2).

If you need STRM-accurate relationships for audit work, import the OLIR STRM
workbook for that framework before promoting its edges, or review and edit the
workbook-derived edges before promoting them. Do not read a workbook edge's `intersects_with` /
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

The framework slug, name, issuer and version default to the column's row in the
`Focal Documents` sheet. `--framework-slug` is required when that metadata is
missing: the workbook has no `Focal Documents` sheet, the sheet has no
`SCF Column Header` column, or it has no row for the selected column (the first
one, when several are combined). Without it the slug would fall back to the
column header and could duplicate a framework already imported under its FDI
slug, so the import fails and names the cause.

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

## SOC 2 Points of Focus

SCF's SOC 2 column lists the Trust Services Criteria (`CC1.1`) and, mixed in
with them, their points of focus (`CC1.1-POF1`). An auditor tests against the
criteria; the points of focus are guidance on what a criterion covers. Imported
one-for-one, SOC 2 would carry several hundred "requirements" and coverage
would count every point of focus in both the numerator and the denominator.

XLSX imports therefore roll each point of focus up into its criterion. Any
code of the form `<criterion>-POF<n>` is treated this way:

- **It never becomes a requirement.** It is not in `requirement_count`, in
  coverage or posture, or in requirement lists.
- **Its mapping becomes an edge from the criterion.** If the criterion already
  has its own edge to the same SCF anchor, that edge wins. If only points of
  focus map the anchor, the strongest of their edges is used (the first one on
  a tie). The criterion never gets two edges to one anchor. A rolled-up edge's
  rationale starts with `Rolled up from SOC 2 point of focus <code>.`
- **The criterion records it as metadata.** Each requirement's
  `points_of_focus` holds the code of each point of focus and the SCF anchors
  it mapped to. `GET /v1/requirements/{id}/coverage` and
  `GET /v1/requirements/{id}/anchors` return it on the requirement; the
  field is omitted when there are none.
- **A missing criterion is created.** If the column names a point of focus
  but never its criterion, the criterion is created (titled by its code, like
  every workbook requirement) and the import summary says so.

The import summary reports the roll-up, for example:

```text
points of focus rolled up=3 into criteria=2
criterion CC2.1 created from its points of focus (the column has no bare CC2.1)
```

The metadata is written when a requirement is first inserted, because a
frozen version's requirements can't be updated afterwards. SOC 2 versions
imported before this roll-up existed are not rewritten; import the workbook
into a new version to get the rolled-up shape.

YAML crosswalks are not rolled up. A YAML requirement may list its points of
focus explicitly, but none of the bundled files does:

```yaml
requirements:
  - code: CC1.1
    title: COSO Principle 1
    points_of_focus:
      - code: CC1.1-POF1
        scf_anchors: [GOV-01]
```

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

A same-source or stronger-source row updates the edge. If that changes the
edge's content (relationship, strength, attribution or rationale), its trust
tier is re-seeded to the incoming source's seed tier: verified for
`scf_official`, draft for everything else. The reviewer's earlier judgement
applied to content that no longer exists. Each re-seed is printed and recorded
as a tier transition. An unchanged row leaves the tier alone.

A `rejected` edge is never re-seeded: rejection is a human call and only a
reviewer reopens it. If a stronger source lands on a `rejected` edge, the edge
stays out of coverage, and the import prints one line per edge so a reviewer
can act on it:

```text
edge upgraded onto rejected tier CC6.1->IAC-01: source_attribution community_draft -> scf_official; mapping_tier stays rejected
```
