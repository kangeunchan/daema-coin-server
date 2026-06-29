ALTER TABLE auth_sessions
    ADD COLUMN IF NOT EXISTS principal_id TEXT;

UPDATE auth_sessions
SET principal_id = COALESCE(customer_id, internal_account_id, id)
WHERE principal_id IS NULL;

ALTER TABLE auth_sessions
    ALTER COLUMN principal_id SET NOT NULL,
    ADD COLUMN IF NOT EXISTS session_data JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE auth_sessions
    DROP CONSTRAINT IF EXISTS auth_sessions_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'auth_sessions'::regclass
            AND conname = 'auth_sessions_principal_shape_check'
    ) THEN
        ALTER TABLE auth_sessions
            ADD CONSTRAINT auth_sessions_principal_shape_check
            CHECK (
                (principal_type = 'customer' AND internal_account_id IS NULL)
                OR
                (principal_type = 'internal_account' AND internal_account_id IS NOT NULL AND customer_id IS NULL)
            );
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_principal
    ON auth_sessions(principal_type, principal_id);
