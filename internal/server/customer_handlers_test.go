package server

import "testing"

func TestFilterUserRankingsExcludesKangeunchanAndReassignsRanks(t *testing.T) {
	items := []map[string]any{
		{"githubLogin": "first", "rank": 1},
		{"githubLogin": " KangeunChan ", "rank": 2},
		{"githubLogin": "third", "rank": 3},
	}

	got := filterUserRankings(items, 20)
	if len(got) != 2 {
		t.Fatalf("filterUserRankings returned %d items, want 2", len(got))
	}
	if got[0]["githubLogin"] != "first" || got[0]["rank"] != 1 {
		t.Fatalf("first ranking = %#v, want first at rank 1", got[0])
	}
	if got[1]["githubLogin"] != "third" || got[1]["rank"] != 2 {
		t.Fatalf("second ranking = %#v, want third at rank 2", got[1])
	}
}

func TestFilterUserRankingsAppliesLimitAfterExclusion(t *testing.T) {
	items := []map[string]any{
		{"githubLogin": "kangeunchan"},
		{"githubLogin": "second"},
		{"githubLogin": "third"},
	}

	got := filterUserRankings(items, 2)
	if len(got) != 2 || got[1]["githubLogin"] != "third" {
		t.Fatalf("filterUserRankings = %#v, want two non-excluded rankings", got)
	}
}

func TestFilterUserRankingsExcludesInternalAccountIDs(t *testing.T) {
	items := []map[string]any{
		{"userId": "customer-1", "githubLogin": "first"},
		{"userId": "teacher-1", "githubLogin": "teacher"},
		{"userId": "booth-1", "githubLogin": "booth"},
		{"userId": "customer-2", "githubLogin": "second"},
	}

	got := filterUserRankingsByUserIDs(items, 20, map[string]bool{
		"teacher-1": true,
		"booth-1":   true,
	})
	if len(got) != 2 {
		t.Fatalf("filterUserRankingsByUserIDs returned %d items, want 2", len(got))
	}
	if got[0]["userId"] != "customer-1" || got[0]["rank"] != 1 {
		t.Fatalf("first ranking = %#v, want customer-1 at rank 1", got[0])
	}
	if got[1]["userId"] != "customer-2" || got[1]["rank"] != 2 {
		t.Fatalf("second ranking = %#v, want customer-2 at rank 2", got[1])
	}
}
