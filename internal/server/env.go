package server

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func env(key, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}

func envBool(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envDuration(key string, defaultValue time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return defaultValue
	}
	return duration
}

func envInt64(key string, defaultValue int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func envInt(key string, defaultValue int) int {
	parsed := envInt64(key, int64(defaultValue))
	if parsed > int64(^uint(0)>>1) {
		return defaultValue
	}
	return int(parsed)
}

func maxJSONBodyBytes() int64 {
	return envInt64("HTTP_MAX_JSON_BODY_BYTES", 1<<20)
}
