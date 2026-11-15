-- security-atlas — #2232: one vendor review due date.
--
-- The vendor review due date was spelled out three times: in
-- ListOverdueVendors, in CountVendorsForBurndown and in the compliance
-- calendar's vendor branch, each with its own cadence CASE (the calendar's
-- also left out never-reviewed vendors). This function is the one
-- definition, and internal/vendor ReviewDueDate is its Go mirror.
--
-- The due date is a DATE:
--   - reviewed: last_review_date plus the cadence as a calendar interval
--     (1, 3, 6 or 12 months; Postgres clamps the day to the month's end);
--   - never reviewed: the UTC date of created_at plus the tenant's
--     initial-review window (#2061).
--
-- A vendor is overdue only once its due day has ended (strictly after the
-- UTC due day), the internal/duedate rule: due_date < cutoff_date.
--
-- STABLE is a conservative marking: every operation in the body, including
-- timezone('UTC', timestamptz), is IMMUTABLE, but nothing indexes on the
-- function, so nothing needs the stronger promise.

CREATE OR REPLACE FUNCTION vendor_review_due_date(
    last_review         date,
    cadence             vendor_review_cadence,
    created_at          timestamptz,
    initial_review_days integer
) RETURNS date
LANGUAGE sql
STABLE
PARALLEL SAFE
AS $$
    SELECT CASE
        WHEN last_review IS NULL THEN
            (created_at AT TIME ZONE 'UTC')::date + initial_review_days
        ELSE
            (last_review + CASE cadence
                WHEN 'monthly'   THEN INTERVAL '1 month'
                WHEN 'quarterly' THEN INTERVAL '3 months'
                WHEN 'biannual'  THEN INTERVAL '6 months'
                WHEN 'annual'    THEN INTERVAL '12 months'
            END)::date
    END
$$;

COMMENT ON FUNCTION vendor_review_due_date(date, vendor_review_cadence, timestamptz, integer) IS
    '#2232: the UTC date a vendor review is due. Overdue only after that day. Mirrored by internal/vendor ReviewDueDate.';
