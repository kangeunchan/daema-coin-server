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

var errInsufficientWalletBalance = errors.New("insufficient wallet balance")

func openPostgresStore(ctx context.Context, dsn string) (*postgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
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

func (s *postgresStore) customerProfileExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

func (s *postgresStore) saveCustomerProfile(ctx context.Context, user authUser, profile map[string]any) (map[string]any, error) {
	now := time.Now().UTC()
	displayName := firstNonEmpty(
		stringValue(profile["name"]),
		stringValue(profile["displayName"]),
		user.Name,
		user.Login,
		user.ID,
	)
	schoolName := stringValue(profile["schoolName"])
	studentNo := stringValue(profile["studentNo"])
	grade := stringValue(profile["grade"])
	classNo := firstNonEmpty(stringValue(profile["classNo"]), stringValue(profile["class"]))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var createdAt time.Time
	var updatedAt time.Time
	if err := tx.QueryRowContext(ctx, `
INSERT INTO customer_profiles (
	id, display_name, school_name, student_no, grade, class_no, status, created_at, updated_at
) VALUES (
	$1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), 'active', $7, $7
)
ON CONFLICT (id) DO UPDATE SET
	display_name = EXCLUDED.display_name,
	school_name = EXCLUDED.school_name,
	student_no = EXCLUDED.student_no,
	grade = EXCLUDED.grade,
	class_no = EXCLUDED.class_no,
	status = 'active',
	updated_at = EXCLUDED.updated_at
RETURNING created_at, updated_at`,
		user.ID,
		displayName,
		schoolName,
		studentNo,
		grade,
		classNo,
		now,
	).Scan(&createdAt, &updatedAt); err != nil {
		return nil, err
	}

	if user.GitHubID > 0 {
		_, err = tx.ExecContext(ctx, `
INSERT INTO github_identities (
	id, customer_id, github_id, login, email, avatar_url, html_url, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8, $8
)
ON CONFLICT (github_id) DO UPDATE SET
	customer_id = EXCLUDED.customer_id,
	login = EXCLUDED.login,
	email = EXCLUDED.email,
	avatar_url = EXCLUDED.avatar_url,
	html_url = EXCLUDED.html_url,
	updated_at = EXCLUDED.updated_at`,
			"github-identity-"+fmt.Sprint(user.GitHubID),
			user.ID,
			user.GitHubID,
			firstNonEmpty(user.Login, user.ID),
			user.Email,
			user.AvatarURL,
			user.HTMLURL,
			now,
		)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return map[string]any{
		"id":          user.ID,
		"userId":      user.ID,
		"name":        displayName,
		"displayName": displayName,
		"schoolName":  schoolName,
		"studentNo":   studentNo,
		"grade":       grade,
		"classNo":     classNo,
		"githubLogin": user.Login,
		"githubId":    user.GitHubID,
		"createdAt":   createdAt.UTC().Format(time.RFC3339),
		"updatedAt":   updatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (s *postgresStore) customerProfiles(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
	SELECT cp.id, cp.display_name, cp.school_name, cp.student_no, cp.grade, cp.class_no,
		cp.status, cp.created_at, cp.updated_at,
		gi.github_id, gi.login, gi.email, gi.avatar_url, gi.html_url
	FROM customer_profiles cp
	LEFT JOIN github_identities gi ON gi.customer_id = cp.id
	ORDER BY cp.updated_at DESC, cp.id ASC
	LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, displayName, status string
		var schoolName, studentNo, grade, classNo sql.NullString
		var githubID sql.NullInt64
		var githubLogin, email, avatarURL, htmlURL sql.NullString
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &displayName, &schoolName, &studentNo, &grade, &classNo, &status, &createdAt, &updatedAt, &githubID, &githubLogin, &email, &avatarURL, &htmlURL); err != nil {
			return nil, err
		}
		item := map[string]any{
			"id":          id,
			"userId":      id,
			"name":        displayName,
			"displayName": displayName,
			"status":      status,
			"createdAt":   createdAt.UTC().Format(time.RFC3339),
			"updatedAt":   updatedAt.UTC().Format(time.RFC3339),
		}
		if schoolName.Valid {
			item["schoolName"] = schoolName.String
		}
		if studentNo.Valid {
			item["studentNo"] = studentNo.String
		}
		if grade.Valid {
			item["grade"] = grade.String
		}
		if classNo.Valid {
			item["classNo"] = classNo.String
		}
		if githubID.Valid {
			item["githubId"] = githubID.Int64
		}
		if githubLogin.Valid {
			item["githubLogin"] = githubLogin.String
		}
		if email.Valid {
			item["email"] = email.String
		}
		if avatarURL.Valid {
			item["avatarUrl"] = avatarURL.String
		}
		if htmlURL.Valid {
			item["htmlUrl"] = htmlURL.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *postgresStore) authUserByGitHubLogin(ctx context.Context, login string) (authUser, bool, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" {
		return authUser{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
	SELECT cp.id, cp.display_name, gi.github_id, gi.login, gi.email, gi.avatar_url, gi.html_url
	FROM github_identities gi
	JOIN customer_profiles cp ON cp.id = gi.customer_id
	WHERE lower(gi.login) = $1 AND cp.status = 'active'`, login)

	var user authUser
	var email, avatarURL, htmlURL sql.NullString
	if err := row.Scan(&user.ID, &user.Name, &user.GitHubID, &user.Login, &email, &avatarURL, &htmlURL); errors.Is(err, sql.ErrNoRows) {
		return authUser{}, false, nil
	} else if err != nil {
		return authUser{}, false, err
	}
	user.Provider = "github"
	user.Roles = []string{roleCustomer}
	if email.Valid {
		user.Email = email.String
	}
	if avatarURL.Valid {
		user.AvatarURL = avatarURL.String
	}
	if htmlURL.Valid {
		user.HTMLURL = htmlURL.String
	}
	return user, true, nil
}

func (s *postgresStore) walletBalance(ctx context.Context, userID, currency string) (int, error) {
	currency = strings.ToUpper(currency)
	var balance int
	err := s.db.QueryRowContext(ctx, `
SELECT balance
FROM wallet_accounts
WHERE customer_id = $1 AND currency = $2`, userID, currency).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return balance, err
}

func (s *postgresStore) walletBalances(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, customer_id, currency, balance, created_at, updated_at
FROM wallet_accounts
WHERE ($1 = '' OR customer_id = $1)
ORDER BY currency ASC
LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, customerID, currency string
		var balance int
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &customerID, &currency, &balance, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id":        id,
			"userId":    customerID,
			"currency":  currency,
			"label":     walletCurrencyLabel(currency),
			"name":      walletCurrencyLabel(currency),
			"balance":   balance,
			"amount":    amount(currency, balance),
			"createdAt": createdAt.UTC().Format(time.RFC3339),
			"updatedAt": updatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, rows.Err()
}

func (s *postgresStore) ledgerTransactions(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, customer_id, direction, currency, amount, transaction_type, reference_type,
	reference_id, description, metadata, occurred_at, created_at
FROM ledger_transactions
WHERE ($1 = '' OR customer_id = $1)
ORDER BY occurred_at DESC, created_at DESC, id DESC
LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, customerID, direction, currency, txType string
		var referenceType, referenceID, description sql.NullString
		var value int
		var metadataRaw []byte
		var occurredAt, createdAt time.Time
		if err := rows.Scan(&id, &customerID, &direction, &currency, &value, &txType, &referenceType, &referenceID, &description, &metadataRaw, &occurredAt, &createdAt); err != nil {
			return nil, err
		}
		item := map[string]any{}
		if len(metadataRaw) > 0 {
			_ = json.Unmarshal(metadataRaw, &item)
		}
		item["id"] = id
		item["userId"] = customerID
		item["type"] = txType
		item["direction"] = direction
		item["currency"] = currency
		item["amount"] = amount(currency, value)
		item["occurredAt"] = occurredAt.UTC().Format(time.RFC3339)
		item["createdAt"] = createdAt.UTC().Format(time.RFC3339)
		if referenceType.Valid {
			item["referenceType"] = referenceType.String
		}
		if referenceID.Valid {
			item["referenceId"] = referenceID.String
		}
		if description.Valid {
			item["description"] = description.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *postgresStore) createLedgerAndAdjustWallet(ctx context.Context, user authUser, id, txType, direction, currency string, value int, extras map[string]any) (bool, error) {
	if value <= 0 {
		return false, nil
	}
	currency = strings.ToUpper(currency)
	if direction != "income" {
		direction = "expense"
	}
	delta := value
	if direction == "expense" {
		delta = -value
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var profileExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE id = $1)`, user.ID).Scan(&profileExists); err != nil {
		return false, err
	}
	if !profileExists {
		return false, fmt.Errorf("customer profile %q does not exist", user.ID)
	}

	walletID := walletBalanceID(user.ID, currency)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO wallet_accounts(id, customer_id, currency, balance, version, created_at, updated_at)
VALUES($1, $2, $3, 0, 0, now(), now())
ON CONFLICT (customer_id, currency) DO NOTHING`, walletID, user.ID, currency); err != nil {
		return false, err
	}
	if err := tx.QueryRowContext(ctx, `
SELECT id
FROM wallet_accounts
WHERE customer_id = $1 AND currency = $2
FOR UPDATE`, user.ID, currency).Scan(&walletID); err != nil {
		return false, err
	}

	metadata := map[string]any{}
	for key, value := range extras {
		metadata[key] = value
	}
	if user.Login != "" {
		metadata["githubLogin"] = user.Login
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}

	referenceType := firstNonEmpty(stringValue(extras["referenceType"]), stringValue(extras["type"]))
	referenceID := firstNonEmpty(stringValue(extras["referenceId"]), stringValue(extras["matchId"]), stringValue(extras["orderId"]), stringValue(extras["paymentId"]))
	description := stringValue(extras["description"])

	var insertedID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO ledger_transactions (
	id, wallet_account_id, customer_id, direction, currency, amount, transaction_type,
	reference_type, reference_id, idempotency_key, description, metadata, occurred_at, created_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7,
	NULLIF($8, ''), NULLIF($9, ''), $1, NULLIF($10, ''), $11, now(), now()
)
ON CONFLICT (id) DO NOTHING
RETURNING id`,
		id,
		walletID,
		user.ID,
		direction,
		currency,
		value,
		txType,
		referenceType,
		referenceID,
		description,
		string(metadataRaw),
	).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}

	var nextBalance int
	err = tx.QueryRowContext(ctx, `
UPDATE wallet_accounts
SET balance = balance + $2,
	version = version + 1,
	updated_at = now()
WHERE id = $1
	AND balance + $2 >= 0
RETURNING balance`, walletID, delta).Scan(&nextBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errInsufficientWalletBalance
	}
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *postgresStore) saveSession(ctx context.Context, session authSession) error {
	stored := storedAuthSession{
		TokenHash:         sessionTokenHashID(session.Token),
		GitHubAccessToken: session.GitHubAccessToken,
		User:              session.User,
		Role:              session.Role,
		ExpiresAt:         session.ExpiresAt,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}

	principalType := "customer"
	var internalAccountID any
	if session.User.Provider == "internal" || session.Role == roleAdmin || session.Role == roleBooth {
		principalType = "internal_account"
		internalAccountID = session.User.ID
	}
	var customerID any
	if principalType == "customer" {
		customerID = nil
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO auth_sessions (
	id, token_hash, principal_id, principal_type, customer_id, internal_account_id,
	role, session_data, expires_at, created_at
) VALUES (
	$1, $1, $2, $3, $4, $5, $6, $7, $8, now()
)
ON CONFLICT (id) DO UPDATE SET
	principal_id = EXCLUDED.principal_id,
	principal_type = EXCLUDED.principal_type,
	customer_id = EXCLUDED.customer_id,
	internal_account_id = EXCLUDED.internal_account_id,
	role = EXCLUDED.role,
	session_data = EXCLUDED.session_data,
	expires_at = EXCLUDED.expires_at,
	revoked_at = NULL`,
		sessionTokenHashID(session.Token),
		session.User.ID,
		principalType,
		customerID,
		internalAccountID,
		session.Role,
		string(data),
		session.ExpiresAt,
	)
	return err
}

func (s *postgresStore) session(ctx context.Context, token string) (authSession, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT session_data, expires_at
FROM auth_sessions
WHERE id = $1 AND revoked_at IS NULL`, sessionTokenHashID(token))

	var raw []byte
	var expiresAt time.Time
	if err := row.Scan(&raw, &expiresAt); errors.Is(err, sql.ErrNoRows) {
		return authSession{}, false, nil
	} else if err != nil {
		return authSession{}, false, err
	}

	stored := storedAuthSession{}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return authSession{}, false, err
	}
	if stored.ExpiresAt.IsZero() {
		stored.ExpiresAt = expiresAt
	}
	session := authSession{Token: token, GitHubAccessToken: stored.GitHubAccessToken, User: stored.User, Role: stored.Role, ExpiresAt: stored.ExpiresAt}
	if time.Now().After(session.ExpiresAt) {
		_ = s.deleteSession(ctx, token)
		return authSession{}, false, nil
	}
	return session, true, nil
}

func (s *postgresStore) deleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE id = $1`, sessionTokenHashID(token))
	return err
}

func (s *postgresStore) saveOAuthState(ctx context.Context, state oauthState) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO auth_oauth_states(state, provider, role, redirect_after, expires_at, created_at)
VALUES($1, 'github', $2, $3, $4, now())
ON CONFLICT (state) DO UPDATE SET
	role = EXCLUDED.role,
	redirect_after = EXCLUDED.redirect_after,
	expires_at = EXCLUDED.expires_at,
	consumed_at = NULL`, state.Value, roleCustomer, state.RedirectAfter, state.ExpiresAt)
	return err
}

func (s *postgresStore) oauthState(ctx context.Context, value string) (oauthState, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT state, role, redirect_after, expires_at
FROM auth_oauth_states
WHERE state = $1 AND consumed_at IS NULL`, value)

	var state oauthState
	if err := row.Scan(&state.Value, &state.Role, &state.RedirectAfter, &state.ExpiresAt); errors.Is(err, sql.ErrNoRows) {
		return oauthState{}, false, nil
	} else if err != nil {
		return oauthState{}, false, err
	}

	if time.Now().After(state.ExpiresAt) {
		_ = s.deleteOAuthState(ctx, value)
		return oauthState{}, false, nil
	}
	return state, true, nil
}

func (s *postgresStore) deleteOAuthState(ctx context.Context, value string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_oauth_states SET consumed_at = now() WHERE state = $1`, value)
	return err
}

type storedAuthSession struct {
	TokenHash         string    `json:"tokenHash"`
	GitHubAccessToken string    `json:"githubAccessToken"`
	User              authUser  `json:"user"`
	Role              string    `json:"role"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

func (s *postgresStore) internalAccounts(ctx context.Context, limit int) ([]internalAccount, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, login_id, password_hash, role, status, display_name, force_password_change,
	created_by, last_login_at, created_at, updated_at
FROM internal_accounts
ORDER BY created_at DESC, id ASC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := []internalAccount{}
	for rows.Next() {
		account, err := scanInternalAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *postgresStore) internalAccount(ctx context.Context, id string) (internalAccount, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, login_id, password_hash, role, status, display_name, force_password_change,
	created_by, last_login_at, created_at, updated_at
FROM internal_accounts
WHERE id = $1`, id)
	account, err := scanInternalAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return internalAccount{}, false, nil
	}
	if err != nil {
		return internalAccount{}, false, err
	}
	return account, true, nil
}

func (s *postgresStore) internalAccountByLogin(ctx context.Context, loginID string) (internalAccount, bool, error) {
	loginID = strings.ToLower(strings.TrimSpace(loginID))
	if loginID == "" {
		return internalAccount{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, login_id, password_hash, role, status, display_name, force_password_change,
	created_by, last_login_at, created_at, updated_at
FROM internal_accounts
WHERE lower(login_id) = $1`, loginID)
	account, err := scanInternalAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return internalAccount{}, false, nil
	}
	if err != nil {
		return internalAccount{}, false, err
	}
	return account, true, nil
}

func (s *postgresStore) saveInternalAccount(ctx context.Context, account internalAccount) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if account.CreatedAt == "" {
		account.CreatedAt = now
	}
	if account.UpdatedAt == "" {
		account.UpdatedAt = now
	}

	var created bool
	err := s.db.QueryRowContext(ctx, `
INSERT INTO internal_accounts (
	id, login_id, password_hash, role, status, display_name, force_password_change,
	created_by, last_login_at, created_at, updated_at
) VALUES (
	$1, $2, $3, $4, $5, NULLIF($6, ''), $7,
	NULLIF($8, ''), NULLIF($9, '')::timestamptz, $10, $11
)
ON CONFLICT (id) DO UPDATE SET
	login_id = EXCLUDED.login_id,
	password_hash = EXCLUDED.password_hash,
	role = EXCLUDED.role,
	status = EXCLUDED.status,
	display_name = EXCLUDED.display_name,
	force_password_change = EXCLUDED.force_password_change,
	created_by = COALESCE(internal_accounts.created_by, EXCLUDED.created_by),
	last_login_at = EXCLUDED.last_login_at,
	updated_at = EXCLUDED.updated_at
RETURNING xmax = 0`,
		account.ID,
		account.LoginID,
		account.PasswordHash,
		account.Role,
		account.Status,
		account.DisplayName,
		account.ForcePasswordChange,
		account.CreatedBy,
		account.LastLoginAt,
		account.CreatedAt,
		account.UpdatedAt,
	).Scan(&created)
	if err != nil {
		return false, err
	}
	return created, nil
}

type internalAccountScanner interface {
	Scan(dest ...any) error
}

func scanInternalAccount(scanner internalAccountScanner) (internalAccount, error) {
	var account internalAccount
	var displayName sql.NullString
	var createdBy sql.NullString
	var lastLoginAt sql.NullTime
	var createdAt time.Time
	var updatedAt time.Time

	if err := scanner.Scan(
		&account.ID,
		&account.LoginID,
		&account.PasswordHash,
		&account.Role,
		&account.Status,
		&displayName,
		&account.ForcePasswordChange,
		&createdBy,
		&lastLoginAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return internalAccount{}, err
	}

	account.DisplayName = displayName.String
	account.CreatedBy = createdBy.String
	if lastLoginAt.Valid {
		account.LastLoginAt = lastLoginAt.Time.UTC().Format(time.RFC3339)
	}
	account.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	account.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return account, nil
}

func mapFromStruct(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeMap(item map[string]any, target any) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func generatedID(prefix string) string {
	prefix = strings.Trim(prefix, "-_ .")
	if prefix == "" {
		prefix = "resource"
	}
	token := randomToken()
	if len(token) > 16 {
		token = token[:16]
	}
	return fmt.Sprintf("%s-%s", prefix, token)
}

func resourceIDPrefix(domain string) string {
	parts := strings.FieldsFunc(domain, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return "resource"
	}
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, "ies") && len(last) > 3 {
		return last[:len(last)-3] + "y"
	}
	return strings.TrimSuffix(last, "s")
}
