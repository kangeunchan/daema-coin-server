package server

import (
	"context"
	"time"
)

func (s *server) githubCommitReward(ctx context.Context, authorLogin string, receivedAt time.Time) (int, int, authUser, error) {
	user, ok, err := s.authUserForGitHubLogin(ctx, authorLogin)
	if err != nil || !ok {
		return 0, 0, authUser{}, err
	}
	rewardedCount, err := s.rewardedGitHubCommitCountForDay(ctx, authorLogin, receivedAt)
	if err != nil {
		return 0, 0, authUser{}, err
	}
	if rewardedCount >= commitRewardLimit {
		return 0, rewardedCount, user, nil
	}
	return commitRewardPoints, rewardedCount, user, nil
}

func (s *server) authUserForGitHubLogin(ctx context.Context, login string) (authUser, bool, error) {
	return s.store.authUserByGitHubLogin(ctx, login)
}

func (s *server) rewardedGitHubCommitCountForDay(ctx context.Context, login string, receivedAt time.Time) (int, error) {
	items, err := s.store.listFiltered(ctx, resourceGitHubCommits, []resourceFilter{{Field: "authorLogin", Value: login}}, 10000)
	if err != nil {
		return 0, err
	}
	targetDay := receivedAt.In(appLocation()).Format("2006-01-02")
	count := 0
	for _, item := range items {
		if amountValue(map[string]any{"amount": item["rewardedPoints"]}) <= 0 {
			continue
		}
		commit := githubCommitItem{}
		if err := decodeMap(item, &commit); err != nil {
			return 0, err
		}
		if commit.OccurredAt.In(appLocation()).Format("2006-01-02") == targetDay {
			count++
		}
	}
	return count, nil
}
