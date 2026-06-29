package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

type apiResponse struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

type apiError struct {
	Error errorBody    `json:"error"`
	Meta  responseMeta `json:"meta"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type responseMeta struct {
	RequestID  string      `json:"requestId"`
	ServerTime string      `json:"serverTime"`
	Pagination *pagination `json:"pagination,omitempty"`
}

type pagination struct {
	Cursor     string `json:"cursor,omitempty"`
	NextCursor string `json:"nextCursor,omitempty"`
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"hasMore"`
}

func (s *server) ok(w http.ResponseWriter, r *http.Request, data any) {
	writeJSON(w, r, http.StatusOK, apiResponse{Data: data, Meta: meta(r, nil)})
}

func (s *server) created(w http.ResponseWriter, r *http.Request, data any) {
	writeJSON(w, r, http.StatusCreated, apiResponse{Data: data, Meta: meta(r, nil)})
}

func (s *server) okPage(w http.ResponseWriter, r *http.Request, data any, p *pagination) {
	writeJSON(w, r, http.StatusOK, apiResponse{Data: data, Meta: meta(r, p)})
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	attrs := []any{
		"request_id", requestID(r),
		"status", status,
		"code", code,
		"message", message,
		"details", sanitizeLogValue(details),
		"method", r.Method,
		"path", r.URL.Path,
	}
	if status >= http.StatusInternalServerError {
		slog.Error("api request failed", attrs...)
	} else {
		slog.Warn("api request failed", attrs...)
	}
	writeJSON(w, r, status, apiError{Error: errorBody{Code: code, Message: message, Details: details}, Meta: meta(r, nil)})
}

func (s *server) failFootball(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errFootballNotConfigured) {
		s.fail(w, r, http.StatusServiceUnavailable, "API_FOOTBALL_NOT_CONFIGURED", "API-FOOTBALL 설정이 없어 경기 데이터를 가져올 수 없습니다.", map[string]any{"required": []string{"API_FOOTBALL_KEY"}})
		return
	}
	s.fail(w, r, http.StatusBadGateway, "API_FOOTBALL_UNAVAILABLE", "API-FOOTBALL에서 경기 데이터를 가져오지 못했습니다.", map[string]any{"cause": err.Error()})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "request_id", requestID(r), "error", err)
	}
}

func meta(r *http.Request, p *pagination) responseMeta {
	return responseMeta{RequestID: requestID(r), ServerTime: time.Now().UTC().Format(time.RFC3339), Pagination: p}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, map[string]any{"status": "ok", "service": "daema-coin-server"})
}
