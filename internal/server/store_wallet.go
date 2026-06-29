package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	return s.createLedgerAndAdjustWalletRequest(ctx, newWalletLedgerRequest(user, id, txType, direction, currency, value, extras))
}

func (s *postgresStore) createLedgerAndAdjustWalletRequest(ctx context.Context, req walletLedgerRequest) (bool, error) {
	var created bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		var err error
		created, err = s.createLedgerAndAdjustWalletRequestTx(ctx, tx, req)
		return err
	})
	return created, err
}

func (s *postgresStore) createLedgerAndAdjustWalletTx(ctx context.Context, tx *sql.Tx, user authUser, id, txType, direction, currency string, value int, extras map[string]any) (bool, error) {
	return s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(user, id, txType, direction, currency, value, extras))
}

func (s *postgresStore) createLedgerAndAdjustWalletRequestTx(ctx context.Context, tx *sql.Tx, req walletLedgerRequest) (bool, error) {
	user := req.User
	id := req.ID
	txType := req.Type
	direction := req.Direction
	currency := req.Currency
	value := req.Amount
	extras := cloneMap(req.Extras)
	if value <= 0 {
		return false, nil
	}
	if strings.TrimSpace(id) == "" {
		return false, errors.New("ledger id is required")
	}
	if strings.TrimSpace(user.ID) == "" {
		return false, errors.New("customer id is required")
	}
	if extras == nil {
		extras = map[string]any{}
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "DMC" && currency != "POINT" {
		return false, fmt.Errorf("unsupported wallet currency %q", currency)
	}
	if direction != "income" {
		direction = "expense"
	}
	delta := value
	if direction == "expense" {
		delta = -value
	}

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
		matches, matchErr := s.ledgerTransactionMatchesTx(ctx, tx, id, walletID, user.ID, direction, currency, value, txType, referenceType, referenceID)
		if matchErr != nil {
			return false, matchErr
		}
		if !matches {
			return false, errLedgerIdempotencyConflict
		}
		return false, nil
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

	return true, nil
}

func (s *postgresStore) ledgerTransactionMatchesTx(ctx context.Context, tx *sql.Tx, id, walletID, customerID, direction, currency string, value int, txType, referenceType, referenceID string) (bool, error) {
	var existingWalletID, existingCustomerID, existingDirection, existingCurrency, existingType string
	var existingReferenceType, existingReferenceID sql.NullString
	var existingAmount int
	err := tx.QueryRowContext(ctx, `
SELECT wallet_account_id, customer_id, direction, currency, amount, transaction_type,
	reference_type, reference_id
FROM ledger_transactions
WHERE id = $1
FOR UPDATE`, id).Scan(
		&existingWalletID,
		&existingCustomerID,
		&existingDirection,
		&existingCurrency,
		&existingAmount,
		&existingType,
		&existingReferenceType,
		&existingReferenceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errLedgerIdempotencyConflict
	}
	if err != nil {
		return false, err
	}
	return existingWalletID == walletID &&
		existingCustomerID == customerID &&
		existingDirection == direction &&
		existingCurrency == currency &&
		existingAmount == value &&
		existingType == txType &&
		existingReferenceType.String == referenceType &&
		existingReferenceID.String == referenceID, nil
}
