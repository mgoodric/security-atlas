-- Reverse of 20260511000006_vendor_lite.sql. Drops the vendor tables and
-- the enum types, leaving the slice-017 baseline byte-identical.

DROP TABLE IF EXISTS vendor_scope_cells CASCADE;
DROP TABLE IF EXISTS vendors CASCADE;

DROP TYPE IF EXISTS vendor_review_cadence;
DROP TYPE IF EXISTS vendor_criticality;
