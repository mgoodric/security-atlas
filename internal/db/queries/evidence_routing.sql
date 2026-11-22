-- name: GetEvidenceRoutingCutover :one
-- #2924: the instant migration 20261290000000 applied. An audit period frozen,
-- or a population created or frozen, before it reads no control-free record
-- of a routed kind (internal/evidence/routed.KindsAsOf).
SELECT cut_at
FROM evidence_routing_cutover
WHERE singleton;
