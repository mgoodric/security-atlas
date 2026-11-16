-- Reverses 20261203000000_scf_edge_remap_proposals.sql (#1777). Edges an
-- approval created stay in fw_to_scf_edges; only the queue and its history
-- go.
DROP FUNCTION IF EXISTS scf_edge_remap_lock_open_unchanged();
DROP FUNCTION IF EXISTS scf_edge_remap_decide(UUID, TEXT, TEXT, TEXT, TEXT, BOOLEAN);
DROP TABLE IF EXISTS scf_edge_remap_decisions;
DROP TABLE IF EXISTS scf_edge_remap_proposals;
