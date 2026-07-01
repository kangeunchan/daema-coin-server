package server

import (
	"fmt"
	"testing"
	"time"
)

func commitStreakTestCommits(start time.Time, dailyCounts ...int) []githubCommitItem {
	commits := []githubCommitItem{}
	for dayIndex, count := range dailyCounts {
		day := start.AddDate(0, 0, dayIndex)
		for commitIndex := 0; commitIndex < count; commitIndex++ {
			commits = append(commits, githubCommitItem{
				SHA:        fmt.Sprintf("day-%d-commit-%d", dayIndex, commitIndex),
				OccurredAt: day.Add(time.Duration(commitIndex) * time.Minute),
			})
		}
	}
	return commits
}

func TestCalculateCommitStreakRequiresTenCommitsPerDay(t *testing.T) {
	location := appLocation()
	start := time.Date(2026, time.June, 18, 9, 0, 0, 0, location)
	commits := commitStreakTestCommits(start, 10, 12, 10, 10, 11, 10, 10, 10, 10, 13, 10, 10, 10, 10)
	now := time.Date(2026, time.July, 1, 23, 0, 0, 0, location)

	stats := calculateCommitStreak(commits, now, 10)
	if !stats.CommittedToday {
		t.Fatal("today should be qualified after 10 commits")
	}
	if stats.TodayCount != 10 {
		t.Fatalf("today count = %d, want 10", stats.TodayCount)
	}
	if stats.CurrentStreak != 14 || stats.LongestStreak != 14 {
		t.Fatalf("streaks = current %d longest %d, want 14 and 14", stats.CurrentStreak, stats.LongestStreak)
	}
	if got := stats.StreakStart.Format("2006-01-02"); got != "2026-06-18" {
		t.Fatalf("streak start = %s, want 2026-06-18", got)
	}
}

func TestCalculateCommitStreakKeepsYesterdayUntilTodayQualifies(t *testing.T) {
	location := appLocation()
	start := time.Date(2026, time.June, 27, 9, 0, 0, 0, location)
	commits := commitStreakTestCommits(start, 10, 10, 10, 10, 9)
	now := time.Date(2026, time.July, 1, 20, 0, 0, 0, location)

	stats := calculateCommitStreak(commits, now, 10)
	if stats.CommittedToday {
		t.Fatal("today should not be qualified with 9 commits")
	}
	if stats.TodayCount != 9 {
		t.Fatalf("today count = %d, want 9", stats.TodayCount)
	}
	if stats.CurrentStreak != 4 || stats.LongestStreak != 4 {
		t.Fatalf("streaks = current %d longest %d, want 4 and 4", stats.CurrentStreak, stats.LongestStreak)
	}
}

func TestCommitStreakRewardLedgerIDIsScopedToAchievement(t *testing.T) {
	location := appLocation()
	first := commitStreakRewardLedgerID("customer-1", 7, time.Date(2026, time.June, 24, 0, 0, 0, 0, location))
	replay := commitStreakRewardLedgerID("customer-1", 7, time.Date(2026, time.June, 24, 23, 0, 0, 0, location))
	nextStreak := commitStreakRewardLedgerID("customer-1", 7, time.Date(2026, time.July, 8, 0, 0, 0, 0, location))
	if first != replay {
		t.Fatalf("same achievement generated different ids: %q != %q", first, replay)
	}
	if first == nextStreak {
		t.Fatalf("different streak achievements generated the same id: %q", first)
	}
}
