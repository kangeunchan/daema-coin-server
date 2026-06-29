package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type postgresStore struct {
	db *sql.DB
}

var (
	errInsufficientWalletBalance = errors.New("insufficient wallet balance")
	errLedgerIdempotencyConflict = errors.New("ledger idempotency conflict")
	errPaymentIntentNotFound     = errors.New("payment intent not found")
	errPaymentIntentClosed       = errors.New("payment intent is closed")
	errPaymentNotFound           = errors.New("payment not found")
	errPaymentClosed             = errors.New("payment is closed")
	errPaymentRefundExceeded     = errors.New("payment refund exceeds captured amount")
)

const serializableTxMaxAttempts = 3

type sqlStateError interface {
	SQLState() string
}

func openPostgresStore(ctx context.Context, dsn string) (*postgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(envInt("DATABASE_MAX_OPEN_CONNS", 10))
	db.SetMaxIdleConns(envInt("DATABASE_MAX_IDLE_CONNS", 5))
	db.SetConnMaxLifetime(envDuration("DATABASE_CONN_MAX_LIFETIME", 30*time.Minute))
	db.SetConnMaxIdleTime(envDuration("DATABASE_CONN_MAX_IDLE_TIME", 5*time.Minute))
	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w; close database: %v", err, closeErr)
		}
		return nil, err
	}
	store := &postgresStore{db: db}
	if err := store.migrate(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w; close database: %v", err, closeErr)
		}
		return nil, err
	}
	return store, nil
}

func (s *postgresStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *postgresStore) migrate(ctx context.Context) error {
	return runMigrations(ctx, s.db)
}

func (s *postgresStore) withSerializableTx(ctx context.Context, fn func(*sql.Tx) error) error {
	var err error
	for attempt := 1; attempt <= serializableTxMaxAttempts; attempt++ {
		tx, beginErr := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if beginErr != nil {
			return beginErr
		}

		err = fn(tx)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
			if retryableTxError(err) && attempt < serializableTxMaxAttempts {
				continue
			}
			return err
		}
		return nil
	}
	return err
}

func retryableTxError(err error) bool {
	var stateErr sqlStateError
	if !errors.As(err, &stateErr) {
		return false
	}
	switch stateErr.SQLState() {
	case "40001", "40P01":
		return true
	default:
		return false
	}
}

func (s *postgresStore) list(ctx context.Context, resource string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	table, err := resourceTable(resource)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT payload
FROM %s
ORDER BY updated_at DESC, id ASC
LIMIT $1`, table), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *postgresStore) listFiltered(ctx context.Context, resource string, filters []resourceFilter, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	table, err := resourceTable(resource)
	if err != nil {
		return nil, err
	}
	criteria := map[string]string{}
	args := []any{}
	for _, filter := range filters {
		if strings.TrimSpace(filter.Field) == "" || strings.TrimSpace(filter.Value) == "" {
			continue
		}
		criteria[filter.Field] = filter.Value
	}
	query := fmt.Sprintf(`
SELECT payload
FROM %s`, table)
	if len(criteria) > 0 {
		raw, err := json.Marshal(criteria)
		if err != nil {
			return nil, err
		}
		args = append(args, string(raw))
		query += `
WHERE payload @> $1::jsonb`
	}
	args = append(args, limit)
	query += fmt.Sprintf(`
ORDER BY updated_at DESC, id ASC
LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		item := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *postgresStore) get(ctx context.Context, resource, id string) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	var raw string
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT payload FROM %s WHERE id = $1`, table), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, false, err
	}
	return item, true, nil
}

func (s *postgresStore) put(ctx context.Context, resource, id string, data map[string]any) (map[string]any, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = generatedID(resourceIDPrefix(resource))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := now
	if existing, ok, err := s.get(ctx, resource, id); err != nil {
		return nil, err
	} else if ok {
		if value, _ := existing["createdAt"].(string); value != "" {
			createdAt = value
		}
	}
	data["id"] = id
	data["createdAt"] = createdAt
	data["updatedAt"] = now
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s(id, payload, created_at, updated_at)
VALUES($1, $2, $3, $4)
ON CONFLICT(id) DO UPDATE SET
	payload = excluded.payload,
	updated_at = excluded.updated_at`, table), id, string(raw), createdAt, now)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *postgresStore) create(ctx context.Context, resource, id string, data map[string]any) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	if id == "" {
		id = generatedID(resourceIDPrefix(resource))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	data["id"] = id
	data["createdAt"] = now
	data["updatedAt"] = now
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false, err
	}
	result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s(id, payload, created_at, updated_at)
VALUES($1, $2, $3, $4)
ON CONFLICT DO NOTHING`, table), id, string(raw), now, now)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	return data, affected > 0, nil
}

func (s *postgresStore) patch(ctx context.Context, resource, id string, patch map[string]any) (map[string]any, bool, error) {
	existing, ok, err := s.get(ctx, resource, id)
	if err != nil || !ok {
		return nil, ok, err
	}
	for key, value := range patch {
		existing[key] = value
	}
	item, err := s.put(ctx, resource, id, existing)
	return item, true, err
}

func (s *postgresStore) delete(ctx context.Context, resource, id string) error {
	table, err := resourceTable(resource)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, table), id)
	return err
}

func (s *postgresStore) deleteReturning(ctx context.Context, resource, id string) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	var raw string
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`
DELETE FROM %s
WHERE id = $1
RETURNING payload`, table), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, false, err
	}
	return item, true, nil
}

func (s *postgresStore) getResourceTx(ctx context.Context, tx *sql.Tx, resource, id string, forUpdate bool) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	query := fmt.Sprintf(`SELECT payload FROM %s WHERE id = $1`, table)
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var raw string
	err = tx.QueryRowContext(ctx, query, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, false, err
	}
	return item, true, nil
}

func (s *postgresStore) createResourceTx(ctx context.Context, tx *sql.Tx, resource, id string, data map[string]any) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	if id == "" {
		id = generatedID(resourceIDPrefix(resource))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item := cloneMap(data)
	item["id"] = id
	item["createdAt"] = now
	item["updatedAt"] = now
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, false, err
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s(id, payload, created_at, updated_at)
VALUES($1, $2, $3, $3)
ON CONFLICT DO NOTHING`, table), id, string(raw), now)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	return item, affected > 0, nil
}

func (s *postgresStore) putResourceTx(ctx context.Context, tx *sql.Tx, resource, id string, data map[string]any) (map[string]any, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = generatedID(resourceIDPrefix(resource))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := now
	if existing, ok, err := s.getResourceTx(ctx, tx, resource, id, true); err != nil {
		return nil, err
	} else if ok {
		if value, _ := existing["createdAt"].(string); value != "" {
			createdAt = value
		}
	}
	item := cloneMap(data)
	item["id"] = id
	item["createdAt"] = createdAt
	item["updatedAt"] = now
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO %s(id, payload, created_at, updated_at)
VALUES($1, $2, $3, $4)
ON CONFLICT(id) DO UPDATE SET
	payload = excluded.payload,
	updated_at = excluded.updated_at`, table), id, string(raw), createdAt, now)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (s *postgresStore) deleteReturningResourceTx(ctx context.Context, tx *sql.Tx, resource, id string) (map[string]any, bool, error) {
	table, err := resourceTable(resource)
	if err != nil {
		return nil, false, err
	}
	var raw string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`
DELETE FROM %s
WHERE id = $1
RETURNING payload`, table), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, false, err
	}
	return item, true, nil
}

func cloneMap(data map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range data {
		out[key] = value
	}
	return out
}

func (s *postgresStore) health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

var resourceTables = map[string]string{
	resourceNavigation:            "navigation_items",
	resourceCustomerProfiles:      "customer_profile_snapshots",
	resourceNotifications:         "notification_messages",
	resourceSearchSuggestions:     "search_suggestions",
	resourceSearchDocuments:       "search_documents",
	resourceNotices:               "notice_posts",
	resourceWalletBalances:        "wallet_balance_snapshots",
	resourceBenefits:              "benefit_programs",
	resourceBenefitClaims:         "benefit_claim_requests",
	resourceShortcuts:             "home_shortcuts",
	resourcePromotions:            "promotion_campaigns",
	resourceLedgerTransactions:    "ledger_transaction_snapshots",
	resourceUserRankings:          "user_ranking_snapshots",
	resourceBoothRankings:         "booth_ranking_snapshots",
	resourceFestivalBanners:       "festival_banner_assets",
	resourcePayBarcodes:           "pay_barcode_tokens",
	resourceBoothCategories:       "booth_category_settings",
	resourceBoothBanners:          "booth_banner_assets",
	resourceBooths:                "booth_profiles",
	resourceProducts:              "product_catalog_items",
	resourceProductViews:          "product_view_events",
	resourceBoothCheckins:         "booth_checkin_events",
	resourceCartItems:             "cart_items",
	resourceOrders:                "customer_order_snapshots",
	resourceFavorites:             "favorite_targets",
	resourceInquiries:             "customer_inquiries",
	resourceShares:                "share_events",
	resourceWorldcupPredictions:   "worldcup_prediction_entries",
	resourcePredictionSettlements: "worldcup_prediction_settlement_runs",
	resourceFeatures:              "feature_flags",
	resourceStaff:                 "booth_staff_profiles",
	resourceInventory:             "inventory_adjustment_requests",
	resourcePurchaseLimits:        "product_purchase_limits",
	resourcePickupVouchers:        "pickup_vouchers",
	resourcePaymentIntents:        "payment_intent_snapshots",
	resourcePayments:              "payment_snapshots",
	resourceVisits:                "booth_visit_events",
	resourceSettlements:           "booth_settlement_runs",
	resourceExports:               "export_jobs",
	resourceFestivals:             "festival_profiles",
	resourceMaps:                  "venue_maps",
	resourceUsers:                 "user_directory_entries",
	resourceUserImports:           "user_import_jobs",
	resourceRoles:                 "role_definitions",
	resourceRoleAssignments:       "role_assignment_entries",
	resourceWalletAdjustments:     "wallet_adjustment_requests",
	resourceLedgerExports:         "ledger_export_jobs",
	resourceRewardRules:           "reward_rule_definitions",
	resourceUploads:               "file_upload_requests",
	resourceWorldcupTeams:         "worldcup_team_profiles",
	resourceWorldcupMatches:       "worldcup_match_schedules",
	resourceWorldcupLineups:       "worldcup_match_lineups",
	resourceWorldcupStats:         "worldcup_match_stats",
	resourceAuditLogs:             "audit_log_entries",
	resourceSystemJobs:            "system_job_states",
	resourceIncidents:             "incident_reports",
	resourceRankingRules:          "ranking_rule_definitions",
	resourceGitHubCommits:         "github_commit_events",
	resourceGitHubInstallations:   "github_app_installation_events",
	resourceInternalAccounts:      "internal_account_snapshots",
	resourceAnalyticsImpressions:  "analytics_impression_events",
	resourceInquiryReplies:        "inquiry_reply_messages",
}

func resourceTable(resource string) (string, error) {
	table, ok := resourceTables[resource]
	if !ok {
		return "", fmt.Errorf("application resource %q is not registered", resource)
	}
	return table, nil
}
