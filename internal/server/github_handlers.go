package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleCommitActivity(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 118)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	counts := map[string]int{}
	rewards := map[string]int{}
	for _, commit := range commits {
		day := commit.OccurredAt.In(appLocation()).Format("2006-01-02")
		counts[day]++
		rewards[day] += commit.RewardPoints
	}
	items := []map[string]any{}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		count := counts[key]
		level := commitLevel(count)
		items = append(items, map[string]any{"date": key, "count": count, "level": level, "rewardedPoints": rewards[key]})
	}
	s.ok(w, r, items)
}

func (s *server) handleCommitStats(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 180)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	groupBy := envDefault(r.URL.Query().Get("groupBy"), "month")
	s.ok(w, r, commitStats(commits, groupBy))
}

func (s *server) handleCommitTransactions(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 30)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 20)
	transactions := commitTransactions(commits, limit)
	s.okPage(w, r, transactions, &pagination{Limit: limit, HasMore: len(commits) > len(transactions)})
}

func (s *server) handleGitHubCommits(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 30)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 100)
	if limit > 0 && limit < len(commits) {
		commits = commits[:limit]
	}
	s.okPage(w, r, commits, &pagination{Limit: limit, HasMore: false})
}

func (s *server) handleGitHubAppInstallation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	installURL := env("GITHUB_APP_INSTALL_URL", "")
	installed, _ := s.githubAppInstalledForUser(r.Context(), session.User)
	s.ok(w, r, map[string]any{
		"configured": installURL != "",
		"installUrl": installURL,
		"installed":  installed,
		"login":      session.User.Login,
	})
}

func (s *server) handleGitHubAppSetup(w http.ResponseWriter, r *http.Request) {
	redirectURL := safeRedirectURL(env("AUTH_SUCCESS_REDIRECT_URL", env("PUBLIC_BASE_URL", "http://localhost:5173")+"/login"))
	session, ok := s.sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, appendQuery(redirectURL, map[string]string{"login": "required", "githubApp": "setup"}), http.StatusFound)
		return
	}
	installationID := strings.TrimSpace(r.URL.Query().Get("installation_id"))
	setupAction := strings.TrimSpace(r.URL.Query().Get("setup_action"))
	if installationID != "" {
		_, _ = s.store.put(r.Context(), resourceGitHubInstallations, "user-"+session.User.ID, map[string]any{
			"installationId": installationID,
			"setupAction":    setupAction,
			"userId":         session.User.ID,
			"githubLogin":    session.User.Login,
			"githubId":       session.User.GitHubID,
			"connectedAt":    time.Now().UTC().Format(time.RFC3339),
		})
	}
	http.Redirect(w, r, appendQuery(redirectURL, map[string]string{"login": "success", "role": session.Role, "githubApp": "installed"}), http.StatusFound)
}

func (s *server) githubAppInstallRedirectURL(ctx context.Context, session authSession, redirectAfter string) (string, bool) {
	if session.Role != "customer" || !envBool("GITHUB_APP_INSTALL_ON_LOGIN", true) {
		return "", false
	}
	installURL := env("GITHUB_APP_INSTALL_URL", "")
	if installURL == "" {
		return "", false
	}
	installed, err := s.githubAppInstalledForUser(ctx, session.User)
	if err != nil {
		slog.Warn("check github app installation", "user_id", session.User.ID, "error", err)
	}
	if installed {
		return "", false
	}
	return appendQuery(installURL, map[string]string{"state": randomToken(), "redirect_after": redirectAfter}), true
}

func (s *server) githubAppInstalledForUser(ctx context.Context, user authUser) (bool, error) {
	if user.ID == "" {
		return false, nil
	}
	if _, ok, err := s.store.get(ctx, resourceGitHubInstallations, "user-"+user.ID); err != nil || ok {
		return ok, err
	}
	items, err := s.store.list(ctx, resourceGitHubInstallations, 1000)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		action := strings.ToLower(stringValue(item["action"]))
		if action == "deleted" || action == "suspend" {
			continue
		}
		if stringValue(item["githubLogin"]) == user.Login || stringValue(item["accountLogin"]) == user.Login || stringValue(item["senderLogin"]) == user.Login {
			return true, nil
		}
		if account, _ := item["account"].(map[string]any); stringValue(account["login"]) == user.Login {
			return true, nil
		}
		if sender, _ := item["sender"].(map[string]any); stringValue(sender["login"]) == user.Login {
			return true, nil
		}
	}
	return false, nil
}

type githubCommitItem struct {
	SHA             string     `json:"sha"`
	Repository      string     `json:"repository"`
	Message         string     `json:"message"`
	Title           string     `json:"title"`
	AuthorName      string     `json:"authorName,omitempty"`
	AuthorEmail     string     `json:"authorEmail,omitempty"`
	AuthorLogin     string     `json:"authorLogin,omitempty"`
	OccurredAt      time.Time  `json:"occurredAt"`
	CommitTimestamp *time.Time `json:"commitTimestamp,omitempty"`
	HTMLURL         string     `json:"htmlUrl"`
	RewardPoints    int        `json:"rewardedPoints"`
}

func (s *server) requireGitHubSession(w http.ResponseWriter, r *http.Request) (authSession, bool) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return authSession{}, false
	}
	if session.GitHubAccessToken == "" {
		s.fail(w, r, http.StatusUnauthorized, "GITHUB_TOKEN_REQUIRED", "GitHub access token이 있는 세션이 필요합니다. 다시 로그인해 주세요.", nil)
		return authSession{}, false
	}
	return session, true
}

func (s *server) fetchGitHubCommitsForRequest(w http.ResponseWriter, r *http.Request, session authSession, from, to time.Time) ([]githubCommitItem, bool) {
	commits, err := s.listStoredGitHubCommits(r.Context(), session.User.Login, from, to)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "GitHub webhook 커밋 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return nil, false
	}
	return commits, true
}
