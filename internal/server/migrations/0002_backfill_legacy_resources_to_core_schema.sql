CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TEMP TABLE IF NOT EXISTS migration_legacy_resource_payloads (
    domain TEXT NOT NULL,
    id TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
) ON COMMIT PRESERVE ROWS;

TRUNCATE migration_legacy_resource_payloads;

DO $$
BEGIN
    IF to_regclass('public.records') IS NOT NULL THEN
        EXECUTE 'INSERT INTO migration_legacy_resource_payloads(domain, id, data, created_at, updated_at)
                 SELECT domain, id, data, created_at, updated_at
                 FROM public.records';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION daema_jsonb_bigint(value JSONB, fallback BIGINT DEFAULT 0)
RETURNS BIGINT
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
    raw TEXT;
BEGIN
    IF value IS NULL OR value = 'null'::jsonb THEN
        RETURN fallback;
    END IF;

    raw := trim(both '"' FROM value::text);
    raw := regexp_replace(raw, '[^0-9\.-]', '', 'g');

    IF raw = '' OR raw = '-' OR raw = '.' THEN
        RETURN fallback;
    END IF;

    RETURN floor(raw::numeric)::bigint;
EXCEPTION WHEN others THEN
    RETURN fallback;
END;
$$;

CREATE OR REPLACE FUNCTION daema_jsonb_timestamptz(value JSONB, fallback TIMESTAMPTZ DEFAULT now())
RETURNS TIMESTAMPTZ
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
    raw TEXT;
BEGIN
    IF value IS NULL OR value = 'null'::jsonb THEN
        RETURN fallback;
    END IF;

    raw := trim(both '"' FROM value::text);
    IF raw = '' THEN
        RETURN fallback;
    END IF;

    RETURN raw::timestamptz;
EXCEPTION WHEN others THEN
    RETURN fallback;
END;
$$;

INSERT INTO internal_accounts (
    id,
    login_id,
    password_hash,
    role,
    status,
    display_name,
    force_password_change,
    created_by,
    last_login_at,
    created_at,
    updated_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'loginId', ''), NULLIF(r.data->>'login', ''), r.id),
    COALESCE(NULLIF(r.data->>'passwordHash', ''), 'MIGRATED_PASSWORD_RESET_REQUIRED'),
    CASE WHEN r.data->>'role' = 'admin' THEN 'admin' ELSE 'booth' END,
    CASE WHEN r.data->>'status' IN ('active', 'locked', 'disabled') THEN r.data->>'status' ELSE 'active' END,
    NULLIF(r.data->>'displayName', ''),
    COALESCE((r.data->>'forcePasswordChange')::boolean, TRUE),
    NULLIF(r.data->>'createdBy', ''),
    daema_jsonb_timestamptz(r.data->'lastLoginAt', NULL),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'auth.internal_accounts'
ON CONFLICT (id) DO UPDATE SET
    login_id = EXCLUDED.login_id,
    password_hash = EXCLUDED.password_hash,
    role = EXCLUDED.role,
    status = EXCLUDED.status,
    display_name = EXCLUDED.display_name,
    force_password_change = EXCLUDED.force_password_change,
    created_by = EXCLUDED.created_by,
    last_login_at = EXCLUDED.last_login_at,
    updated_at = EXCLUDED.updated_at;

INSERT INTO customer_profiles (
    id,
    display_name,
    school_name,
    student_no,
    grade,
    class_no,
    status,
    created_at,
    updated_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'displayName', ''), NULLIF(r.data->>'githubLogin', ''), r.id),
    NULLIF(r.data->>'schoolName', ''),
    NULLIF(r.data->>'studentNo', ''),
    NULLIF(r.data->>'grade', ''),
    NULLIF(r.data->>'classNo', ''),
    CASE WHEN r.data->>'status' IN ('active', 'disabled', 'deleted') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'customer_profiles'
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    school_name = EXCLUDED.school_name,
    student_no = EXCLUDED.student_no,
    grade = EXCLUDED.grade,
    class_no = EXCLUDED.class_no,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

INSERT INTO github_identities (
    id,
    customer_id,
    github_id,
    login,
    email,
    avatar_url,
    html_url,
    created_at,
    updated_at
)
SELECT
    'github-identity-' || COALESCE(NULLIF(r.data->>'githubId', ''), regexp_replace(r.id, '[^a-zA-Z0-9_-]', '_', 'g')),
    r.id,
    daema_jsonb_bigint(r.data->'githubId', 0),
    COALESCE(NULLIF(r.data->>'githubLogin', ''), NULLIF(r.data->>'login', ''), r.id),
    NULLIF(r.data->>'email', ''),
    NULLIF(r.data->>'avatarUrl', ''),
    NULLIF(r.data->>'htmlUrl', ''),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'customer_profiles'
    AND (r.data ? 'githubId' OR r.data ? 'githubLogin')
ON CONFLICT (id) DO UPDATE SET
    customer_id = EXCLUDED.customer_id,
    github_id = EXCLUDED.github_id,
    login = EXCLUDED.login,
    email = EXCLUDED.email,
    avatar_url = EXCLUDED.avatar_url,
    html_url = EXCLUDED.html_url,
    updated_at = EXCLUDED.updated_at;

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

INSERT INTO auth_sessions (
    id,
    token_hash,
    principal_id,
    principal_type,
    customer_id,
    internal_account_id,
    role,
    session_data,
    expires_at,
    created_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'tokenHash', ''), r.id),
    COALESCE(NULLIF(r.data->'user'->>'id', ''), r.id),
    CASE
        WHEN r.data->>'role' = 'customer' THEN 'customer'
        ELSE 'internal_account'
    END,
    CASE
        WHEN r.data->>'role' = 'customer'
            AND EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->'user'->>'id')
            THEN r.data->'user'->>'id'
        ELSE NULL
    END,
    CASE
        WHEN r.data->>'role' IN ('admin', 'booth', 'seller') THEN r.data->'user'->>'id'
        ELSE NULL
    END,
    CASE
        WHEN r.data->>'role' = 'admin' THEN 'admin'
        WHEN r.data->>'role' IN ('booth', 'seller') THEN 'booth'
        ELSE 'customer'
    END,
    r.data,
    daema_jsonb_timestamptz(r.data->'expiresAt', now()),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'auth.sessions'
    AND (
        r.data->>'role' = 'customer'
        OR
        (r.data->>'role' IN ('admin', 'booth', 'seller') AND EXISTS (SELECT 1 FROM internal_accounts ia WHERE ia.id = r.data->'user'->>'id'))
    )
ON CONFLICT (id) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    principal_id = EXCLUDED.principal_id,
    principal_type = EXCLUDED.principal_type,
    customer_id = EXCLUDED.customer_id,
    internal_account_id = EXCLUDED.internal_account_id,
    role = EXCLUDED.role,
    session_data = EXCLUDED.session_data,
    expires_at = EXCLUDED.expires_at;

INSERT INTO auth_oauth_states (
    state,
    provider,
    role,
    redirect_after,
    expires_at,
    created_at
)
SELECT
    r.id,
    'github',
    'customer',
    COALESCE(NULLIF(r.data->>'redirectAfter', ''), '/'),
    daema_jsonb_timestamptz(r.data->'expiresAt', now()),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'auth.oauth_states'
ON CONFLICT (state) DO UPDATE SET
    redirect_after = EXCLUDED.redirect_after,
    expires_at = EXCLUDED.expires_at;

INSERT INTO festivals (id, name, status, starts_at, ends_at, created_at, updated_at)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'title', ''), r.id),
    CASE WHEN r.data->>'status' IN ('draft', 'active', 'ended', 'archived') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'startsAt', NULL),
    daema_jsonb_timestamptz(r.data->'endsAt', NULL),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'festivals'
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    updated_at = EXCLUDED.updated_at;

INSERT INTO booth_categories (id, name, sort_order, created_at, updated_at)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'label', ''), r.id),
    daema_jsonb_bigint(r.data->'sortOrder', 0)::integer,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'booth_categories'
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    sort_order = EXCLUDED.sort_order,
    updated_at = EXCLUDED.updated_at;

INSERT INTO booths (id, festival_id, category_id, name, description, location_label, status, created_at, updated_at)
SELECT
    r.id,
    CASE WHEN EXISTS (SELECT 1 FROM festivals f WHERE f.id = r.data->>'festivalId') THEN r.data->>'festivalId' ELSE NULL END,
    CASE WHEN EXISTS (SELECT 1 FROM booth_categories bc WHERE bc.id = r.data->>'categoryId') THEN r.data->>'categoryId' ELSE NULL END,
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'title', ''), r.id),
    NULLIF(r.data->>'description', ''),
    COALESCE(NULLIF(r.data->>'locationLabel', ''), NULLIF(r.data->>'location', '')),
    CASE WHEN r.data->>'status' IN ('draft', 'active', 'paused', 'closed', 'archived') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'booths'
ON CONFLICT (id) DO UPDATE SET
    festival_id = EXCLUDED.festival_id,
    category_id = EXCLUDED.category_id,
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    location_label = EXCLUDED.location_label,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

INSERT INTO booths (id, name, status)
SELECT DISTINCT
    r.data->>'boothId',
    r.data->>'boothId',
    'active'
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'auth.internal_accounts'
    AND r.data->>'boothId' IS NOT NULL
    AND r.data->>'boothId' <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO booth_members (id, booth_id, account_id, role, status, created_at, updated_at)
SELECT
    'booth-member-' || regexp_replace(r.data->>'boothId', '[^a-zA-Z0-9_-]', '_', 'g') || '-' || regexp_replace(r.id, '[^a-zA-Z0-9_-]', '_', 'g'),
    r.data->>'boothId',
    r.id,
    'owner',
    'active',
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'auth.internal_accounts'
    AND r.data->>'boothId' IS NOT NULL
    AND r.data->>'boothId' <> ''
    AND EXISTS (SELECT 1 FROM internal_accounts ia WHERE ia.id = r.id)
ON CONFLICT (booth_id, account_id) DO UPDATE SET
    role = EXCLUDED.role,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

INSERT INTO products (id, booth_id, name, description, currency, price, stock_quantity, status, created_at, updated_at)
SELECT
    r.id,
    r.data->>'boothId',
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'title', ''), r.id),
    NULLIF(r.data->>'description', ''),
    CASE WHEN r.data->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'price', r.data->'amount'->'value', r.data->'amount'), 0), 0),
    NULLIF(daema_jsonb_bigint(r.data->'stockQuantity', -1), -1)::integer,
    CASE WHEN r.data->>'status' IN ('draft', 'active', 'sold_out', 'hidden', 'archived') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'products'
    AND r.data->>'boothId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM booths b WHERE b.id = r.data->>'boothId')
ON CONFLICT (id) DO UPDATE SET
    booth_id = EXCLUDED.booth_id,
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    currency = EXCLUDED.currency,
    price = EXCLUDED.price,
    stock_quantity = EXCLUDED.stock_quantity,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

INSERT INTO customer_profiles (id, display_name, status)
SELECT DISTINCT
    r.data->>'userId',
    COALESCE(NULLIF(r.data->>'githubLogin', ''), r.data->>'userId'),
    'active'
FROM migration_legacy_resource_payloads r
WHERE r.domain IN ('wallet_balances', 'ledger_transactions', 'orders', 'pay_barcodes', 'benefit_claims', 'worldcup_predictions', 'github_commits')
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'userId' <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO wallet_accounts (id, customer_id, currency, balance, created_at, updated_at)
SELECT
    r.id,
    r.data->>'userId',
    CASE WHEN r.data->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'balance', r.data->'amount'->'value', r.data->'amount'), 0), 0),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'wallet_balances'
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'userId' <> ''
ON CONFLICT (customer_id, currency) DO UPDATE SET
    balance = EXCLUDED.balance,
    updated_at = EXCLUDED.updated_at;

INSERT INTO wallet_accounts (id, customer_id, currency, balance)
SELECT DISTINCT
    'wallet-' || regexp_replace(r.data->>'userId', '[^a-zA-Z0-9_-]', '_', 'g') || '-' ||
        CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    r.data->>'userId',
    CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    0
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'ledger_transactions'
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'userId' <> ''
ON CONFLICT (customer_id, currency) DO NOTHING;

INSERT INTO ledger_transactions (
    id,
    wallet_account_id,
    customer_id,
    direction,
    currency,
    amount,
    transaction_type,
    reference_type,
    reference_id,
    idempotency_key,
    description,
    metadata,
    occurred_at,
    created_at
)
SELECT
    r.id,
    wa.id,
    r.data->>'userId',
    CASE WHEN r.data->>'direction' = 'income' THEN 'income' ELSE 'expense' END,
    wa.currency,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'amount'->'value', r.data->'amount', r.data->'value'), 0), 1),
    COALESCE(NULLIF(r.data->>'type', ''), 'migrated-resource'),
    NULLIF(COALESCE(r.data->>'referenceType', r.data->>'type'), ''),
    NULLIF(COALESCE(r.data->>'referenceId', r.data->>'orderId', r.data->>'matchId', r.data->>'commitSha'), ''),
    'legacy-resources:' || r.domain || ':' || r.id,
    NULLIF(r.data->>'description', ''),
    r.data,
    daema_jsonb_timestamptz(r.data->'occurredAt', daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
JOIN wallet_accounts wa
    ON wa.customer_id = r.data->>'userId'
    AND wa.currency = CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END
WHERE r.domain = 'ledger_transactions'
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'userId' <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO pay_barcodes (id, customer_id, code_hash, status, expires_at, used_at, created_at)
SELECT
    r.id,
    r.data->>'userId',
    encode(digest(COALESCE(r.data->>'code', r.id), 'sha256'), 'hex'),
    CASE WHEN r.data->>'status' IN ('active', 'used', 'expired', 'revoked') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'expiresAt', now()),
    daema_jsonb_timestamptz(r.data->'usedAt', NULL),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'pay_barcodes'
    AND r.data->>'userId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId')
ON CONFLICT (id) DO UPDATE SET
    customer_id = EXCLUDED.customer_id,
    code_hash = EXCLUDED.code_hash,
    status = EXCLUDED.status,
    expires_at = EXCLUDED.expires_at,
    used_at = EXCLUDED.used_at;

INSERT INTO booths (id, name, status)
SELECT DISTINCT
    r.data->>'boothId',
    r.data->>'boothId',
    'active'
FROM migration_legacy_resource_payloads r
WHERE r.domain IN ('orders', 'products', 'payment_intents', 'payments')
    AND r.data->>'boothId' IS NOT NULL
    AND r.data->>'boothId' <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO orders (
    id,
    customer_id,
    booth_id,
    status,
    currency,
    subtotal_amount,
    discount_amount,
    total_amount,
    created_at,
    updated_at
)
SELECT
    r.id,
    r.data->>'userId',
    r.data->>'boothId',
    CASE WHEN r.data->>'status' IN ('pending', 'paid', 'preparing', 'ready', 'completed', 'cancelled', 'refunded') THEN r.data->>'status' ELSE 'pending' END,
    CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'subtotalAmount'->'value', r.data->'subtotalAmount', r.data->'totalAmount'->'value', r.data->'totalAmount', r.data->'amount'->'value', r.data->'amount'), 0), 0),
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'discountAmount'->'value', r.data->'discountAmount'), 0), 0),
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'totalAmount'->'value', r.data->'totalAmount', r.data->'amount'->'value', r.data->'amount'), 0), 0),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'orders'
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'boothId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId')
    AND EXISTS (SELECT 1 FROM booths b WHERE b.id = r.data->>'boothId')
ON CONFLICT (id) DO UPDATE SET
    customer_id = EXCLUDED.customer_id,
    booth_id = EXCLUDED.booth_id,
    status = EXCLUDED.status,
    currency = EXCLUDED.currency,
    subtotal_amount = EXCLUDED.subtotal_amount,
    discount_amount = EXCLUDED.discount_amount,
    total_amount = EXCLUDED.total_amount,
    updated_at = EXCLUDED.updated_at;

INSERT INTO payment_intents (
    id,
    order_id,
    booth_id,
    customer_id,
    status,
    currency,
    amount,
    idempotency_key,
    expires_at,
    created_at,
    updated_at
)
SELECT
    r.id,
    CASE WHEN EXISTS (SELECT 1 FROM orders o WHERE o.id = r.data->>'orderId') THEN r.data->>'orderId' ELSE NULL END,
    r.data->>'boothId',
    CASE WHEN EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId') THEN r.data->>'userId' ELSE NULL END,
    CASE WHEN r.data->>'status' IN ('requires_capture', 'captured', 'cancelled', 'expired') THEN r.data->>'status' ELSE 'requires_capture' END,
    CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'amount'->'value', r.data->'amount'), 0), 1),
    'legacy-resources:payment_intents:' || r.id,
    daema_jsonb_timestamptz(r.data->'expiresAt', NULL),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'payment_intents'
    AND r.data->>'boothId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM booths b WHERE b.id = r.data->>'boothId')
ON CONFLICT (id) DO UPDATE SET
    status = EXCLUDED.status,
    amount = EXCLUDED.amount,
    updated_at = EXCLUDED.updated_at;

INSERT INTO payments (
    id,
    payment_intent_id,
    order_id,
    booth_id,
    customer_id,
    status,
    currency,
    amount,
    captured_at,
    created_at
)
SELECT
    r.id,
    r.data->>'paymentIntentId',
    CASE WHEN EXISTS (SELECT 1 FROM orders o WHERE o.id = r.data->>'orderId') THEN r.data->>'orderId' ELSE NULL END,
    r.data->>'boothId',
    CASE WHEN EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId') THEN r.data->>'userId' ELSE NULL END,
    CASE WHEN r.data->>'status' IN ('captured', 'partially_refunded', 'refunded', 'voided') THEN r.data->>'status' ELSE 'captured' END,
    CASE WHEN r.data->>'currency' = 'POINT' OR r.data->'amount'->>'currency' = 'POINT' THEN 'POINT' ELSE 'DMC' END,
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'amount'->'value', r.data->'amount'), 0), 1),
    daema_jsonb_timestamptz(r.data->'capturedAt', daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'payments'
    AND r.data->>'paymentIntentId' IS NOT NULL
    AND r.data->>'boothId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM payment_intents pi WHERE pi.id = r.data->>'paymentIntentId')
    AND EXISTS (SELECT 1 FROM booths b WHERE b.id = r.data->>'boothId')
ON CONFLICT (id) DO UPDATE SET
    status = EXCLUDED.status,
    amount = EXCLUDED.amount;

INSERT INTO benefits (id, benefit_type, name, status, metadata, created_at, updated_at)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'type', ''), 'general'),
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'title', ''), r.id),
    CASE WHEN r.data->>'status' IN ('active', 'disabled', 'archived') THEN r.data->>'status' ELSE 'active' END,
    r.data,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'benefits'
ON CONFLICT (id) DO UPDATE SET
    benefit_type = EXCLUDED.benefit_type,
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    metadata = EXCLUDED.metadata,
    updated_at = EXCLUDED.updated_at;

INSERT INTO benefit_claims (id, benefit_id, customer_id, status, claimed_at)
SELECT
    r.id,
    r.data->>'benefitId',
    r.data->>'userId',
    CASE WHEN r.data->>'status' = 'cancelled' THEN 'cancelled' ELSE 'claimed' END,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'benefit_claims'
    AND r.data->>'benefitId' IS NOT NULL
    AND r.data->>'userId' IS NOT NULL
    AND EXISTS (SELECT 1 FROM benefits b WHERE b.id = r.data->>'benefitId')
    AND EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId')
ON CONFLICT (benefit_id, customer_id) DO UPDATE SET
    status = EXCLUDED.status,
    claimed_at = EXCLUDED.claimed_at;

INSERT INTO worldcup_teams (id, country_code, name, flag_url, created_at, updated_at)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'countryCode', ''), r.id),
    COALESCE(NULLIF(r.data->>'name', ''), NULLIF(r.data->>'countryName', ''), r.id),
    NULLIF(r.data->>'flagUrl', ''),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'worldcup_teams'
ON CONFLICT (id) DO UPDATE SET
    country_code = EXCLUDED.country_code,
    name = EXCLUDED.name,
    flag_url = EXCLUDED.flag_url,
    updated_at = EXCLUDED.updated_at;

INSERT INTO worldcup_matches (
    id,
    external_fixture_id,
    home_team_id,
    away_team_id,
    status,
    starts_at,
    home_score,
    away_score,
    metadata,
    created_at,
    updated_at
)
SELECT
    r.id,
    NULLIF(COALESCE(r.data->>'externalFixtureId', r.data->>'fixtureId'), ''),
    CASE
        WHEN EXISTS (SELECT 1 FROM worldcup_teams wt WHERE wt.id = COALESCE(r.data->>'homeTeamId', r.data->'home'->>'id'))
        THEN COALESCE(r.data->>'homeTeamId', r.data->'home'->>'id')
        ELSE NULL
    END,
    CASE
        WHEN EXISTS (SELECT 1 FROM worldcup_teams wt WHERE wt.id = COALESCE(r.data->>'awayTeamId', r.data->'away'->>'id'))
        THEN COALESCE(r.data->>'awayTeamId', r.data->'away'->>'id')
        ELSE NULL
    END,
    CASE WHEN r.data->>'status' IN ('scheduled', 'live', 'finished', 'cancelled') THEN r.data->>'status' ELSE 'scheduled' END,
    daema_jsonb_timestamptz(COALESCE(r.data->'startsAt', r.data->'date'), r.created_at),
    NULLIF(daema_jsonb_bigint(COALESCE(r.data->'homeScore', r.data->'home'->'score'), -999999), -999999)::integer,
    NULLIF(daema_jsonb_bigint(COALESCE(r.data->'awayScore', r.data->'away'->'score'), -999999), -999999)::integer,
    r.data,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'worldcup_matches'
ON CONFLICT (id) DO UPDATE SET
    external_fixture_id = EXCLUDED.external_fixture_id,
    home_team_id = EXCLUDED.home_team_id,
    away_team_id = EXCLUDED.away_team_id,
    status = EXCLUDED.status,
    starts_at = EXCLUDED.starts_at,
    home_score = EXCLUDED.home_score,
    away_score = EXCLUDED.away_score,
    metadata = EXCLUDED.metadata,
    updated_at = EXCLUDED.updated_at;

INSERT INTO worldcup_matches (id, status, starts_at)
SELECT DISTINCT
    r.data->>'matchId',
    'scheduled',
    now()
FROM migration_legacy_resource_payloads r
WHERE r.domain IN ('worldcup_predictions', 'prediction_settlements')
    AND r.data->>'matchId' IS NOT NULL
    AND r.data->>'matchId' <> ''
ON CONFLICT (id) DO NOTHING;

INSERT INTO worldcup_predictions (
    id,
    match_id,
    customer_id,
    pick,
    currency,
    stake_amount,
    status,
    created_at,
    cancelled_at
)
SELECT
    r.id,
    r.data->>'matchId',
    r.data->>'userId',
    r.data->>'pick',
    'POINT',
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'stakeAmount', r.data->'stake'), 100), 1),
    CASE WHEN r.data->>'status' IN ('active', 'cancelled', 'settled') THEN r.data->>'status' ELSE 'active' END,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'cancelledAt', NULL)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'worldcup_predictions'
    AND r.data->>'matchId' IS NOT NULL
    AND r.data->>'userId' IS NOT NULL
    AND r.data->>'pick' IN ('home', 'draw', 'away')
    AND EXISTS (SELECT 1 FROM worldcup_matches wm WHERE wm.id = r.data->>'matchId')
    AND EXISTS (SELECT 1 FROM customer_profiles cp WHERE cp.id = r.data->>'userId')
ON CONFLICT (match_id, customer_id) DO UPDATE SET
    pick = EXCLUDED.pick,
    stake_amount = EXCLUDED.stake_amount,
    status = EXCLUDED.status,
    cancelled_at = EXCLUDED.cancelled_at;

INSERT INTO prediction_settlements (
    id,
    match_id,
    winning_pick,
    status,
    total_pool,
    allocated_amount,
    source,
    note,
    settled_at,
    created_at
)
SELECT
    r.id,
    r.data->>'matchId',
    r.data->>'winningPick',
    CASE WHEN r.data->>'status' = 'voided' THEN 'voided' ELSE 'settled' END,
    GREATEST(daema_jsonb_bigint(r.data->'totalPool', 0), 0),
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'allocatedPointTotal', r.data->'allocatedAmount'), 0), 0),
    CASE WHEN r.data->>'source' = 'admin' THEN 'admin' ELSE 'worker' END,
    NULLIF(r.data->>'note', ''),
    daema_jsonb_timestamptz(r.data->'settledAt', r.created_at),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'prediction_settlements'
    AND r.data->>'matchId' IS NOT NULL
    AND r.data->>'winningPick' IN ('home', 'draw', 'away')
    AND EXISTS (SELECT 1 FROM worldcup_matches wm WHERE wm.id = r.data->>'matchId')
ON CONFLICT (match_id) DO UPDATE SET
    winning_pick = EXCLUDED.winning_pick,
    status = EXCLUDED.status,
    total_pool = EXCLUDED.total_pool,
    allocated_amount = EXCLUDED.allocated_amount,
    source = EXCLUDED.source,
    note = EXCLUDED.note,
    settled_at = EXCLUDED.settled_at;

INSERT INTO github_app_installations (
    id,
    installation_id,
    account_login,
    status,
    connected_at,
    updated_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'installationId', ''), r.id),
    COALESCE(NULLIF(r.data->>'accountLogin', ''), NULLIF(r.data->>'githubLogin', ''), NULLIF(r.data->>'senderLogin', '')),
    CASE
        WHEN r.data->>'action' IN ('deleted', 'suspend') THEN 'deleted'
        ELSE 'active'
    END,
    daema_jsonb_timestamptz(COALESCE(r.data->'connectedAt', r.data->'receivedAt'), r.created_at),
    daema_jsonb_timestamptz(r.data->'updatedAt', r.updated_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'github_installations'
ON CONFLICT (id) DO UPDATE SET
    installation_id = EXCLUDED.installation_id,
    account_login = EXCLUDED.account_login,
    status = EXCLUDED.status,
    updated_at = EXCLUDED.updated_at;

INSERT INTO github_commits (
    id,
    repository,
    sha,
    author_login,
    message,
    html_url,
    occurred_at,
    rewarded_points,
    created_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'repository', ''), 'unknown'),
    COALESCE(NULLIF(r.data->>'sha', ''), NULLIF(r.data->>'commitSha', ''), r.id),
    COALESCE(NULLIF(r.data->>'authorLogin', ''), NULLIF(r.data->>'githubLogin', ''), 'unknown'),
    NULLIF(r.data->>'message', ''),
    NULLIF(r.data->>'htmlUrl', ''),
    daema_jsonb_timestamptz(COALESCE(r.data->'occurredAt', r.data->'timestamp'), r.created_at),
    GREATEST(daema_jsonb_bigint(COALESCE(r.data->'rewardedPoints', r.data->'rewardPoints'), 0), 0),
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'github_commits'
ON CONFLICT (repository, sha) DO UPDATE SET
    author_login = EXCLUDED.author_login,
    message = EXCLUDED.message,
    html_url = EXCLUDED.html_url,
    occurred_at = EXCLUDED.occurred_at,
    rewarded_points = EXCLUDED.rewarded_points;

INSERT INTO file_uploads (
    id,
    file_name,
    content_type,
    byte_size,
    storage_key,
    public_url,
    metadata,
    created_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'fileName', ''), NULLIF(r.data->>'name', ''), r.id),
    NULLIF(r.data->>'contentType', ''),
    NULLIF(daema_jsonb_bigint(r.data->'byteSize', -1), -1),
    COALESCE(NULLIF(r.data->>'storageKey', ''), NULLIF(r.data->>'url', ''), r.id),
    NULLIF(r.data->>'url', ''),
    r.data,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'uploads'
ON CONFLICT (id) DO NOTHING;

INSERT INTO audit_logs (
    id,
    action,
    resource_type,
    resource_id,
    metadata,
    created_at
)
SELECT
    r.id,
    COALESCE(NULLIF(r.data->>'action', ''), 'migrated-resource'),
    COALESCE(NULLIF(r.data->>'resourceType', ''), 'legacy_resource'),
    NULLIF(r.data->>'resourceId', ''),
    r.data,
    daema_jsonb_timestamptz(r.data->'createdAt', r.created_at)
FROM migration_legacy_resource_payloads r
WHERE r.domain = 'audit_logs'
ON CONFLICT (id) DO NOTHING;

DROP FUNCTION IF EXISTS daema_jsonb_bigint(JSONB, BIGINT);
DROP FUNCTION IF EXISTS daema_jsonb_timestamptz(JSONB, TIMESTAMPTZ);
