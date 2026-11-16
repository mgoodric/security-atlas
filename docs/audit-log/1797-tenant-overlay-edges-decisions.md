# #1797 — tenant overlay edges: create, list, retire (decisions log)

The first step of #1735: an organisation can record its own requirement ->
SCF anchor edge (an "overlay edge"), with a rationale and source, in a
tenant-scoped table under RLS. API (create, list, retire) and CLI
(`atlas-cli mappings overlay add | list | retire`). Coverage does not read
overlay edges yet; that is #1735's coverage-union step, a change to
`effective_fw_to_scf_edges` and nothing else.

- detection_tier_actual: integration
- detection_tier_target: integration

The integration tier caught one fixture defect while building (a control
seeded with an implementation type the enum does not have). No product
defect surfaced.

## D1 — table `fw_to_scf_overlay_edges`, beside the catalog edges

Named after `fw_to_scf_edges` so the two read as a pair. Four-policy RLS
(`tenant_read` / `tenant_write` / `tenant_update` / `tenant_delete`),
`FORCE ROW LEVEL SECURITY`, and the app role gets SELECT, INSERT, UPDATE,
DELETE. No route deletes an overlay edge; retirement keeps the row. The
catalog edges table gets no new grant: an integration test asserts the app
role still has no INSERT, DELETE or `UPDATE(rationale)` on it.

## D2 — uniqueness is a partial unique index over active rows

`(tenant_id, framework_requirement_id, scf_anchor_id) WHERE status =
'active'`. A retired edge never blocks re-adding the pair, and its history
stays. A concurrent duplicate create loses on the index (23505), which maps
to 409 `overlay_edge_exists`.

## D3 — an edge's assertion is immutable; only retirement changes it

A BEFORE UPDATE trigger refuses any change to tenant, requirement, anchor,
relationship, strength, rationale, source or creator, and any change to a
retired row. Correcting an edge is retire plus add, so the audit trail shows
both. A CHECK ties the retirement fields together: all set exactly when
`status = 'retired'`, with a non-blank reason.

## D4 — FKs cascade, like the catalog edges

`framework_requirement_id` and `scf_anchor_id` are `ON DELETE CASCADE`,
matching `fw_to_scf_edges`. Production never deletes anchors or frozen
requirements (releases are kept as legacy since #1770; frozen versions are
immutable), so the cascade only fires in catalog wipes. `RESTRICT` (the
controls choice) would make every catalog-wipe path, including the SCF
re-import work in flight in #1999, aware of a tenant table. Carrying edges
across a new framework version or SCF release is #1735's later steps
(migration queue, remap queue). The audit trail has no FK, so it outlives a
cascade.

## D5 — requirement named by framework slug + version label + code

`framework` (slug), `framework_version` (label; empty means the framework's
current version) and `requirement_code`, resolved in the global catalog
(`tenant_id IS NULL`). An unknown framework or version is
`unknown_framework_version`; a code the version lacks is
`unknown_requirement`. Both 422: the request is well-formed but names
something that is not there. Frozen (non-draft) versions are accepted: an
overlay edge never writes `framework_requirements`, and a test compares the
requirement rows before and after.

## D6 — anchor named by SCF release + code through `internal/scfrelease`

`scf_release` (empty: the current release) and `anchor_code` go through
`scfrelease.Resolve`, keeping its distinct errors: `unknown_scf_release`
(no such release, or no current release) and `unknown_anchor` (the release
exists but lacks the code). The stored anchor id pins the release; the list
reports it back as `scf_release`.

## D7 — "active catalog edge" means any tier except `rejected`

A pair already joined by a catalog edge is refused with 409
`catalog_edge_exists`, and the response names the edge (id, relationship,
tier). A catalog edge a reviewer rejected (ADR 0018) is not active, so the
organisation may assert the pair itself. `draft` and `under_review` catalog
edges still count as present: the organisation should push the catalog
review rather than shadow it.

## D8 — `no_relationship` is refused

Relationship uses the catalog vocabulary minus `no_relationship`: an overlay
edge exists to credit a requirement, and one that asserts no relationship
would credit nothing. Strength is required (0..1) on the API and the CLI;
there is no default, because a default would put a number on an auditor
link nobody chose.

## D9 — routes under `/v1/mapping-overlays`, OPA resource `mapping-overlays`

`POST /v1/mapping-overlays`, `GET /v1/mapping-overlays`
(`?framework`, `?requirement`, `?anchor`, `?status`), and
`POST /v1/mapping-overlays/{id}/retire`. `retire` is not an authz
transition action, so it maps to `write`. `mapping-overlays` is added to
grc_engineer's writable resources (admin allows everything); reads follow
the existing read-everything rules. The handler guard (admin or
grc_engineer, by flag or JWT tenant role) is the testable twin, as in #1810.
The tenant always comes from the credential: the request body cannot name
one (strict decode refuses unknown fields), and the CLI has no tenant flag.

## D10 — audit trail is its own append-only table, kind `mapping_overlay`

`fw_to_scf_overlay_edge_audit_log` follows `taxonomy_audit_log`: SELECT +
INSERT policies only, a trigger refusing UPDATE and DELETE for every role,
one row per create and retire in the same transaction as the change, with
the edge's state before and after (codes, relationship, strength, rationale,
source, status, reason). It joins the unified admin audit log as the twelfth
kind, `mapping_overlay` (target type `overlay_edge`), appended at the end of
`AllKinds` as that contract requires. No existing audit table's CHECK was
changed. The CSV/YAML import action #1735 describes will add `import` to the
action CHECK when it lands.

## D11 — the shared #1735 fixture lives with the API test

Two tenants; a small framework with one frozen version and three criteria;
the #1770 two-release SCF fixture; catalog edges C1 -> IAC-06 (verified) and
C3 -> CRY-03 (rejected); and a tenant A control anchored to IAC-06 whose
tested-under references are C1 (reached through the catalog edge) and C2
(not reached). A fixture test asserts that one-reached / one-unreached shape
so the later "audited under" report step can rely on it. The package runs on
integration Leg A because it imports the SCF fixture into the global
catalog.
