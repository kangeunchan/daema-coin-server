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
