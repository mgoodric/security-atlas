# OSCAL Catalog Import

security-atlas treats **OSCAL as the wire format** in both directions
(constitutional invariant 8): it _exports_ SSP / AP / AR / POA&M bundles, and
it _imports_ OSCAL catalogs. This page documents catalog import — bringing an
externally-authored OSCAL `catalog` document (for example, a NIST SP 800-53
rev5 control catalog) into a tenant's program.

> **Scope.** This release imports **catalogs** only. **Profile import**
> (resolving `import` / `merge` / `modify` directives against a catalog) and
> **component-definition import** (vendor-supplied control implementations) are
> follow-on features. Each is a meaningfully different OSCAL model with its own
> resolution semantics; see the OSCAL profile/component-definition import slices
> in the issue tracker.

## What import does

1. The CLI reads an OSCAL catalog JSON file.
2. The bytes are size-checked, then handed to the Python `oscal-bridge`
   (wrapping IBM `compliance-trestle`), which validates the document against
   OSCAL v1.1.x and returns a normalized control projection. **No `href` or
   external resource the document references is ever fetched** — back-matter
   resources are treated as opaque metadata.
3. The imported controls are persisted **transactionally** as a distinct,
   provenance-labeled set. A validation failure or partial error commits
   **nothing**.
4. Each imported control is mapped **to** an SCF anchor where its OSCAL
   `control-id` exactly matches a Secure Controls Framework `scf_id`; otherwise
   it is recorded as **needs operator mapping** (`scf_anchor_id` is left empty).
   Imported controls never overwrite the bundled SCF spine — they point at it.

## Provenance and audit

Every import run records:

- **`source`** — `oscal-import`.
- **`imported_by`** — the operator that performed the import.
- **`source_sha256`** — the SHA-256 of the exact inbound document bytes, so an
  imported catalog is tamper-evident and attributable after the fact.
- **`source_label`** — the operator-declared framework label
  (for example `NIST SP 800-53 rev5`).
- **`oscal_version`**, **`catalog_title`**, **`control_count`**, and an import
  timestamp.

A success writes a `catalog_imported` row to an append-only import audit log; a
rejected document writes an `import_rejected` row (with no catalog persisted).

## Authorization

Import is gated to the **catalog-author** role: `grc_engineer` (or `admin`,
which is a superset). It is always tenant-scoped — the controls are written
under the importing tenant via PostgreSQL Row-Level Security, and another
tenant never sees them. There is no anonymous import path.

## Limits (denial-of-service guardrails)

| Limit         | Default | Why                                                               |
| ------------- | ------- | ----------------------------------------------------------------- |
| Document size | 16 MiB  | ~4× a full NIST 800-53 rev5 catalog; caps expansion attacks.      |
| Control count | 10,000  | SCF is ~1,400 controls; bounds the import transaction.            |
| Parse timeout | 30 s    | A 4 MiB catalog parses sub-second; this is cold-process headroom. |

Documents over a limit are rejected with a clear error and nothing is
persisted.

## CLI

```bash
atlas-oscal import-catalog <file> \
  --dsn "$DATABASE_URL_APP" \
  --tenant-id <tenant-uuid> \
  --bridge-addr 127.0.0.1:50070 \
  --source-label "NIST SP 800-53 rev5" \
  --imported-by "alice@example.com" \
  --role grc_engineer
```

`--dsn` defaults to `$DATABASE_URL_APP` (use the `atlas_app` role). Add `--json`
to emit a machine-readable report instead of text. The command requires a
running `oscal-bridge` sidecar (see the bridge README).

Text output reports the catalog id, title, OSCAL version, source label, source
SHA-256, and how many controls were imported / mapped to SCF anchors / left for
operator mapping.

## Changelog

The catalog-import surface shipped via the OSCAL catalog-import slice (#492).
