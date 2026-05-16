-- Down migration for 20260516000001_metrics_catalog.sql.
--
-- Drops in reverse FK + dependency order: trigger + function, then the
-- five tables in reverse-FK order (the cascade-edges + observations +
-- targets + inputs reference metrics_catalog).

DROP TRIGGER IF EXISTS trg_metric_inputs_replicate ON metric_inputs;
DROP FUNCTION IF EXISTS fn_metric_inputs_replicate();

DROP TABLE IF EXISTS metric_inputs;
DROP TABLE IF EXISTS metric_targets;
DROP TABLE IF EXISTS metric_observations;
DROP TABLE IF EXISTS metric_cascade_edges;
DROP TABLE IF EXISTS metrics_catalog;
