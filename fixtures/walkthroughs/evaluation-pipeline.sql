-- Slice 070 — fixture for `evaluation-pipeline.md`.
--
-- The base seed (00-seed.sql) installs one tenant, scope, framework,
-- and control. The audit-period fixture seeds three evidence records.
-- This fixture is intentionally empty — the evaluation-pipeline
-- walkthrough re-uses the seed + audit-period evidence to demonstrate
-- the end-to-end trace.
--
-- Kept as a placeholder file so `walkthroughs-refresh` can apply a
-- single per-walkthrough SQL per recipe iteration (no special-casing).

BEGIN;
-- intentionally empty
COMMIT;
