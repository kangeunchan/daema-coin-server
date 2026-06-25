-- Final production schema checks after all migrations.
-- Every row should return status = 'ok'.

WITH required_tables(table_name) AS (
    VALUES
        ('internal_accounts'),
        ('auth_sessions'),
        ('auth_oauth_states'),
        ('customer_profiles'),
        ('github_identities'),
        ('wallet_accounts'),
        ('ledger_transactions'),
        ('booths'),
        ('products'),
        ('orders'),
        ('payment_intents'),
        ('payments'),
        ('worldcup_predictions'),
        ('prediction_settlements'),
        ('github_commit_events'),
        ('audit_logs')
)
SELECT
    'required_table' AS check_name,
    table_name AS target,
    CASE WHEN to_regclass('public.' || table_name) IS NOT NULL THEN 'ok' ELSE 'missing' END AS status
FROM required_tables
ORDER BY table_name;

SELECT
    'legacy_jsonb_store_absent' AS check_name,
    'public.records' AS target,
    CASE WHEN to_regclass('public.records') IS NULL THEN 'ok' ELSE 'drop_required' END AS status;

SELECT
    'wallet_non_negative' AS check_name,
    'wallet_accounts.balance' AS target,
    CASE WHEN count(*) = 0 THEN 'ok' ELSE 'invalid_balance' END AS status,
    count(*) AS invalid_count
FROM wallet_accounts
WHERE balance < 0;

SELECT
    'ledger_wallet_fk' AS check_name,
    'ledger_transactions.wallet_account_id' AS target,
    CASE WHEN count(*) = 0 THEN 'ok' ELSE 'missing_wallet' END AS status,
    count(*) AS invalid_count
FROM ledger_transactions lt
LEFT JOIN wallet_accounts wa ON wa.id = lt.wallet_account_id
WHERE wa.id IS NULL;

SELECT
    'active_internal_account_roles' AS check_name,
    'internal_accounts.role' AS target,
    CASE WHEN count(*) = 0 THEN 'ok' ELSE 'invalid_role' END AS status,
    count(*) AS invalid_count
FROM internal_accounts
WHERE role NOT IN ('admin', 'booth');
