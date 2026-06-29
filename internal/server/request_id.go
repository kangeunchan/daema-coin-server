package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func requestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDContextKey).(string); ok && id != "" {
		return id
	}
	if id := cleanRequestID(r.Header.Get("X-Request-Id")); id != "" {
		return id
	}
	return newRequestID()
}

func cleanRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	const maxRequestIDLength = 128
	var b strings.Builder
	b.Grow(min(len(value), maxRequestIDLength))
	for _, r := range value {
		if b.Len() >= maxRequestIDLength {
			break
		}
		if r < 33 || r > 126 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func requestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDContextKey).(string); ok {
		return id
	}
	return ""
}
