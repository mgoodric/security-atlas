-- Reverses 20261217000000_onboarding_hint_settings_admin_modules.sql
-- (#2155). Dismissals for the Settings & admin modules are deleted first,
-- because the narrower CHECK would reject them; those hints show again.

DELETE FROM user_onboarding_hint_dismissals WHERE module <> 'controls';

ALTER TABLE user_onboarding_hint_dismissals
    DROP CONSTRAINT IF EXISTS user_onboarding_hint_dismissals_module_check;

ALTER TABLE user_onboarding_hint_dismissals
    ADD CONSTRAINT user_onboarding_hint_dismissals_module_check
    CHECK (module IN ('controls'));
