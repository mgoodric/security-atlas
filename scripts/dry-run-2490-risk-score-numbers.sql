-- security-atlas #2490 dry run: how many stored risks carry a score that
-- POST /v1/risks and PATCH /v1/risks/{id} now refuse with a 422.
--
-- Read-only and counts-only. Every statement runs in a READ ONLY
-- transaction that is rolled back. It prints counts per tenant and
-- methodology, never a risk id, title or score. It writes nothing and
-- fixes nothing: the rows it counts stay as they are, and the readers keep
-- reading a non-numeric score as unscored. Run it as a role that can see
-- every tenant (the migrate/owner role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2490-risk-score-numbers.sql
--
-- The rule (risk.ValidateScoreNumbers), for every non-FAIR risk:
--   a score field present in inherent_score or residual_score is a JSON
--   number in range: score and severity 0..25, likelihood and impact 1..5.
--   residual_score is unscored ('{}' or JSON null) or an object that
--   carries score, severity, or likelihood and impact as numbers.
-- FAIR keeps its own validation and is not counted.

BEGIN TRANSACTION READ ONLY;

WITH fields(key, lo, hi) AS (
    VALUES ('score', 0, 25), ('severity', 0, 25), ('likelihood', 1, 5), ('impact', 1, 5)
),
blobs AS (
    SELECT r.tenant_id, r.methodology::text AS methodology, b.which, b.blob
    FROM risks r
    CROSS JOIN LATERAL (
        VALUES ('inherent_score', r.inherent_score), ('residual_score', r.residual_score)
    ) AS b(which, blob)
    WHERE r.methodology <> 'fair'
),
checked AS (
    SELECT tenant_id, methodology, which,
           CASE
               WHEN blob IS NULL OR blob = 'null'::jsonb OR blob = '{}'::jsonb THEN 'ok'
               WHEN jsonb_typeof(blob) <> 'object' THEN 'not_an_object'
               WHEN EXISTS (
                   SELECT 1 FROM fields f
                   WHERE blob ? f.key AND jsonb_typeof(blob -> f.key) <> 'number'
               ) THEN 'field_not_a_number'
               WHEN EXISTS (
                   SELECT 1 FROM fields f
                   WHERE jsonb_typeof(blob -> f.key) = 'number'
                     AND ((blob ->> f.key)::numeric < f.lo OR (blob ->> f.key)::numeric > f.hi)
               ) THEN 'field_out_of_range'
               WHEN which = 'residual_score'
                AND jsonb_typeof(blob -> 'score') IS DISTINCT FROM 'number'
                AND jsonb_typeof(blob -> 'severity') IS DISTINCT FROM 'number'
                AND (jsonb_typeof(blob -> 'likelihood') IS DISTINCT FROM 'number'
                     OR jsonb_typeof(blob -> 'impact') IS DISTINCT FROM 'number')
                   THEN 'residual_without_a_score'
               ELSE 'ok'
           END AS finding
    FROM blobs
)
SELECT tenant_id, methodology, which, finding, count(*) AS risks
FROM checked
WHERE finding <> 'ok'
GROUP BY tenant_id, methodology, which, finding
ORDER BY tenant_id, methodology, which, finding;

-- The total across every tenant, so an empty result above reads as zero
-- rather than as a query that did not run.
SELECT count(*) FILTER (WHERE methodology <> 'fair') AS non_fair_risks,
       count(*) AS all_risks
FROM risks;

ROLLBACK;
