CREATE TABLE IF NOT EXISTS internal_accounts (
    id TEXT PRIMARY KEY,
    login_id TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'booth')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'locked', 'disabled')),
    display_name TEXT,
    force_password_change BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_internal_accounts_role_status
    ON internal_accounts(role, status);

CREATE TABLE IF NOT EXISTS customer_profiles (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    school_name TEXT,
    student_no TEXT,
    grade TEXT,
    class_no TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS github_identities (
    id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE CASCADE,
    github_id BIGINT NOT NULL UNIQUE,
    login TEXT NOT NULL UNIQUE,
    email TEXT,
    avatar_url TEXT,
    html_url TEXT,
    access_token_ciphertext TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_github_identities_customer_id
    ON github_identities(customer_id);

CREATE TABLE IF NOT EXISTS auth_sessions (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    principal_id TEXT NOT NULL,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('customer', 'internal_account')),
    customer_id TEXT REFERENCES customer_profiles(id) ON DELETE CASCADE,
    internal_account_id TEXT REFERENCES internal_accounts(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('customer', 'admin', 'booth')),
    session_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (principal_type = 'customer' AND internal_account_id IS NULL)
        OR
        (principal_type = 'internal_account' AND internal_account_id IS NOT NULL AND customer_id IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_principal
    ON auth_sessions(principal_type, principal_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_customer_id
    ON auth_sessions(customer_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_internal_account_id
    ON auth_sessions(internal_account_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expires_at
    ON auth_sessions(expires_at);

CREATE TABLE IF NOT EXISTS auth_oauth_states (
    state TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'customer' CHECK (role = 'customer'),
    redirect_after TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS festivals (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'ended', 'archived')),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS booth_categories (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS booths (
    id TEXT PRIMARY KEY,
    festival_id TEXT REFERENCES festivals(id) ON DELETE SET NULL,
    category_id TEXT REFERENCES booth_categories(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT,
    location_label TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'paused', 'closed', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_booths_festival_id
    ON booths(festival_id);
CREATE INDEX IF NOT EXISTS idx_booths_category_id
    ON booths(category_id);
CREATE INDEX IF NOT EXISTS idx_booths_status
    ON booths(status);

CREATE TABLE IF NOT EXISTS booth_members (
    id TEXT PRIMARY KEY,
    booth_id TEXT NOT NULL REFERENCES booths(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES internal_accounts(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'manager', 'staff')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (booth_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_booth_members_account_id
    ON booth_members(account_id);

CREATE TABLE IF NOT EXISTS products (
    id TEXT PRIMARY KEY,
    booth_id TEXT NOT NULL REFERENCES booths(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    currency TEXT NOT NULL DEFAULT 'DMC' CHECK (currency IN ('DMC', 'POINT')),
    price BIGINT NOT NULL CHECK (price >= 0),
    stock_quantity INTEGER,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'sold_out', 'hidden', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_products_booth_id
    ON products(booth_id);
CREATE INDEX IF NOT EXISTS idx_products_status
    ON products(status);

CREATE TABLE IF NOT EXISTS inventory_adjustments (
    id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    delta INTEGER NOT NULL,
    reason TEXT NOT NULL,
    actor_account_id TEXT REFERENCES internal_accounts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wallet_accounts (
    id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (currency IN ('DMC', 'POINT')),
    balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (customer_id, currency)
);

CREATE INDEX IF NOT EXISTS idx_wallet_accounts_customer_id
    ON wallet_accounts(customer_id);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id TEXT PRIMARY KEY,
    wallet_account_id TEXT NOT NULL REFERENCES wallet_accounts(id) ON DELETE RESTRICT,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE RESTRICT,
    direction TEXT NOT NULL CHECK (direction IN ('income', 'expense')),
    currency TEXT NOT NULL CHECK (currency IN ('DMC', 'POINT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    transaction_type TEXT NOT NULL,
    reference_type TEXT,
    reference_id TEXT,
    idempotency_key TEXT UNIQUE,
    description TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ledger_transactions_customer_time
    ON ledger_transactions(customer_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_transactions_reference
    ON ledger_transactions(reference_type, reference_id);
CREATE INDEX IF NOT EXISTS idx_ledger_transactions_type
    ON ledger_transactions(transaction_type);

CREATE TABLE IF NOT EXISTS pay_barcodes (
    id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'used', 'expired', 'revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pay_barcodes_customer_id
    ON pay_barcodes(customer_id);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE RESTRICT,
    booth_id TEXT NOT NULL REFERENCES booths(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'preparing', 'ready', 'completed', 'cancelled', 'refunded')),
    currency TEXT NOT NULL DEFAULT 'DMC' CHECK (currency IN ('DMC', 'POINT')),
    subtotal_amount BIGINT NOT NULL DEFAULT 0 CHECK (subtotal_amount >= 0),
    discount_amount BIGINT NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),
    total_amount BIGINT NOT NULL DEFAULT 0 CHECK (total_amount >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_orders_customer_time
    ON orders(customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_booth_time
    ON orders(booth_id, created_at DESC);

CREATE TABLE IF NOT EXISTS order_items (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id TEXT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    product_name TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_amount BIGINT NOT NULL CHECK (unit_amount >= 0),
    total_amount BIGINT NOT NULL CHECK (total_amount >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_items_order_id
    ON order_items(order_id);

CREATE TABLE IF NOT EXISTS payment_intents (
    id TEXT PRIMARY KEY,
    order_id TEXT REFERENCES orders(id) ON DELETE SET NULL,
    booth_id TEXT NOT NULL REFERENCES booths(id) ON DELETE RESTRICT,
    customer_id TEXT REFERENCES customer_profiles(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'requires_capture' CHECK (status IN ('requires_capture', 'captured', 'cancelled', 'expired')),
    currency TEXT NOT NULL DEFAULT 'DMC' CHECK (currency IN ('DMC', 'POINT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    idempotency_key TEXT UNIQUE,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_payment_intents_order_id
    ON payment_intents(order_id);
CREATE INDEX IF NOT EXISTS idx_payment_intents_booth_id
    ON payment_intents(booth_id);

CREATE TABLE IF NOT EXISTS payments (
    id TEXT PRIMARY KEY,
    payment_intent_id TEXT NOT NULL REFERENCES payment_intents(id) ON DELETE RESTRICT,
    order_id TEXT REFERENCES orders(id) ON DELETE SET NULL,
    booth_id TEXT NOT NULL REFERENCES booths(id) ON DELETE RESTRICT,
    customer_id TEXT REFERENCES customer_profiles(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'captured' CHECK (status IN ('captured', 'partially_refunded', 'refunded', 'voided')),
    currency TEXT NOT NULL DEFAULT 'DMC' CHECK (currency IN ('DMC', 'POINT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_payments_booth_time
    ON payments(booth_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_customer_time
    ON payments(customer_id, captured_at DESC);

CREATE TABLE IF NOT EXISTS refunds (
    id TEXT PRIMARY KEY,
    payment_id TEXT NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'succeeded' CHECK (status IN ('pending', 'succeeded', 'failed')),
    currency TEXT NOT NULL DEFAULT 'DMC' CHECK (currency IN ('DMC', 'POINT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    reason TEXT,
    idempotency_key TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS benefits (
    id TEXT PRIMARY KEY,
    benefit_type TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'archived')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS benefit_claims (
    id TEXT PRIMARY KEY,
    benefit_id TEXT NOT NULL REFERENCES benefits(id) ON DELETE RESTRICT,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'claimed' CHECK (status IN ('claimed', 'cancelled')),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (benefit_id, customer_id)
);

CREATE TABLE IF NOT EXISTS worldcup_teams (
    id TEXT PRIMARY KEY,
    country_code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    flag_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS worldcup_matches (
    id TEXT PRIMARY KEY,
    external_fixture_id TEXT UNIQUE,
    home_team_id TEXT REFERENCES worldcup_teams(id) ON DELETE SET NULL,
    away_team_id TEXT REFERENCES worldcup_teams(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'live', 'finished', 'cancelled')),
    starts_at TIMESTAMPTZ NOT NULL,
    home_score INTEGER,
    away_score INTEGER,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_worldcup_matches_starts_at
    ON worldcup_matches(starts_at);

CREATE TABLE IF NOT EXISTS worldcup_predictions (
    id TEXT PRIMARY KEY,
    match_id TEXT NOT NULL REFERENCES worldcup_matches(id) ON DELETE CASCADE,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE CASCADE,
    pick TEXT NOT NULL CHECK (pick IN ('home', 'draw', 'away')),
    currency TEXT NOT NULL DEFAULT 'POINT' CHECK (currency = 'POINT'),
    stake_amount BIGINT NOT NULL CHECK (stake_amount > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'settled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_at TIMESTAMPTZ,
    UNIQUE (match_id, customer_id)
);

CREATE INDEX IF NOT EXISTS idx_worldcup_predictions_customer_id
    ON worldcup_predictions(customer_id);

CREATE TABLE IF NOT EXISTS prediction_settlements (
    id TEXT PRIMARY KEY,
    match_id TEXT NOT NULL UNIQUE REFERENCES worldcup_matches(id) ON DELETE RESTRICT,
    winning_pick TEXT NOT NULL CHECK (winning_pick IN ('home', 'draw', 'away')),
    status TEXT NOT NULL DEFAULT 'settled' CHECK (status IN ('settled', 'voided')),
    total_pool BIGINT NOT NULL DEFAULT 0 CHECK (total_pool >= 0),
    allocated_amount BIGINT NOT NULL DEFAULT 0 CHECK (allocated_amount >= 0),
    source TEXT NOT NULL CHECK (source IN ('worker', 'admin')),
    note TEXT,
    settled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS prediction_settlement_entries (
    id TEXT PRIMARY KEY,
    settlement_id TEXT NOT NULL REFERENCES prediction_settlements(id) ON DELETE CASCADE,
    prediction_id TEXT NOT NULL REFERENCES worldcup_predictions(id) ON DELETE RESTRICT,
    customer_id TEXT NOT NULL REFERENCES customer_profiles(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('won', 'lost', 'voided')),
    payout_amount BIGINT NOT NULL DEFAULT 0 CHECK (payout_amount >= 0),
    ledger_transaction_id TEXT REFERENCES ledger_transactions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (settlement_id, prediction_id)
);

CREATE INDEX IF NOT EXISTS idx_prediction_settlement_entries_customer_id
    ON prediction_settlement_entries(customer_id);

CREATE TABLE IF NOT EXISTS github_app_installations (
    id TEXT PRIMARY KEY,
    github_identity_id TEXT REFERENCES github_identities(id) ON DELETE SET NULL,
    installation_id TEXT NOT NULL UNIQUE,
    account_login TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS github_commits (
    id TEXT PRIMARY KEY,
    github_identity_id TEXT REFERENCES github_identities(id) ON DELETE SET NULL,
    repository TEXT NOT NULL,
    sha TEXT NOT NULL,
    author_login TEXT NOT NULL,
    message TEXT,
    html_url TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    rewarded_points BIGINT NOT NULL DEFAULT 0 CHECK (rewarded_points >= 0),
    ledger_transaction_id TEXT REFERENCES ledger_transactions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repository, sha)
);

CREATE INDEX IF NOT EXISTS idx_github_commits_author_time
    ON github_commits(author_login, occurred_at DESC);

CREATE TABLE IF NOT EXISTS file_uploads (
    id TEXT PRIMARY KEY,
    owner_account_id TEXT REFERENCES internal_accounts(id) ON DELETE SET NULL,
    owner_customer_id TEXT REFERENCES customer_profiles(id) ON DELETE SET NULL,
    file_name TEXT NOT NULL,
    content_type TEXT,
    byte_size BIGINT CHECK (byte_size IS NULL OR byte_size >= 0),
    storage_key TEXT NOT NULL,
    public_url TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    actor_account_id TEXT REFERENCES internal_accounts(id) ON DELETE SET NULL,
    actor_customer_id TEXT REFERENCES customer_profiles(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    ip_address TEXT,
    user_agent TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_account_time
    ON audit_logs(actor_account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource
    ON audit_logs(resource_type, resource_id);
