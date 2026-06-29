package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type worldcupTeam struct {
	ID          string `json:"id"`
	CountryCode string `json:"countryCode"`
	Name        string `json:"name"`
	Logo        string `json:"logo,omitempty"`
	Score       *int   `json:"score,omitempty"`
	Winner      *bool  `json:"winner,omitempty"`
}

type worldcupMatch struct {
	ID          string       `json:"id"`
	MatchDayID  string       `json:"matchDayId"`
	Home        worldcupTeam `json:"home"`
	Away        worldcupTeam `json:"away"`
	Status      string       `json:"status"`
	StatusLabel string       `json:"statusLabel"`
	Subtitle    string       `json:"subtitle"`
	StartsAt    string       `json:"startsAt,omitempty"`
	DisplayTime string       `json:"displayTime,omitempty"`
	ExternalID  int          `json:"externalId,omitempty"`
}

func (s *server) handleWorldcupMatchDays(w http.ResponseWriter, r *http.Request) {
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.ok(w, r, groupMatchDays(matches))
}

func (s *server) handleWorldcupMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.ok(w, r, matches)
}

func (s *server) handleWorldcupMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("matchId")
	match, ok, err := s.worldcupMatchByID(r.Context(), id)
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	if ok {
		s.ok(w, r, match)
		return
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_NOT_FOUND", "경기를 찾을 수 없습니다.", nil)
}

func (s *server) worldcupMatches(ctx context.Context) ([]worldcupMatch, error) {
	matches, ok, err := s.footballCache.fixtures(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || len(matches) == 0 {
		return nil, errors.New("API-FOOTBALL 경기 캐시가 비어 있습니다")
	}
	return matches, nil
}

func (s *server) worldcupMatchByID(ctx context.Context, id string) (worldcupMatch, bool, error) {
	if isNumeric(id) {
		if match, ok, err := s.footballCache.fixture(ctx, id); err != nil || ok {
			return match, ok, err
		}
	}
	matches, err := s.worldcupMatches(ctx)
	if err != nil {
		return worldcupMatch{}, false, err
	}
	for _, m := range matches {
		if m.ID == id || strconv.Itoa(m.ExternalID) == id {
			return m, true, nil
		}
	}
	return worldcupMatch{}, false, nil
}

func groupMatchDays(matches []worldcupMatch) []map[string]any {
	byDay := map[string][]worldcupMatch{}
	order := []string{}
	for _, m := range matches {
		key := m.MatchDayID
		if key == "" {
			key = "unknown"
		}
		if _, ok := byDay[key]; !ok {
			order = append(order, key)
		}
		byDay[key] = append(byDay[key], m)
	}
	todayKey := time.Now().In(appLocation()).Format("2006-01-02")
	activeKey := ""
	for _, key := range order {
		if key == todayKey {
			activeKey = key
			break
		}
	}
	if activeKey == "" {
		for _, key := range order {
			if key >= todayKey {
				activeKey = key
				break
			}
		}
	}
	if activeKey == "" && len(order) > 0 {
		activeKey = order[len(order)-1]
	}
	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		items := byDay[key]
		badge := ""
		for _, m := range items {
			if m.Home.CountryCode == "KR" || m.Away.CountryCode == "KR" {
				badge = "대한민국"
			}
		}
		label := weekdayLabel(items[0].StartsAt)
		dateLabel := dateDot(items[0].StartsAt, key)
		if key == todayKey {
			label = "오늘"
		}
		out = append(out, map[string]any{"id": key, "date": dateLabel, "label": label, "badge": badge, "isActive": key == activeKey, "matches": items})
	}
	return out
}

func (s *server) handleWorldcupStats(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	if stats, ok, err := s.footballCache.stats(r.Context(), matchID); err == nil && ok && len(stats) > 0 {
		s.ok(w, r, stats)
		return
	} else if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_STATS_NOT_FOUND", "경기 지표를 찾을 수 없습니다.", map[string]any{"matchId": matchID})
}

func (s *server) handleWorldcupLineups(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	if lineups, ok, err := s.footballCache.lineups(r.Context(), matchID); err == nil && ok && len(lineups) > 0 {
		s.ok(w, r, lineups)
		return
	} else if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_LINEUPS_NOT_FOUND", "경기 라인업을 찾을 수 없습니다.", map[string]any{"matchId": matchID})
}
