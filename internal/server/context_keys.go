package server

type contextKey string

const (
	requestIDContextKey   contextKey = "request_id"
	authSessionContextKey contextKey = "auth_session"
)
