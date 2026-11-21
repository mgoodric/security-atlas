-- security-atlas -- #2850: synced policies (Confluence policy sync, #2813).
--
-- POST /v1/policies:sync takes the full set of policy pages from one
-- source (a Confluence site and space) and writes each new page as a
-- published policy. This migration gives such a row somewhere to live:
--
--   * source_attribution gains 'synced', beside 'imported'.
--   * New columns on policies:
--       sync_site / sync_space  the source the page came from.
--       sync_page_id            the page's id within that source, which
--                               identifies it across runs (its title and
--                               URL can change).
--       synced_body_sha256      hex sha256 of body_md as synced, so an
--                               unchanged re-push is recognised.
--       synced_by / synced_at   the policy-sync key that wrote the row,
--                               and when.
--     A CHECK requires all of them, and the historical approver and date,
--     on every synced row, and forbids the sync columns on any other row.
--   * One published synced row per (tenant, source, page): a partial
--     unique index, so two overlapping runs cannot both create the page.
--   * policy_audit_log.action gains 'synced'. The row's actor is the
--     sync key's id.
--
-- The historical_approver_name / historical_approved_at / source_url
-- columns (#1747) hold the page's recorded approver, approval date and
-- URL, as they do for an imported policy.
--
-- Constitutional invariant #6: policies and policy_audit_log keep their
-- RLS policies; this migration adds no table.
--
-- Reversible via 20261279000000_policy_sync.down.sql.

ALTER TABLE policies
    ADD COLUMN sync_site    TEXT        NULL,
    ADD COLUMN sync_space   TEXT        NULL,
    ADD COLUMN sync_page_id TEXT        NULL,
    ADD COLUMN synced_body_sha256 TEXT        NULL,
    ADD COLUMN synced_by    TEXT        NULL,
    ADD COLUMN synced_at    TIMESTAMPTZ NULL;

ALTER TABLE policies
    DROP CONSTRAINT policies_source_attribution_chk;

ALTER TABLE policies
    ADD CONSTRAINT policies_source_attribution_chk
        CHECK (source_attribution IN ('community_draft', 'tenant_authored', 'vendor_provided', 'imported', 'synced')),
    ADD CONSTRAINT policies_synced_fields_chk
        CHECK (
            (source_attribution = 'synced') = (
                sync_site IS NOT NULL AND length(sync_site) BETWEEN 1 AND 500
                AND sync_space IS NOT NULL AND length(sync_space) BETWEEN 1 AND 500
                AND sync_page_id IS NOT NULL AND length(sync_page_id) BETWEEN 1 AND 500
                AND synced_body_sha256 IS NOT NULL AND synced_body_sha256 ~ '^[0-9a-f]{64}$'
                AND synced_by IS NOT NULL AND length(synced_by) > 0
                AND synced_at IS NOT NULL
            )
        ),
    ADD CONSTRAINT policies_synced_historical_chk
        CHECK (
            source_attribution <> 'synced' OR (
                historical_approver_name IS NOT NULL
                AND length(historical_approver_name) > 0
                AND historical_approved_at IS NOT NULL
                AND source_url IS NOT NULL
            )
        );

CREATE UNIQUE INDEX idx_policies_synced_page
    ON policies (tenant_id, sync_site, sync_space, sync_page_id)
    WHERE source_attribution = 'synced' AND status = 'published';

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped', 'synced'));
