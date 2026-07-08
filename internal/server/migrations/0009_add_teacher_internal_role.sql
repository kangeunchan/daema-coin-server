ALTER TABLE internal_accounts
    DROP CONSTRAINT IF EXISTS internal_accounts_role_check;

ALTER TABLE internal_accounts
    ADD CONSTRAINT internal_accounts_role_check
    CHECK (role IN ('admin', 'booth', 'teacher'));

ALTER TABLE auth_sessions
    DROP CONSTRAINT IF EXISTS auth_sessions_role_check;

ALTER TABLE auth_sessions
    ADD CONSTRAINT auth_sessions_role_check
    CHECK (role IN ('customer', 'admin', 'booth', 'teacher'));
