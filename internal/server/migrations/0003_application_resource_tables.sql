CREATE TEMP TABLE IF NOT EXISTS migration_legacy_resource_payloads (
    domain TEXT NOT NULL,
    id TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
) ON COMMIT PRESERVE ROWS;

TRUNCATE migration_legacy_resource_payloads;

CREATE TEMP TABLE IF NOT EXISTS migration_resource_table_map (
    source_domain TEXT PRIMARY KEY,
    table_name TEXT NOT NULL
) ON COMMIT PRESERVE ROWS;

TRUNCATE migration_resource_table_map;

INSERT INTO migration_resource_table_map(source_domain, table_name)
VALUES
    ('navigation', 'navigation_items'),
    ('customer_profiles', 'customer_profile_snapshots'),
    ('notifications', 'notification_messages'),
    ('search_suggestions', 'search_suggestions'),
    ('search_documents', 'search_documents'),
    ('notices', 'notice_posts'),
    ('wallet_balances', 'wallet_balance_snapshots'),
    ('benefits', 'benefit_programs'),
    ('benefit_claims', 'benefit_claim_requests'),
    ('shortcuts', 'home_shortcuts'),
    ('promotions', 'promotion_campaigns'),
    ('ledger_transactions', 'ledger_transaction_snapshots'),
    ('rankings_user', 'user_ranking_snapshots'),
    ('rankings_booth', 'booth_ranking_snapshots'),
    ('festival_banners', 'festival_banner_assets'),
    ('pay_barcodes', 'pay_barcode_tokens'),
    ('booth_categories', 'booth_category_settings'),
    ('booth_banners', 'booth_banner_assets'),
    ('booths', 'booth_profiles'),
    ('products', 'product_catalog_items'),
    ('product_views', 'product_view_events'),
    ('booth_checkins', 'booth_checkin_events'),
    ('cart_items', 'cart_items'),
    ('orders', 'customer_order_snapshots'),
    ('favorites', 'favorite_targets'),
    ('inquiries', 'customer_inquiries'),
    ('shares', 'share_events'),
    ('worldcup_predictions', 'worldcup_prediction_entries'),
    ('prediction_settlements', 'worldcup_prediction_settlement_runs'),
    ('features', 'feature_flags'),
    ('staff', 'booth_staff_profiles'),
    ('inventory', 'inventory_adjustment_requests'),
    ('purchase_limits', 'product_purchase_limits'),
    ('pickup_vouchers', 'pickup_vouchers'),
    ('payment_intents', 'payment_intent_snapshots'),
    ('payments', 'payment_snapshots'),
    ('visits', 'booth_visit_events'),
    ('settlements', 'booth_settlement_runs'),
    ('exports', 'export_jobs'),
    ('festivals', 'festival_profiles'),
    ('maps', 'venue_maps'),
    ('users', 'user_directory_entries'),
    ('user_imports', 'user_import_jobs'),
    ('roles', 'role_definitions'),
    ('role_assignments', 'role_assignment_entries'),
    ('wallet_adjustments', 'wallet_adjustment_requests'),
    ('ledger_exports', 'ledger_export_jobs'),
    ('reward_rules', 'reward_rule_definitions'),
    ('uploads', 'file_upload_requests'),
    ('worldcup_teams', 'worldcup_team_profiles'),
    ('worldcup_matches', 'worldcup_match_schedules'),
    ('worldcup_lineups', 'worldcup_match_lineups'),
    ('worldcup_stats', 'worldcup_match_stats'),
    ('audit_logs', 'audit_log_entries'),
    ('system_jobs', 'system_job_states'),
    ('incidents', 'incident_reports'),
    ('ranking_rules', 'ranking_rule_definitions'),
    ('github_commits', 'github_commit_events'),
    ('github_installations', 'github_app_installation_events'),
    ('auth.internal_accounts', 'internal_account_snapshots'),
    ('analytics_impressions', 'analytics_impression_events'),
    ('inquiry_replies', 'inquiry_reply_messages')
ON CONFLICT (source_domain) DO UPDATE SET
    table_name = EXCLUDED.table_name;

DO $$
BEGIN
    IF to_regclass('public.records') IS NOT NULL THEN
        EXECUTE 'INSERT INTO migration_legacy_resource_payloads(domain, id, data, created_at, updated_at)
                 SELECT domain, id, data, created_at, updated_at
                 FROM public.records';
    END IF;
END $$;

DO $$
DECLARE
    item RECORD;
BEGIN
    FOR item IN
        SELECT source_domain, table_name
        FROM migration_resource_table_map
        ORDER BY source_domain
    LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I (
                id TEXT PRIMARY KEY,
                payload JSONB NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
            )',
            item.table_name
        );

        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS %I ON %I(updated_at DESC, id ASC)',
            'idx_' || item.table_name || '_updated_at',
            item.table_name
        );

        EXECUTE format(
	            'INSERT INTO %I(id, payload, created_at, updated_at)
	             SELECT id, data, created_at, updated_at
	             FROM migration_legacy_resource_payloads
	             WHERE domain = %L
             ON CONFLICT (id) DO UPDATE SET
                payload = EXCLUDED.payload,
                updated_at = EXCLUDED.updated_at',
            item.table_name,
            item.source_domain
        );
    END LOOP;
END $$;

DO $$
DECLARE
    item RECORD;
    missing_count BIGINT;
    unknown_count BIGINT;
BEGIN
    SELECT count(*)
    INTO unknown_count
    FROM migration_legacy_resource_payloads source
    LEFT JOIN migration_resource_table_map target
        ON target.source_domain = source.domain
    WHERE target.source_domain IS NULL
        AND source.domain NOT IN ('auth.oauth_states', 'auth.sessions');

    IF unknown_count > 0 THEN
        RAISE EXCEPTION 'legacy resource migration has % unmapped rows; refusing to drop public.records', unknown_count;
    END IF;

    FOR item IN
        SELECT source_domain, table_name
        FROM migration_resource_table_map
        ORDER BY source_domain
    LOOP
        EXECUTE format(
            'SELECT count(*)
             FROM migration_legacy_resource_payloads source
             WHERE source.domain = %L
                AND NOT EXISTS (
                    SELECT 1
                    FROM %I target
                    WHERE target.id = source.id
                )',
            item.source_domain,
            item.table_name
        )
        INTO missing_count;

        IF missing_count > 0 THEN
            RAISE EXCEPTION 'legacy resource migration for domain % has % missing rows in table %; refusing to drop public.records',
                item.source_domain,
                missing_count,
                item.table_name;
        END IF;
    END LOOP;
END $$;

DROP TABLE IF EXISTS records;
DROP TABLE IF EXISTS migration_resource_table_map;
DROP TABLE IF EXISTS migration_legacy_resource_payloads;
