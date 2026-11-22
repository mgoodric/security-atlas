-- Slice 022 — policy library queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. The state machine
--   draft -> under_review -> approved -> published -> superseded
-- is enforced by per-transition UPDATEs that include `WHERE status = '...'`
-- as the prior-state guard; zero affected rows means the transition was
-- attempted from the wrong state (the application probes after to
-- disambiguate ErrNotFound vs ErrWrongState, matching slice 021's pattern).
-- Publishing a later version is a two-step operation (supersede prior +
-- publish the successor row in place); the application wraps both queries
-- in a single tx so the chain is atomic.

-- name: CreatePolicy :one
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, source_attribution, created_by, draft_editors
)
VALUES (
    $1, $2, $3,
    $4, $5, $6,
    $7, $8,
    $9, $10,
    'draft', $11, $12, COALESCE(sqlc.narg('draft_editors')::text[], '{}')
)
RETURNING *;

-- name: GetPolicyByID :one
SELECT *
FROM policies
WHERE tenant_id = $1 AND id = $2;

-- name: ListPolicies :many
-- Returns every policy for the tenant, newest first. Handler applies
-- status filter in-memory (cardinality is small per canvas v1 scope).
SELECT *
FROM policies
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListPoliciesUnderReview :many
-- #2457: the policies waiting for approval, for GET /v1/approvals and its
-- sidebar badge. Served by the (tenant_id, status, created_at DESC) index.
SELECT *
FROM policies
WHERE tenant_id = $1
  AND status = 'under_review'
ORDER BY created_at DESC, id ASC;

-- name: ListPoliciesWithAckRate :many
-- Slice 107 — paginated policy list LEFT JOINed to the per-policy
-- acknowledgment-rate cell. Single query; the handler MUST NOT loop
-- per-policy (anti-criterion P0 -- the whole point of the extension).
--
-- Shape:
--   1. The base policies SELECT is identical to ListPolicies (same
--      columns, same WHERE, same ORDER BY) so the result row's
--      first N columns scan into the existing dbx.Policy struct
--      verbatim.
--   2. Two scalar subqueries compute the denominator + numerator
--      ONLY for rows with status = 'published'. A CASE WHEN wraps
--      the subqueries so non-published rows return NULL for both
--      columns (the handler renders `ack_rate: null`).
--   3. The denominator subquery mirrors CountRequiredRoleUsersForVersion
--      verbatim (#2586: distinct active users holding a live user_roles
--      row the audience names, or any live role for 'everyone').
--   4. The numerator subquery mirrors CountFreshAcksForVersion verbatim
--      (distinct user_ids in policy_acknowledgments for THIS policy
--      version, acknowledged_at >= freshness_cutoff, gated on the same
--      role-intersect EXISTS predicate). The freshness cutoff is a
--      parameter so tests inject without time-bombing.
--
-- Constitutional invariants:
--   #6 RLS: policies + user_roles + users + policy_acknowledgments are tenant-scoped
--      under FORCE ROW LEVEL SECURITY. The tenant GUC the slice-033
--      middleware sets filters every table this query touches.
--   #9 Manual evidence first-class: this query is uniform whether the
--      policy was manually authored or imported from a vendor template.
--
-- See policy_acknowledgments.sql `CountRequiredRoleUsersForVersion` and
-- `CountFreshAcksForVersion` for the canonical predicates this query
-- mirrors -- they MUST stay in sync. The HTTP handler GET
-- /v1/policies/{id}/acknowledgment-rate calls those queries directly
-- per-policy; this slice runs the same math in one round-trip for the
-- list view.
-- Slice 159 Option C: the per-policy ack-rate cells are computed in
-- a CTE that filters `WHERE p.status = 'published'` and is then
-- LEFT JOINed back to the full policy list. sqlc v1.31.1 sees the
-- LEFT JOIN as nullable and emits `*int64` (under
-- `emit_pointers_for_null_types: true`) — non-published policies
-- get nil pointers for both ack columns. The slice-107 handler is
-- updated to use the pointer-style API (`r.AckDenominator != nil`
-- + `*r.AckDenominator`). JSON response shape is unchanged: nil
-- pointers marshal to `null`, populated pointers marshal to the
-- bigint value. See
-- `docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md`.
WITH ack_cells AS (
    SELECT
        p.id AS policy_id,
        (SELECT COUNT(DISTINCT u.id)::bigint
         FROM user_roles ur
         JOIN users u
           ON u.tenant_id = ur.tenant_id
          AND u.id::text  = regexp_replace(ur.user_id, '^user:', '')
         WHERE ur.tenant_id = p.tenant_id
           AND u.status = 'active'
           AND (
               ur.role = ANY(p.acknowledgment_required_roles)
               -- #2524: an "everyone" audience counts every live member.
               OR 'everyone' = ANY(p.acknowledgment_required_roles)
           )
        ) AS ack_denominator,
        (SELECT COUNT(DISTINCT pa.user_id)::bigint
         FROM policy_acknowledgments pa
         WHERE pa.tenant_id = p.tenant_id
           AND pa.policy_version_id = p.id
           AND pa.acknowledged_at >= sqlc.arg('freshness_cutoff')::timestamptz
           AND EXISTS (
               SELECT 1
               FROM user_roles ur
               JOIN users u
                 ON u.tenant_id = ur.tenant_id
                AND u.id::text  = regexp_replace(ur.user_id, '^user:', '')
               WHERE ur.tenant_id = pa.tenant_id
                 AND u.id = pa.user_id
                 AND u.status = 'active'
                 AND (
                     ur.role = ANY(p.acknowledgment_required_roles)
                     OR 'everyone' = ANY(p.acknowledgment_required_roles)
                 )
           )
        ) AS ack_numerator
    FROM policies p
    WHERE p.tenant_id = $1
      AND p.status = 'published'
)
SELECT
    p.id, p.tenant_id, p.title, p.version, p.effective_date, p.body_md,
    p.acknowledgment_required_roles, p.status, p.created_at, p.updated_at,
    p.predecessor_id, p.owner_role, p.approver_role, p.linked_control_ids,
    p.source_attribution, p.created_by, p.submitted_at, p.submitted_by,
    p.approved_at, p.approved_by, p.published_at, p.published_by,
    p.superseded_at, p.next_review_at,
    p.historical_approver_name, p.historical_approved_at, p.source_url,
    p.imported_by, p.imported_at, p.self_approved,
    p.sync_drift_detected_at, p.sync_drift_body_sha256,
    p.sync_missing_since,
    ac.ack_denominator,
    ac.ack_numerator
FROM policies p
LEFT JOIN ack_cells ac ON ac.policy_id = p.id
WHERE p.tenant_id = $1
ORDER BY p.created_at DESC, p.id ASC;

-- name: ListPolicyVersionChain :many
-- Returns the version chain for a policy id by walking predecessor_id.
-- Recursive CTE keeps the query inside Postgres rather than client-side
-- traversal. Returns oldest-first so the chain reads naturally
-- (predecessor -> successor).
WITH RECURSIVE chain AS (
    -- Anchor: the policy itself.
    SELECT *
    FROM policies p0
    WHERE p0.tenant_id = $1 AND p0.id = $2

    UNION ALL

    -- Walk forward: any row whose predecessor_id is the current chain tip.
    SELECT p.*
    FROM policies p
    INNER JOIN chain c ON p.predecessor_id = c.id AND p.tenant_id = c.tenant_id
)
SELECT chain.*
FROM chain
ORDER BY chain.created_at ASC, chain.id ASC;

-- name: SubmitPolicyForReview :one
-- Transition: draft -> under_review. Operator action; no role gate.
-- Also re-submits an under_review policy whose submitter was recorded
-- before #1807 as a session id ('jwt:<token id>') or a credential id
-- ('key_<id>'), which approval refuses because it names no person:
-- re-submitting records the person (#2200).
UPDATE policies
SET status = 'under_review',
    submitted_at = now(),
    submitted_by = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND (status = 'draft'
       OR (status = 'under_review'
           AND (submitted_by LIKE 'jwt:%' OR submitted_by LIKE 'key\_%')))
RETURNING *;

-- name: ApprovePolicy :one
-- Transition: under_review -> approved. Requires IsApprover at handler
-- (AC-4). The DB only guards the prior-state. self_approved is set by the
-- store after the approval rule allowed a self-approval (#2101).
UPDATE policies
SET status = 'approved',
    approved_at = now(),
    approved_by = $3,
    self_approved = sqlc.arg('self_approved'),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'under_review'
RETURNING *;

-- name: SupersedePolicyAtPublish :one
-- Part 1 of the two-step Publish: marks the prior 'published' row as
-- 'superseded'. Used inside the same transaction as PublishApprovedPolicy
-- (which publishes the successor row in place) so the chain transition is
-- atomic. Returns the updated prior row so the
-- handler can audit who superseded it. Only matches when the row is
-- currently 'published' (defense-in-depth -- a chain can't supersede a
-- draft).
--
-- next_review_at is left as it was (#2009): the successor carries its own
-- review date, and the superseded row keeps the date it was due so an
-- auditor can still see it and compare it with the successor's
-- published_at. Only a 'published' row has a review due, so the calendar
-- and the list's review_status ignore it.
UPDATE policies
SET status = 'superseded',
    superseded_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'published'
RETURNING *;

-- name: PublishApprovedPolicy :one
-- Publishes an approved row in place. Sets status -> published, populates
-- effective_date, published_at, published_by. predecessor_id is never
-- changed: a first version keeps NULL, and a later version (a draft
-- created with predecessor_id set) keeps the link to the row that Part 1
-- just superseded (#1747). next_review_at is the review date the publisher
-- names (#1806); NULL when none is given.
UPDATE policies
SET status = 'published',
    effective_date = $3,
    published_at = now(),
    published_by = $4,
    next_review_at = $5,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'approved'
RETURNING *;

-- name: ImportPublishedPolicy :one
-- #1747 — insert an imported policy directly as 'published', carrying its
-- historical lifecycle. The in-tool submit/approve/publish transitions do
-- not apply; the handler gates the import on an admin credential instead.
-- approved_by stays NULL: the historical approver is a free-text name in
-- historical_approver_name, not a credential.
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, effective_date, next_review_at, source_attribution,
    created_by, published_at, published_by,
    historical_approver_name, historical_approved_at, source_url,
    imported_by, imported_at
)
VALUES (
    sqlc.arg('id'), sqlc.arg('tenant_id'), NULL,
    sqlc.arg('title'), sqlc.arg('version'), sqlc.arg('body_md'),
    sqlc.arg('owner_role'), sqlc.arg('approver_role'),
    sqlc.arg('linked_control_ids'), sqlc.arg('acknowledgment_required_roles'),
    'published', sqlc.arg('effective_date'), sqlc.narg('next_review_at'), 'imported',
    sqlc.arg('imported_by'), now(), sqlc.arg('imported_by'),
    sqlc.arg('historical_approver_name'), sqlc.arg('historical_approved_at'), sqlc.narg('source_url'),
    sqlc.arg('imported_by'), now()
)
RETURNING *;

-- name: PolicyTitleVersionExists :one
-- #1747 — duplicate check for import: a policy with this title and version
-- already exists in the tenant (in any state).
SELECT EXISTS (
    SELECT 1 FROM policies
    WHERE tenant_id = $1 AND title = $2 AND version = $3
) AS exists;

-- name: InsertPolicyAuditLog :one
-- #1747 — append one row to the policy audit log.
INSERT INTO policy_audit_log (
    tenant_id, policy_id, action, actor, source_file,
    historical_approver_name, historical_approved_at, source_url,
    previous_next_review_at, new_next_review_at,
    policy_title, policy_version,
    previous_state, new_state,
    body_sha256, editors,
    previous_linked_control_ids, new_linked_control_ids,
    previous_source_attribution, sync_drift_body_sha256
)
VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('policy_id'), sqlc.arg('action'), sqlc.arg('actor'), sqlc.arg('source_file'),
    sqlc.narg('historical_approver_name'), sqlc.narg('historical_approved_at'), sqlc.narg('source_url'),
    sqlc.narg('previous_next_review_at'), sqlc.narg('new_next_review_at'),
    sqlc.narg('policy_title'), sqlc.narg('policy_version'),
    sqlc.narg('previous_state'), sqlc.narg('new_state'),
    sqlc.narg('body_sha256'), sqlc.narg('editors'),
    sqlc.narg('previous_linked_control_ids'), sqlc.narg('new_linked_control_ids'),
    sqlc.narg('previous_source_attribution'), sqlc.narg('sync_drift_body_sha256')
)
RETURNING *;

-- name: ListPolicyAuditLog :many
-- #1747 — the audit trail for one policy, oldest first.
SELECT *
FROM policy_audit_log
WHERE tenant_id = $1 AND policy_id = $2
ORDER BY created_at ASC, id ASC;

-- name: UpdateSyncedPolicyLinks :one
-- #2852 — replace a published synced policy's control links in place. It
-- is not a new version: the links are kept in security-atlas, not synced.
-- The caller writes the policy_audit_log row in the same transaction.
-- Other rows do not match.
UPDATE policies
SET linked_control_ids = sqlc.arg('linked_control_ids'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'synced'
  AND status = 'published'
RETURNING *;

-- name: UpdatePolicyNextReviewAt :one
-- #1806 — change the review date of a published policy in place. It is not
-- a new version: the row keeps its id, version and approval. The caller
-- writes the policy_audit_log row in the same transaction. Only the
-- published chain tip has a review due, so other states do not match.
UPDATE policies
SET next_review_at = sqlc.narg('next_review_at'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND status = 'published'
RETURNING *;

-- name: DeleteDraftPolicy :one
-- #1987 — discard a draft. Only a row still in 'draft' matches, so a policy
-- that has been submitted, approved, published or superseded is never
-- deleted here. Deleting a next-version draft frees its predecessor's
-- successor slot (policies_predecessor_unique_when_set). The caller writes
-- the policy_audit_log row in the same transaction.
DELETE FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND status = 'draft'
RETURNING *;

-- name: UpdateDraftPolicy :one
-- #2116 — edit a draft in place. Only a row still in 'draft' matches, so a
-- submitted, approved, published or superseded policy is never rewritten
-- here: a published policy changes through a new version (predecessor_id).
-- version, predecessor_id and source_attribution are not editable.
UPDATE policies
SET title = sqlc.arg('title'),
    body_md = sqlc.arg('body_md'),
    owner_role = sqlc.arg('owner_role'),
    approver_role = sqlc.arg('approver_role'),
    linked_control_ids = sqlc.arg('linked_control_ids'),
    acknowledgment_required_roles = sqlc.arg('acknowledgment_required_roles'),
    draft_editors = CASE
        WHEN sqlc.arg('editor')::text = ANY(draft_editors) THEN draft_editors
        ELSE array_append(draft_editors, sqlc.arg('editor')::text)
    END,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND status = 'draft'
RETURNING *;

-- name: CountTenantControls :one
-- #2446 — how many of the given control ids exist in the tenant. A policy's
-- linked_control_ids must all resolve here on create and draft edit.
-- A retired control is not counted (#2459): it no longer governs anything,
-- so it cannot keep a policy from being an orphan. Controls are never
-- soft-deleted; every other lifecycle state counts.
SELECT count(*)::bigint AS n
FROM controls
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = ANY(sqlc.arg('ids')::uuid[])
  AND lifecycle_state <> 'retired';

-- name: GetSyncedPolicyByPage :one
-- #2850 — the published synced row for one page of one sync source, if
-- any. idx_policies_synced_page makes it unique.
SELECT *
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_site = sqlc.arg('sync_site')
  AND sync_space = sqlc.arg('sync_space')
  AND sync_page_id = sqlc.arg('sync_page_id');

-- name: InsertSyncedPolicy :one
-- #2850 — insert a page's synced version directly as 'published'. A
-- page's first sync has no predecessor; a later version (#2851) names the
-- synced row it supersedes, which SupersedeSyncedPolicy has just marked
-- 'superseded' in the same transaction, and carries that row's control
-- links forward (links are kept in security-atlas, not synced). As with
-- ImportPublishedPolicy, the in-tool submit/approve/publish transitions do
-- not apply: the source's approval, kept in historical_approver_name and
-- historical_approved_at, is the approval. approved_by stays NULL.
-- created_by, published_by and synced_by are the policy-sync key's id.
INSERT INTO policies (
    id, tenant_id, predecessor_id,
    title, version, body_md,
    owner_role, approver_role,
    linked_control_ids, acknowledgment_required_roles,
    status, effective_date, next_review_at, source_attribution,
    created_by, published_at, published_by,
    historical_approver_name, historical_approved_at, source_url,
    sync_site, sync_space, sync_page_id, synced_body_sha256,
    synced_by, synced_at
)
VALUES (
    sqlc.arg('id'), sqlc.arg('tenant_id'), sqlc.narg('predecessor_id'),
    sqlc.arg('title'), sqlc.arg('version'), sqlc.arg('body_md'),
    sqlc.arg('owner_role'), sqlc.arg('approver_role'),
    sqlc.arg('linked_control_ids'), sqlc.arg('acknowledgment_required_roles'),
    'published', sqlc.arg('effective_date'), sqlc.narg('next_review_at'), 'synced',
    sqlc.arg('synced_by'), now(), sqlc.arg('synced_by'),
    sqlc.arg('historical_approver_name'), sqlc.arg('historical_approved_at'), sqlc.arg('source_url'),
    sqlc.arg('sync_site'), sqlc.arg('sync_space'), sqlc.arg('sync_page_id'), sqlc.arg('synced_body_sha256'),
    sqlc.arg('synced_by'), now()
)
RETURNING *;

-- name: LockPolicySyncSource :exec
-- #2850: serialises sync runs for one source in one tenant, so an
-- overlapping run waits for the first to commit and then finds its pages
-- unchanged instead of creating them twice. Released at the end of the
-- transaction. Namespace 13 is advisorylock.PolicySyncSource.
SELECT pg_advisory_xact_lock(
    13,
    hashtext(sqlc.arg(tenant_id)::text || '|' || sqlc.arg(sync_site)::text || '|' || sqlc.arg(sync_space)::text)
);

-- name: SupersedeSyncedPolicy :one
-- #2851 — a page arrived with a new declared version: its published
-- synced row is superseded, in the same transaction as InsertSyncedPolicy
-- writes the next version. Any drift recorded on it is cleared, since the
-- new version is what the page holds now. next_review_at is left as it
-- was, as SupersedePolicyAtPublish leaves it (#2009).
UPDATE policies
SET status = 'superseded',
    superseded_at = now(),
    sync_drift_detected_at = NULL,
    sync_drift_body_sha256 = NULL,
    sync_missing_since = NULL,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'synced'
  AND status = 'published'
RETURNING *;

-- name: SetSyncedPolicyDrift :one
-- #2851 — the page's body changed without a version bump. Records the
-- body hash the source holds now; sync_drift_detected_at keeps the time
-- drift was first seen, so a page edited twice without a bump still shows
-- how long it has been out of step.
--
-- A run that sees the same drifted body again writes nothing: the
-- IS DISTINCT FROM guard leaves the row (and updated_at) alone, and the
-- query returns no row, which the caller reads as "already recorded".
UPDATE policies
SET sync_drift_detected_at = COALESCE(sync_drift_detected_at, now()),
    sync_drift_body_sha256 = sqlc.arg('sync_drift_body_sha256'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_drift_body_sha256 IS DISTINCT FROM sqlc.arg('sync_drift_body_sha256')
RETURNING *;

-- name: ClearSyncedPolicyDrift :one
-- #2851 — the page's body matches the synced version again: the drift
-- recorded on the row is cleared.
UPDATE policies
SET sync_drift_detected_at = NULL,
    sync_drift_body_sha256 = NULL,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'synced'
  AND status = 'published'
RETURNING *;

-- name: LockSyncedPolicyForSuccessor :one
-- #2851 — before a sync run supersedes a page's synced row, lock it and
-- report whether another row already names it as predecessor. FOR UPDATE
-- conflicts with the KEY SHARE lock a concurrent in-tool create takes
-- through the predecessor FK, so the answer cannot change before the run
-- commits. A successor (an in-tool draft of the next version) would make
-- the run's insert violate policies_predecessor_unique_when_set.
SELECT p.id,
       s.id AS successor_id,
       s.status AS successor_status
FROM policies p
LEFT JOIN policies s
  ON s.tenant_id = p.tenant_id AND s.predecessor_id = p.id
WHERE p.tenant_id = sqlc.arg('tenant_id')
  AND p.id = sqlc.arg('id')
FOR UPDATE OF p;

-- name: ClearSyncedPolicyMissing :exec
-- #2853 — the pages of this run are present: any missing state recorded
-- on their published synced rows by an earlier run is cleared. Rows not
-- marked missing are left alone, so updated_at moves only on a change.
UPDATE policies
SET sync_missing_since = NULL,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_site = sqlc.arg('sync_site')
  AND sync_space = sqlc.arg('sync_space')
  AND sync_page_id = ANY(sqlc.arg('present_page_ids')::text[])
  AND sync_missing_since IS NOT NULL;

-- name: MarkSyncedPoliciesMissing :exec
-- #2853 — every published synced row of this source whose page is absent
-- from the run, not yet marked, gets sync_missing_since = now(). A row
-- already marked keeps the time it was first found missing and is not
-- rewritten. The policy itself is untouched: still published, and
-- acknowledgements continue.
UPDATE policies
SET sync_missing_since = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_site = sqlc.arg('sync_site')
  AND sync_space = sqlc.arg('sync_space')
  AND NOT (sync_page_id = ANY(sqlc.arg('present_page_ids')::text[]))
  AND sync_missing_since IS NULL;

-- name: CountPublishedSyncedPoliciesInSource :one
-- #2902 — the published synced rows of one source at the start of a run:
-- an empty push for a source that has some is refused without
-- allow_empty, and a run that leaves more than half of them missing
-- carries a warning. Read under LockPolicySyncSource.
SELECT count(*)
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_site = sqlc.arg('sync_site')
  AND sync_space = sqlc.arg('sync_space');

-- name: ListSyncedPoliciesMissingFromSource :many
-- #2853 — the published synced rows of one source whose page a run found
-- absent, for that run's report. Read after MarkSyncedPoliciesMissing in
-- the same transaction.
SELECT *
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_site = sqlc.arg('sync_site')
  AND sync_space = sqlc.arg('sync_space')
  AND sync_missing_since IS NOT NULL
ORDER BY sync_page_id;

-- name: ListMissingSyncedPolicies :many
-- #2853 — the published synced policies whose page a sync run found
-- absent, for GET /v1/needs-attention.
SELECT id, title, owner_role, sync_missing_since
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'synced'
  AND status = 'published'
  AND sync_missing_since IS NOT NULL
ORDER BY sync_missing_since, id;

-- name: ListImportedPoliciesForTakeover :many
-- #2854 — on a page's first sync, every published imported policy in the
-- tenant with the page's title. The page takes over the policy only when
-- there is exactly one; the import does not chain versions, so a title
-- can carry several, and then the sync refuses to choose. FOR UPDATE locks
-- the rows until the run commits. A concurrent run for another source
-- that reads the same title waits on the lock; under READ COMMITTED its
-- read then re-checks the committed row, which is no longer a published
-- imported one, and leaves it out. There is no LIMIT, so the waiting run
-- sees every remaining candidate, not an empty result.
SELECT *
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution = 'imported'
  AND status = 'published'
  AND title = sqlc.arg('title')
ORDER BY published_at DESC NULLS LAST, id
FOR UPDATE;

-- name: GetAuthoredPolicyByTitle :one
-- #2854 — a policy authored in security-atlas (not imported, not synced)
-- with the page's title, which a sync never takes over. Only read to say
-- so in the run report.
SELECT id, status
FROM policies
WHERE tenant_id = sqlc.arg('tenant_id')
  AND source_attribution NOT IN ('imported', 'synced')
  AND status <> 'superseded'
  AND title = sqlc.arg('title')
ORDER BY created_at DESC, id
LIMIT 1;

-- name: TakeOverImportedPolicy :one
-- #2854 — the page's first sync carries the version an imported policy
-- already has: the imported row becomes the page's synced row in place,
-- so its acknowledgements and control links stay where they are. Its body
-- and version are not rewritten. synced_body_sha256 is the hash of the
-- body it was published with; a page body that differs is recorded as
-- drift (sync_drift_body_sha256 non-NULL), as a later run would. The
-- historical approver stays the import's. imported_by and imported_at are
-- cleared, as policies_imported_fields_chk requires off imported rows.
UPDATE policies
SET source_attribution = 'synced',
    imported_by = NULL,
    imported_at = NULL,
    source_url = sqlc.arg('source_url'),
    sync_site = sqlc.arg('sync_site'),
    sync_space = sqlc.arg('sync_space'),
    sync_page_id = sqlc.arg('sync_page_id'),
    synced_body_sha256 = sqlc.arg('synced_body_sha256'),
    synced_by = sqlc.arg('synced_by'),
    synced_at = now(),
    sync_drift_detected_at = CASE WHEN sqlc.narg('sync_drift_body_sha256')::text IS NULL THEN NULL ELSE now() END,
    sync_drift_body_sha256 = sqlc.narg('sync_drift_body_sha256'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'imported'
  AND status = 'published'
RETURNING *;

-- name: SupersedeImportedPolicy :one
-- #2854 — the page's first sync carries a different version from the
-- imported policy it takes over: the imported row is superseded, in the
-- same transaction as InsertSyncedPolicy writes the synced version as its
-- next version. It stays 'imported'. next_review_at is left as it was, as
-- SupersedeSyncedPolicy leaves it.
UPDATE policies
SET status = 'superseded',
    superseded_at = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id')
  AND source_attribution = 'imported'
  AND status = 'published'
RETURNING *;
