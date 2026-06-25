package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubRolesAreCustomerOnly(t *testing.T) {
	roles := rolesForGitHubUser("admin", "admin@example.com", "admin-login")
	if len(roles) != 1 || roles[0] != roleCustomer {
		t.Fatalf("GitHub roles = %#v, want customer only", roles)
	}
}

func TestSessionTokenHashIDDoesNotExposeToken(t *testing.T) {
	token := "plain-session-token"
	id := sessionTokenHashID(token)
	if id == token {
		t.Fatal("session token hash id must not equal the raw token")
	}
	if id != sessionTokenHashID(token) {
		t.Fatal("session token hash id must be deterministic")
	}
}

func TestInternalAccountSanitizeRemovesPasswordHash(t *testing.T) {
	account := internalAccount{
		ID:           "account-admin",
		LoginID:      "admin",
		PasswordHash: "hashed-password",
		Role:         roleAdmin,
		Status:       "active",
	}
	data := sanitizeInternalAccount(account)
	if _, ok := data["passwordHash"]; ok {
		t.Fatal("sanitized account leaked passwordHash")
	}
	if data["loginId"] != "admin" {
		t.Fatalf("loginId = %v, want admin", data["loginId"])
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("long-enough-password")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}
	if !checkPassword(hash, "long-enough-password") {
		t.Fatal("checkPassword rejected the original password")
	}
	if checkPassword(hash, "wrong-password") {
		t.Fatal("checkPassword accepted the wrong password")
	}
}

func TestNormalizeInternalRole(t *testing.T) {
	cases := map[string]string{
		"admin":       roleAdmin,
		"booth":       roleBooth,
		"seller":      roleBooth,
		"booth_owner": roleBooth,
		"":            roleBooth,
	}
	for input, want := range cases {
		if got := normalizeInternalRole(input); got != want {
			t.Fatalf("normalizeInternalRole(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBoothIDFromSellerPath(t *testing.T) {
	cases := map[string]string{
		"/api/seller/booths/booth-1":               "booth-1",
		"/api/seller/booths/booth-1/products":      "booth-1",
		"/api/seller/booths/booth-1/reports/sales": "booth-1",
		"/api/seller/products/product-1":           "",
		"/api/customer/booths/booth-1":             "",
		"/api/seller/booths":                       "",
		"/api/seller/booths/":                      "",
	}
	for input, want := range cases {
		if got := boothIDFromSellerPath(input); got != want {
			t.Fatalf("boothIDFromSellerPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSafeRedirectURLRejectsExternalOrigins(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://app.example.com")
	t.Setenv("AUTH_SUCCESS_REDIRECT_URL", "https://app.example.com/login")
	t.Setenv("CORS_ALLOW_ORIGINS", "https://app.example.com,https://admin.example.com")

	cases := map[string]string{
		"https://app.example.com/login?next=%2F": "https://app.example.com/login?next=%2F",
		"https://admin.example.com/console":      "https://admin.example.com/console",
		"/login":                                 "/login",
		"//evil.example/login":                   "https://app.example.com/login",
		"https://evil.example/login":             "https://app.example.com/login",
		"javascript:alert(1)":                    "https://app.example.com/login",
		"":                                       "https://app.example.com/login",
	}
	for input, want := range cases {
		if got := safeRedirectURL(input); got != want {
			t.Fatalf("safeRedirectURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCORSMiddlewareReflectsAllowedOriginsOnly(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "https://app.example.com,https://admin.example.com")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodOptions, "/api/customer/home", nil)
	allowedReq.Header.Set("Origin", "https://app.example.com")
	handler.ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d, want %d", allowed.Code, http.StatusNoContent)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allowed origin header = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q, want true", got)
	}

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodOptions, "/api/customer/home", nil)
	deniedReq.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied preflight status = %d, want %d", denied.Code, http.StatusForbidden)
	}
	if got := denied.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied origin header = %q, want empty", got)
	}
}

func TestCSRFMiddlewareBlocksUntrustedMutationOrigins(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "https://app.example.com")

	called := false
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/api/customer/orders", nil)
	deniedReq.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied mutation status = %d, want %d", denied.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("csrf middleware called next handler for denied origin")
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/api/customer/orders", nil)
	allowedReq.Header.Set("Origin", "https://app.example.com")
	handler.ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed mutation status = %d, want %d", allowed.Code, http.StatusOK)
	}
}

func TestCustomerAPIsRequireCustomerSession(t *testing.T) {
	s := &server{}
	handler := s.authzMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/customer/wallet/balances", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("customer API without session status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestSessionCookieIsAlwaysSecure(t *testing.T) {
	cookie := sessionCookie("token", time.Now(), 0)
	if !cookie.Secure {
		t.Fatal("session cookie must always use Secure")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie SameSite = %v, want %v", cookie.SameSite, http.SameSiteLaxMode)
	}
}

func TestRequestPayloadRejectsOversizedBodies(t *testing.T) {
	t.Setenv("HTTP_MAX_JSON_BODY_BYTES", "8")
	s := &server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/customer/orders", strings.NewReader(`{"name":"too-large"}`))

	if _, ok := s.requestPayload(recorder, request); ok {
		t.Fatal("oversized body was accepted")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestResourceOwnershipHelpers(t *testing.T) {
	item := map[string]any{"userId": "user-1", "boothId": "booth-1"}
	if !resourceBelongsToUser(item, "user-1") {
		t.Fatal("resourceBelongsToUser rejected matching user")
	}
	if resourceBelongsToUser(item, "user-2") {
		t.Fatal("resourceBelongsToUser accepted another user")
	}
	if !resourceBelongsToBooth(item, "booth-1") {
		t.Fatal("resourceBelongsToBooth rejected matching booth")
	}
	if resourceBelongsToBooth(item, "booth-2") {
		t.Fatal("resourceBelongsToBooth accepted another booth")
	}
}
