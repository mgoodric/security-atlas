# Slice 599 — OSCAL resolved-chain provenance read surface — decisions log

**Type:** standard (code). Parent: slice 578. A handful of build-time
JUDGMENT calls are recorded here per the slice-353 convention; none touch the
runtime AI-assist boundary.

## Context

Slice 578 WRITES the resolved import chain as provenance into the
`imported_catalog_audit_log.detail` JSON of the `profile_imported` success
row — a `chain` array of `{role, sha256, bytes}` entries (entry profile +
intermediate profiles + catalogs) plus a `chain_depth` count — but provided
no read surface. This slice adds the READ.

## Decisions

### D1 — Surface choice: HTTP read endpoint (not a CLI flag)

The spec offered either an HTTP read in the imported-baseline detail view OR
a `--show-chain` CLI flag, "pick one." Chose the **HTTP read endpoint**
`GET /v1/oscal/imported-profiles/{id}/provenance`. Rationale: (a) every
existing OSCAL-adjacent read/operator surface in v1 is HTTP (the
`oscalexport` handler, the `controldetail` reads); a new HTTP read mirrors
the established pattern and is directly consumable by the web baseline-detail
view the spec names as the primary consumer. (b) The CLI `atlas-oscal` binary
is an import-direction tool (it pushes documents to the bridge); a read-only
provenance query fits the platform API surface, not the import CLI. A CLI
`--show-chain` can be a thin later wrapper over this endpoint if demand
surfaces — it is strictly additive and not blocked by this choice.

### D2 — Route shape `/v1/oscal/imported-profiles/{id}/provenance`

A fresh `/v1/oscal/...` top-level segment (no existing route lives under it),
so no chi shadowing. The `{id}` is the imported-profile baseline id
(`imported_catalogs.id`, which is the `catalog_id` the audit row is keyed by).
`/provenance` as the sub-resource names exactly what is returned.

### D3 — Authz role set mirrors `controldetail.hasControlRead`

The handler-level defense-in-depth guard admits admin (wildcard) +
grc_engineer (`IsApprover`) + control_owner (`OwnerRoles`) — the same set
`controldetail.requireControlRead` admits. The Credential model carries no
separate `auditor` flag in v1, and an auditor in practice holds one of these
role signals (admin/grc_engineer). A bare push credential (no flags) is
denied: provenance is an operator/auditor surface, not a connector surface.
The slice-035 OPA middleware remains the PRIMARY gate; this handler check is
its testable twin (OPA is not wired in unit/integration test servers — the
`controldetail` precedent).

### D4 — Read path is bridge-free; tested with seeded DB rows

The provenance is already persisted in Postgres by slice 578, so the read
NEVER calls the compliance-trestle bridge. This is the load-bearing
testability decision: the integration suite seeds an `imported_catalogs`
profile baseline + a `profile_imported` audit row carrying the chain in
detail JSON (exactly the shape 578's `persist()` writes) and exercises the
full request path — tenancy middleware, RLS, the sqlc query — with **no
Python bridge**, so the AC tests run in CI regardless of bridge presence.

### D5 — New sqlc query joins to confirm `kind = 'profile'`

`GetProfileImportProvenance` joins `imported_catalogs` to its
`profile_imported` audit row, filtering `ic.kind = 'profile'`, so the read
both (a) confirms the id is a profile baseline (a catalog-import or
component-definition id, or a non-existent id, returns `ErrNoRows` → 404) and
(b) carries the baseline display metadata in one round-trip. Tenant isolation
rides RLS plus the explicit leading `$1` tenant_id predicate
(canvas invariant #6). `ORDER BY al.occurred_at DESC LIMIT 1` returns the
most-recent success row defensively (the persist path writes exactly one
`profile_imported` row per baseline, so this is belt-and-suspenders).

### D6 — Empty/nil detail renders `"chain":[]`, malformed detail 500s

A baseline whose audit detail has no chain (defensive — every 578-written
row has one) renders an empty chain array rather than `null`, so the client
can iterate unconditionally. A detail JSON that fails to unmarshal is a
server-side data-integrity fault → 500 (not a 200 with a partial body).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: none (no bug surfaced during the slice).
- `detection_tier_target`: n/a.

No defect was found building this slice. The read is a pure SELECT over an
already-persisted, slice-578-tested provenance shape; the wire shape and the
RLS isolation are both covered at the unit (stub-seam) and integration
(real-Postgres) tiers.

## Spillover

None. The slice is self-contained: read API over existing persisted
provenance, no migration, one additive sqlc query, one new read package.
