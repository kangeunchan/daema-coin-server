package main

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

type recordStore struct {
	db *sql.DB
}

func openRecordStore(ctx context.Context, dsn string) (*recordStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	store := &recordStore{db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *recordStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *recordStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS records (
	domain TEXT NOT NULL,
	id TEXT NOT NULL,
	data JSONB NOT NULL,
	created_at TIMESTAMPTZ NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (domain, id)
);
CREATE INDEX IF NOT EXISTS idx_records_domain_updated_at ON records(domain, updated_at DESC);
WITH ranked_predictions AS (
	SELECT
		ctid,
		ROW_NUMBER() OVER (
			PARTITION BY data->>'matchId', data->>'userId'
			ORDER BY updated_at DESC, id DESC
		) AS rn
	FROM records
	WHERE domain = 'worldcup_predictions'
		AND data ? 'matchId'
		AND data ? 'userId'
)
DELETE FROM records
USING ranked_predictions
WHERE records.ctid = ranked_predictions.ctid
	AND ranked_predictions.rn > 1;
CREATE UNIQUE INDEX IF NOT EXISTS idx_records_prediction_user_match
	ON records ((data->>'matchId'), (data->>'userId'))
	WHERE domain = 'worldcup_predictions';
`)
	return err
}

func (s *recordStore) list(ctx context.Context, domain string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT data
FROM records
WHERE domain = $1
ORDER BY updated_at DESC, id ASC
LIMIT $2`, domain, limit)
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

func (s *recordStore) get(ctx context.Context, domain, id string) (map[string]any, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM records WHERE domain = $1 AND id = $2`, domain, id).Scan(&raw)
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

func (s *recordStore) put(ctx context.Context, domain, id string, data map[string]any) (map[string]any, error) {
	if id == "" {
		id = generatedID(domainIDPrefix(domain))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := now
	if existing, ok, err := s.get(ctx, domain, id); err != nil {
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
	_, err = s.db.ExecContext(ctx, `
INSERT INTO records(domain, id, data, created_at, updated_at)
VALUES($1, $2, $3, $4, $5)
ON CONFLICT(domain, id) DO UPDATE SET
	data = excluded.data,
	updated_at = excluded.updated_at`, domain, id, string(raw), createdAt, now)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *recordStore) create(ctx context.Context, domain, id string, data map[string]any) (map[string]any, bool, error) {
	if id == "" {
		id = generatedID(domainIDPrefix(domain))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	data["id"] = id
	data["createdAt"] = now
	data["updatedAt"] = now
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false, err
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO records(domain, id, data, created_at, updated_at)
VALUES($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING`, domain, id, string(raw), now, now)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	return data, affected > 0, nil
}

func (s *recordStore) patch(ctx context.Context, domain, id string, patch map[string]any) (map[string]any, bool, error) {
	existing, ok, err := s.get(ctx, domain, id)
	if err != nil || !ok {
		return nil, ok, err
	}
	for key, value := range patch {
		existing[key] = value
	}
	item, err := s.put(ctx, domain, id, existing)
	return item, true, err
}

func (s *recordStore) delete(ctx context.Context, domain, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM records WHERE domain = $1 AND id = $2`, domain, id)
	return err
}

func (s *recordStore) health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *recordStore) saveSession(ctx context.Context, session authSession) error {
	data, err := typedRecord(storedAuthSession{
		Token:             session.Token,
		GitHubAccessToken: session.GitHubAccessToken,
		User:              session.User,
		Role:              session.Role,
		ExpiresAt:         session.ExpiresAt,
	})
	if err != nil {
		return err
	}
	_, err = s.put(ctx, "auth.sessions", session.Token, data)
	return err
}

func (s *recordStore) session(ctx context.Context, token string) (authSession, bool, error) {
	item, ok, err := s.get(ctx, "auth.sessions", token)
	if err != nil || !ok {
		return authSession{}, ok, err
	}
	stored := storedAuthSession{}
	if err := decodeRecord(item, &stored); err != nil {
		return authSession{}, false, err
	}
	session := authSession{Token: stored.Token, GitHubAccessToken: stored.GitHubAccessToken, User: stored.User, Role: stored.Role, ExpiresAt: stored.ExpiresAt}
	if time.Now().After(session.ExpiresAt) {
		_ = s.delete(ctx, "auth.sessions", token)
		return authSession{}, false, nil
	}
	return session, true, nil
}

func (s *recordStore) deleteSession(ctx context.Context, token string) error {
	return s.delete(ctx, "auth.sessions", token)
}

func (s *recordStore) saveOAuthState(ctx context.Context, state oauthState) error {
	data, err := typedRecord(state)
	if err != nil {
		return err
	}
	_, err = s.put(ctx, "auth.oauth_states", state.Value, data)
	return err
}

func (s *recordStore) oauthState(ctx context.Context, value string) (oauthState, bool, error) {
	item, ok, err := s.get(ctx, "auth.oauth_states", value)
	if err != nil || !ok {
		return oauthState{}, ok, err
	}
	state := oauthState{}
	if err := decodeRecord(item, &state); err != nil {
		return oauthState{}, false, err
	}
	if time.Now().After(state.ExpiresAt) {
		_ = s.delete(ctx, "auth.oauth_states", value)
		return oauthState{}, false, nil
	}
	return state, true, nil
}

func (s *recordStore) deleteOAuthState(ctx context.Context, value string) error {
	return s.delete(ctx, "auth.oauth_states", value)
}

type storedAuthSession struct {
	Token             string    `json:"token"`
	GitHubAccessToken string    `json:"githubAccessToken"`
	User              authUser  `json:"user"`
	Role              string    `json:"role"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

func typedRecord(v any) (map[string]any, error) {
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

func decodeRecord(item map[string]any, target any) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func generatedID(prefix string) string {
	prefix = strings.Trim(prefix, "-_ .")
	if prefix == "" {
		prefix = "record"
	}
	token := randomToken()
	if len(token) > 16 {
		token = token[:16]
	}
	return fmt.Sprintf("%s-%s", prefix, token)
}

func domainIDPrefix(domain string) string {
	parts := strings.FieldsFunc(domain, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return "record"
	}
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, "ies") && len(last) > 3 {
		return last[:len(last)-3] + "y"
	}
	return strings.TrimSuffix(last, "s")
}
