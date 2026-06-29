package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type githubOAuthClient struct {
	clientID     string
	clientSecret string
	redirectURI  string
	scopes       string
	authURL      string
	tokenURL     string
	apiBaseURL   string
	http         *http.Client
}

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
	Description string `json:"error_description,omitempty"`
}

type githubUserResponse struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
}

type githubEmailResponse struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility"`
}

type githubRepositoryResponse struct {
	FullName string `json:"full_name"`
	Archived bool   `json:"archived"`
	Disabled bool   `json:"disabled"`
	Fork     bool   `json:"fork"`
}

type githubRepositoryCommitResponse struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubAPIError struct {
	Path   string
	Status int
	Body   string
}

func (e githubAPIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("GitHub API %s returned status %d", e.Path, e.Status)
	}
	return fmt.Sprintf("GitHub API %s returned status %d: %s", e.Path, e.Status, e.Body)
}

func newGitHubOAuthClientFromEnv() *githubOAuthClient {
	return &githubOAuthClient{
		clientID:     env("GITHUB_OAUTH_CLIENT_ID", ""),
		clientSecret: env("GITHUB_OAUTH_CLIENT_SECRET", ""),
		redirectURI:  env("GITHUB_OAUTH_REDIRECT_URI", env("PUBLIC_BASE_URL", "http://localhost:8080")+"/api/auth/github/callback"),
		scopes:       env("GITHUB_OAUTH_SCOPES", "read:user user:email repo"),
		authURL:      env("GITHUB_OAUTH_AUTHORIZE_URL", "https://github.com/login/oauth/authorize"),
		tokenURL:     env("GITHUB_OAUTH_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		apiBaseURL:   strings.TrimRight(env("GITHUB_API_BASE_URL", "https://api.github.com"), "/"),
		http:         &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *githubOAuthClient) Configured() bool {
	return c.clientID != "" && c.clientSecret != "" && c.redirectURI != ""
}

func (c *githubOAuthClient) AuthorizeURL(state string) string {
	values := url.Values{}
	values.Set("client_id", c.clientID)
	values.Set("redirect_uri", c.redirectURI)
	values.Set("scope", c.scopes)
	values.Set("state", state)
	values.Set("allow_signup", "true")
	return c.authURL + "?" + values.Encode()
}

func (c *githubOAuthClient) Exchange(ctx context.Context, code string) (githubTokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", c.clientID)
	values.Set("client_secret", c.clientSecret)
	values.Set("code", code)
	values.Set("redirect_uri", c.redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return githubTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("github token exchange failed",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"url", c.tokenURL,
			"duration", time.Since(start).String(),
			"error", err,
		)
		return githubTokenResponse{}, err
	}
	defer resp.Body.Close()
	slog.Debug("github token exchange completed",
		"request_id", requestIDFromContext(ctx),
		"method", req.Method,
		"url", c.tokenURL,
		"status", resp.StatusCode,
		"duration", time.Since(start).String(),
	)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("github token exchange returned non-success status",
			"request_id", requestIDFromContext(ctx),
			"status", resp.StatusCode,
			"duration", time.Since(start).String(),
		)
		return githubTokenResponse{}, fmt.Errorf("GitHub token endpoint returned status %d", resp.StatusCode)
	}

	token := githubTokenResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return githubTokenResponse{}, err
	}
	if token.Error != "" {
		return githubTokenResponse{}, fmt.Errorf("%s: %s", token.Error, token.Description)
	}
	if token.AccessToken == "" {
		return githubTokenResponse{}, errors.New("GitHub access token이 비어 있습니다")
	}
	return token, nil
}

func (c *githubOAuthClient) User(ctx context.Context, accessToken string) (githubUserResponse, error) {
	user := githubUserResponse{}
	if err := c.githubGet(ctx, accessToken, "/user", &user); err != nil {
		return githubUserResponse{}, err
	}
	if user.ID == 0 || user.Login == "" {
		return githubUserResponse{}, errors.New("GitHub 사용자 정보를 읽을 수 없습니다")
	}
	return user, nil
}

func (c *githubOAuthClient) PrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	emails := []githubEmailResponse{}
	if err := c.githubGet(ctx, accessToken, "/user/emails", &emails); err != nil {
		return "", err
	}
	for _, item := range emails {
		if item.Primary && item.Verified {
			return item.Email, nil
		}
	}
	for _, item := range emails {
		if item.Verified {
			return item.Email, nil
		}
	}
	return "", nil
}

func (c *githubOAuthClient) Commits(ctx context.Context, accessToken, login string, from, to time.Time) ([]githubCommitItem, error) {
	if accessToken == "" {
		return nil, errors.New("GitHub access token is empty")
	}
	if login == "" {
		return nil, errors.New("GitHub login is empty")
	}
	maxPages := queryIntValue(env("GITHUB_COMMIT_MAX_PAGES", "5"), 5)
	if maxPages < 1 {
		maxPages = 1
	}
	repositories, err := c.UserRepositories(ctx, accessToken, login)
	if err != nil {
		return nil, err
	}
	all := []githubCommitItem{}
	for _, repository := range repositories {
		commits, err := c.RepositoryCommits(ctx, accessToken, login, repository, from, to, maxPages)
		if err != nil {
			slog.Warn("skip github repository commits", "repository", repository, "error", err)
			continue
		}
		all = append(all, commits...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].OccurredAt.After(all[j].OccurredAt)
	})
	return all, nil
}

func (c *githubOAuthClient) UserRepositories(ctx context.Context, accessToken, login string) ([]string, error) {
	maxPages := queryIntValue(env("GITHUB_REPOSITORY_MAX_PAGES", "10"), 10)
	if maxPages < 1 {
		maxPages = 1
	}
	repositories := []string{}
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		values := url.Values{}
		values.Set("affiliation", "owner,collaborator,organization_member")
		values.Set("visibility", "all")
		values.Set("sort", "updated")
		values.Set("direction", "desc")
		values.Set("per_page", "100")
		values.Set("page", strconv.Itoa(page))
		response := []githubRepositoryResponse{}
		if err := c.githubGet(ctx, accessToken, "/user/repos?"+values.Encode(), &response); err != nil {
			return nil, err
		}
		for _, repo := range response {
			if repo.FullName == "" || repo.Archived || repo.Disabled || seen[repo.FullName] {
				continue
			}
			seen[repo.FullName] = true
			repositories = append(repositories, repo.FullName)
		}
		if len(response) < 100 {
			break
		}
	}
	sort.Strings(repositories)
	return repositories, nil
}

func (c *githubOAuthClient) RepositoryCommits(ctx context.Context, accessToken, login, repository string, from, to time.Time, maxPages int) ([]githubCommitItem, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository %q", repository)
	}
	toEnd := to.AddDate(0, 0, 1).Add(-time.Nanosecond)
	out := []githubCommitItem{}
	for page := 1; page <= maxPages; page++ {
		values := url.Values{}
		values.Set("author", login)
		values.Set("since", from.Format(time.RFC3339))
		values.Set("until", toEnd.Format(time.RFC3339))
		values.Set("per_page", "100")
		values.Set("page", strconv.Itoa(page))

		path := fmt.Sprintf("/repos/%s/%s/commits?%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), values.Encode())
		response := []githubRepositoryCommitResponse{}
		if err := c.githubGet(ctx, accessToken, path, &response); err != nil {
			var apiErr githubAPIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
				break
			}
			return nil, err
		}
		for _, item := range response {
			title := strings.TrimSpace(strings.Split(item.Commit.Message, "\n")[0])
			authorLogin := ""
			if item.Author != nil {
				authorLogin = item.Author.Login
			}
			out = append(out, githubCommitItem{
				SHA:          item.SHA,
				Repository:   repository,
				Message:      item.Commit.Message,
				Title:        title,
				AuthorName:   item.Commit.Author.Name,
				AuthorEmail:  item.Commit.Author.Email,
				AuthorLogin:  authorLogin,
				OccurredAt:   item.Commit.Author.Date,
				HTMLURL:      item.HTMLURL,
				RewardPoints: commitRewardPoints,
			})
		}
		if len(response) < 100 {
			break
		}
	}
	return out, nil
}

func (c *githubOAuthClient) githubGet(ctx context.Context, accessToken, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-GitHub-Api-Version", env("GITHUB_API_VERSION", "2022-11-28"))

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("github api request failed",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"path", path,
			"duration", time.Since(start).String(),
			"error", err,
		)
		return err
	}
	defer resp.Body.Close()
	slog.Debug("github api request completed",
		"request_id", requestIDFromContext(ctx),
		"method", req.Method,
		"path", path,
		"status", resp.StatusCode,
		"duration", time.Since(start).String(),
	)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("github api request returned non-success status",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"path", path,
			"status", resp.StatusCode,
			"duration", time.Since(start).String(),
			"body", truncateLogString(strings.TrimSpace(string(body))),
		)
		return githubAPIError{Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return json.NewDecoder(resp.Body).Decode(target)
}
