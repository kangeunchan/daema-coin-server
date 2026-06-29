package server

import (
	"encoding/json"
	"errors"
	"testing"
)

func testIntPointer(value int) *int {
	return &value
}

func testBoolPointer(value bool) *bool {
	return &value
}

func TestAPIFootballWinnerSurvivesMatchConversionAndCacheEncoding(t *testing.T) {
	var response apiFootballFixtureResponse
	err := json.Unmarshal([]byte(`{
		"response": [{
			"fixture": {"id": 978072, "date": "2022-12-09T18:00:00+00:00", "status": {"short": "PEN", "long": "Match Finished"}},
			"league": {"name": "World Cup", "round": "Quarter-finals"},
			"teams": {
				"home": {"id": 3, "name": "Croatia", "winner": true},
				"away": {"id": 6, "name": "Brazil", "winner": false}
			},
			"goals": {"home": 1, "away": 1}
		}]
	}`), &response)
	if err != nil {
		t.Fatalf("decode API-Football fixture: %v", err)
	}

	match := response.Response[0].toWorldcupMatch()
	raw, err := json.Marshal(match)
	if err != nil {
		t.Fatalf("encode cached match: %v", err)
	}
	var cached worldcupMatch
	if err := json.Unmarshal(raw, &cached); err != nil {
		t.Fatalf("decode cached match: %v", err)
	}

	pick, err := predictionWinningPickFromMatch(cached)
	if err != nil {
		t.Fatalf("predictionWinningPickFromMatch: %v", err)
	}
	if pick != "home" {
		t.Fatalf("winning pick = %q, want home", pick)
	}
}

func TestPredictionWinningPickFromMatch(t *testing.T) {
	tests := []struct {
		name    string
		match   worldcupMatch
		want    string
		wantErr error
	}{
		{
			name: "penalty shootout home winner overrides tied goals",
			match: worldcupMatch{
				Home: worldcupTeam{Score: testIntPointer(1), Winner: testBoolPointer(true)},
				Away: worldcupTeam{Score: testIntPointer(1), Winner: testBoolPointer(false)},
			},
			want: "home",
		},
		{
			name: "penalty shootout away winner overrides tied goals",
			match: worldcupMatch{
				Home: worldcupTeam{Score: testIntPointer(2), Winner: testBoolPointer(false)},
				Away: worldcupTeam{Score: testIntPointer(2), Winner: testBoolPointer(true)},
			},
			want: "away",
		},
		{
			name: "score decides when API winner is unavailable",
			match: worldcupMatch{
				Home: worldcupTeam{Score: testIntPointer(2)},
				Away: worldcupTeam{Score: testIntPointer(1)},
			},
			want: "home",
		},
		{
			name: "tied score remains draw without API winner",
			match: worldcupMatch{
				Home: worldcupTeam{Score: testIntPointer(0)},
				Away: worldcupTeam{Score: testIntPointer(0)},
			},
			want: "draw",
		},
		{
			name: "contradictory API winners are rejected",
			match: worldcupMatch{
				Home: worldcupTeam{Score: testIntPointer(1), Winner: testBoolPointer(true)},
				Away: worldcupTeam{Score: testIntPointer(1), Winner: testBoolPointer(true)},
			},
			wantErr: errPredictionResultUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := predictionWinningPickFromMatch(tt.match)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("winning pick = %q, want %q", got, tt.want)
			}
		})
	}
}
