# #1971 — SCF 2026.3 remap, IAC family group: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

The first head of the PR re-anchored the sample rows and every reference correctly, but did nothing about installs that already ran the old bootstrap. The PR reviewer caught it: `scfimport` updates anchors in place by `scf_id` and the crosswalk import only upserted edges, so on an upgraded install `IAC-10` became PAM while keeping its old Authenticator Management edges. The integration test added here (`TestSCFRemapUpgrade_PrunesEdgesLeftOnOldAnchors`) is the tier that should have caught it. It fails with `[IAC-10 IAC-14]` when the prune is disabled.

## D1 — The remap ran as one simultaneous substitution

The D5 table in `1717-scf-pin-ratchet-decisions.md` has a chain (`IAC-21`→`IAC-10` while `IAC-10`→`IAC-14`). A Perl lookup-table regex rewrote each file in one pass, so no ID was rewritten twice. The pattern requires a non-alphanumeric character before the ID and refuses a following digit or `.digit`, so `IAC-060` and `IAC-06.1` are untouched. The lowercase `scf-iac-06` form in `fixtures/readme-demo` followed. Arbitrary bundle ids such as `ctrl-iac-06` did not, because they name a fixture rather than an anchor.

Left as written: existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, `migrations/sql/` (applied, and the IDs there are only in comments), `Plans/_archive/`, `Plans/prompts/`, and `internal/api/scfseed/testdata/scf-pin.json`, which is the source of truth.

## D2 — The crosswalk import prunes the edges it no longer lists

The dispatcher chose this fix, and it serves every later family group. After the upsert pass, in the same transaction, `importIntoTx` reads every edge under the framework version (`ListFwToScfEdgesForVersionForUpdate`, row-locked) and deletes the ones the crosswalk does not list.

- **Scope is the source that wrote the edge.** `source_attribution` cannot do this job on its own. Workbook imports land as `community_draft`, the same as the bundled YAML, and the operator doc's ISO 27001 preset imports a workbook into the YAML's own slug and version. Scoped by attribution alone, each import deleted the other's edges. So migration `20260914000000` adds a nullable `fw_to_scf_edges.crosswalk_source`, stamped on every insert and update. YAML imports write `yaml:<file name>`, wide workbooks write `scf-workbook:<columns, with prefixes>`, and OLIR workbooks write `scf-olir:<sheet>`. An import prunes an unlisted edge only when it wrote that edge (`ownsEdge`) **and** the edge's `source_attribution` is one the crosswalk writes. An edge another source wrote is kept and reported as `EdgesUnlisted`. The dispatcher chose this over an opt-in `--prune` flag, which would still have wiped workbook edges merged into `soc2/2017` on every boot.
- **A NULL source belongs to the YAML.** Rows written before the column existed have no source. Until workbook imports arrived (#1934, #2001, days before this change), the bundled YAML crosswalks were the only importer, so a YAML import, or an in-memory crosswalk with no `Source`, owns NULL rows. A workbook import never prunes them. This is how an upgraded install converges on its next YAML import. The cost is one-time: workbook edges merged into a bundled slug or version before this release are pruned by the next YAML import, and come back with their own source when the operator re-runs the workbook import. The CHANGELOG says so.
- **A pair both sources list has one owner, the last writer.** An update stamps the importer's source. An unchanged edge keeps its source, because nothing is written. If the owner stops listing a pair the other source still lists, the edge is pruned and comes back on the other source's next import. That is the same last-writer behaviour the edge content already has.
- **The workbook source names its columns.** Importing different columns into one version in separate runs gives separate sources, which leave each other alone. Re-importing the same columns prunes that import's stale edges. A later SCF release that renames a column header produces a new source, so the old column's edges are not pruned automatically.
- **No tenant edges exist.** `fw_to_scf_edges` has no `tenant_id`; it is catalog data only. Tenant overlay edges are #1735 and are not built yet. When they land, they need their own table or column, and this prune must stay scoped to catalog rows.
- **Deleting cascades the tier-transition audit rows** (`fw_to_scf_edge_tier_transitions.edge_id ON DELETE CASCADE`, an existing schema choice). Every pruned edge is reported with its tier, and the CLI prints one `edge pruned` line per edge, so a removed reviewer decision shows in the import output. Keeping the audit rows would need a schema change, which this ticket does not justify.
- **Upgrade convergence.** The only migration is the additive column above, with no backfill. The upgrade path is a re-run of the crosswalk import, and that import now converges on its own. The bootstrap re-imports only the SOC 2 crosswalk. The opt-in crosswalks (HIPAA, ISO 27001, PCI DSS, NIST CSF) converge only when the operator re-runs `atlas-cli catalog import-crosswalk` for each one, and the CHANGELOG upgrade note says so. Having the bootstrap re-import every bundled crosswalk whose framework already exists would close that gap automatically. It is left open because it changes the bootstrap's opt-in contract, and the dispatcher is filing it as a follow-up.

## D3 — Orphaned old anchor rows are left in place

`IAC-06`, `IAC-07`, `IAC-15`, `IAC-21`, `IAC-24` and `IAC-25` stay in `scf_anchors` on an upgraded install, with their old titles. They are not deleted:

- the superseded versions of the five stock controls still reference them, and `controls.scf_anchor_id` is `ON DELETE RESTRICT`;
- a tenant-authored control may reference them too, and deleting that anchor would fail, or would need a re-anchor decision that belongs to the tenant.

After the D2 prune, no shipped crosswalk edge points at them, so they contribute no framework coverage. The CHANGELOG says that a tenant control anchored on one of them loses framework coverage until it is re-anchored. Retiring stale anchors properly (a status column, or a prune in `scfimport` that skips referenced rows) is a separate change, and it would apply to every family group.

## D4 — `IAC-10` changing meaning is documented, not migrated

`IAC-10` is the only old ID that 2026.3 reuses for a different concept. Evidence recorded against `scf:IAC-10` is append-only and is not rewritten (invariant #2). A tenant control anchored on `IAC-10` silently becomes a PAM control. The CHANGELOG names both cases and the connector flags. Nothing here can tell which `IAC-10` references meant authenticator management, so the operator has to re-point them.
