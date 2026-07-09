package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *postgresStore) customerProfileExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM customer_profiles
	WHERE id = $1
		AND NULLIF(BTRIM(student_no), '') IS NOT NULL
)`, id).Scan(&exists)
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
	studentNo := strings.TrimSpace(stringValue(profile["studentNo"]))
	if studentNo == "" {
		return nil, errStudentNoRequired
	}
	if !validStudentNo(studentNo) {
		return nil, errInvalidStudentNo
	}
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
		var stateErr sqlStateError
		if errors.As(err, &stateErr) && stateErr.SQLState() == "23505" {
			return nil, errDuplicateStudentNo
		}
		return nil, err
	}

	if user.GitHubID > 0 {
		if err := s.saveGitHubIdentityTx(ctx, tx, user, now); err != nil {
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

func (s *postgresStore) saveGitHubIdentity(ctx context.Context, user authUser) error {
	if user.GitHubID <= 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.saveGitHubIdentityTx(ctx, tx, user, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *postgresStore) saveGitHubIdentityTx(ctx context.Context, tx *sql.Tx, user authUser, now time.Time) error {
	if user.GitHubID <= 0 {
		return nil
	}

	login := firstNonEmpty(user.Login, user.ID)
	result, err := tx.ExecContext(ctx, `
UPDATE github_identities
SET
	customer_id = $1,
	login = $3,
	email = NULLIF($4, ''),
	avatar_url = NULLIF($5, ''),
	html_url = NULLIF($6, ''),
	updated_at = $7
WHERE github_id = $2`,
		user.ID,
		user.GitHubID,
		login,
		user.Email,
		user.AvatarURL,
		user.HTMLURL,
		now,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected > 0 {
		return nil
	}

	result, err = tx.ExecContext(ctx, `
UPDATE github_identities
SET
	customer_id = $1,
	github_id = $2,
	login = $3,
	email = NULLIF($4, ''),
	avatar_url = NULLIF($5, ''),
	html_url = NULLIF($6, ''),
	updated_at = $7
WHERE lower(login) = lower($3)
	AND github_id = 0`,
		user.ID,
		user.GitHubID,
		login,
		user.Email,
		user.AvatarURL,
		user.HTMLURL,
		now,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected > 0 {
		return nil
	}

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
		login,
		user.Email,
		user.AvatarURL,
		user.HTMLURL,
		now,
	)
	return err
}

func validStudentNo(studentNo string) bool {
	if len(studentNo) < 4 || len(studentNo) > 12 {
		return false
	}
	for i := 0; i < len(studentNo); i++ {
		if studentNo[i] < '0' || studentNo[i] > '9' {
			return false
		}
	}
	return true
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

func (s *postgresStore) authUserByGitHubIdentity(ctx context.Context, githubID int64, login string) (authUser, bool, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if githubID <= 0 && login == "" {
		return authUser{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
	SELECT cp.id, cp.display_name, gi.github_id, gi.login, gi.email, gi.avatar_url, gi.html_url
	FROM github_identities gi
	JOIN customer_profiles cp ON cp.id = gi.customer_id
	WHERE cp.status = 'active'
		AND (
			($1 > 0 AND gi.github_id = $1)
			OR ($2 <> '' AND lower(gi.login) = $2 AND gi.github_id = 0)
		)
	ORDER BY CASE WHEN $1 > 0 AND gi.github_id = $1 THEN 0 ELSE 1 END
	LIMIT 1`, githubID, login)

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
