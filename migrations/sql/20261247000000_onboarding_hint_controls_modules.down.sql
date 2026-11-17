-- Reverses 20261247000000_onboarding_hint_controls_modules.sql (#2150).
-- Dismissals for the Controls modules are deleted first, because the narrower
-- CHECK would reject them; those hints show again.

DELETE FROM user_onboarding_hint_dismissals
WHERE module IN ('evidence', 'exceptions', 'controls-checklist');

ALTER TABLE user_onboarding_hint_dismissals
    DROP CONSTRAINT IF EXISTS user_onboarding_hint_dismissals_module_check;

ALTER TABLE user_onboarding_hint_dismissals
    ADD CONSTRAINT user_onboarding_hint_dismissals_module_check
    CHECK (module IN (
        'controls',
        'settings',
        'admin',
        'admin-users',
        'admin-api-keys',
        'admin-sso',
        'admin-features',
        'admin-tenants',
        'admin-super-admins',
        'admin-demo',
        'audits',
        'framework-scopes',
        'board-packs',
        'csf',
        'vendor-claims',
        'policies',
        'vendors',
        'personnel-security',
        'questionnaires',
        'scf-catalog'
    ));
