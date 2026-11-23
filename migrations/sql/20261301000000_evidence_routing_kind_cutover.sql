-- security-atlas -- #2973: a routing cutover per kind, for kinds routed late.
--
-- sast.scan_result.v1 was a bound kind, and becomes routed by type
-- (internal/evidence/routed). Rows of it already stored with control_id
-- NULL (pushed under an scf: control_ref, which no control resolves) become
-- readable by every control that declares the kind.
--
-- evidence_routing_cutover (20261290000000) records one instant for the
-- kinds routed in #2924. An audit period frozen after that instant but
-- before this migration read no control-free sast record, and must keep
-- reading exactly that (invariant 10). So a kind routed after the first
-- cutover gets its own row here, with the instant this migration applied,
-- and routed.KindsAsOf compares a pinned read against that kind's own
-- instant. A kind listed late in Go with no row here is an error, never "no
-- cutover": a pinned read must not silently widen.
--
-- Global, like evidence_routing_cutover: a property of the deployment, not of
-- a tenant, so no tenant_id and no RLS. The app role may read it, never
-- write it.
CREATE TABLE evidence_routing_kind_cutover (
    evidence_kind text        PRIMARY KEY CHECK (length(evidence_kind) > 0),
    cut_at        timestamptz NOT NULL DEFAULT now()
);

INSERT INTO evidence_routing_kind_cutover (evidence_kind) VALUES ('sast.scan_result.v1');

GRANT SELECT ON evidence_routing_kind_cutover TO atlas_app;
