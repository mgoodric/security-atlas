-- Reverse #2232: drop the shared vendor review due-date function.
DROP FUNCTION IF EXISTS vendor_review_due_date(date, vendor_review_cadence, timestamptz, integer);
