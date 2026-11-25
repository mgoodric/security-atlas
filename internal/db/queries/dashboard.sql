-- Slice 066 — dashboard backend read endpoints.
--
-- Pure SELECT families that surface existing data behind the
-- dashboard-grained read paths slice 040's program dashboard view shipped
-- binding placeholders for. This slice adds NO migration and NO write
-- surface — it reads framework_versions + the UCF graph and the slice-062
-- admin_audit_log_v view. (The upcoming rollup it once also served was
-- retired by #2130; the compliance calendar is the one list of what is due.)
--
-- All tenant-scoped reads run inside the request's app.current_tenant GUC
-- (set by the slice-033 tenancy middleware); RLS is the defense-in-depth
-- layer and the WHERE clauses on tenant_id are the primary correctness
-- guarantee (canvas invariant #6). Catalog tables (framework_versions,
-- frameworks, framework_requirements, fw_to_scf_edges, scf_anchors) carry
-- no tenant_id and no RLS — they are global, platform-bundled.
--
-- Cursor pagination over the activity feed is
-- keyset, not OFFSET: a (timestamp, id) keyset is stable under concurrent
-- appends. The keyset predicate is decomposed (ts < cursor_ts OR (ts =
-- cursor_ts AND id < cursor_id)) rather than a ROW(...) tuple comparison —
-- a row-comparison tuple mis-infers the id placeholder's type under sqlc
-- (the slice-064 decisions log records this). Each paginated query fetches
-- limit+1 rows so the handler detects a next page without a COUNT
-- round-trip.

-- name: FrameworkPosture :many
-- AC-1: one row per framework version, with coverage_pct, a freshness
-- composite, and a 90-day trend delta. Aggregates slice-008 UCF coverage to
-- the framework-version grain.
--
-- Coverage path (constitutional invariant #1 — through the SCF anchor
-- spine, never per-framework duplicated controls):
--   framework_versions
--     -> framework_requirements (per version)
--       -> fw_to_scf_edges (non-`no_relationship`)
--         -> scf_anchors
--           <- controls.scf_anchor_id (the tenant's active controls)
-- A requirement is "covered" when at least one active (superseded_by IS
-- NULL) tenant control is anchored on an SCF anchor that a real STRM edge
-- connects to that requirement. coverage_pct = covered / total
-- requirements for the version.
--
-- freshness_composite: of the tenant controls that cover this version's
-- requirements, the fraction that are NOT stale per the slice-016
-- evidence_freshness read model. A control with no freshness row counts as
-- stale (it has, definitionally, no current evidence).
--
-- trend_delta_90d: coverage_pct now minus the coverage_pct as it stood ~90
-- days ago. "As it stood" is reconstructed from the control_evaluations
-- ledger (slice 012): a control counted toward coverage 90 days ago iff its
-- latest evaluation on/before the cutoff had result 'pass' or 'excepted'
-- (#2112: excepted counts as covered, now and then). The delta is in
-- percentage points (e.g. +12.5 means coverage rose 12.5 points).
--
-- covered_by_exception_pct (#2112): the share of requirements covered today
-- only by controls that rest on an active exception (at least one of the
-- control's cells evaluates `excepted`). It is part of coverage_pct, and
-- callers show it beside it so a waiver never reads as a demonstrated control.
--
-- Statement of Applicability (#1788): a requirement the tenant's ACTIVATED
-- framework-scope revision for the version marks `excluded` leaves the
-- requirement set entirely, so it drops out of the coverage numerator and
-- denominator, the freshness composite, and both halves of the trend. The
-- trend uses today's exclusions for the 90-day-ago half too, so excluding a
-- requirement never shows up as a jump. Exclusions on draft, review or
-- approved revisions have no effect; undecided requirements stay in.
--
-- scope_activated (#2056, #2114): whether the tenant has an ACTIVATED
-- framework-scope revision for the version. Without one, nothing has been
-- decided out of scope, so the percentage is over the whole requirement set;
-- callers show "Scope not set" for that version instead of the number.
--
-- trend_history (#2056): whether any control covering the version has an
-- evaluation (pass or fail) on or before the cutoff. Without one there is no
-- 90-day-ago state to compare against, covered_then is empty, and
-- trend_delta_90d equals coverage_pct; callers show "no baseline yet"
-- instead of that delta.
--
-- The whole thing is ONE statement with CTEs — no N+1 (anti-criterion P0).
-- $1 = tenant_id; $2 = the 90-day-ago cutoff timestamptz (computed in Go).
WITH version_reqs AS (
    -- Every (framework_version, requirement) pair for current versions,
    -- less the requirements the activated scope revision excludes.
    SELECT
        fv.id            AS framework_version_id,
        fv.framework_id  AS framework_id,
        fv.version       AS framework_version,
        f.name           AS framework_name,
        fr.id            AS requirement_id
    FROM framework_versions fv
    JOIN frameworks f ON f.id = fv.framework_id
    JOIN framework_requirements fr ON fr.framework_version_id = fv.id
    WHERE fv.status = 'current'
      AND NOT EXISTS (
          SELECT 1
          FROM framework_scopes fs
          JOIN framework_scope_applicability fsa
            ON fsa.framework_scope_id = fs.id
           AND fsa.tenant_id = fs.tenant_id
          WHERE fs.tenant_id = $1
            AND fs.framework_version_id = fv.id
            AND fs.state = 'activated'
            AND fsa.framework_requirement_id = fr.id
            AND fsa.decision = 'excluded'
      )
),
req_anchor AS (
    -- Each requirement -> the SCF anchors a real STRM edge connects it to.
    SELECT
        vr.framework_version_id,
        vr.framework_id,
        vr.framework_version,
        vr.requirement_id,
        e.scf_anchor_id
    FROM version_reqs vr
    JOIN effective_fw_to_scf_edges e
      ON e.framework_requirement_id = vr.requirement_id
     AND e.relationship_type <> 'no_relationship'
     AND e.mapping_tier <> 'rejected'
),
covering_control AS (
    -- Each (requirement, anchor) pair joined to the tenant's active controls
    -- anchored there. RLS scopes `controls` to the caller's tenant; the
    -- explicit tenant_id predicate is the primary guarantee.
    SELECT DISTINCT
        ra.framework_version_id,
        ra.framework_id,
        ra.framework_version,
        ra.requirement_id,
        c.id AS control_id
    FROM req_anchor ra
    JOIN controls c
      ON c.scf_anchor_id = ra.scf_anchor_id
     AND c.tenant_id = $1
     -- Any non-superseded control counts, whatever its lifecycle_state:
     -- coverage measures defined controls. ListFrameworkScopeContradictions
     -- (framework_scopes.sql) also requires lifecycle_state = 'active', on
     -- purpose (#1789 D3). Do not align either one to the other. See
     -- GLOSSARY "Active control".
     AND c.superseded_by IS NULL
),
covered_now AS (
    -- Requirements covered today: at least one covering control exists.
    SELECT framework_version_id, requirement_id
    FROM covering_control
    GROUP BY framework_version_id, requirement_id
),
control_passed_then AS (
    -- For each covering control, whether its latest evaluation on/before the
    -- 90-day cutoff counted toward coverage: a pass, or `excepted` (#2112).
    -- The engine writes `excepted` only while an exception is in force, so an
    -- excepted row at the cutoff means the exception was active then.
    -- DISTINCT ON picks the latest row per control.
    SELECT DISTINCT ON (ce.control_id)
        ce.control_id,
        (ce.result IN ('pass', 'excepted')) AS passed_then
    FROM control_evaluations ce
    WHERE ce.tenant_id = $1
      AND ce.evaluated_at <= $2
      AND ce.evaluated_as_of IS NULL  -- #3071: replays are not live state
    ORDER BY ce.control_id, ce.evaluated_at DESC
),
covered_then AS (
    -- Requirements covered 90 days ago: at least one covering control that
    -- was passing (or excepted) on/before the cutoff.
    SELECT DISTINCT cc.framework_version_id, cc.requirement_id
    FROM covering_control cc
    JOIN control_passed_then cpt ON cpt.control_id = cc.control_id
    WHERE cpt.passed_then
),
control_latest_run AS (
    -- #2269: each control's latest evaluation run. One run writes one row
    -- per scope cell the control's applicability_expr resolves to at that
    -- moment, so the run's rows are the cells that currently apply. A cell
    -- that stopped applying has no row in it.
    SELECT DISTINCT ON (ce.control_id)
        ce.control_id,
        ce.eval_run_id
    FROM control_evaluations ce
    WHERE ce.tenant_id = $1
      AND ce.evaluated_as_of IS NULL  -- #3071: replays are not live state
    ORDER BY ce.control_id, ce.evaluated_at DESC, ce.created_at DESC
),
control_excepted_now AS (
    -- #2112: covering controls whose coverage rests on an exception today:
    -- at least one of their scope cells is `excepted` in the control's
    -- latest run. #2269: only that run counts, so a cell excepted in an
    -- earlier run that no longer applies does not keep the control
    -- excepted.
    SELECT DISTINCT ce.control_id
    FROM control_evaluations ce
    JOIN control_latest_run lr
      ON lr.control_id = ce.control_id
     AND lr.eval_run_id = ce.eval_run_id
    WHERE ce.tenant_id = $1
      AND ce.result = 'excepted'
),
covered_by_exception AS (
    -- Requirements covered today only by exception: every covering control
    -- rests on an exception. A requirement with one covering control that is
    -- not excepted does not depend on a waiver and is not counted.
    SELECT cc.framework_version_id, cc.requirement_id
    FROM covering_control cc
    LEFT JOIN control_excepted_now cen ON cen.control_id = cc.control_id
    GROUP BY cc.framework_version_id, cc.requirement_id
    HAVING BOOL_AND(cen.control_id IS NOT NULL)
),
control_latest_run_then AS (
    -- #2269: each control's latest evaluation run on/before the cutoff, the
    -- 90-day counterpart of control_latest_run.
    SELECT DISTINCT ON (ce.control_id)
        ce.control_id,
        ce.eval_run_id
    FROM control_evaluations ce
    WHERE ce.tenant_id = $1
      AND ce.evaluated_at <= $2
      AND ce.evaluated_as_of IS NULL  -- #3071: replays are not live state
    ORDER BY ce.control_id, ce.evaluated_at DESC, ce.created_at DESC
),
control_excepted_then AS (
    -- #2269: controls resting on an exception at the cutoff: a scope cell is
    -- `excepted` in their latest run on/before it.
    SELECT DISTINCT ce.control_id
    FROM control_evaluations ce
    JOIN control_latest_run_then lr
      ON lr.control_id = ce.control_id
     AND lr.eval_run_id = ce.eval_run_id
    WHERE ce.tenant_id = $1
      AND ce.result = 'excepted'
),
covered_by_exception_then AS (
    -- #2269: requirements covered at the cutoff only by exception: every
    -- covering control that counted toward coverage then rested on an
    -- exception then. The baseline's share of covered_by_exception.
    SELECT cc.framework_version_id, cc.requirement_id
    FROM covering_control cc
    JOIN control_passed_then cpt ON cpt.control_id = cc.control_id
    LEFT JOIN control_excepted_then cet ON cet.control_id = cc.control_id
    WHERE cpt.passed_then
    GROUP BY cc.framework_version_id, cc.requirement_id
    HAVING BOOL_AND(cet.control_id IS NOT NULL)
),
history_versions AS (
    -- Framework versions with at least one covering control evaluated on or
    -- before the cutoff.
    SELECT DISTINCT cc.framework_version_id
    FROM covering_control cc
    JOIN control_passed_then cpt ON cpt.control_id = cc.control_id
),
scoped_versions AS (
    -- Framework versions with an activated scope revision in the tenant.
    SELECT DISTINCT fs.framework_version_id
    FROM framework_scopes fs
    WHERE fs.tenant_id = $1
      AND fs.state = 'activated'
),
control_freshness AS (
    -- Per covering control, whether it is fresh per the slice-016 read
    -- model. A control with no freshness row is treated as stale.
    SELECT DISTINCT
        cc.framework_version_id,
        cc.control_id,
        COALESCE(NOT ef.is_stale, FALSE) AS is_fresh
    FROM covering_control cc
    LEFT JOIN evidence_freshness ef
      ON ef.tenant_id = $1
     AND ef.control_id = cc.control_id
)
SELECT
    vr.framework_id,
    vr.framework_version,
    vr.framework_version_id,
    -- framework_name (#2083): callers label a version by name plus
    -- version ("ISO/IEC 27001:2022"), never the version alone.
    vr.framework_name,
    -- coverage_pct: covered requirements / total requirements, 0..100.
    CASE WHEN COUNT(DISTINCT vr.requirement_id) = 0 THEN 0.0
         ELSE ROUND(
             100.0 * COUNT(DISTINCT cn.requirement_id)
                   / COUNT(DISTINCT vr.requirement_id), 2)
    END::float8 AS coverage_pct,
    -- covered_by_exception_pct (#2112): requirements covered only by
    -- excepted controls / total requirements, 0..100. A subset of
    -- coverage_pct, reported separately so a waiver never reads as a
    -- demonstrated control.
    CASE WHEN COUNT(DISTINCT vr.requirement_id) = 0 THEN 0.0
         ELSE ROUND(
             100.0 * COUNT(DISTINCT cbe.requirement_id)
                   / COUNT(DISTINCT vr.requirement_id), 2)
    END::float8 AS covered_by_exception_pct,
    -- covered_by_exception_then_pct (#2269): the same share at the 90-day
    -- cutoff, so a baseline derived from trend_delta_90d can say how much of
    -- it rested on exceptions. A subset of the coverage then.
    CASE WHEN COUNT(DISTINCT vr.requirement_id) = 0 THEN 0.0
         ELSE ROUND(
             100.0 * COUNT(DISTINCT cbet.requirement_id)
                   / COUNT(DISTINCT vr.requirement_id), 2)
    END::float8 AS covered_by_exception_then_pct,
    -- freshness_composite: fresh covering controls / all covering controls,
    -- 0..100. 0 when the version has no covering controls.
    CASE WHEN COUNT(DISTINCT cf.control_id) = 0 THEN 0.0
         ELSE ROUND(
             100.0 * COUNT(DISTINCT cf.control_id) FILTER (WHERE cf.is_fresh)
                   / COUNT(DISTINCT cf.control_id), 2)
    END::float8 AS freshness_composite,
    -- trend_delta_90d: coverage_pct now minus coverage_pct at the cutoff, in
    -- percentage points.
    CASE WHEN COUNT(DISTINCT vr.requirement_id) = 0 THEN 0.0
         ELSE ROUND(
             100.0 * COUNT(DISTINCT cn.requirement_id)
                   / COUNT(DISTINCT vr.requirement_id)
           - 100.0 * COUNT(DISTINCT ct.requirement_id)
                   / COUNT(DISTINCT vr.requirement_id), 2)
    END::float8 AS trend_delta_90d,
    BOOL_OR(sv.framework_version_id IS NOT NULL)::bool AS scope_activated,
    BOOL_OR(hv.framework_version_id IS NOT NULL)::bool AS trend_history
FROM version_reqs vr
LEFT JOIN covered_now      cn ON cn.framework_version_id = vr.framework_version_id
                              AND cn.requirement_id      = vr.requirement_id
LEFT JOIN covered_then     ct ON ct.framework_version_id = vr.framework_version_id
                              AND ct.requirement_id      = vr.requirement_id
LEFT JOIN covered_by_exception cbe ON cbe.framework_version_id = vr.framework_version_id
                                  AND cbe.requirement_id      = vr.requirement_id
LEFT JOIN covered_by_exception_then cbet ON cbet.framework_version_id = vr.framework_version_id
                                        AND cbet.requirement_id      = vr.requirement_id
LEFT JOIN control_freshness cf ON cf.framework_version_id = vr.framework_version_id
LEFT JOIN scoped_versions  sv ON sv.framework_version_id = vr.framework_version_id
LEFT JOIN history_versions hv ON hv.framework_version_id = vr.framework_version_id
GROUP BY vr.framework_id, vr.framework_version, vr.framework_version_id, vr.framework_name
ORDER BY vr.framework_id, vr.framework_version;

-- name: ListEvidenceActivity :many
-- AC-2: paginated read model over the evidence-ingest event archive. Reads
-- the slice-062 admin_audit_log_v view filtered to the evidence_audit_log
-- branch — that branch IS the slice-013/015 evidence-ingest event archive,
-- projected to the AC-2 row shape {ts, event_type, actor, resource_type,
-- resource_id, summary}. The view is RLS-aware: each source table's
-- tenant_read policy fires under the caller's GUC, so this SELECT returns
-- only the caller's tenant rows. The explicit tenant_id predicate is the
-- primary guarantee.
--
-- Keyset paginated newest-first over (ts, resource_id). The keyset
-- predicate is decomposed (not a ROW(...) tuple) for the sqlc
-- type-inference reason in the file header. A sentinel cursor
-- (cursor_ts = 'infinity', cursor_id = max-uuid string) selects the first
-- page. The handler computes the cutoff values in Go and fetches limit+1.
SELECT ts, event_type, actor, resource_type, resource_id, summary
FROM admin_audit_log_v
WHERE tenant_id = $1
  AND source_table = 'evidence_audit_log'
  AND (
        ts < sqlc.arg(cursor_ts)
        OR (ts = sqlc.arg(cursor_ts) AND resource_id < sqlc.arg(cursor_id))
      )
ORDER BY ts DESC, resource_id DESC
LIMIT sqlc.arg(row_limit);
