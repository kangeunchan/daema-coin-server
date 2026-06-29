package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

func configureLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(env("LOG_LEVEL", "info"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{
		Level:     level,
		AddSource: envBool("LOG_ADD_SOURCE", false),
	}
	if strings.EqualFold(env("LOG_FORMAT", "json"), "text") {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, options)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, options)))
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := cleanRequestID(r.Header.Get("X-Request-Id"))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey, id)))
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		slog.Debug("http request started", requestLogAttrs(r)...)

		defer func() {
			if recovered := recover(); recovered != nil {
				attrs := append(requestLogAttrs(r), "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				slog.Error("http request panic", attrs...)
				if !recorder.wroteHeader {
					writeJSON(recorder, r, http.StatusInternalServerError, apiError{
						Error: errorBody{Code: "INTERNAL_SERVER_ERROR", Message: "서버 내부 오류가 발생했습니다."},
						Meta:  meta(r, nil),
					})
				}
			}

			attrs := append(requestLogAttrs(r),
				"status", recorder.status,
				"response_bytes", recorder.bytes,
				"duration", time.Since(start).String(),
				"duration_ms", float64(time.Since(start).Microseconds())/1000,
			)
			switch {
			case recorder.status >= 500:
				slog.Error("http request completed", attrs...)
			case recorder.status >= 400:
				slog.Warn("http request completed", attrs...)
			default:
				slog.Info("http request completed", attrs...)
			}
		}()

		next.ServeHTTP(recorder, r)
	})
}

func requestLogAttrs(r *http.Request) []any {
	attrs := []any{
		"request_id", requestID(r),
		"method", r.Method,
		"path", r.URL.Path,
		"query", redactedQuery(r.URL.Query()),
		"host", r.Host,
		"proto", r.Proto,
		"remote_addr", r.RemoteAddr,
		"client_ip", clientIP(r),
		"user_agent", r.UserAgent(),
		"referer", r.Referer(),
		"origin", r.Header.Get("Origin"),
		"content_length", r.ContentLength,
		"headers", safeHeaders(r.Header),
	}
	if r.Pattern != "" {
		attrs = append(attrs, "route", r.Pattern)
	}
	if session, ok := authSessionFromContext(r.Context()); ok {
		attrs = append(attrs,
			"user_id", session.User.ID,
			"github_login", session.User.Login,
			"role", session.Role,
			"roles", session.User.Roles,
			"booth_id", session.User.BoothID,
		)
	}
	return attrs
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func safeHeaders(headers http.Header) map[string]any {
	out := make(map[string]any, len(headers))
	for key, values := range headers {
		if sensitiveLogKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = sanitizeLogValue(values)
	}
	return out
}

func redactedQuery(values url.Values) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if sensitiveLogKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = sanitizeLogValue(value)
	}
	return out
}

func sanitizeLogValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if sensitiveLogKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = sanitizeLogValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeLogValue(item))
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeLogValue(item))
		}
		return out
	case string:
		return truncateLogString(v)
	default:
		return v
	}
}

func truncateLogString(value string) string {
	const max = 512
	if len(value) <= max {
		return value
	}
	return value[:max] + "...[TRUNCATED]"
}

func sensitiveLogKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"authorization", "cookie", "password", "passwd", "secret", "token", "csrf", "code", "state", "credential", "session"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}
