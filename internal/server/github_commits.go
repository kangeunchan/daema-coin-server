package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *server) listStoredGitHubCommits(ctx context.Context, login string, from, to time.Time) ([]githubCommitItem, error) {
	items, err := s.store.listFiltered(ctx, resourceGitHubCommits, []resourceFilter{{Field: "authorLogin", Value: login}}, 10000)
	if err != nil {
		return nil, err
	}
	from = from.In(appLocation())
	toEnd := to.AddDate(0, 0, 1).Add(-time.Nanosecond)
	commits := []githubCommitItem{}
	for _, item := range items {
		commit := githubCommitItem{}
		if err := decodeMap(item, &commit); err != nil {
			return nil, err
		}
		occurredAt := commit.OccurredAt.In(appLocation())
		if occurredAt.Before(from) || occurredAt.After(toEnd) {
			continue
		}
		commits = append(commits, commit)
	}
	sort.SliceStable(commits, func(i, j int) bool {
		return commits[i].OccurredAt.After(commits[j].OccurredAt)
	})
	return commits, nil
}

func githubCommitID(repository, sha string) string {
	sum := sha256.Sum256([]byte(repository + "|" + sha))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func commitDateRange(r *http.Request, defaultDays int) (time.Time, time.Time) {
	today := time.Now().In(appLocation())
	to := parseDate(envDefault(r.URL.Query().Get("to"), today.Format("2006-01-02")))
	from := parseDate(envDefault(r.URL.Query().Get("from"), to.AddDate(0, 0, -defaultDays).Format("2006-01-02")))
	return from, to
}

func commitLevel(count int) int {
	switch {
	case count <= 0:
		return 0
	case count <= 2:
		return 1
	case count <= 5:
		return 2
	case count <= 9:
		return 3
	default:
		return 4
	}
}

func commitStats(commits []githubCommitItem, groupBy string) []map[string]any {
	counts := map[string]int{}
	rewards := map[string]int{}
	labels := map[string]string{}
	now := time.Now().In(appLocation())
	for _, commit := range commits {
		period, label := commitPeriod(commit.OccurredAt, groupBy)
		counts[period]++
		rewards[period] += commit.RewardPoints
		labels[period] = label
	}
	periods := make([]string, 0, len(counts))
	for period := range counts {
		periods = append(periods, period)
	}
	sort.Strings(periods)
	out := make([]map[string]any, 0, len(periods))
	currentPeriod, _ := commitPeriod(now, groupBy)
	for _, period := range periods {
		count := counts[period]
		out = append(out, map[string]any{"period": period, "label": labels[period], "commitCount": count, "rewardedPoints": rewards[period], "current": period == currentPeriod})
	}
	return out
}

func commitPeriod(t time.Time, groupBy string) (string, string) {
	local := t.In(appLocation())
	switch groupBy {
	case "day":
		return local.Format("2006-01-02"), local.Format("1월 2일")
	case "week":
		year, week := local.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week), fmt.Sprintf("%d년 %02d주", year, week)
	default:
		return local.Format("2006-01"), fmt.Sprintf("%d월", int(local.Month()))
	}
}

func commitTransactions(commits []githubCommitItem, limit int) []map[string]any {
	if limit <= 0 || limit > len(commits) {
		limit = len(commits)
	}
	out := make([]map[string]any, 0, limit)
	for _, commit := range commits[:limit] {
		out = append(out, map[string]any{
			"id":                commit.SHA,
			"sha":               commit.SHA,
			"repository":        commit.Repository,
			"title":             commit.Title,
			"message":           commit.Message,
			"commitCount":       1,
			"amount":            amount("POINT", commit.RewardPoints),
			"occurredAt":        commit.OccurredAt.Format(time.RFC3339),
			"relativeTimeLabel": relativeTimeLabel(commit.OccurredAt),
			"htmlUrl":           commit.HTMLURL,
		})
	}
	return out
}

func relativeTimeLabel(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		minutes := int(d.Minutes())
		if minutes < 1 {
			return "방금 전"
		}
		return fmt.Sprintf("%d분 전", minutes)
	case d < 24*time.Hour:
		return fmt.Sprintf("%d시간 전", int(d.Hours()))
	case d < 48*time.Hour:
		return "어제"
	default:
		return fmt.Sprintf("%d일 전", int(d.Hours()/24))
	}
}

func splitCSV(raw string) []string {
	items := []string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}
