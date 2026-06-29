package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WEBHOOK_BODY", "Webhook body를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !verifyGitHubWebhookSignature(body, r.Header.Get("X-Hub-Signature-256")) {
		s.fail(w, r, http.StatusUnauthorized, "INVALID_GITHUB_WEBHOOK_SIGNATURE", "GitHub webhook signature가 유효하지 않습니다.", nil)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	switch event {
	case "ping":
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "ok"})
	case "installation", "installation_repositories":
		if err := s.storeGitHubInstallationEvent(r.Context(), event, deliveryID, body); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "GITHUB_WEBHOOK_STORE_FAILED", "GitHub installation 이벤트를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "stored"})
	case "push":
		count, err := s.storeGitHubPushEvent(r.Context(), deliveryID, body)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "GITHUB_WEBHOOK_STORE_FAILED", "GitHub push 이벤트를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "storedCommits": count})
	default:
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "ignored"})
	}
}

type githubPushWebhookPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
		Private  bool   `json:"private"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Pusher struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Sender struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
	} `json:"sender"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Commits []struct {
		ID        string    `json:"id"`
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
		URL       string    `json:"url"`
		Distinct  bool      `json:"distinct"`
		Author    struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"author"`
		Committer struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"committer"`
	} `json:"commits"`
}

func verifyGitHubWebhookSignature(body []byte, signature string) bool {
	secret := env("GITHUB_WEBHOOK_SECRET", "")
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (s *server) storeGitHubInstallationEvent(ctx context.Context, event, deliveryID string, body []byte) error {
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	installationID := ""
	if installation, _ := payload["installation"].(map[string]any); installation != nil {
		installationID = fmt.Sprint(installation["id"])
	}
	if installationID == "" {
		installationID = deliveryID
	}
	payload["event"] = event
	payload["deliveryId"] = deliveryID
	payload["receivedAt"] = time.Now().UTC().Format(time.RFC3339)
	if account, _ := payload["account"].(map[string]any); account != nil {
		payload["accountLogin"] = stringValue(account["login"])
	}
	if sender, _ := payload["sender"].(map[string]any); sender != nil {
		payload["senderLogin"] = stringValue(sender["login"])
	}
	_, err := s.store.put(ctx, resourceGitHubInstallations, installationID, payload)
	return err
}

func (s *server) storeGitHubPushEvent(ctx context.Context, deliveryID string, body []byte) (int, error) {
	payload := githubPushWebhookPayload{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}
	if payload.Repository.FullName == "" {
		return 0, errors.New("repository full_name is empty")
	}
	count := 0
	for _, commit := range payload.Commits {
		if commit.ID == "" {
			continue
		}
		occurredAt := commit.Timestamp
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		authorLogin := firstNonEmpty(commit.Author.Username, commit.Committer.Username, payload.Sender.Login, payload.Pusher.Name)
		rewardPoints, rewardUser, err := s.githubCommitReward(ctx, authorLogin, occurredAt)
		if err != nil {
			return count, err
		}
		title := strings.TrimSpace(strings.Split(commit.Message, "\n")[0])
		item := githubCommitItem{
			SHA:          commit.ID,
			Repository:   payload.Repository.FullName,
			Message:      commit.Message,
			Title:        title,
			AuthorName:   firstNonEmpty(commit.Author.Name, commit.Committer.Name),
			AuthorEmail:  firstNonEmpty(commit.Author.Email, commit.Committer.Email),
			AuthorLogin:  authorLogin,
			OccurredAt:   occurredAt,
			HTMLURL:      commit.URL,
			RewardPoints: rewardPoints,
		}
		data, err := mapFromStruct(item)
		if err != nil {
			return count, err
		}
		data["authorLogin"] = authorLogin
		data["pusherLogin"] = payload.Pusher.Name
		data["senderLogin"] = payload.Sender.Login
		data["repositoryId"] = payload.Repository.ID
		data["repositoryOwner"] = payload.Repository.Owner.Login
		data["repositoryPrivate"] = payload.Repository.Private
		data["ref"] = payload.Ref
		data["before"] = payload.Before
		data["after"] = payload.After
		data["distinct"] = commit.Distinct
		data["deliveryId"] = deliveryID
		if payload.Installation != nil {
			data["installationId"] = payload.Installation.ID
		}
		commitEventID := githubCommitID(payload.Repository.FullName, commit.ID)
		_, created, err := s.store.create(ctx, resourceGitHubCommits, commitEventID, data)
		if err != nil {
			return count, err
		}
		if !created {
			continue
		}
		if rewardPoints > 0 && rewardUser.ID != "" {
			_, err = s.createLedgerAndAdjustWallet(ctx, rewardUser, ledgerID("github-commit-reward", rewardUser.ID, payload.Repository.FullName, commit.ID), "github-commit-reward", "income", commitRewardCurrency, rewardPoints, map[string]any{
				"description": "GitHub 커밋 리워드",
				"commitSha":   commit.ID,
				"repository":  payload.Repository.FullName,
				"htmlUrl":     commit.URL,
				"occurredAt":  occurredAt.UTC().Format(time.RFC3339),
			})
			if err != nil {
				return count, err
			}
		}
		count++
	}
	return count, nil
}
