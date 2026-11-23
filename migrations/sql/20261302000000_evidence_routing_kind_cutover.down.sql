-- security-atlas -- reverse 20261302000000_evidence_routing_kind_cutover.sql.
--
-- Run it only with a build whose routed list no longer names a late kind:
-- routed.Cutover refuses a late kind without its row, so pinned reads fail
-- closed rather than widen.

DROP TABLE IF EXISTS evidence_routing_kind_cutover;
