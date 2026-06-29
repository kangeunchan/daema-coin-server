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

func TestCSRFMiddlewareBlocksMissingMutationOriginByDefault(t *testing.T) {
	handler := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/api/customer/orders", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing origin mutation status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	bearer := httptest.NewRecorder()
	bearerReq := httptest.NewRequest(http.MethodPost, "/api/customer/orders", nil)
	bearerReq.Header.Set("Authorization", "Bearer token")
	handler.ServeHTTP(bearer, bearerReq)
	if bearer.Code != http.StatusOK {
		t.Fatalf("bearer mutation without origin status = %d, want %d", bearer.Code, http.StatusOK)
	}

	webhook := httptest.NewRecorder()
	handler.ServeHTTP(webhook, httptest.NewRequest(http.MethodPost, "/api/github/webhooks", nil))
	if webhook.Code != http.StatusOK {
		t.Fatalf("github webhook without origin status = %d, want %d", webhook.Code, http.StatusOK)
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

func TestFileUploadRequiresPrivilegedSession(t *testing.T) {
	s := &server{}
	handler := s.authzMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, "/api/files/uploads", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("file upload without session status = %d, want %d", anonymous.Code, http.StatusUnauthorized)
	}

	customer := httptest.NewRecorder()
	customerReq := httptest.NewRequest(http.MethodPost, "/api/files/uploads", nil)
	customerReq = customerReq.WithContext(contextWithAuthSession(customerReq.Context(), authSession{User: authUser{ID: "customer-1", Roles: []string{roleCustomer}}}))
	handler.ServeHTTP(customer, customerReq)
	if customer.Code != http.StatusForbidden {
		t.Fatalf("file upload with customer session status = %d, want %d", customer.Code, http.StatusForbidden)
	}

	for _, role := range []string{roleAdmin, roleBooth} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/files/uploads", nil)
		request = request.WithContext(contextWithAuthSession(request.Context(), authSession{User: authUser{ID: role + "-1", Roles: []string{role}}}))
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("file upload with %s session status = %d, want %d", role, recorder.Code, http.StatusNoContent)
		}
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

func TestDecodeStrictJSONRejectsUnknownFields(t *testing.T) {
	s := &server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/accounts", strings.NewReader(`{"loginId":"admin","password":"long-enough-password","unexpected":true}`))

	var body adminAccountCreateRequest
	if s.decodeStrictJSON(recorder, request, &body) {
		t.Fatal("decodeStrictJSON accepted unknown field")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestQueryIntUsesPositiveBoundedLimit(t *testing.T) {
	tooLarge := httptest.NewRequest(http.MethodGet, "/api/customer/orders?limit=999999", nil)
	if got := queryInt(tooLarge, "limit", 20); got != 1000 {
		t.Fatalf("large limit = %d, want 1000", got)
	}

	negative := httptest.NewRequest(http.MethodGet, "/api/customer/orders?limit=-1", nil)
	if got := queryInt(negative, "limit", 20); got != 20 {
		t.Fatalf("negative limit = %d, want default 20", got)
	}

	missing := httptest.NewRequest(http.MethodGet, "/api/customer/orders", nil)
	if got := queryInt(missing, "limit", 2000); got != 1000 {
		t.Fatalf("large default limit = %d, want bounded 1000", got)
	}
}

func TestRequestIDMiddlewareKeepsStableRequestID(t *testing.T) {
	var first string
	var second string
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first = requestID(r)
		second = requestID(r)
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(recorder, request)

	if first == "" {
		t.Fatal("request id was empty")
	}
	if first != second {
		t.Fatalf("request id changed within one request: %q != %q", first, second)
	}
	if got := recorder.Header().Get("X-Request-Id"); got != first {
		t.Fatalf("response request id = %q, want %q", got, first)
	}
}

func TestRequestIDMiddlewarePreservesIncomingRequestID(t *testing.T) {
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestID(r); got != "client-request-id" {
			t.Fatalf("request id = %q, want incoming id", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-Id", "client-request-id")
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Request-Id"); got != "client-request-id" {
		t.Fatalf("response request id = %q, want incoming id", got)
	}
}

func TestRequestIDMiddlewareSanitizesIncomingRequestID(t *testing.T) {
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestID(r); got != "badid" {
			t.Fatalf("request id = %q, want sanitized id", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-Id", "bad\nid")
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Request-Id"); got != "badid" {
		t.Fatalf("response request id = %q, want sanitized id", got)
	}
}

func TestSanitizeLogValueRedactsSensitiveFields(t *testing.T) {
	value := sanitizeLogValue(map[string]any{
		"loginId":  "admin",
		"password": "secret-password",
		"nested": map[string]any{
			"accessToken": "secret-token",
			"safe":        "visible",
		},
	}).(map[string]any)

	if value["loginId"] != "admin" {
		t.Fatalf("loginId = %v, want admin", value["loginId"])
	}
	if value["password"] != "[REDACTED]" {
		t.Fatalf("password was not redacted: %v", value["password"])
	}
	nested := value["nested"].(map[string]any)
	if nested["accessToken"] != "[REDACTED]" {
		t.Fatalf("nested accessToken was not redacted: %v", nested["accessToken"])
	}
	if nested["safe"] != "visible" {
		t.Fatalf("nested safe = %v, want visible", nested["safe"])
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

func TestRouteRoleWrappersEnforcePolicy(t *testing.T) {
	s := &server{}
	called := false
	handler := s.requireRouteRole(roleCustomer, func(w http.ResponseWriter, r *http.Request) {
		called = true
		if session, ok := authSessionFromContext(r.Context()); !ok || session.User.ID != "customer-1" {
			t.Fatalf("route context session = %#v, %v; want customer session", session, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/customer/me", nil)
	request = request.WithContext(contextWithAuthSession(request.Context(), authSession{
		User: authUser{ID: "customer-1", Roles: []string{roleCustomer}},
		Role: roleCustomer,
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if !called {
		t.Fatal("customer route handler was not called")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("customer route status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRouteRoleWrappersRejectWrongRole(t *testing.T) {
	s := &server{}
	handler := s.requireRouteRole(roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("admin route handler should not be called for customer role")
	})

	request := httptest.NewRequest(http.MethodGet, "/api/admin/dashboard", nil)
	request = request.WithContext(contextWithAuthSession(request.Context(), authSession{
		User: authUser{ID: "customer-1", Roles: []string{roleCustomer}},
		Role: roleCustomer,
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong role status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestSellerRouteWrapperEnforcesBoothScope(t *testing.T) {
	s := &server{}
	handler := s.requireSellerRoute(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("seller route handler should not be called for another booth")
	})

	request := httptest.NewRequest(http.MethodGet, "/api/seller/booths/booth-2/orders", nil)
	request = request.WithContext(contextWithAuthSession(request.Context(), authSession{
		User: authUser{ID: "seller-1", BoothID: "booth-1", Roles: []string{roleBooth}},
		Role: roleBooth,
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("booth scope status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestShouldRefreshFootballDetails(t *testing.T) {
	t.Setenv("APP_TIMEZONE", "Asia/Seoul")
	t.Setenv("API_FOOTBALL_DETAIL_CACHE_BEFORE", "1h")
	t.Setenv("API_FOOTBALL_DETAIL_CACHE_AFTER", "2h")

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, appLocation())
	matchAt := func(status string, startsAt time.Time) worldcupMatch {
		return worldcupMatch{Status: status, StartsAt: startsAt.Format(time.RFC3339)}
	}

	cases := []struct {
		name  string
		match worldcupMatch
		want  bool
	}{
		{name: "live always refreshes", match: matchAt("live", now.Add(-24*time.Hour)), want: true},
		{name: "scheduled inside before window", match: matchAt("scheduled", now.Add(30*time.Minute)), want: true},
		{name: "scheduled outside before window", match: matchAt("scheduled", now.Add(2*time.Hour)), want: false},
		{name: "finished inside after window", match: matchAt("finished", now.Add(-90*time.Minute)), want: true},
		{name: "finished outside after window", match: matchAt("finished", now.Add(-3*time.Hour)), want: false},
		{name: "invalid start time", match: worldcupMatch{Status: "scheduled", StartsAt: "bad"}, want: false},
	}
	for _, tc := range cases {
		if got := shouldRefreshFootballDetails(tc.match, now); got != tc.want {
			t.Fatalf("%s: shouldRefreshFootballDetails = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFootballDetailRefreshIntervalDefault(t *testing.T) {
	if got := footballDetailRefreshInterval(); got != 10*time.Minute {
		t.Fatalf("footballDetailRefreshInterval = %s, want 10m", got)
	}
}
