-- Reverse of 20260511000015_seed_default_themes.sql. Removes the 10 default
-- themes by name (tenant_id IS NULL) so a clean re-up reseeds without
-- duplicate-violation risk on the partial unique index.
--
-- Down sequence: when both *14.down and *15.down are applied as part of a
-- full revert, the runner applies them in reverse-timestamp order (latest
-- first), so *15.down runs against an org_themes table that still exists.
-- Running *15.down alone (without *14.down) wipes only the seed and leaves
-- org_themes intact — useful while iterating on slice 053 theme work.

DELETE FROM org_themes
WHERE tenant_id IS NULL
  AND theme_name IN (
    'ownership',
    'tech-debt',
    'access-control',
    'key-management',
    'data-protection',
    'availability',
    'monitoring',
    'supply-chain',
    'vendor-risk',
    'human-process'
  );
