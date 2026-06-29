package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

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
