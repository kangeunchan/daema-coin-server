package server

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type commitStreakMilestoneRule struct {
	Days         int
	RewardAmount int
}

var commitStreakMilestones = []commitStreakMilestoneRule{
	{Days: 7, RewardAmount: 5_000},
	{Days: 14, RewardAmount: 15_000},
}

type commitStreakStats struct {
	CommittedToday  bool
	CurrentStreak   int
	DailyCounts     map[string]int
	LastCommittedAt *time.Time
	LongestStreak   int
	StreakStart     time.Time
	TodayCount      int
}

type commitRewardMilestoneResponse struct {
	AchievedAt   string `json:"achievedAt,omitempty"`
	Days         int    `json:"days"`
	PaidAt       string `json:"paidAt,omitempty"`
	RewardAmount int    `json:"rewardAmount"`
	Status       string `json:"status"`
}

type commitRewardSummaryResponse struct {
	CommittedToday    bool                            `json:"committedToday"`
	CurrentStreakDays int                             `json:"currentStreakDays"`
	DailyCommitGoal   int                             `json:"dailyCommitGoal"`
	LastCommittedAt   string                          `json:"lastCommittedAt,omitempty"`
	LongestStreakDays int                             `json:"longestStreakDays"`
	Milestones        []commitRewardMilestoneResponse `json:"milestones"`
	RewardCurrency    string                          `json:"rewardCurrency"`
	TodayCommitCount  int                             `json:"todayCommitCount"`
	TotalRewardAmount int                             `json:"totalRewardAmount"`
}

func calculateCommitStreak(commits []githubCommitItem, now time.Time, dailyGoal int) commitStreakStats {
	if dailyGoal <= 0 {
		dailyGoal = commitDailyGoal
	}
	location := appLocation()
	now = now.In(location)
	counts := map[string]int{}
	var lastCommittedAt *time.Time
	for _, commit := range commits {
		occurredAt := commit.OccurredAt.In(location)
		if occurredAt.After(now) {
			continue
		}
		counts[occurredAt.Format("2006-01-02")]++
		if lastCommittedAt == nil || occurredAt.After(*lastCommittedAt) {
			copy := occurredAt
			lastCommittedAt = &copy
		}
	}

	qualifiedDays := make([]time.Time, 0, len(counts))
	for date, count := range counts {
		if count < dailyGoal {
			continue
		}
		parsed, err := time.ParseInLocation("2006-01-02", date, location)
		if err == nil {
			qualifiedDays = append(qualifiedDays, parsed)
		}
	}
	sort.Slice(qualifiedDays, func(i, j int) bool { return qualifiedDays[i].Before(qualifiedDays[j]) })

	longest := 0
	run := 0
	var previous time.Time
	for _, day := range qualifiedDays {
		if !previous.IsZero() && day.Equal(previous.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		previous = day
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	todayKey := today.Format("2006-01-02")
	committedToday := counts[todayKey] >= dailyGoal
	anchor := today
	if !committedToday {
		anchor = anchor.AddDate(0, 0, -1)
	}
	current := 0
	for counts[anchor.Format("2006-01-02")] >= dailyGoal {
		current++
		anchor = anchor.AddDate(0, 0, -1)
	}
	streakStart := time.Time{}
	if current > 0 {
		streakStart = anchor.AddDate(0, 0, 1)
	}

	return commitStreakStats{
		CommittedToday:  committedToday,
		CurrentStreak:   current,
		DailyCounts:     counts,
		LastCommittedAt: lastCommittedAt,
		LongestStreak:   longest,
		StreakStart:     streakStart,
		TodayCount:      counts[todayKey],
	}
}

func commitStreakRewardLedgerID(userID string, milestoneDays int, achievedAt time.Time) string {
	return ledgerID(commitStreakType, userID, fmt.Sprintf("%d", milestoneDays), achievedAt.In(appLocation()).Format("2006-01-02"))
}

func (s *server) loadCommitStreakStats(ctx context.Context, login string, now time.Time) (commitStreakStats, error) {
	from := time.Date(2000, time.January, 1, 0, 0, 0, 0, appLocation())
	commits, err := s.listStoredGitHubCommits(ctx, login, from, now.In(appLocation()))
	if err != nil {
		return commitStreakStats{}, err
	}
	return calculateCommitStreak(commits, now, commitDailyGoal), nil
}

func (s *server) grantGitHubCommitStreakRewards(ctx context.Context, user authUser, login string, now time.Time) error {
	if user.ID == "" || login == "" {
		return nil
	}
	stats, err := s.loadCommitStreakStats(ctx, login, now)
	if err != nil {
		return err
	}
	for _, milestone := range commitStreakMilestones {
		if stats.CurrentStreak < milestone.Days || stats.StreakStart.IsZero() {
			continue
		}
		achievedAt := stats.StreakStart.AddDate(0, 0, milestone.Days-1)
		_, err := s.createLedgerAndAdjustWallet(ctx, user,
			commitStreakRewardLedgerID(user.ID, milestone.Days, achievedAt),
			commitStreakType,
			"income",
			commitStreakCurrency,
			milestone.RewardAmount,
			map[string]any{
				"description":     fmt.Sprintf("%d일 연속 커밋 리워드", milestone.Days),
				"referenceType":   "github-commit-streak",
				"referenceId":     achievedAt.Format("2006-01-02"),
				"milestoneDays":   milestone.Days,
				"dailyCommitGoal": commitDailyGoal,
				"achievedAt":      achievedAt.Format("2006-01-02"),
			},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) commitRewardSummary(ctx context.Context, user authUser, login string, now time.Time) (commitRewardSummaryResponse, error) {
	stats, err := s.loadCommitStreakStats(ctx, login, now)
	if err != nil {
		return commitRewardSummaryResponse{}, err
	}
	totalRewardAmount, err := s.store.ledgerIncomeTotalByType(ctx, user.ID, commitStreakType)
	if err != nil {
		return commitRewardSummaryResponse{}, err
	}

	milestones := make([]commitRewardMilestoneResponse, 0, len(commitStreakMilestones))
	for _, rule := range commitStreakMilestones {
		milestone := commitRewardMilestoneResponse{
			Days:         rule.Days,
			RewardAmount: rule.RewardAmount,
			Status:       "locked",
		}
		if stats.CurrentStreak >= rule.Days && !stats.StreakStart.IsZero() {
			achievedAt := stats.StreakStart.AddDate(0, 0, rule.Days-1)
			milestone.AchievedAt = achievedAt.Format("2006-01-02")
			paidAt, paid, err := s.store.ledgerTransactionOccurredAt(ctx, user.ID, commitStreakRewardLedgerID(user.ID, rule.Days, achievedAt))
			if err != nil {
				return commitRewardSummaryResponse{}, err
			}
			if paid {
				milestone.Status = "paid"
				milestone.PaidAt = paidAt.UTC().Format(time.RFC3339)
			} else {
				milestone.Status = "earned"
			}
		}
		milestones = append(milestones, milestone)
	}

	response := commitRewardSummaryResponse{
		CommittedToday:    stats.CommittedToday,
		CurrentStreakDays: stats.CurrentStreak,
		DailyCommitGoal:   commitDailyGoal,
		LongestStreakDays: stats.LongestStreak,
		Milestones:        milestones,
		RewardCurrency:    commitStreakCurrency,
		TodayCommitCount:  stats.TodayCount,
		TotalRewardAmount: totalRewardAmount,
	}
	if stats.LastCommittedAt != nil {
		response.LastCommittedAt = stats.LastCommittedAt.UTC().Format(time.RFC3339)
	}
	return response, nil
}
