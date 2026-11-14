# #1971 — SCF 2026.3 remap, IAC family group: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

The first head of the PR re-anchored the sample rows and every reference correctly, but did nothing about installs that already ran the old bootstrap. The PR reviewer caught it: the crosswalk import only upserted edges, so on an upgraded install HIPAA Password Management kept its edge to the old Authenticator Management row. The second review found that the first fix deleted append-only tier history. Then #2011 (#1768) landed, freezing each SCF release, which made the in-place re-titling of the sample illegal. The integration test `TestSCFRemapUpgrade_RetiresEdgesLeftOnTheOldRelease` is the tier that should have caught the first gap.

## D1 — The remap ran as one simultaneous substitution

The D5 table in `1717-scf-pin-ratchet-decisions.md` has a chain (`IAC-21`→`IAC-10` while `IAC-10`→`IAC-14`). A Perl lookup-table regex rewrote each file in one pass, so no ID was rewritten twice. The pattern requires a non-alphanumeric character before the ID and refuses a following digit or `.digit`, so `IAC-060` and `IAC-06.1` are untouched. The lowercase `scf-iac-06` form in `fixtures/readme-demo` followed. Arbitrary bundle ids such as `ctrl-iac-06` did not, because they name a fixture rather than an anchor.

Left as written: existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, `migrations/sql/` (applied, and the IDs there are only in comments), `Plans/_archive/`, `Plans/prompts/`, and `internal/api/scfseed/testdata/scf-pin.json`, which is the source of truth.

## D2 — The sample ships as a new SCF release, `test-2026.3`

Matt's call (2026-11-14, on #1999 and #1717). Since #2011 an imported release is frozen: re-importing `test-2026.1` with the remapped rows would be a `ReleaseConflictError` and fail every upgraded install's bootstrap. So the sample's `release_version` moves to `test-2026.3`, and the upgrade goes down the new-release path. A new framework version is created with new anchor rows, and `test-2026.1` stays as a legacy version with its rows unchanged.

- **The legacy rows are kept, not orphaned.** The old IDs (`IAC-06`, `IAC-21` and the rest) are simply the legacy release's rows. Superseded control versions still reference them (`controls.scf_anchor_id` is `ON DELETE RESTRICT`), and audit replay can read them.
- **A pin keeps the label honest.** `internal/api/scfseed/fixturepin_test.go` pins a sha256 of the sample's anchor content (`scf_id`, `family`, `title`, `description`, `subtopics`, in file order) to its `release_version`. Editing a row without a new label fails in CI, and the failure prints the hash to pin. Entries are never edited, only added.

## D3 — Stock controls move to the new release's anchor in place

Coverage joins controls to crosswalk edges by anchor row (`dashboard.sql`, `covering_control`). After a new release, a control left on a legacy row matches no live edge. The bootstrap re-uploads every stock bundle on each boot, and an unchanged bundle hits the byte-identical no-op (or, after a description edit, the in-place metadata path), neither of which touched `scf_anchor_id`.

`control.Store.Upload` now re-binds in both paths: when the manifest's SCF code resolves to a different anchor row than the active row's, the row moves to it in place (`RebindControlAnchorInPlace`), keeping its id, version, owners and evidence. That condition can only mean a new release re-minted the code's row. A manifest that names a UUID resolves to itself, and a changed code is a manifest change that supersedes as before. The five stock controls whose code this PR changes still supersede, which the CHANGELOG states.

Re-binding by code is right for stock bundles, because their codes are maintained against the current catalog. For a tenant bundle it inherits any meaning change, `IAC-10` being the case here, and the CHANGELOG tells operators to re-point. Tenant controls that are never re-uploaded stay on legacy rows and drop out of coverage. A general re-bind of every control on a legacy row would apply the same meaning-change hazard silently, so it is left for a follow-up decision.

## D4 — The crosswalk import retires the edges it no longer lists

After the upsert pass, in the same transaction, `importIntoTx` reads every live edge under the framework version (`ListFwToScfEdgesForVersionForUpdate`, row-locked) and retires the ones the crosswalk does not list.

- **Retire, don't delete.** Matt's call, after the second review: deleting cascaded the append-only `fw_to_scf_edge_tier_transitions` rows. The same migration adds `fw_to_scf_edges.retired_at`. Retiring sets it and writes a transition row by `SystemReviewerID`, with from-tier equal to to-tier and the note `retired: dropped by crosswalk <source>`. The table has no CHECK requiring the tier to change, so no second audit table is needed. A crosswalk that lists the pair again clears `retired_at` and writes a `restored` row. Since a retired edge's old content no longer stands for anything, the restore runs before the provenance precedence check.
- **Readers.** `effective_fw_to_scf_edges` (coverage and traversal, #1796) excludes retired edges. So do the base-table readers: the catalog export, the tier state machine's reads (a transition on a retired edge is a 404), `GetFwToScfEdge`, the attribution count, and the importer's own list. The importer's per-pair lookup alone still sees retired rows, so it can restore them.
- **Keyed on rows, not codes.** The listed set is built from the requirement and anchor row ids resolved during the upsert pass. Keying on `(requirement code, scf_id)` would have treated an edge to `IAC-01` in the legacy release as listed, because the code matches, and left it live. The deterministic edge id now includes the anchor row id, so the edge to a new release's row cannot collide with the retired one to the legacy row. Nothing depended on the old id derivation.
- **Scoped to the source that wrote the edge.** `source_attribution` cannot do this alone: workbook imports land as `community_draft`, the same as the bundled YAML, and the ISO 27001 preset imports a workbook into the YAML's own slug and version. So the same migration adds `fw_to_scf_edges.crosswalk_source`, stamped on every insert and update: `yaml:<file name>`, `scf-workbook:<columns, with prefixes>` or `scf-olir:<sheet>`. An import retires an edge only when it wrote that edge (`ownsEdge`) **and** the edge's `source_attribution` is one the crosswalk writes. The dispatcher chose this over an opt-in `--prune` flag, which would still have wiped workbook edges merged into `soc2/2017` on every boot.
- **A NULL source belongs to the YAML.** Rows written before the column existed have no source. Until workbook imports arrived (#1934, #2001), the bundled YAML crosswalks were the only importer, so a YAML import, or an in-memory crosswalk with no `Source`, owns NULL rows. A workbook import never retires them. The cost is one-time: legacy workbook edges merged into a bundled slug or version are retired by the next YAML import, and come back under their own source when the operator re-runs the workbook import. The CHANGELOG says so.
- **A pair both sources list has one owner, the last writer.** An update stamps the importer's source; an unchanged edge keeps its own. If the owner stops listing a pair the other source still lists, the edge is retired, and the other source's next import restores it.
- **The workbook source names its columns.** Importing different columns into one version in separate runs gives separate sources. A later SCF release that renames a column header produces a new source, so the old column's edges are not retired automatically.
- **Platform edges only.** `fw_to_scf_edges` has no `tenant_id`; it is catalog data. Tenant overlay edges (#1735) are not built yet. When they land they need their own rows, and this pass must stay scoped to catalog edges.
- **Convergence.** The upgrade path is a re-run of the crosswalk import. The bootstrap re-imports only the SOC 2 crosswalk; the opt-in crosswalks converge when the operator re-runs `atlas-cli catalog import-crosswalk`, and the CHANGELOG says so. The dispatcher is filing the follow-up for the bootstrap to converge every bundled crosswalk.

## D5 — Migration numbering

The dispatcher named `20260914000000`, but by the time this branch merged main, main had gained `20260916000000_scf_anchor_immutability.sql`, then `20260920000000_oidc_identity_link_audit.sql`, then `20260925000000_control_import_candidates.sql`, then `20260926000000_scf_anchor_legacy_scf_id.sql` (which took the slot this branch had used). The dispatcher then fixed the slot at `20261014000000`, clear of main's queue. None of those migrations touches `fw_to_scf_edges`, so the order between them does not matter. It is additive: two nullable columns, plus the view redefined with the same column list. The down migration restores the view's earlier shape first, because the view references `retired_at`, and then drops both columns.

## D6 — `IAC-10` changing meaning is documented, not migrated

`IAC-10` is the only old ID that 2026.3 reuses for a different concept. Evidence recorded against `scf:IAC-10` is append-only and is not rewritten (invariant #2). The legacy release's `IAC-10` row keeps its old title, but anything that resolves the code today gets PAM. The CHANGELOG names the connector flags, evidence and tenant bundles. Nothing here can tell which `IAC-10` references meant authenticator management, so the operator has to re-point them.
