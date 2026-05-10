# 006 — SCF catalog importer + Framework / FrameworkVersion API

**Cluster:** Catalog & UCF graph
**Estimate:** 2d
**Type:** AFK

## Narrative

Ingest the Secure Controls Framework's published JSON catalog into Postgres. Create the `Framework` row for SCF and one `FrameworkVersion` row pinned to the SCF release we import. Each SCF control becomes an entry in `scf_anchors` (the semantic spine of the UCF graph). Expose read endpoints `GET /v1/frameworks`, `GET /v1/frameworks/scf/versions`, `GET /v1/anchors`, `GET /v1/anchors/:id` for downstream slices (UCF traversal, control bundles, frontend SCF browser). The slice delivers value because the canonical control library is queryable; later slices anchor everything to these rows.

## Acceptance criteria

- [ ] AC-1: `just import-scf <path-to-scf-release.json>` ingests the catalog and creates 1,400+ `scf_anchors` rows
- [ ] AC-2: `GET /v1/anchors` returns the anchor list with pagination
- [ ] AC-3: `GET /v1/anchors/SCF:IAC-06` returns the MFA anchor with title, family, description
- [ ] AC-4: A second import of the same release is idempotent (no duplicate rows)
- [ ] AC-5: A second import of a newer SCF release creates a new `framework_versions` row and links new anchors; old version remains queryable
- [ ] AC-6: Import surfaces a report: counts of anchors created/updated/unchanged

## Constitutional invariants honored

- **Invariant 7 (SCF canonical catalog):** SCF is the spine, ingested from SCF's permissively-licensed JSON
- **Licensing constraints:** uses SCF's free standard license (legal review still pending — flagged in open questions)

## Canvas references

- `Plans/canvas/03-ucf.md` §3.5 (SCF as canonical)
- `Plans/canvas/sources.md` (SCF download URL)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT bundle CCM, CAIQ, SIG content (license-restricted)
- Does NOT mutate `framework_versions` once `status='current'` (mappings are version-pinned)
- Does NOT silently drop anchors on import — surface counts

## Skill mix (3–5)

- Go HTTP handlers
- sqlc-typed Postgres queries
- JSON parsing for SCF catalog format
- Atlas migrations for the catalog tables
- Idempotent batch import patterns
