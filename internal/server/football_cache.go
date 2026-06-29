package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisFootballCache struct {
	client    *redis.Client
	prefix    string
	ttl       time.Duration
	detailTTL time.Duration
}

type footballCacheJobSummary struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Status                string    `json:"status"`
	RefreshedAt           time.Time `json:"refreshedAt,omitempty"`
	MatchCount            int       `json:"matchCount"`
	DetailCount           int       `json:"detailCount"`
	Error                 string    `json:"error,omitempty"`
	NextRunHint           string    `json:"nextRunHint,omitempty"`
	DetailWindow          string    `json:"detailWindow"`
	CacheTTLSeconds       int64     `json:"cacheTtlSeconds"`
	DetailCacheTTLSeconds int64     `json:"detailCacheTtlSeconds"`
	DetailRefreshInterval string    `json:"detailRefreshInterval"`
}

func openRedisFootballCache(ctx context.Context) (*redisFootballCache, error) {
	redisURL := env("REDIS_URL", "redis://localhost:6379/0")
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	options.DialTimeout = envDuration("REDIS_DIAL_TIMEOUT", 5*time.Second)
	options.ReadTimeout = envDuration("REDIS_READ_TIMEOUT", 3*time.Second)
	options.WriteTimeout = envDuration("REDIS_WRITE_TIMEOUT", 3*time.Second)
	client := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, envDuration("REDIS_PING_TIMEOUT", 5*time.Second))
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &redisFootballCache{
		client:    client,
		prefix:    strings.Trim(strings.TrimSpace(env("API_FOOTBALL_CACHE_PREFIX", "daema:api-football")), ":"),
		ttl:       envDuration("API_FOOTBALL_CACHE_TTL", 5*time.Minute),
		detailTTL: envDuration("API_FOOTBALL_DETAIL_CACHE_TTL", 30*time.Minute),
	}, nil
}

func (c *redisFootballCache) close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func (c *redisFootballCache) health(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("redis football cache is not configured")
	}
	return c.client.Ping(ctx).Err()
}

func (c *redisFootballCache) key(parts ...string) string {
	clean := []string{c.prefix}
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), ":")
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ":")
}

func (c *redisFootballCache) fixtures(ctx context.Context) ([]worldcupMatch, bool, error) {
	var out []worldcupMatch
	ok, err := c.getJSON(ctx, c.key("fixtures"), &out)
	return out, ok, err
}

func (c *redisFootballCache) fixture(ctx context.Context, id string) (worldcupMatch, bool, error) {
	var out worldcupMatch
	ok, err := c.getJSON(ctx, c.key("fixtures", id), &out)
	return out, ok, err
}

func (c *redisFootballCache) stats(ctx context.Context, id string) ([]map[string]any, bool, error) {
	var out []map[string]any
	ok, err := c.getJSON(ctx, c.key("stats", id), &out)
	return out, ok, err
}

func (c *redisFootballCache) lineups(ctx context.Context, id string) ([]map[string]any, bool, error) {
	var out []map[string]any
	ok, err := c.getJSON(ctx, c.key("lineups", id), &out)
	return out, ok, err
}

func (c *redisFootballCache) saveFixtures(ctx context.Context, matches []worldcupMatch) error {
	pipe := c.client.Pipeline()
	c.setJSONPipe(ctx, pipe, c.key("fixtures"), matches)
	for _, match := range matches {
		if match.ID == "" {
			continue
		}
		c.setJSONPipe(ctx, pipe, c.key("fixtures", match.ID), match)
		if match.ExternalID > 0 && strconv.Itoa(match.ExternalID) != match.ID {
			c.setJSONPipe(ctx, pipe, c.key("fixtures", strconv.Itoa(match.ExternalID)), match)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *redisFootballCache) saveStats(ctx context.Context, id string, stats []map[string]any) error {
	return c.setJSONTTL(ctx, c.key("stats", id), stats, c.detailTTL)
}

func (c *redisFootballCache) saveLineups(ctx context.Context, id string, lineups []map[string]any) error {
	return c.setJSONTTL(ctx, c.key("lineups", id), lineups, c.detailTTL)
}

func (c *redisFootballCache) detailRefreshDue(ctx context.Context, id string, interval time.Duration) (bool, error) {
	return c.client.SetNX(ctx, c.key("detail-refresh", id), time.Now().UTC().Format(time.RFC3339), interval).Result()
}

func (c *redisFootballCache) getJSON(ctx context.Context, key string, target any) (bool, error) {
	raw, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return false, err
	}
	return true, nil
}

func (c *redisFootballCache) setJSON(ctx context.Context, key string, value any) error {
	return c.setJSONTTL(ctx, key, value, c.ttl)
}

func (c *redisFootballCache) setJSONTTL(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, raw, ttl).Err()
}

func (c *redisFootballCache) setJSONPipe(ctx context.Context, pipe redis.Pipeliner, key string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	pipe.Set(ctx, key, raw, c.ttl)
}

func (s *server) runFootballCacheWorker(ctx context.Context, interval time.Duration) {
	slog.Info("starting api-football cache worker", "interval", interval.String())
	s.runFootballCacheRefreshCycle(ctx, interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runFootballCacheRefreshCycle(ctx, interval)
		}
	}
}

func (s *server) runFootballCacheRefreshCycle(ctx context.Context, interval time.Duration) {
	summary := footballCacheJobSummary{
		ID:                    "api-football-cache-refresh",
		Name:                  "API-FOOTBALL cache refresh",
		Status:                "running",
		NextRunHint:           time.Now().Add(interval).UTC().Format(time.RFC3339),
		DetailWindow:          footballDetailCacheWindowLabel(),
		CacheTTLSeconds:       int64(s.footballCache.ttl.Seconds()),
		DetailCacheTTLSeconds: int64(s.footballCache.detailTTL.Seconds()),
		DetailRefreshInterval: footballDetailRefreshInterval().String(),
	}

	matches, err := s.football.Fixtures(ctx)
	if err != nil {
		summary.Status = "failed"
		summary.Error = err.Error()
		s.storeFootballCacheJobSummary(ctx, summary)
		slog.Warn("api-football cache refresh failed to load fixtures", "error", err)
		return
	}
	if err := s.footballCache.saveFixtures(ctx, matches); err != nil {
		summary.Status = "failed"
		summary.Error = err.Error()
		s.storeFootballCacheJobSummary(ctx, summary)
		slog.Warn("api-football cache refresh failed to save fixtures", "error", err)
		return
	}

	detailCount := 0
	for _, match := range matches {
		if !shouldRefreshFootballDetails(match, time.Now().In(appLocation())) {
			continue
		}
		id := strconv.Itoa(match.ExternalID)
		if id == "0" {
			id = match.ID
		}
		if !isNumeric(id) {
			continue
		}
		due, err := s.footballCache.detailRefreshDue(ctx, id, footballDetailRefreshInterval())
		if err != nil {
			slog.Warn("check api-football detail refresh interval", "match_id", id, "error", err)
			continue
		}
		if !due {
			continue
		}
		if stats, err := s.football.Stats(ctx, id); err == nil && len(stats) > 0 {
			if err := s.footballCache.saveStats(ctx, id, stats); err != nil {
				slog.Warn("save api-football stats cache", "match_id", id, "error", err)
			}
		} else if err != nil && !errors.Is(err, errFootballNotConfigured) {
			slog.Debug("skip api-football stats cache", "match_id", id, "error", err)
		}
		if lineups, err := s.football.Lineups(ctx, id); err == nil && len(lineups) > 0 {
			if err := s.footballCache.saveLineups(ctx, id, lineups); err != nil {
				slog.Warn("save api-football lineups cache", "match_id", id, "error", err)
			}
		} else if err != nil && !errors.Is(err, errFootballNotConfigured) {
			slog.Debug("skip api-football lineups cache", "match_id", id, "error", err)
		}
		detailCount++
	}

	summary.Status = "ok"
	summary.RefreshedAt = time.Now().UTC()
	summary.MatchCount = len(matches)
	summary.DetailCount = detailCount
	s.storeFootballCacheJobSummary(ctx, summary)
}

func (s *server) storeFootballCacheJobSummary(ctx context.Context, summary footballCacheJobSummary) {
	if s.store == nil {
		return
	}
	data, err := mapFromStruct(summary)
	if err != nil {
		slog.Warn("encode api-football cache job summary", "error", err)
		return
	}
	if _, err := s.store.put(ctx, resourceSystemJobs, summary.ID, data); err != nil {
		slog.Warn("store api-football cache job summary", "error", err)
	}
}

func shouldRefreshFootballDetails(match worldcupMatch, now time.Time) bool {
	if match.Status == "live" {
		return true
	}
	start, err := time.Parse(time.RFC3339, match.StartsAt)
	if err != nil {
		return false
	}
	start = start.In(appLocation())
	before := envDuration("API_FOOTBALL_DETAIL_CACHE_BEFORE", 3*time.Hour)
	after := envDuration("API_FOOTBALL_DETAIL_CACHE_AFTER", 4*time.Hour)
	return !now.Before(start.Add(-before)) && !now.After(start.Add(after))
}

func footballDetailCacheWindowLabel() string {
	return fmt.Sprintf(
		"%s before / %s after",
		envDuration("API_FOOTBALL_DETAIL_CACHE_BEFORE", 3*time.Hour),
		envDuration("API_FOOTBALL_DETAIL_CACHE_AFTER", 4*time.Hour),
	)
}

func footballDetailRefreshInterval() time.Duration {
	return envDuration("API_FOOTBALL_DETAIL_CACHE_REFRESH_INTERVAL", 10*time.Minute)
}

func (c *footballClient) Configured() bool {
	return c != nil && c.apiKey != ""
}

func (c *footballClient) RequestURL(path string, values url.Values) string {
	u := c.baseURL + path
	if encoded := values.Encode(); encoded != "" {
		u += "?" + encoded
	}
	return u
}
