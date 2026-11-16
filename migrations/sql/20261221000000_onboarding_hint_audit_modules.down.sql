-- Reverses 20261221000000_onboarding_hint_audit_modules.sql (#2152).
-- Dismissals for the Audit modules are deleted first, because the narrower
-- CHECK would reject them; those hints show again.

DELETE FROM user_onboarding_hint_dismissals
WHERE module IN ('audits', 'framework-scopes', 'board-packs', 'csf', 'vendor-claims');

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
        'admin-demo'
    ));
