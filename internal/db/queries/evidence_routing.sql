-- name: GetEvidenceRoutingCutover :one
-- #2924: the instant migration 20261290000000 applied. An audit period frozen,
-- or a population created or frozen, before it reads no control-free record
-- of a routed kind (internal/evidence/routed.KindsAsOf).
SELECT cut_at
FROM evidence_routing_cutover
WHERE singleton;

-- name: ListEvidenceRoutingKindCutovers :many
-- #2973: the instant each kind routed after the first cutover started
-- routing (migration 20261301000000). routed.KindsAsOf pins a read anchored
-- before a kind's own instant to exclude that kind's control-free records.
SELECT evidence_kind, cut_at
FROM evidence_routing_kind_cutover
ORDER BY evidence_kind;
