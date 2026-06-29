package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type footballClient struct {
	baseURL  string
	apiKey   string
	league   string
	season   string
	from     string
	to       string
	timezone string
	http     *http.Client
}

var errFootballNotConfigured = errors.New("api-football is not configured")

func newFootballClientFromEnv() *footballClient {
	return &footballClient{
		baseURL:  strings.TrimRight(env("API_FOOTBALL_BASE_URL", "https://v3.football.api-sports.io"), "/"),
		apiKey:   env("API_FOOTBALL_KEY", ""),
		league:   env("API_FOOTBALL_WORLDCUP_LEAGUE", "1"),
		season:   env("API_FOOTBALL_WORLDCUP_SEASON", "2026"),
		from:     env("API_FOOTBALL_WORLDCUP_FROM", "2026-06-11"),
		to:       env("API_FOOTBALL_WORLDCUP_TO", "2026-07-19"),
		timezone: env("API_FOOTBALL_TIMEZONE", "Asia/Seoul"),
		http:     &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *footballClient) Fixtures(ctx context.Context) ([]worldcupMatch, error) {
	if c.apiKey == "" {
		return nil, errFootballNotConfigured
	}
	values := url.Values{"league": {c.league}, "season": {c.season}, "from": {c.from}, "to": {c.to}, "timezone": {c.timezone}}
	var res apiFootballFixtureResponse
	if err := c.get(ctx, "/fixtures", values, &res); err != nil {
		return nil, err
	}
	out := make([]worldcupMatch, 0, len(res.Response))
	for _, item := range res.Response {
		out = append(out, item.toWorldcupMatch())
	}
	return out, nil
}

func (c *footballClient) Fixture(ctx context.Context, id string) (worldcupMatch, error) {
	if c.apiKey == "" || !isNumeric(id) {
		return worldcupMatch{}, errFootballNotConfigured
	}
	var res apiFootballFixtureResponse
	if err := c.get(ctx, "/fixtures", url.Values{"id": {id}, "timezone": {c.timezone}}, &res); err != nil {
		return worldcupMatch{}, err
	}
	if len(res.Response) == 0 {
		return worldcupMatch{}, errors.New("fixture not found")
	}
	return res.Response[0].toWorldcupMatch(), nil
}

func (c *footballClient) Stats(ctx context.Context, id string) ([]map[string]any, error) {
	if c.apiKey == "" || !isNumeric(id) {
		return nil, errFootballNotConfigured
	}
	var res apiFootballStatisticsResponse
	if err := c.get(ctx, "/fixtures/statistics", url.Values{"fixture": {id}}, &res); err != nil {
		return nil, err
	}
	if len(res.Response) < 2 {
		return nil, errors.New("statistics not available")
	}
	home := statsMap(res.Response[0].Statistics)
	away := statsMap(res.Response[1].Statistics)
	keys := []struct{ api, key, label string }{
		{"Ball Possession", "possession", "볼점유율"}, {"Total Shots", "shots", "슈팅"}, {"Shots on Goal", "shots-on-goal", "유효슈팅"},
		{"Total passes", "passes-total", "패스시도"}, {"Passes accurate", "passes-accurate", "패스성공"}, {"Corner Kicks", "corner-kicks", "코너킥"},
		{"Offsides", "offsides", "오프사이드"}, {"Goalkeeper Saves", "goalkeeper-saves", "선방"}, {"Fouls", "fouls", "파울"},
		{"Yellow Cards", "yellow-cards", "경고"}, {"Red Cards", "red-cards", "퇴장"},
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		h, hd := statValue(home[k.api])
		a, ad := statValue(away[k.api])
		out = append(out, map[string]any{"key": k.key, "label": k.label, "home": h, "away": a, "homeDisplay": hd, "awayDisplay": ad})
	}
	return out, nil
}

func (c *footballClient) Lineups(ctx context.Context, id string) ([]map[string]any, error) {
	if c.apiKey == "" || !isNumeric(id) {
		return nil, errFootballNotConfigured
	}
	var res apiFootballLineupResponse
	if err := c.get(ctx, "/fixtures/lineups", url.Values{"fixture": {id}}, &res); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Response))
	for _, lineup := range res.Response {
		players := []map[string]any{}
		for _, p := range lineup.StartXI {
			players = append(players, map[string]any{"id": strconv.Itoa(p.Player.ID), "name": p.Player.Name, "number": p.Player.Number, "position": p.Player.Pos})
		}
		out = append(out, map[string]any{"teamId": strconv.Itoa(lineup.Team.ID), "coach": lineup.Coach.Name, "formation": lineup.Formation, "players": players})
	}
	return out, nil
}

func (c *footballClient) get(ctx context.Context, path string, values url.Values, target any) error {
	u := c.baseURL + path
	if encoded := values.Encode(); encoded != "" {
		u += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-apisports-key", c.apiKey)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("api-football request failed",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"path", path,
			"query", redactedQuery(values),
			"duration", time.Since(start).String(),
			"error", err,
		)
		return err
	}
	defer resp.Body.Close()
	slog.Debug("api-football request completed",
		"request_id", requestIDFromContext(ctx),
		"method", req.Method,
		"path", path,
		"query", redactedQuery(values),
		"status", resp.StatusCode,
		"duration", time.Since(start).String(),
	)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("api-football request returned non-success status",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"path", path,
			"query", redactedQuery(values),
			"status", resp.StatusCode,
			"duration", time.Since(start).String(),
			"body", truncateLogString(strings.TrimSpace(string(body))),
		)
		return fmt.Errorf("api-football status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		slog.Warn("api-football response decode failed",
			"request_id", requestIDFromContext(ctx),
			"method", req.Method,
			"path", path,
			"query", redactedQuery(values),
			"status", resp.StatusCode,
			"duration", time.Since(start).String(),
			"error", err,
		)
		return err
	}
	return nil
}

type apiFootballFixtureResponse struct {
	Response []apiFootballFixture `json:"response"`
}

type apiFootballFixture struct {
	Fixture struct {
		ID     int    `json:"id"`
		Date   string `json:"date"`
		Status struct {
			Short string `json:"short"`
			Long  string `json:"long"`
		} `json:"status"`
	} `json:"fixture"`
	League struct {
		Name  string `json:"name"`
		Round string `json:"round"`
	} `json:"league"`
	Teams struct {
		Home apiFootballTeam `json:"home"`
		Away apiFootballTeam `json:"away"`
	} `json:"teams"`
	Goals struct {
		Home *int `json:"home"`
		Away *int `json:"away"`
	} `json:"goals"`
}

type apiFootballTeam struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Logo    string `json:"logo"`
	Winner  *bool  `json:"winner"`
	Country string `json:"country"`
}

func (f apiFootballFixture) toWorldcupMatch() worldcupMatch {
	start, _ := time.Parse(time.RFC3339, f.Fixture.Date)
	startLocal := start.In(appLocation())
	status := normalizeFixtureStatus(f.Fixture.Status.Short)
	subtitle := envDefault(f.League.Round, f.League.Name)
	id := strconv.Itoa(f.Fixture.ID)
	if f.Fixture.ID == 0 {
		id = makeMatchID(startLocal, f.Teams.Home.Name, f.Teams.Away.Name)
	}
	return worldcupMatch{
		ID: id, MatchDayID: startLocal.Format("2006-01-02"),
		Home:   worldcupTeam{ID: strconv.Itoa(f.Teams.Home.ID), CountryCode: countryCode(f.Teams.Home.Name), Name: koreanTeamName(f.Teams.Home.Name), Logo: f.Teams.Home.Logo, Score: f.Goals.Home, Winner: f.Teams.Home.Winner},
		Away:   worldcupTeam{ID: strconv.Itoa(f.Teams.Away.ID), CountryCode: countryCode(f.Teams.Away.Name), Name: koreanTeamName(f.Teams.Away.Name), Logo: f.Teams.Away.Logo, Score: f.Goals.Away, Winner: f.Teams.Away.Winner},
		Status: status, StatusLabel: statusLabel(status), Subtitle: subtitle, StartsAt: startLocal.Format(time.RFC3339), DisplayTime: startLocal.Format("15:04"), ExternalID: f.Fixture.ID,
	}
}

type apiFootballStatisticsResponse struct {
	Response []struct {
		Team       apiFootballTeam `json:"team"`
		Statistics []struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"statistics"`
	} `json:"response"`
}

type apiFootballLineupResponse struct {
	Response []struct {
		Team struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Logo string `json:"logo"`
		} `json:"team"`
		Coach struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"coach"`
		Formation string `json:"formation"`
		StartXI   []struct {
			Player struct {
				ID     int    `json:"id"`
				Name   string `json:"name"`
				Number int    `json:"number"`
				Pos    string `json:"pos"`
			} `json:"player"`
		} `json:"startXI"`
	} `json:"response"`
}

func statsMap(items []struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}) map[string]any {
	m := map[string]any{}
	for _, item := range items {
		m[item.Type] = item.Value
	}
	return m
}

func statValue(v any) (float64, string) {
	switch x := v.(type) {
	case string:
		if strings.HasSuffix(x, "%") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(x, "%"), 64)
			return n, x
		}
		n, _ := strconv.ParseFloat(x, 64)
		return n, x
	case float64:
		return x, trimFloat(x)
	case nil:
		return 0, "0"
	default:
		return 0, fmt.Sprint(x)
	}
}

func normalizeFixtureStatus(short string) string {
	switch short {
	case "1H", "HT", "2H", "ET", "P", "BT", "INT", "LIVE":
		return "live"
	case "FT", "AET", "PEN":
		return "finished"
	default:
		return "scheduled"
	}
}

func statusLabel(status string) string {
	switch status {
	case "live":
		return "진행중"
	case "finished":
		return "종료"
	default:
		return "예정"
	}
}

func countryCode(name string) string {
	table := map[string]string{"Argentina": "AR", "Austria": "AT", "Algeria": "DZ", "France": "FR", "Iraq": "IQ", "Jordan": "JO", "South Korea": "KR", "Korea Republic": "KR", "Mexico": "MX", "Norway": "NO", "Senegal": "SN"}
	if code := table[name]; code != "" {
		return code
	}
	return strings.ToUpper(firstLetters(name, 2))
}

func koreanTeamName(name string) string {
	table := map[string]string{"Argentina": "아르헨티나", "Austria": "오스트리아", "Algeria": "알제리", "France": "프랑스", "Iraq": "이라크", "Jordan": "요르단", "South Korea": "대한민국", "Korea Republic": "대한민국", "Mexico": "멕시코", "Norway": "노르웨이", "Senegal": "세네갈"}
	if value := table[name]; value != "" {
		return value
	}
	return name
}

func makeMatchID(t time.Time, home, away string) string {
	return strings.ToLower(fmt.Sprintf("%s-%s-%s", t.Format("102"), firstLetters(home, 2), firstLetters(away, 2)))
}

func firstLetters(s string, n int) string {
	parts := strings.Fields(s)
	out := ""
	for _, p := range parts {
		out += p[:1]
	}
	if len(out) >= n {
		return out[:n]
	}
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < n {
		return s
	}
	return s[:n]
}

func weekdayLabel(startsAt string) string {
	t, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return ""
	}
	t = t.In(appLocation())
	return []string{"일", "월", "화", "수", "목", "금", "토"}[int(t.Weekday())]
}

func dateDot(startsAt, defaultValue string) string {
	t, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return defaultValue
	}
	t = t.In(appLocation())
	return fmt.Sprintf("%d.%d", int(t.Month()), t.Day())
}

func parseDate(value string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", value, appLocation())
	if err != nil {
		return time.Now().In(appLocation())
	}
	return t
}

func queryInt(r *http.Request, key string, defaultValue int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value <= 0 {
		return boundedQueryLimit(defaultValue)
	}
	return boundedQueryLimit(value)
}

func queryIntValue(raw string, defaultValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultValue
	}
	return value
}

func boundedQueryLimit(value int) int {
	const maxQueryLimit = 1000
	if value <= 0 {
		return 1
	}
	if value > maxQueryLimit {
		return maxQueryLimit
	}
	return value
}

func envDefault(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" || value == "<nil>" {
		return defaultValue
	}
	return value
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func isNumeric(value string) bool {
	_, err := strconv.Atoi(value)
	return err == nil
}

func trimFloat(v float64) string {
	if math.Trunc(v) == v {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
