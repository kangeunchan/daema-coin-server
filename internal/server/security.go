package server

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			if isAllowedOrigin(origin) {
				w.Header().Set("Access-Control-Allow-Origin", normalizeOrigin(origin))
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id, X-CSRF-Token")
				w.Header().Set("Access-Control-Max-Age", "600")
			} else if r.Method == http.MethodOptions {
				writeJSON(w, r, http.StatusForbidden, apiError{
					Error: errorBody{Code: "CORS_ORIGIN_DENIED", Message: "허용되지 않은 origin입니다."},
					Meta:  meta(r, nil),
				})
				return
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || csrfExemptPath(r.URL.Path) || bearerToken(r) != "" || requestOriginAllowed(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, r, http.StatusForbidden, apiError{
			Error: errorBody{Code: "CSRF_ORIGIN_DENIED", Message: "허용되지 않은 origin의 변경 요청입니다."},
			Meta:  meta(r, nil),
		})
	})
}

func csrfExemptPath(path string) bool {
	switch path {
	case "/api/github/webhooks":
		return true
	default:
		return false
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func configuredCORSOrigins() map[string]bool {
	raw := firstNonEmpty(os.Getenv("CORS_ALLOW_ORIGINS"), os.Getenv("CORS_ALLOW_ORIGIN"))
	if raw == "" {
		raw = env("PUBLIC_BASE_URL", "http://localhost:5173")
	}
	return originSet(raw)
}

func redirectAllowedOrigins() map[string]bool {
	values := []string{
		env("PUBLIC_BASE_URL", "http://localhost:5173"),
		env("AUTH_SUCCESS_REDIRECT_URL", env("PUBLIC_BASE_URL", "http://localhost:5173")+"/login"),
		os.Getenv("CORS_ALLOW_ORIGINS"),
		os.Getenv("CORS_ALLOW_ORIGIN"),
	}
	return originSet(strings.Join(values, ","))
}

func originSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		if value == "*" {
			continue
		}
		if origin := normalizeOrigin(value); origin != "" {
			out[origin] = true
		}
	}
	return out
}

func normalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

func isAllowedOrigin(origin string) bool {
	normalized := normalizeOrigin(origin)
	return normalized != "" && configuredCORSOrigins()[normalized]
}

func requestOriginAllowed(r *http.Request) bool {
	origin := normalizeOrigin(r.Header.Get("Origin"))
	if origin == "" {
		origin = normalizeOrigin(r.Header.Get("Referer"))
	}
	if origin == "" {
		return envBool("CSRF_ALLOW_MISSING_ORIGIN", false)
	}
	return configuredCORSOrigins()[origin]
}

func safeRedirectURL(raw string) string {
	fallback := env("AUTH_SUCCESS_REDIRECT_URL", env("PUBLIC_BASE_URL", "http://localhost:5173")+"/login")
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\") {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fallback
	}
	if redirectAllowedOrigins()[normalizeOrigin(value)] {
		return value
	}
	return fallback
}
