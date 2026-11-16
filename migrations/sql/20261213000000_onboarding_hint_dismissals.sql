-- security-atlas — #2145: first-visit onboarding hints, remembered per user.
--
-- One row per (tenant, user, module) the user has dismissed the "New to
-- <module>?" hint for. No row means the hint shows. Read and written through
-- GET/PATCH /v1/me/preferences (`dismissed_hints`), alongside the slice 108
-- notification matrix; "Show onboarding hints again" deletes the user's rows.
--
-- Keyed per user like user_notification_preferences: there is no tenant-wide
-- row. The module CHECK mirrors userprefs.HintModules (the help module ids in
-- web/lib/help/modules.ts) and the two move together, so the allow-list also
-- caps how many rows one user can hold.
--
-- Constitutional invariant #6: tenant_id on every row, ENABLE + FORCE RLS,
-- one policy per verb the app role uses (no UPDATE: a dismissal is inserted
-- or deleted, never edited).

CREATE TABLE IF NOT EXISTS user_onboarding_hint_dismissals (
    tenant_id     UUID NOT NULL,
    user_id       UUID NOT NULL,
    module        TEXT NOT NULL,
    dismissed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_onboarding_hint_dismissals_pk
        PRIMARY KEY (tenant_id, user_id, module),
    -- Composite FK: the user must belong to the row's tenant.
    CONSTRAINT user_onboarding_hint_dismissals_user_fk
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT user_onboarding_hint_dismissals_module_check
        CHECK (module IN ('controls'))
);

ALTER TABLE user_onboarding_hint_dismissals ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_onboarding_hint_dismissals FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON user_onboarding_hint_dismissals
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_write ON user_onboarding_hint_dismissals
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_delete ON user_onboarding_hint_dismissals
        FOR DELETE USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, DELETE ON user_onboarding_hint_dismissals TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON user_onboarding_hint_dismissals TO atlas_migrate;
