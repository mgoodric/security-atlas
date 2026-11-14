-- Reverts 20261025000000_control_import_settings.sql. Every tenant falls back
-- to the cloud-only rerank gate.
DROP TABLE IF EXISTS control_import_settings CASCADE;
