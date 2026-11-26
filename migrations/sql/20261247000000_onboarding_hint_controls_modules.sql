-- security-atlas — #2150: onboarding hints for the Controls help modules.
--
-- Widens the module CHECK on user_onboarding_hint_dismissals
-- (20261229000000, #2154) to the help modules #2150 adds, so a user can
-- dismiss their "New to <module>?" hint. The list mirrors
-- userprefs.HintModules (internal/auth/userprefs/hints.go) and the help
-- files registered in web/lib/help/modules.ts; the three move together.

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
        'scf-catalog',
        'evidence',
        'exceptions',
        'controls-checklist'
    ));
