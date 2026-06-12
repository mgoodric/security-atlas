-- Reverse of 20260611000000_vendor_reviews_ledger.sql. Drops the ledger
-- table and the outcome enum, leaving the slice-024 vendor baseline
-- byte-identical. The back-filled rows are dropped with the table; the
-- vendors.last_review_date scalar is untouched (this slice never altered
-- the vendors table).

DROP TABLE IF EXISTS vendor_reviews CASCADE;

DROP TYPE IF EXISTS vendor_review_outcome;
