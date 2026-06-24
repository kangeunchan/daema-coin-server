package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
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

type server struct {
	store      *recordStore
	football   *footballClient
	githubAuth *githubOAuthClient
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("load .env", "error", err)
	}

	ctx := context.Background()
	store, err := openRecordStore(ctx, env("DATABASE_URL", "postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable"))
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.close()

	s := &server{
		store:      store,
		football:   newFootballClientFromEnv(),
		githubAuth: newGitHubOAuthClientFromEnv(),
	}

	port := env("PORT", "8080")
	addr := ":" + port

	slog.Info("starting daema coin server", "addr", addr)
	if err := http.ListenAndServe(addr, s.routes()); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("GET /api/auth/github/login", s.handleGitHubLogin)
	mux.HandleFunc("GET /api/auth/github/callback", s.handleGitHubCallback)
	mux.HandleFunc("POST /api/auth/github/exchange", s.handleGitHubExchange)
	mux.HandleFunc("POST /api/auth/github/session", s.handleGitHubSession)
	mux.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	mux.HandleFunc("PUT /api/auth/me/student-profile", s.handleStudentProfile)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("POST /api/github/webhooks", s.handleGitHubWebhook)

	mux.HandleFunc("GET /api/customer/me", s.handleCustomerMe)
	mux.HandleFunc("GET /api/customer/navigation", s.handleNavigation)
	mux.HandleFunc("GET /api/customer/notifications/summary", s.handleNotificationSummary)
	mux.HandleFunc("GET /api/customer/cart/summary", s.handleCartSummary)
	mux.HandleFunc("GET /api/search/suggestions", s.handleSearchSuggestions)
	mux.HandleFunc("GET /api/search", s.handleSearch)

	mux.HandleFunc("GET /api/customer/home", s.handleHome)
	mux.HandleFunc("GET /api/customer/notices/highlight", s.handleNoticeHighlight)
	mux.HandleFunc("GET /api/customer/wallet/balances", s.handleWalletBalances)
	mux.HandleFunc("GET /api/customer/benefits/interest", s.handleInterestBenefit)
	mux.HandleFunc("POST /api/customer/benefits/{benefitId}/claim", s.handleClaimBenefit)
	mux.HandleFunc("GET /api/customer/home/shortcuts", s.handleShortcuts)
	mux.HandleFunc("GET /api/customer/promotions", s.handlePromotions)
	mux.HandleFunc("GET /api/customer/ledger/recent", s.handleLedgerRecent)
	mux.HandleFunc("GET /api/customer/rankings", s.handleRankings)
	mux.HandleFunc("GET /api/customer/festival/banner", s.handleFestivalBanner)
	mux.HandleFunc("GET /api/customer/schedules/highlight", s.handleFestivalBanner)

	mux.HandleFunc("POST /api/customer/pay/barcodes", s.handleCreateBarcode)

	mux.HandleFunc("GET /api/customer/booth/categories", s.handleBoothCategories)
	mux.HandleFunc("GET /api/customer/booth/banners", s.handleBoothBanners)
	mux.HandleFunc("GET /api/customer/booth/home", s.handleBoothHome)
	mux.HandleFunc("GET /api/customer/booth/products", s.handleBoothProducts)
	mux.HandleFunc("GET /api/customer/booth/products/search", s.handleBoothProducts)
	mux.HandleFunc("GET /api/customer/booth/products/{productId}", s.handleBoothProduct)
	mux.HandleFunc("POST /api/customer/booth/products/{productId}/view", s.handleProductView)
	mux.HandleFunc("GET /api/customer/booths/{boothId}", s.handleCustomerBooth)
	mux.HandleFunc("POST /api/customer/booths/{boothId}/check-in", s.handleBoothCheckIn)
	mux.HandleFunc("GET /api/customer/booth-rankings", s.handleBoothRankings)
	mux.HandleFunc("POST /api/customer/analytics/impressions", s.handleAccepted)
	mux.HandleFunc("GET /api/customer/cart", s.handleCart)
	mux.HandleFunc("POST /api/customer/cart/items", s.handleCartItem)
	mux.HandleFunc("POST /api/customer/orders/preview", s.handleOrderPreview)
	mux.HandleFunc("POST /api/customer/orders", s.handleOrderCreate)
	mux.HandleFunc("GET /api/customer/orders/{orderId}", s.handleOrderDetail)
	mux.HandleFunc("POST /api/customer/favorites", s.handleFavorite)
	mux.HandleFunc("DELETE /api/customer/favorites/{targetId}", s.handleFavoriteDelete)
	mux.HandleFunc("GET /api/customer/inquiries", s.handleInquiries)
	mux.HandleFunc("POST /api/customer/inquiries", s.handleInquiryCreate)
	mux.HandleFunc("POST /api/customer/shares", s.handleShare)

	mux.HandleFunc("GET /api/customer/points/commit-activity", s.handleCommitActivity)
	mux.HandleFunc("GET /api/customer/points/commit-stats", s.handleCommitStats)
	mux.HandleFunc("GET /api/customer/points/commit-transactions", s.handleCommitTransactions)
	mux.HandleFunc("GET /api/customer/github/commits", s.handleGitHubCommits)
	mux.HandleFunc("GET /api/customer/github/commit-activity", s.handleCommitActivity)
	mux.HandleFunc("GET /api/customer/github/commit-stats", s.handleCommitStats)
	mux.HandleFunc("GET /api/customer/github/app-installation", s.handleGitHubAppInstallation)

	mux.HandleFunc("GET /api/customer/worldcup/match-days", s.handleWorldcupMatchDays)
	mux.HandleFunc("GET /api/customer/worldcup/matches", s.handleWorldcupMatches)
	mux.HandleFunc("GET /api/customer/worldcup/matches/{matchId}", s.handleWorldcupMatch)
	mux.HandleFunc("GET /api/customer/worldcup/matches/{matchId}/predictions/summary", s.handlePredictionSummary)
	mux.HandleFunc("POST /api/customer/worldcup/matches/{matchId}/predictions", s.handlePredictionCreate)
	mux.HandleFunc("GET /api/customer/worldcup/matches/{matchId}/stats", s.handleWorldcupStats)
	mux.HandleFunc("GET /api/customer/worldcup/matches/{matchId}/lineups", s.handleWorldcupLineups)

	mux.HandleFunc("GET /api/customer/ledger/calendar", s.handleLedgerCalendar)
	mux.HandleFunc("GET /api/customer/ledger/transactions", s.handleLedgerTransactions)
	mux.HandleFunc("GET /api/customer/ledger/analysis", s.handleLedgerAnalysis)
	mux.HandleFunc("GET /api/customer/features", s.handleFeatures)

	mux.HandleFunc("POST /api/auth/seller/login", s.handleSellerLogin)
	mux.HandleFunc("POST /api/auth/seller/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/seller/me", s.handleSellerMe)
	mux.HandleFunc("GET /api/seller/booths", s.handleSellerBooths)
	mux.HandleFunc("GET /api/seller/booths/{boothId}", s.handleSellerBooth)
	mux.HandleFunc("PATCH /api/seller/booths/{boothId}", s.handleSellerBooth)
	mux.HandleFunc("PATCH /api/seller/booths/{boothId}/status", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/staff", s.handleSellerStaff)
	mux.HandleFunc("POST /api/seller/booths/{boothId}/staff", s.handleAccepted)
	mux.HandleFunc("PATCH /api/seller/booths/{boothId}/staff/{staffId}", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/products", s.handleSellerProducts)
	mux.HandleFunc("POST /api/seller/booths/{boothId}/products", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/products/{productId}", s.handleSellerProduct)
	mux.HandleFunc("PATCH /api/seller/products/{productId}", s.handleSellerProduct)
	mux.HandleFunc("PATCH /api/seller/products/{productId}/status", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/products/{productId}/images", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/products/{productId}/inventory", s.handleInventory)
	mux.HandleFunc("POST /api/seller/products/{productId}/inventory/adjustments", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/products/{productId}/purchase-limits", s.handlePurchaseLimits)
	mux.HandleFunc("PATCH /api/seller/products/{productId}/purchase-limits", s.handlePurchaseLimits)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/orders", s.handleSellerOrders)
	mux.HandleFunc("GET /api/seller/orders/{orderId}", s.handleOrderDetail)
	mux.HandleFunc("PATCH /api/seller/orders/{orderId}/status", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/orders/{orderId}/cancel", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/orders/{orderId}/refund", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/pickup-vouchers/verify", s.handleVoucherVerify)
	mux.HandleFunc("POST /api/seller/pickup-vouchers/{voucherId}/redeem", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/pay/barcodes/lookup", s.handleBarcodeLookup)
	mux.HandleFunc("POST /api/seller/pay/payment-intents", s.handlePaymentIntent)
	mux.HandleFunc("POST /api/seller/pay/payment-intents/{intentId}/capture", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/pay/payment-intents/{intentId}/cancel", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/pay/payments/{paymentId}/refund", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/payments", s.handleSellerPayments)
	mux.HandleFunc("POST /api/seller/booths/{boothId}/visits/verify", s.handleVisitVerify)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/visits", s.handleVisits)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/ranking", s.handleBoothRanking)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/inquiries", s.handleInquiries)
	mux.HandleFunc("POST /api/seller/inquiries/{inquiryId}/replies", s.handleAccepted)
	mux.HandleFunc("POST /api/seller/booths/{boothId}/notices", s.handleAccepted)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/dashboard", s.handleSellerDashboard)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/settlements", s.handleSettlements)
	mux.HandleFunc("GET /api/seller/settlements/{settlementId}", s.handleSettlement)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/reports/sales", s.handleSalesReport)
	mux.HandleFunc("GET /api/seller/booths/{boothId}/reports/inventory", s.handleInventoryReport)
	mux.HandleFunc("POST /api/seller/booths/{boothId}/exports", s.handleExport)

	mux.HandleFunc("GET /api/admin/dashboard", s.handleAdminDashboard)
	mux.HandleFunc("GET /api/admin/festivals", s.handleAdminFestivals)
	mux.HandleFunc("POST /api/admin/festivals", s.handleAdminFestivalCreate)
	mux.HandleFunc("PATCH /api/admin/festivals/{festivalId}", s.handleAdminFestivalUpdate)
	mux.HandleFunc("GET /api/admin/booths", s.handleAdminBooths)
	mux.HandleFunc("POST /api/admin/booths", s.handleAdminBoothCreate)
	mux.HandleFunc("PATCH /api/admin/booths/{boothId}", s.handleAdminBoothUpdate)
	mux.HandleFunc("GET /api/admin/booth-categories", s.handleAdminBoothCategories)
	mux.HandleFunc("POST /api/admin/booth-categories", s.handleAdminBoothCategoryCreate)
	mux.HandleFunc("PATCH /api/admin/booth-categories/{categoryId}", s.handleAdminBoothCategoryUpdate)
	mux.HandleFunc("POST /api/admin/maps", s.handleAdminMapCreate)
	mux.HandleFunc("GET /api/admin/users", s.handleAdminUsers)
	mux.HandleFunc("POST /api/admin/users/import", s.handleAdminUsersImport)
	mux.HandleFunc("GET /api/admin/users/{userId}", s.handleAdminUser)
	mux.HandleFunc("PATCH /api/admin/users/{userId}", s.handleAdminUserUpdate)
	mux.HandleFunc("GET /api/admin/roles", s.handleAdminRoles)
	mux.HandleFunc("POST /api/admin/role-assignments", s.handleAdminRoleAssignmentCreate)
	mux.HandleFunc("DELETE /api/admin/role-assignments/{assignmentId}", s.handleAdminRoleAssignmentDelete)
	mux.HandleFunc("GET /api/admin/wallets", s.handleAdminWallets)
	mux.HandleFunc("POST /api/admin/wallets/adjustments", s.handleAdminWalletAdjustment)
	mux.HandleFunc("GET /api/admin/ledger/transactions", s.handleAdminLedgerTransactions)
	mux.HandleFunc("GET /api/admin/ledger/exports", s.handleAdminLedgerExports)
	mux.HandleFunc("POST /api/admin/reward-rules", s.handleAdminRewardRuleCreate)
	mux.HandleFunc("PATCH /api/admin/reward-rules/{ruleId}", s.handleAdminRewardRuleUpdate)
	mux.HandleFunc("GET /api/admin/notices", s.handleAdminNotices)
	mux.HandleFunc("POST /api/admin/notices", s.handleAdminNoticeCreate)
	mux.HandleFunc("PATCH /api/admin/notices/{noticeId}", s.handleAdminNoticeUpdate)
	mux.HandleFunc("GET /api/admin/promotions", s.handleAdminPromotions)
	mux.HandleFunc("POST /api/admin/promotions", s.handleAdminPromotionCreate)
	mux.HandleFunc("PATCH /api/admin/promotions/{promotionId}", s.handleAdminPromotionUpdate)
	mux.HandleFunc("POST /api/admin/notifications", s.handleAdminNotificationSend)
	mux.HandleFunc("POST /api/files/uploads", s.handleFileUpload)
	mux.HandleFunc("GET /api/admin/worldcup/teams", s.handleAdminWorldcupTeams)
	mux.HandleFunc("POST /api/admin/worldcup/teams", s.handleAdminWorldcupTeamCreate)
	mux.HandleFunc("GET /api/admin/worldcup/matches", s.handleAdminWorldcupMatches)
	mux.HandleFunc("POST /api/admin/worldcup/matches", s.handleAdminWorldcupMatchCreate)
	mux.HandleFunc("PATCH /api/admin/worldcup/matches/{matchId}", s.handleAdminWorldcupMatchUpdate)
	mux.HandleFunc("PUT /api/admin/worldcup/matches/{matchId}/lineups", s.handleAdminWorldcupLineupsPut)
	mux.HandleFunc("PUT /api/admin/worldcup/matches/{matchId}/stats", s.handleAdminWorldcupStatsPut)
	mux.HandleFunc("POST /api/admin/worldcup/matches/{matchId}/predictions/settle", s.handleAdminPredictionSettle)
	mux.HandleFunc("GET /api/admin/worldcup/predictions", s.handleAdminWorldcupPredictions)
	mux.HandleFunc("GET /api/admin/audit-logs", s.handleAdminAuditLogs)
	mux.HandleFunc("GET /api/admin/system/health", s.handleAdminSystemHealth)
	mux.HandleFunc("GET /api/admin/system/jobs", s.handleAdminSystemJobs)
	mux.HandleFunc("POST /api/admin/incidents", s.handleAdminIncidentCreate)
	mux.HandleFunc("POST /api/admin/ranking-rules", s.handleAccepted)

	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", env("CORS_ALLOW_ORIGIN", "*"))
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func env(key, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}

func amount(currency string, value int) map[string]any {
	suffix := currency
	if currency == "POINT" {
		suffix = "P"
	}
	return map[string]any{"currency": currency, "value": value, "formatted": fmt.Sprintf("%s %s", number(value), suffix)}
}

func appLocation() *time.Location {
	location, err := time.LoadLocation(env("APP_TIMEZONE", "Asia/Seoul"))
	if err != nil {
		return time.FixedZone("Asia/Seoul", 9*60*60)
	}
	return location
}

func number(v int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	s := strconv.Itoa(v)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

func media(id, path, alt string) map[string]any {
	return map[string]any{"id": id, "url": path, "alt": alt}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.ok(w, r, map[string]any{"status": "ok", "service": "daema-coin-server"})
}

type oauthState struct {
	Value         string
	Role          string
	RedirectAfter string
	ExpiresAt     time.Time
}

type authSession struct {
	Token             string    `json:"token"`
	GitHubAccessToken string    `json:"-"`
	User              authUser  `json:"user"`
	Role              string    `json:"role"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type authUser struct {
	ID        string   `json:"id"`
	GitHubID  int64    `json:"githubId"`
	Login     string   `json:"login"`
	Name      string   `json:"name,omitempty"`
	Email     string   `json:"email,omitempty"`
	AvatarURL string   `json:"avatarUrl,omitempty"`
	HTMLURL   string   `json:"htmlUrl,omitempty"`
	Roles     []string `json:"roles"`
}

func (s *server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if !s.githubAuth.Configured() {
		s.fail(w, r, http.StatusServiceUnavailable, "GITHUB_OAUTH_NOT_CONFIGURED", "GitHub OAuth 환경변수가 설정되지 않았습니다.", map[string]any{"required": []string{"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET", "GITHUB_OAUTH_REDIRECT_URI"}})
		return
	}

	state := randomToken()
	role := normalizeRole(r.URL.Query().Get("role"))
	redirectAfter := envDefault(r.URL.Query().Get("redirectAfter"), env("AUTH_SUCCESS_REDIRECT_URL", env("PUBLIC_BASE_URL", "http://localhost:5173")+"/login"))

	expiresAt := time.Now().Add(10 * time.Minute)
	if err := s.store.saveOAuthState(r.Context(), oauthState{Value: state, Role: role, RedirectAfter: redirectAfter, ExpiresAt: expiresAt}); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "OAuth state를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}

	authorizeURL := s.githubAuth.AuthorizeURL(state)
	if r.URL.Query().Get("format") == "json" {
		s.ok(w, r, map[string]any{"provider": "github", "authorizeUrl": authorizeURL, "state": state, "role": role, "expiresAt": expiresAt.Format(time.RFC3339)})
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (s *server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if errMessage := r.URL.Query().Get("error"); errMessage != "" {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_ERROR", errMessage, map[string]any{"description": r.URL.Query().Get("error_description")})
		return
	}

	session, redirectAfter, err := s.completeGitHubOAuth(r.Context(), r.URL.Query().Get("code"), r.URL.Query().Get("state"), "")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_FAILED", err.Error(), nil)
		return
	}

	setSessionCookie(w, session)
	redirectURL := appendQuery(redirectAfter, map[string]string{"login": "success", "role": session.Role})
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *server) handleGitHubExchange(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Code  string `json:"code"`
		State string `json:"state"`
		Role  string `json:"role"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", nil)
		return
	}

	session, _, err := s.completeGitHubOAuth(r.Context(), req.Code, req.State, req.Role)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "GITHUB_OAUTH_FAILED", err.Error(), nil)
		return
	}

	setSessionCookie(w, session)
	s.created(w, r, map[string]any{"accessToken": session.Token, "tokenType": "Bearer", "expiresAt": session.ExpiresAt.Format(time.RFC3339), "user": session.User, "role": session.Role})
}

func (s *server) handleGitHubSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	if _, found, err := s.store.get(r.Context(), domainCustomerProfiles, session.User.ID); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "학생 프로필을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	} else if !found {
		s.ok(w, r, map[string]any{"status": "profile_required"})
		return
	}
	s.ok(w, r, map[string]any{"status": "authenticated"})
}

func (s *server) handleStudentProfile(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	profile, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	profile["userId"] = session.User.ID
	profile["githubLogin"] = session.User.Login
	profile["githubId"] = session.User.GitHubID
	item, err := s.store.put(r.Context(), domainCustomerProfiles, session.User.ID, profile)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "학생 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, item)
}

func (s *server) completeGitHubOAuth(ctx context.Context, code, state, roleOverride string) (authSession, string, error) {
	if !s.githubAuth.Configured() {
		return authSession{}, "", errors.New("GitHub OAuth 환경변수가 설정되지 않았습니다")
	}
	if strings.TrimSpace(code) == "" {
		return authSession{}, "", errors.New("code가 비어 있습니다")
	}

	stateRecord, hasState, err := s.store.oauthState(ctx, state)
	if err != nil {
		return authSession{}, "", err
	}
	if hasState {
		_ = s.store.deleteOAuthState(ctx, state)
	}
	if !hasState {
		return authSession{}, "", errors.New("state가 없거나 이미 사용되었습니다")
	}
	if time.Now().After(stateRecord.ExpiresAt) {
		return authSession{}, "", errors.New("state가 만료되었습니다")
	}

	githubToken, err := s.githubAuth.Exchange(ctx, code)
	if err != nil {
		return authSession{}, "", err
	}
	githubUser, err := s.githubAuth.User(ctx, githubToken.AccessToken)
	if err != nil {
		return authSession{}, "", err
	}
	email, err := s.githubAuth.PrimaryEmail(ctx, githubToken.AccessToken)
	if err == nil && email != "" {
		githubUser.Email = email
	}

	role := stateRecord.Role
	if roleOverride != "" {
		role = normalizeRole(roleOverride)
	}
	user := authUser{
		ID:        "github-" + strconv.FormatInt(githubUser.ID, 10),
		GitHubID:  githubUser.ID,
		Login:     githubUser.Login,
		Name:      githubUser.Name,
		Email:     githubUser.Email,
		AvatarURL: githubUser.AvatarURL,
		HTMLURL:   githubUser.HTMLURL,
		Roles:     rolesForGitHubUser(role, githubUser.Email, githubUser.Login),
	}
	if !containsString(user.Roles, role) {
		return authSession{}, "", fmt.Errorf("%s 권한으로 로그인할 수 없는 GitHub 계정입니다", role)
	}

	session := authSession{Token: randomToken(), GitHubAccessToken: githubToken.AccessToken, User: user, Role: role, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := s.store.saveSession(ctx, session); err != nil {
		return authSession{}, "", err
	}

	return session, stateRecord.RedirectAfter, nil
}

func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return
	}
	s.ok(w, r, map[string]any{"user": session.User, "role": session.Role, "expiresAt": session.ExpiresAt.Format(time.RFC3339)})
}

func (s *server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		_ = s.store.deleteSession(r.Context(), token)
	}
	if cookie, err := r.Cookie("daema_session"); err == nil {
		_ = s.store.deleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "daema_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	s.ok(w, r, map[string]any{"loggedOut": true})
}

func (s *server) sessionFromRequest(r *http.Request) (authSession, bool) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("daema_session"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return authSession{}, false
	}
	session, ok, err := s.store.session(r.Context(), token)
	if err != nil {
		slog.Error("load auth session", "error", err)
		return authSession{}, false
	}
	return session, ok
}

func setSessionCookie(w http.ResponseWriter, session authSession) {
	http.SetCookie(w, &http.Cookie{Name: "daema_session", Value: session.Token, Path: "/", Expires: session.ExpiresAt, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "seller", "admin":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "customer"
	}
}

func rolesForGitHubUser(requestedRole, email, login string) []string {
	roles := []string{"customer"}
	trustRequestedRole := strings.EqualFold(env("GITHUB_OAUTH_TRUST_REQUESTED_ROLE", "false"), "true")
	if (trustRequestedRole && requestedRole == "seller") || listContainsEnv("GITHUB_OAUTH_SELLER_LOGINS", login) || listContainsEnv("GITHUB_OAUTH_SELLER_EMAILS", email) {
		roles = append(roles, "seller")
	}
	if (trustRequestedRole && requestedRole == "admin") || listContainsEnv("GITHUB_OAUTH_ADMIN_LOGINS", login) || listContainsEnv("GITHUB_OAUTH_ADMIN_EMAILS", email) {
		roles = append(roles, "admin")
	}
	return roles
}

func listContainsEnv(key, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, item := range strings.Split(env(key, ""), ",") {
		if strings.ToLower(strings.TrimSpace(item)) == value {
			return true
		}
	}
	return false
}

func appendQuery(raw string, values map[string]string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for key, value := range values {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

const (
	domainNavigation          = "navigation"
	domainCustomerProfiles    = "customer_profiles"
	domainNotifications       = "notifications"
	domainSearchSuggestions   = "search_suggestions"
	domainSearchDocuments     = "search_documents"
	domainNotices             = "notices"
	domainWalletBalances      = "wallet_balances"
	domainBenefits            = "benefits"
	domainBenefitClaims       = "benefit_claims"
	domainShortcuts           = "shortcuts"
	domainPromotions          = "promotions"
	domainLedgerTransactions  = "ledger_transactions"
	domainUserRankings        = "rankings_user"
	domainBoothRankings       = "rankings_booth"
	domainFestivalBanners     = "festival_banners"
	domainPayBarcodes         = "pay_barcodes"
	domainBoothCategories     = "booth_categories"
	domainBoothBanners        = "booth_banners"
	domainBooths              = "booths"
	domainProducts            = "products"
	domainProductViews        = "product_views"
	domainBoothCheckins       = "booth_checkins"
	domainCartItems           = "cart_items"
	domainOrders              = "orders"
	domainFavorites           = "favorites"
	domainInquiries           = "inquiries"
	domainShares              = "shares"
	domainWorldcupPredictions = "worldcup_predictions"
	domainFeatures            = "features"
	domainStaff               = "staff"
	domainInventory           = "inventory"
	domainPurchaseLimits      = "purchase_limits"
	domainPickupVouchers      = "pickup_vouchers"
	domainPaymentIntents      = "payment_intents"
	domainPayments            = "payments"
	domainVisits              = "visits"
	domainSettlements         = "settlements"
	domainExports             = "exports"
	domainFestivals           = "festivals"
	domainMaps                = "maps"
	domainUsers               = "users"
	domainUserImports         = "user_imports"
	domainRoles               = "roles"
	domainRoleAssignments     = "role_assignments"
	domainWalletAdjustments   = "wallet_adjustments"
	domainLedgerExports       = "ledger_exports"
	domainRewardRules         = "reward_rules"
	domainUploads             = "uploads"
	domainWorldcupTeams       = "worldcup_teams"
	domainWorldcupMatches     = "worldcup_matches"
	domainWorldcupLineups     = "worldcup_lineups"
	domainWorldcupStats       = "worldcup_stats"
	domainAuditLogs           = "audit_logs"
	domainSystemJobs          = "system_jobs"
	domainIncidents           = "incidents"
	domainRankingRules        = "ranking_rules"
	domainGitHubCommits       = "github_commits"
	domainGitHubInstallations = "github_installations"
)

type recordFilter struct {
	Field string
	Value string
}

func (s *server) listStoredRecords(w http.ResponseWriter, r *http.Request, domain string, defaultLimit int, filters ...recordFilter) ([]map[string]any, int, bool) {
	limit := queryInt(r, "limit", defaultLimit)
	if limit <= 0 {
		limit = defaultLimit
	}
	storeLimit := limit
	if len(filters) > 0 || strings.TrimSpace(r.URL.Query().Get("q")) != "" {
		storeLimit = 1000
	}
	items, err := s.store.list(r.Context(), domain, storeLimit)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "데이터를 읽지 못했습니다.", map[string]any{"domain": domain, "cause": err.Error()})
		return nil, limit, false
	}
	sortRecords(items)
	items = filterRecords(items, r.URL.Query().Get("q"), filters...)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, limit, true
}

func (s *server) respondRecordList(w http.ResponseWriter, r *http.Request, domain string, defaultLimit int, filters ...recordFilter) {
	items, limit, ok := s.listStoredRecords(w, r, domain, defaultLimit, filters...)
	if !ok {
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func (s *server) createStoredRecord(w http.ResponseWriter, r *http.Request, domain, prefix string, extras map[string]any, idCandidates ...string) (map[string]any, bool) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return nil, false
	}
	for key, value := range extras {
		body[key] = value
	}
	id := recordID(body, prefix, idCandidates...)
	item, err := s.store.put(r.Context(), domain, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 저장하지 못했습니다.", map[string]any{"domain": domain, "cause": err.Error()})
		return nil, false
	}
	return item, true
}

func (s *server) updateStoredRecord(w http.ResponseWriter, r *http.Request, domain, id string, extras map[string]any) (map[string]any, bool) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return nil, false
	}
	for key, value := range extras {
		body[key] = value
	}
	item, found, err := s.store.patch(r.Context(), domain, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 수정하지 못했습니다.", map[string]any{"domain": domain, "id": id, "cause": err.Error()})
		return nil, false
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "RECORD_NOT_FOUND", "대상 데이터를 찾을 수 없습니다.", map[string]any{"domain": domain, "id": id})
		return nil, false
	}
	return item, true
}

func (s *server) putStoredRecord(w http.ResponseWriter, r *http.Request, domain, id string, extras map[string]any) (map[string]any, bool) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return nil, false
	}
	for key, value := range extras {
		body[key] = value
	}
	item, err := s.store.put(r.Context(), domain, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 저장하지 못했습니다.", map[string]any{"domain": domain, "id": id, "cause": err.Error()})
		return nil, false
	}
	return item, true
}

func (s *server) deleteStoredRecord(w http.ResponseWriter, r *http.Request, domain, id string) bool {
	if err := s.store.delete(r.Context(), domain, id); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 삭제하지 못했습니다.", map[string]any{"domain": domain, "id": id, "cause": err.Error()})
		return false
	}
	return true
}

func (s *server) requestRecord(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	defer r.Body.Close()
	if r.Body == nil {
		return map[string]any{}, true
	}
	var payload any
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, http.ErrBodyReadAfterClose) {
			return map[string]any{}, true
		}
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", map[string]any{"cause": err.Error()})
		return nil, false
	}
	if payload == nil {
		return map[string]any{}, true
	}
	if data, ok := normalizeJSON(payload).(map[string]any); ok {
		return data, true
	}
	return map[string]any{"payload": normalizeJSON(payload)}, true
}

func normalizeJSON(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	case map[string]any:
		out := map[string]any{}
		for key, child := range v {
			out[key] = normalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, normalizeJSON(child))
		}
		return out
	default:
		return value
	}
}

func recordID(data map[string]any, prefix string, candidates ...string) string {
	candidates = append([]string{"id"}, candidates...)
	for _, key := range candidates {
		if value := stringValue(data[key]); value != "" {
			return value
		}
	}
	return generatedID(prefix)
}

func filterRecords(items []map[string]any, query string, filters ...recordFilter) []map[string]any {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		matched := true
		for _, filter := range filters {
			if strings.TrimSpace(filter.Value) == "" {
				continue
			}
			if stringValue(item[filter.Field]) != filter.Value {
				matched = false
				break
			}
		}
		if matched && query != "" && !recordContains(item, query) {
			matched = false
		}
		if matched {
			out = append(out, item)
		}
	}
	return out
}

func recordContains(value any, query string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(strings.ToLower(v), query)
	case map[string]any:
		for _, child := range v {
			if recordContains(child, query) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if recordContains(child, query) {
				return true
			}
		}
	default:
		return strings.Contains(strings.ToLower(fmt.Sprint(v)), query)
	}
	return false
}

func sortRecords(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		left, leftOK := numericValue(items[i]["sortOrder"])
		right, rightOK := numericValue(items[j]["sortOrder"])
		if leftOK && rightOK {
			return left < right
		}
		return leftOK && !rightOK
	})
}

func firstRecord(items []map[string]any) any {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func amountValue(record map[string]any) int {
	fields := []string{"totalAmount", "amount", "price", "unitAmount"}
	for _, field := range fields {
		if value, ok := record[field]; ok {
			if amountMap, ok := value.(map[string]any); ok {
				if n, ok := numericValue(amountMap["value"]); ok {
					return int(n)
				}
			}
			if n, ok := numericValue(value); ok {
				return int(n)
			}
		}
	}
	return 0
}

func countUnread(items []map[string]any) int {
	count := 0
	for _, item := range items {
		if read, ok := item["read"].(bool); ok && read {
			continue
		}
		if readAt := stringValue(item["readAt"]); readAt != "" {
			continue
		}
		count++
	}
	return count
}

func (s *server) currentUserID(r *http.Request) string {
	if session, ok := s.sessionFromRequest(r); ok {
		return session.User.ID
	}
	return ""
}

func (s *server) handleCustomerMe(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.sessionFromRequest(r); ok {
		s.ok(w, r, map[string]any{
			"id":          session.User.ID,
			"displayName": envDefault(session.User.Name, session.User.Login),
			"schoolName":  "대마고등학교",
			"avatar":      media("avatar-"+session.User.Login, session.User.AvatarURL, session.User.Login+" 프로필"),
			"roles":       session.User.Roles,
			"github":      map[string]any{"id": session.User.GitHubID, "login": session.User.Login, "htmlUrl": session.User.HTMLURL},
		})
		return
	}
	s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
}

func (s *server) handleNavigation(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainNavigation, 50)
}

func (s *server) handleNotificationSummary(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainNotifications, 20)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"unreadCount": countUnread(items), "latest": firstRecord(items)})
}

func (s *server) handleCartSummary(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainCartItems, 500, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	total := 0
	for _, item := range items {
		total += amountValue(item)
	}
	s.ok(w, r, map[string]any{"itemCount": len(items), "pendingAmount": amount("DMC", total)})
}

func (s *server) handleSearchSuggestions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainSearchSuggestions, 20)
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	s.respondRecordList(w, r, domainSearchDocuments, 50, recordFilter{Field: "scope", Value: scope})
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	notices, _, ok := s.listStoredRecords(w, r, domainNotices, 20)
	if !ok {
		return
	}
	wallet, _, ok := s.listStoredRecords(w, r, domainWalletBalances, 20, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	shortcuts, _, ok := s.listStoredRecords(w, r, domainShortcuts, 50)
	if !ok {
		return
	}
	promotions, _, ok := s.listStoredRecords(w, r, domainPromotions, 20, recordFilter{Field: "placement", Value: "home"})
	if !ok {
		return
	}
	transactions, _, ok := s.listStoredRecords(w, r, domainLedgerTransactions, 6, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	userRanking, _, ok := s.listStoredRecords(w, r, domainUserRankings, 10)
	if !ok {
		return
	}
	boothRanking, _, ok := s.listStoredRecords(w, r, domainBoothRankings, 10)
	if !ok {
		return
	}
	banners, _, ok := s.listStoredRecords(w, r, domainFestivalBanners, 10)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{
		"notice":             firstRecord(notices),
		"wallet":             map[string]any{"balances": wallet},
		"shortcuts":          shortcuts,
		"promotions":         promotions,
		"recentTransactions": transactions,
		"rankings":           map[string]any{"userPoint": userRanking, "boothPoint": boothRanking},
		"festivalBanner":     firstRecord(banners),
	})
}

func (s *server) handleNoticeHighlight(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainNotices, 20)
	if !ok {
		return
	}
	s.ok(w, r, firstRecord(items))
}

func (s *server) handleWalletBalances(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainWalletBalances, 20, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"balances": items})
}

func (s *server) handleInterestBenefit(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainBenefits, 10, recordFilter{Field: "type", Value: "interest"})
	if !ok {
		return
	}
	s.ok(w, r, firstRecord(items))
}

func (s *server) handleClaimBenefit(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainBenefitClaims, "benefit-claim", map[string]any{"benefitId": r.PathValue("benefitId"), "userId": s.currentUserID(r)}, "claimId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainShortcuts, 50)
}

func (s *server) handlePromotions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainPromotions, 50, recordFilter{Field: "placement", Value: r.URL.Query().Get("placement")})
}

func (s *server) handleLedgerRecent(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainLedgerTransactions, 6, recordFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) handleRankings(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("type") {
	case "booth":
		s.respondRecordList(w, r, domainBoothRankings, 20)
	case "user":
		s.respondRecordList(w, r, domainUserRankings, 20)
	default:
		users, _, ok := s.listStoredRecords(w, r, domainUserRankings, 20)
		if !ok {
			return
		}
		booths, _, ok := s.listStoredRecords(w, r, domainBoothRankings, 20)
		if !ok {
			return
		}
		s.ok(w, r, map[string]any{"userPoint": users, "boothPoint": booths})
	}
}

func (s *server) handleFestivalBanner(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainFestivalBanners, 10)
	if !ok {
		return
	}
	s.ok(w, r, firstRecord(items))
}

func (s *server) handleCreateBarcode(w http.ResponseWriter, r *http.Request) {
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	item, ok := s.createStoredRecord(w, r, domainPayBarcodes, "barcode", map[string]any{"code": randomToken()[:24], "userId": s.currentUserID(r), "expiresAt": expiresAt}, "barcodeId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleBoothCategories(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBoothCategories, 100)
}

func (s *server) handleBoothBanners(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBoothBanners, 50)
}

func (s *server) handleBoothHome(w http.ResponseWriter, r *http.Request) {
	categories, _, ok := s.listStoredRecords(w, r, domainBoothCategories, 100)
	if !ok {
		return
	}
	banners, _, ok := s.listStoredRecords(w, r, domainBoothBanners, 20)
	if !ok {
		return
	}
	products, _, ok := s.listStoredRecords(w, r, domainProducts, 50)
	if !ok {
		return
	}
	booths, _, ok := s.listStoredRecords(w, r, domainBooths, 50)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"categories": categories, "banners": banners, "products": products, "booths": booths})
}

func (s *server) handleBoothProducts(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainProducts, 100,
		recordFilter{Field: "boothId", Value: r.PathValue("boothId")},
		recordFilter{Field: "categoryId", Value: r.URL.Query().Get("categoryId")},
		recordFilter{Field: "status", Value: r.URL.Query().Get("status")},
	)
}

func (s *server) handleBoothProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("productId")
	item, found, err := s.store.get(r.Context(), domainProducts, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품을 읽지 못했습니다.", map[string]any{"productId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "PRODUCT_NOT_FOUND", "상품을 찾을 수 없습니다.", map[string]any{"productId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleProductView(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainProductViews, "product-view", map[string]any{"productId": r.PathValue("productId"), "userId": s.currentUserID(r)}, "viewId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleCustomerBooth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	item, found, err := s.store.get(r.Context(), domainBooths, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스를 읽지 못했습니다.", map[string]any{"boothId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "BOOTH_NOT_FOUND", "부스를 찾을 수 없습니다.", map[string]any{"boothId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleBoothCheckIn(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainBoothCheckins, "checkin", map[string]any{"boothId": r.PathValue("boothId"), "userId": s.currentUserID(r)}, "checkinId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleBoothRankings(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBoothRankings, 20)
}

func (s *server) handleCart(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainCartItems, 100, recordFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) handleCartItem(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainCartItems, "cart-item", map[string]any{"userId": s.currentUserID(r)}, "cartItemId", "productId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleOrderPreview(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	productID := stringValue(body["productId"])
	if productID == "" {
		s.fail(w, r, http.StatusBadRequest, "PRODUCT_ID_REQUIRED", "productId가 필요합니다.", nil)
		return
	}
	product, found, err := s.store.get(r.Context(), domainProducts, productID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품을 읽지 못했습니다.", map[string]any{"productId": productID, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "PRODUCT_NOT_FOUND", "상품을 찾을 수 없습니다.", map[string]any{"productId": productID})
		return
	}
	quantity := 1
	if n, ok := numericValue(body["quantity"]); ok && n > 0 {
		quantity = int(n)
	}
	unit := amountValue(product)
	total := unit * quantity
	s.ok(w, r, map[string]any{
		"product":     map[string]any{"id": productID, "title": product["title"], "thumbnail": product["thumbnail"]},
		"quantity":    quantity,
		"itemAmount":  amount("DMC", total),
		"totalAmount": amount("DMC", total),
	})
}

func (s *server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainOrders, "order", map[string]any{"userId": s.currentUserID(r)}, "orderId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleOrderDetail(w http.ResponseWriter, r *http.Request) {
	id := envDefault(r.PathValue("orderId"), r.PathValue("settlementId"))
	item, found, err := s.store.get(r.Context(), domainOrders, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "주문을 읽지 못했습니다.", map[string]any{"orderId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "ORDER_NOT_FOUND", "주문을 찾을 수 없습니다.", map[string]any{"orderId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainFavorites, "favorite", map[string]any{"userId": s.currentUserID(r)}, "favoriteId", "targetId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("targetId")
	_ = s.deleteStoredRecord(w, r, domainFavorites, targetID)
	items, _, ok := s.listStoredRecords(w, r, domainFavorites, 100, recordFilter{Field: "targetId", Value: targetID}, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	for _, item := range items {
		_ = s.store.delete(r.Context(), domainFavorites, stringValue(item["id"]))
	}
	s.ok(w, r, map[string]any{"deleted": true, "targetId": targetID})
}

func (s *server) handleInquiries(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainInquiries, 100,
		recordFilter{Field: "userId", Value: s.currentUserID(r)},
		recordFilter{Field: "boothId", Value: r.PathValue("boothId")},
	)
}

func (s *server) handleInquiryCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainInquiries, "inquiry", map[string]any{"userId": s.currentUserID(r)}, "inquiryId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleShare(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainShares, "share", map[string]any{"userId": s.currentUserID(r)}, "shareId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleCommitActivity(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 118)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	counts := map[string]int{}
	for _, commit := range commits {
		counts[commit.OccurredAt.In(appLocation()).Format("2006-01-02")]++
	}
	items := []map[string]any{}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		count := counts[day.Format("2006-01-02")]
		level := commitLevel(count)
		items = append(items, map[string]any{"date": day.Format("2006-01-02"), "count": count, "level": level, "rewardedPoints": count * 10})
	}
	s.ok(w, r, items)
}

func (s *server) handleCommitStats(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 180)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	groupBy := envDefault(r.URL.Query().Get("groupBy"), "month")
	s.ok(w, r, commitStats(commits, groupBy))
}

func (s *server) handleCommitTransactions(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 30)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 20)
	transactions := commitTransactions(commits, limit)
	s.okPage(w, r, transactions, &pagination{Limit: limit, HasMore: len(commits) > len(transactions)})
}

func (s *server) handleGitHubCommits(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	from, to := commitDateRange(r, 30)
	commits, ok := s.fetchGitHubCommitsForRequest(w, r, session, from, to)
	if !ok {
		return
	}
	limit := queryInt(r, "limit", 100)
	if limit > 0 && limit < len(commits) {
		commits = commits[:limit]
	}
	s.okPage(w, r, commits, &pagination{Limit: limit, HasMore: false})
}

func (s *server) handleGitHubAppInstallation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireGitHubSession(w, r)
	if !ok {
		return
	}
	installURL := env("GITHUB_APP_INSTALL_URL", "")
	s.ok(w, r, map[string]any{
		"configured": installURL != "",
		"installUrl": installURL,
		"login":      session.User.Login,
	})
}

func (s *server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WEBHOOK_BODY", "Webhook body를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !verifyGitHubWebhookSignature(body, r.Header.Get("X-Hub-Signature-256")) {
		s.fail(w, r, http.StatusUnauthorized, "INVALID_GITHUB_WEBHOOK_SIGNATURE", "GitHub webhook signature가 유효하지 않습니다.", nil)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	switch event {
	case "ping":
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "ok"})
	case "installation", "installation_repositories":
		if err := s.storeGitHubInstallationEvent(r.Context(), event, deliveryID, body); err != nil {
			s.fail(w, r, http.StatusInternalServerError, "GITHUB_WEBHOOK_STORE_FAILED", "GitHub installation 이벤트를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "stored"})
	case "push":
		count, err := s.storeGitHubPushEvent(r.Context(), deliveryID, body)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "GITHUB_WEBHOOK_STORE_FAILED", "GitHub push 이벤트를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "storedCommits": count})
	default:
		s.ok(w, r, map[string]any{"event": event, "deliveryId": deliveryID, "status": "ignored"})
	}
}

type githubCommitItem struct {
	SHA          string    `json:"sha"`
	Repository   string    `json:"repository"`
	Message      string    `json:"message"`
	Title        string    `json:"title"`
	AuthorName   string    `json:"authorName,omitempty"`
	AuthorEmail  string    `json:"authorEmail,omitempty"`
	AuthorLogin  string    `json:"authorLogin,omitempty"`
	OccurredAt   time.Time `json:"occurredAt"`
	HTMLURL      string    `json:"htmlUrl"`
	RewardPoints int       `json:"rewardedPoints"`
}

func (s *server) requireGitHubSession(w http.ResponseWriter, r *http.Request) (authSession, bool) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return authSession{}, false
	}
	if session.GitHubAccessToken == "" {
		s.fail(w, r, http.StatusUnauthorized, "GITHUB_TOKEN_REQUIRED", "GitHub access token이 있는 세션이 필요합니다. 다시 로그인해 주세요.", nil)
		return authSession{}, false
	}
	return session, true
}

func (s *server) fetchGitHubCommitsForRequest(w http.ResponseWriter, r *http.Request, session authSession, from, to time.Time) ([]githubCommitItem, bool) {
	commits, err := s.listStoredGitHubCommits(r.Context(), session.User.Login, from, to)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "GitHub webhook 커밋 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return nil, false
	}
	return commits, true
}

type githubPushWebhookPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
		Private  bool   `json:"private"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Pusher struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Sender struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
	} `json:"sender"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Commits []struct {
		ID        string    `json:"id"`
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
		URL       string    `json:"url"`
		Distinct  bool      `json:"distinct"`
		Author    struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"author"`
		Committer struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"committer"`
	} `json:"commits"`
}

func verifyGitHubWebhookSignature(body []byte, signature string) bool {
	secret := env("GITHUB_WEBHOOK_SECRET", "")
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (s *server) storeGitHubInstallationEvent(ctx context.Context, event, deliveryID string, body []byte) error {
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	installationID := ""
	if installation, _ := payload["installation"].(map[string]any); installation != nil {
		installationID = fmt.Sprint(installation["id"])
	}
	if installationID == "" {
		installationID = deliveryID
	}
	payload["event"] = event
	payload["deliveryId"] = deliveryID
	payload["receivedAt"] = time.Now().UTC().Format(time.RFC3339)
	_, err := s.store.put(ctx, domainGitHubInstallations, installationID, payload)
	return err
}

func (s *server) storeGitHubPushEvent(ctx context.Context, deliveryID string, body []byte) (int, error) {
	payload := githubPushWebhookPayload{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}
	if payload.Repository.FullName == "" {
		return 0, errors.New("repository full_name is empty")
	}
	count := 0
	for _, commit := range payload.Commits {
		if commit.ID == "" {
			continue
		}
		occurredAt := commit.Timestamp
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		authorLogin := firstNonEmpty(commit.Author.Username, commit.Committer.Username, payload.Sender.Login, payload.Pusher.Name)
		title := strings.TrimSpace(strings.Split(commit.Message, "\n")[0])
		item := githubCommitItem{
			SHA:          commit.ID,
			Repository:   payload.Repository.FullName,
			Message:      commit.Message,
			Title:        title,
			AuthorName:   firstNonEmpty(commit.Author.Name, commit.Committer.Name),
			AuthorEmail:  firstNonEmpty(commit.Author.Email, commit.Committer.Email),
			AuthorLogin:  authorLogin,
			OccurredAt:   occurredAt,
			HTMLURL:      commit.URL,
			RewardPoints: 10,
		}
		data, err := typedRecord(item)
		if err != nil {
			return count, err
		}
		data["authorLogin"] = authorLogin
		data["pusherLogin"] = payload.Pusher.Name
		data["senderLogin"] = payload.Sender.Login
		data["repositoryId"] = payload.Repository.ID
		data["repositoryOwner"] = payload.Repository.Owner.Login
		data["repositoryPrivate"] = payload.Repository.Private
		data["ref"] = payload.Ref
		data["before"] = payload.Before
		data["after"] = payload.After
		data["distinct"] = commit.Distinct
		data["deliveryId"] = deliveryID
		if payload.Installation != nil {
			data["installationId"] = payload.Installation.ID
		}
		_, err = s.store.put(ctx, domainGitHubCommits, githubCommitRecordID(payload.Repository.FullName, commit.ID), data)
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *server) listStoredGitHubCommits(ctx context.Context, login string, from, to time.Time) ([]githubCommitItem, error) {
	items, err := s.store.list(ctx, domainGitHubCommits, 10000)
	if err != nil {
		return nil, err
	}
	from = from.In(appLocation())
	toEnd := to.AddDate(0, 0, 1).Add(-time.Nanosecond)
	commits := []githubCommitItem{}
	for _, item := range items {
		authorLogin, _ := item["authorLogin"].(string)
		if !strings.EqualFold(authorLogin, login) {
			continue
		}
		commit := githubCommitItem{}
		if err := decodeRecord(item, &commit); err != nil {
			return nil, err
		}
		occurredAt := commit.OccurredAt.In(appLocation())
		if occurredAt.Before(from) || occurredAt.After(toEnd) {
			continue
		}
		commits = append(commits, commit)
	}
	sort.SliceStable(commits, func(i, j int) bool {
		return commits[i].OccurredAt.After(commits[j].OccurredAt)
	})
	return commits, nil
}

func githubCommitRecordID(repository, sha string) string {
	sum := sha256.Sum256([]byte(repository + "|" + sha))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func commitDateRange(r *http.Request, defaultDays int) (time.Time, time.Time) {
	today := time.Now().In(appLocation())
	to := parseDate(envDefault(r.URL.Query().Get("to"), today.Format("2006-01-02")))
	from := parseDate(envDefault(r.URL.Query().Get("from"), to.AddDate(0, 0, -defaultDays).Format("2006-01-02")))
	return from, to
}

func commitLevel(count int) int {
	switch {
	case count <= 0:
		return 0
	case count <= 2:
		return 1
	case count <= 5:
		return 2
	case count <= 9:
		return 3
	default:
		return 4
	}
}

func commitStats(commits []githubCommitItem, groupBy string) []map[string]any {
	counts := map[string]int{}
	labels := map[string]string{}
	now := time.Now().In(appLocation())
	for _, commit := range commits {
		period, label := commitPeriod(commit.OccurredAt, groupBy)
		counts[period]++
		labels[period] = label
	}
	periods := make([]string, 0, len(counts))
	for period := range counts {
		periods = append(periods, period)
	}
	sort.Strings(periods)
	out := make([]map[string]any, 0, len(periods))
	currentPeriod, _ := commitPeriod(now, groupBy)
	for _, period := range periods {
		count := counts[period]
		out = append(out, map[string]any{"period": period, "label": labels[period], "commitCount": count, "rewardedPoints": count * 10, "current": period == currentPeriod})
	}
	return out
}

func commitPeriod(t time.Time, groupBy string) (string, string) {
	local := t.In(appLocation())
	switch groupBy {
	case "day":
		return local.Format("2006-01-02"), local.Format("1월 2일")
	case "week":
		year, week := local.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week), fmt.Sprintf("%d년 %02d주", year, week)
	default:
		return local.Format("2006-01"), fmt.Sprintf("%d월", int(local.Month()))
	}
}

func commitTransactions(commits []githubCommitItem, limit int) []map[string]any {
	if limit <= 0 || limit > len(commits) {
		limit = len(commits)
	}
	out := make([]map[string]any, 0, limit)
	for _, commit := range commits[:limit] {
		out = append(out, map[string]any{
			"id":                commit.SHA,
			"sha":               commit.SHA,
			"repository":        commit.Repository,
			"title":             commit.Title,
			"message":           commit.Message,
			"commitCount":       1,
			"amount":            amount("POINT", commit.RewardPoints),
			"occurredAt":        commit.OccurredAt.Format(time.RFC3339),
			"relativeTimeLabel": relativeTimeLabel(commit.OccurredAt),
			"htmlUrl":           commit.HTMLURL,
		})
	}
	return out
}

func relativeTimeLabel(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		minutes := int(d.Minutes())
		if minutes < 1 {
			return "방금 전"
		}
		return fmt.Sprintf("%d분 전", minutes)
	case d < 24*time.Hour:
		return fmt.Sprintf("%d시간 전", int(d.Hours()))
	case d < 48*time.Hour:
		return "어제"
	default:
		return fmt.Sprintf("%d일 전", int(d.Hours()/24))
	}
}

func splitCSV(raw string) []string {
	items := []string{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

type worldcupTeam struct {
	ID          string `json:"id"`
	CountryCode string `json:"countryCode"`
	Name        string `json:"name"`
	Logo        string `json:"logo,omitempty"`
	Score       *int   `json:"score,omitempty"`
}

type worldcupMatch struct {
	ID          string       `json:"id"`
	MatchDayID  string       `json:"matchDayId"`
	Home        worldcupTeam `json:"home"`
	Away        worldcupTeam `json:"away"`
	Status      string       `json:"status"`
	StatusLabel string       `json:"statusLabel"`
	Subtitle    string       `json:"subtitle"`
	StartsAt    string       `json:"startsAt,omitempty"`
	DisplayTime string       `json:"displayTime,omitempty"`
	ExternalID  int          `json:"externalId,omitempty"`
}

func (s *server) handleWorldcupMatchDays(w http.ResponseWriter, r *http.Request) {
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.ok(w, r, groupMatchDays(matches))
}

func (s *server) handleWorldcupMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.ok(w, r, matches)
}

func (s *server) handleWorldcupMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("matchId")
	if m, err := s.football.Fixture(r.Context(), id); err == nil {
		s.ok(w, r, m)
		return
	} else if errors.Is(err, errFootballNotConfigured) {
		s.failFootball(w, r, err)
		return
	}
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	for _, m := range matches {
		if m.ID == id || strconv.Itoa(m.ExternalID) == id {
			s.ok(w, r, m)
			return
		}
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_NOT_FOUND", "경기를 찾을 수 없습니다.", nil)
}

func (s *server) worldcupMatches(ctx context.Context) ([]worldcupMatch, error) {
	matches, err := s.football.Fixtures(ctx)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, errors.New("API-FOOTBALL 경기 데이터가 비어 있습니다")
	}
	return matches, nil
}

func (s *server) worldcupMatchByID(ctx context.Context, id string) (worldcupMatch, bool, error) {
	if m, err := s.football.Fixture(ctx, id); err == nil {
		return m, true, nil
	} else if errors.Is(err, errFootballNotConfigured) {
		return worldcupMatch{}, false, err
	}
	matches, err := s.worldcupMatches(ctx)
	if err != nil {
		return worldcupMatch{}, false, err
	}
	for _, m := range matches {
		if m.ID == id || strconv.Itoa(m.ExternalID) == id {
			return m, true, nil
		}
	}
	return worldcupMatch{}, false, nil
}

func groupMatchDays(matches []worldcupMatch) []map[string]any {
	byDay := map[string][]worldcupMatch{}
	order := []string{}
	for _, m := range matches {
		key := m.MatchDayID
		if key == "" {
			key = "unknown"
		}
		if _, ok := byDay[key]; !ok {
			order = append(order, key)
		}
		byDay[key] = append(byDay[key], m)
	}
	todayKey := time.Now().In(appLocation()).Format("2006-01-02")
	activeKey := ""
	for _, key := range order {
		if key == todayKey {
			activeKey = key
			break
		}
	}
	if activeKey == "" {
		for _, key := range order {
			if key >= todayKey {
				activeKey = key
				break
			}
		}
	}
	if activeKey == "" && len(order) > 0 {
		activeKey = order[len(order)-1]
	}
	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		items := byDay[key]
		badge := ""
		for _, m := range items {
			if m.Home.CountryCode == "KR" || m.Away.CountryCode == "KR" {
				badge = "대한민국"
			}
		}
		label := weekdayLabel(items[0].StartsAt)
		dateLabel := dateDot(items[0].StartsAt, key)
		if key == todayKey {
			label = "오늘"
		}
		out = append(out, map[string]any{"id": key, "date": dateLabel, "label": label, "badge": badge, "isActive": key == activeKey, "matches": items})
	}
	return out
}

func (s *server) handlePredictionSummary(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	match, matchStatusKnown, err := s.worldcupMatchByID(r.Context(), matchID)
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	items, _, ok := s.listStoredRecords(w, r, domainWorldcupPredictions, 1000, recordFilter{Field: "matchId", Value: matchID})
	if !ok {
		return
	}
	counts := map[string]int{"home": 0, "draw": 0, "away": 0}
	myPrediction := ""
	userID := s.currentUserID(r)
	for _, item := range items {
		pick := stringValue(item["pick"])
		if _, ok := counts[pick]; ok {
			counts[pick]++
		}
		if userID != "" && stringValue(item["userId"]) == userID {
			myPrediction = pick
		}
	}
	total := counts["home"] + counts["draw"] + counts["away"]
	percent := func(count int) int {
		if total == 0 {
			return 0
		}
		return int(math.Round(float64(count) / float64(total) * 100))
	}
	data := map[string]any{
		"matchId":      matchID,
		"homePercent":  percent(counts["home"]),
		"drawPercent":  percent(counts["draw"]),
		"awayPercent":  percent(counts["away"]),
		"totalCount":   total,
		"canPredict":   myPrediction == "" && (!matchStatusKnown || match.Status == "scheduled"),
		"myPrediction": nil,
	}
	if matchStatusKnown {
		data["matchStatus"] = match.Status
		data["matchStatusLabel"] = match.StatusLabel
	}
	if myPrediction != "" {
		data["myPrediction"] = myPrediction
	}
	s.ok(w, r, data)
}

func (s *server) handlePredictionCreate(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	matchID := r.PathValue("matchId")
	body, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	pick := stringValue(body["pick"])
	if pick != "home" && pick != "draw" && pick != "away" {
		s.fail(w, r, http.StatusBadRequest, "INVALID_PREDICTION_PICK", "pick은 home, draw, away 중 하나여야 합니다.", map[string]any{"pick": pick})
		return
	}
	match, matchStatusKnown, err := s.worldcupMatchByID(r.Context(), matchID)
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	if matchStatusKnown && match.Status != "scheduled" {
		s.fail(w, r, http.StatusConflict, "PREDICTION_CLOSED", "이미 시작했거나 종료된 경기는 투표할 수 없습니다.", map[string]any{"matchId": matchID, "status": match.Status, "statusLabel": match.StatusLabel})
		return
	}
	body["matchId"] = matchID
	body["userId"] = session.User.ID
	body["githubLogin"] = session.User.Login
	id := predictionRecordID(matchID, session.User.ID)
	item, created, err := s.store.create(r.Context(), domainWorldcupPredictions, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !created {
		existing, _, _ := s.store.get(r.Context(), domainWorldcupPredictions, id)
		s.fail(w, r, http.StatusConflict, "PREDICTION_ALREADY_EXISTS", "이미 이 경기에 투표했습니다.", map[string]any{"matchId": matchID, "prediction": existing})
		return
	}
	s.created(w, r, item)
}

func (s *server) handleWorldcupStats(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	if stats, err := s.football.Stats(r.Context(), matchID); err == nil && len(stats) > 0 {
		s.ok(w, r, stats)
		return
	} else if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_STATS_NOT_FOUND", "경기 지표를 찾을 수 없습니다.", map[string]any{"matchId": matchID})
}

func (s *server) handleWorldcupLineups(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	if lineups, err := s.football.Lineups(r.Context(), matchID); err == nil && len(lineups) > 0 {
		s.ok(w, r, lineups)
		return
	} else if err != nil {
		s.failFootball(w, r, err)
		return
	}
	s.fail(w, r, http.StatusNotFound, "MATCH_LINEUPS_NOT_FOUND", "경기 라인업을 찾을 수 없습니다.", map[string]any{"matchId": matchID})
}

func predictionRecordID(matchID, userID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "prediction-" + replacer.Replace(matchID) + "-" + replacer.Replace(userID)
}

func (s *server) handleLedgerCalendar(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, _, ok := s.listStoredRecords(w, r, domainLedgerTransactions, 1000, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	byDate := map[string]map[string]int{}
	for _, item := range items {
		occurredAt := stringValue(item["occurredAt"])
		if len(occurredAt) < len("2006-01-02") || !strings.HasPrefix(occurredAt, month) {
			continue
		}
		date := occurredAt[:10]
		if byDate[date] == nil {
			byDate[date] = map[string]int{"income": 0, "expense": 0}
		}
		direction := stringValue(item["direction"])
		if direction != "income" {
			direction = "expense"
		}
		byDate[date][direction] += amountValue(item)
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	out := []map[string]any{}
	for _, date := range dates {
		day, _ := strconv.Atoi(date[len(date)-2:])
		row := map[string]any{"date": date, "day": day, "active": true}
		if byDate[date]["income"] > 0 {
			row["income"] = amount("DMC", byDate[date]["income"])
		}
		if byDate[date]["expense"] > 0 {
			row["expense"] = amount("DMC", byDate[date]["expense"])
		}
		out = append(out, row)
	}
	s.ok(w, r, out)
}

func (s *server) handleLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainLedgerTransactions, 50, recordFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) handleLedgerAnalysis(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, _, ok := s.listStoredRecords(w, r, domainLedgerTransactions, 1000, recordFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	incomeTotal := 0
	expenseTotal := 0
	incomeCategories := map[string]int{}
	expenseCategories := map[string]int{}
	labels := map[string]string{}
	for _, item := range items {
		occurredAt := stringValue(item["occurredAt"])
		if len(occurredAt) < len("2006-01") || !strings.HasPrefix(occurredAt, month) {
			continue
		}
		value := amountValue(item)
		category := envDefault(stringValue(item["categoryId"]), stringValue(item["type"]))
		if category == "" {
			category = "uncategorized"
		}
		labels[category] = envDefault(stringValue(item["categoryLabel"]), category)
		if stringValue(item["direction"]) == "income" {
			incomeTotal += value
			incomeCategories[category] += value
		} else {
			expenseTotal += value
			expenseCategories[category] += value
		}
	}
	categoryRows := func(values map[string]int) []map[string]any {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := []map[string]any{}
		for _, key := range keys {
			rows = append(rows, map[string]any{"id": key, "label": labels[key], "value": amount("DMC", values[key]), "color": ""})
		}
		return rows
	}
	s.ok(w, r, map[string]any{
		"month":             month,
		"incomeTotal":       amount("DMC", incomeTotal),
		"expenseTotal":      amount("DMC", expenseTotal),
		"incomeCategories":  categoryRows(incomeCategories),
		"expenseCategories": categoryRows(expenseCategories),
	})
}

func (s *server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainFeatures, 100)
}

func (s *server) handleAccepted(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/customer/analytics/impressions" {
		item, ok := s.createStoredRecord(w, r, "analytics_impressions", "impression", map[string]any{"userId": s.currentUserID(r)}, "impressionId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/status") {
		item, ok := s.updateStoredRecord(w, r, domainBooths, r.PathValue("boothId"), nil)
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.Contains(path, "/staff") {
		if r.Method == http.MethodPatch {
			item, ok := s.updateStoredRecord(w, r, domainStaff, r.PathValue("staffId"), map[string]any{"boothId": r.PathValue("boothId")})
			if ok {
				s.ok(w, r, item)
			}
			return
		}
		item, ok := s.createStoredRecord(w, r, domainStaff, "staff", map[string]any{"boothId": r.PathValue("boothId")}, "staffId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/products") {
		item, ok := s.createStoredRecord(w, r, domainProducts, "product", map[string]any{"boothId": r.PathValue("boothId"), "sellerId": s.currentUserID(r)}, "productId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/status") {
		item, ok := s.updateStoredRecord(w, r, domainProducts, r.PathValue("productId"), nil)
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/images") {
		item, ok := s.createStoredRecord(w, r, domainUploads, "upload", map[string]any{"productId": r.PathValue("productId")}, "uploadId", "imageId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/inventory/adjustments") {
		item, ok := s.createStoredRecord(w, r, domainInventory, "inventory", map[string]any{"productId": r.PathValue("productId")}, "adjustmentId", "inventoryId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/orders/") {
		item, ok := s.updateStoredRecord(w, r, domainOrders, r.PathValue("orderId"), map[string]any{"sellerAction": strings.TrimPrefix(strings.TrimPrefix(path, "/api/seller/orders/"+r.PathValue("orderId")), "/")})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pickup-vouchers/") && strings.HasSuffix(path, "/redeem") {
		item, ok := s.updateStoredRecord(w, r, domainPickupVouchers, r.PathValue("voucherId"), map[string]any{"redeemedAt": time.Now().UTC().Format(time.RFC3339)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pay/payment-intents/") {
		item, ok := s.updateStoredRecord(w, r, domainPaymentIntents, r.PathValue("intentId"), map[string]any{"paymentAction": strings.TrimPrefix(strings.TrimPrefix(path, "/api/seller/pay/payment-intents/"+r.PathValue("intentId")), "/")})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pay/payments/") && strings.HasSuffix(path, "/refund") {
		item, ok := s.updateStoredRecord(w, r, domainPayments, r.PathValue("paymentId"), map[string]any{"refundedAt": time.Now().UTC().Format(time.RFC3339)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/inquiries/") && strings.HasSuffix(path, "/replies") {
		item, ok := s.createStoredRecord(w, r, "inquiry_replies", "reply", map[string]any{"inquiryId": r.PathValue("inquiryId"), "sellerId": s.currentUserID(r)}, "replyId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/notices") {
		item, ok := s.createStoredRecord(w, r, domainNotices, "notice", map[string]any{"boothId": r.PathValue("boothId")}, "noticeId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if path == "/api/admin/ranking-rules" {
		item, ok := s.createStoredRecord(w, r, domainRankingRules, "ranking-rule", nil, "ruleId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	domain := "actions_" + strings.NewReplacer("/", "_", "-", "_").Replace(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/"))
	item, ok := s.createStoredRecord(w, r, domain, "action", map[string]any{"method": r.Method, "path": r.URL.Path}, "actionId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleSellerLogin(w http.ResponseWriter, r *http.Request) {
	if !s.githubAuth.Configured() {
		s.fail(w, r, http.StatusServiceUnavailable, "GITHUB_OAUTH_NOT_CONFIGURED", "GitHub OAuth 환경변수가 설정되지 않았습니다.", map[string]any{"required": []string{"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET", "GITHUB_OAUTH_REDIRECT_URI"}})
		return
	}
	state := randomToken()
	redirectAfter := envDefault(r.URL.Query().Get("redirectAfter"), env("SELLER_AUTH_SUCCESS_REDIRECT_URL", "http://localhost:5174/"))
	expiresAt := time.Now().Add(10 * time.Minute)
	if err := s.store.saveOAuthState(r.Context(), oauthState{Value: state, Role: "seller", RedirectAfter: redirectAfter, ExpiresAt: expiresAt}); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "OAuth state를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"provider": "github", "authorizeUrl": s.githubAuth.AuthorizeURL(state), "state": state, "role": "seller", "expiresAt": expiresAt.Format(time.RFC3339)})
}

func (s *server) handleSellerMe(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.sessionFromRequest(r); ok && containsString(session.User.Roles, "seller") {
		s.ok(w, r, map[string]any{"id": session.User.ID, "displayName": envDefault(session.User.Name, session.User.Login), "githubLogin": session.User.Login, "roles": session.User.Roles})
		return
	}
	if _, ok := s.sessionFromRequest(r); !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	s.fail(w, r, http.StatusForbidden, "SELLER_ROLE_REQUIRED", "셀러 권한이 필요합니다.", nil)
}

func (s *server) handleSellerBooths(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBooths, 100, recordFilter{Field: "sellerId", Value: s.currentUserID(r)})
}

func (s *server) handleSellerBooth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	if r.Method == http.MethodPatch {
		item, ok := s.updateStoredRecord(w, r, domainBooths, id, map[string]any{"sellerId": s.currentUserID(r)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	item, found, err := s.store.get(r.Context(), domainBooths, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스를 읽지 못했습니다.", map[string]any{"boothId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "BOOTH_NOT_FOUND", "부스를 찾을 수 없습니다.", map[string]any{"boothId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSellerStaff(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainStaff, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSellerProducts(w http.ResponseWriter, r *http.Request) {
	s.handleBoothProducts(w, r)
}

func (s *server) handleSellerProduct(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		item, ok := s.updateStoredRecord(w, r, domainProducts, r.PathValue("productId"), map[string]any{"sellerId": s.currentUserID(r)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	s.handleBoothProduct(w, r)
}

func (s *server) handleInventory(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainInventory, 100, recordFilter{Field: "productId", Value: r.PathValue("productId")})
}

func (s *server) handlePurchaseLimits(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productId")
	if r.Method == http.MethodPatch {
		item, ok := s.putStoredRecord(w, r, domainPurchaseLimits, productID, map[string]any{"productId": productID})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	item, found, err := s.store.get(r.Context(), domainPurchaseLimits, productID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "구매 제한을 읽지 못했습니다.", map[string]any{"productId": productID, "cause": err.Error()})
		return
	}
	if !found {
		s.ok(w, r, nil)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSellerOrders(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainOrders, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleVoucherVerify(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	code := envDefault(stringValue(body["voucherId"]), stringValue(body["code"]))
	items, _, ok := s.listStoredRecords(w, r, domainPickupVouchers, 1000)
	if !ok {
		return
	}
	for _, item := range items {
		if stringValue(item["id"]) == code || stringValue(item["code"]) == code {
			s.ok(w, r, item)
			return
		}
	}
	s.fail(w, r, http.StatusNotFound, "VOUCHER_NOT_FOUND", "픽업 바우처를 찾을 수 없습니다.", nil)
}

func (s *server) handleBarcodeLookup(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	code := envDefault(stringValue(body["barcode"]), stringValue(body["code"]))
	items, _, ok := s.listStoredRecords(w, r, domainPayBarcodes, 1000)
	if !ok {
		return
	}
	for _, item := range items {
		if stringValue(item["id"]) == code || stringValue(item["code"]) == code {
			s.ok(w, r, item)
			return
		}
	}
	s.fail(w, r, http.StatusNotFound, "BARCODE_NOT_FOUND", "결제 바코드를 찾을 수 없습니다.", nil)
}

func (s *server) handlePaymentIntent(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainPaymentIntents, "payment-intent", map[string]any{"sellerId": s.currentUserID(r)}, "intentId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleSellerPayments(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainPayments, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleVisitVerify(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainVisits, "visit", map[string]any{"boothId": r.PathValue("boothId")}, "visitId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleVisits(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainVisits, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleBoothRanking(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listStoredRecords(w, r, domainBoothRankings, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
	if !ok {
		return
	}
	s.ok(w, r, firstRecord(items))
}

func (s *server) handleSellerDashboard(w http.ResponseWriter, r *http.Request) {
	boothID := r.PathValue("boothId")
	products, _, ok := s.listStoredRecords(w, r, domainProducts, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	orders, _, ok := s.listStoredRecords(w, r, domainOrders, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	payments, _, ok := s.listStoredRecords(w, r, domainPayments, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	visits, _, ok := s.listStoredRecords(w, r, domainVisits, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	revenue := 0
	for _, payment := range payments {
		revenue += amountValue(payment)
	}
	s.ok(w, r, map[string]any{"boothId": boothID, "productCount": len(products), "orderCount": len(orders), "paymentCount": len(payments), "visitCount": len(visits), "revenue": amount("DMC", revenue)})
}

func (s *server) handleSettlements(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainSettlements, 100, recordFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSettlement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("settlementId")
	item, found, err := s.store.get(r.Context(), domainSettlements, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "정산을 읽지 못했습니다.", map[string]any{"settlementId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "SETTLEMENT_NOT_FOUND", "정산을 찾을 수 없습니다.", map[string]any{"settlementId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSalesReport(w http.ResponseWriter, r *http.Request) {
	boothID := r.PathValue("boothId")
	payments, _, ok := s.listStoredRecords(w, r, domainPayments, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	total := 0
	for _, payment := range payments {
		total += amountValue(payment)
	}
	s.ok(w, r, map[string]any{"boothId": boothID, "paymentCount": len(payments), "totalAmount": amount("DMC", total), "payments": payments})
}

func (s *server) handleInventoryReport(w http.ResponseWriter, r *http.Request) {
	boothID := r.PathValue("boothId")
	products, _, ok := s.listStoredRecords(w, r, domainProducts, 1000, recordFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"boothId": boothID, "products": products})
}

func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainExports, "export", map[string]any{"boothId": r.PathValue("boothId"), "sellerId": s.currentUserID(r)}, "exportId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	festivals, _, ok := s.listStoredRecords(w, r, domainFestivals, 1000)
	if !ok {
		return
	}
	booths, _, ok := s.listStoredRecords(w, r, domainBooths, 1000)
	if !ok {
		return
	}
	users, _, ok := s.listStoredRecords(w, r, domainUsers, 1000)
	if !ok {
		return
	}
	orders, _, ok := s.listStoredRecords(w, r, domainOrders, 1000)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"festivalCount": len(festivals), "boothCount": len(booths), "userCount": len(users), "orderCount": len(orders)})
}

func (s *server) handleAdminFestivals(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainFestivals, 100)
}

func (s *server) handleAdminFestivalCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainFestivals, "festival", nil, "festivalId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminFestivalUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainFestivals, r.PathValue("festivalId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminBooths(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBooths, 100, recordFilter{Field: "festivalId", Value: r.URL.Query().Get("festivalId")})
}

func (s *server) handleAdminBoothCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainBooths, "booth", nil, "boothId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminBoothUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainBooths, r.PathValue("boothId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminBoothCategories(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainBoothCategories, 100)
}

func (s *server) handleAdminBoothCategoryCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainBoothCategories, "category", nil, "categoryId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminBoothCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainBoothCategories, r.PathValue("categoryId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminMapCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainMaps, "map", nil, "mapId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainUsers, 100)
}

func (s *server) handleAdminUsersImport(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestRecord(w, r)
	if !ok {
		return
	}
	importRecord, err := s.store.put(r.Context(), domainUserImports, recordID(body, "user-import", "importId"), body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "사용자 가져오기를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	imported := 0
	if users, ok := body["users"].([]any); ok {
		for _, raw := range users {
			user, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, err := s.store.put(r.Context(), domainUsers, recordID(user, "user", "userId", "studentId", "email"), user); err != nil {
				s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "사용자를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
				return
			}
			imported++
		}
	}
	importRecord["importedCount"] = imported
	s.created(w, r, importRecord)
}

func (s *server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userId")
	item, found, err := s.store.get(r.Context(), domainUsers, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "사용자를 읽지 못했습니다.", map[string]any{"userId": id, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다.", map[string]any{"userId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainUsers, r.PathValue("userId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainRoles, 100)
}

func (s *server) handleAdminRoleAssignmentCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainRoleAssignments, "role-assignment", nil, "assignmentId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminRoleAssignmentDelete(w http.ResponseWriter, r *http.Request) {
	if s.deleteStoredRecord(w, r, domainRoleAssignments, r.PathValue("assignmentId")) {
		s.ok(w, r, map[string]any{"deleted": true, "assignmentId": r.PathValue("assignmentId")})
	}
}

func (s *server) handleAdminWallets(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainWalletBalances, 100, recordFilter{Field: "userId", Value: r.URL.Query().Get("userId")})
}

func (s *server) handleAdminWalletAdjustment(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainWalletAdjustments, "wallet-adjustment", nil, "adjustmentId")
	if !ok {
		return
	}
	ledger := map[string]any{}
	for key, value := range item {
		ledger[key] = value
	}
	ledger["type"] = envDefault(stringValue(ledger["type"]), "admin-adjustment")
	_, err := s.store.put(r.Context(), domainLedgerTransactions, stringValue(item["id"]), ledger)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "지갑 조정 내역을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.created(w, r, item)
}

func (s *server) handleAdminLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainLedgerTransactions, 100, recordFilter{Field: "userId", Value: r.URL.Query().Get("userId")})
}

func (s *server) handleAdminLedgerExports(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainLedgerExports, 100)
}

func (s *server) handleAdminRewardRuleCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainRewardRules, "reward-rule", nil, "ruleId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminRewardRuleUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainRewardRules, r.PathValue("ruleId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminNotices(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainNotices, 100)
}

func (s *server) handleAdminNoticeCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainNotices, "notice", nil, "noticeId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminNoticeUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainNotices, r.PathValue("noticeId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminPromotions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainPromotions, 100, recordFilter{Field: "placement", Value: r.URL.Query().Get("placement")})
}

func (s *server) handleAdminPromotionCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainPromotions, "promotion", nil, "promotionId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminPromotionUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainPromotions, r.PathValue("promotionId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminNotificationSend(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainNotifications, "notification", map[string]any{"sentAt": time.Now().UTC().Format(time.RFC3339)}, "notificationId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainUploads, "upload", nil, "uploadId", "fileId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminWorldcupTeams(w http.ResponseWriter, r *http.Request) {
	seen := map[string]worldcupTeam{}
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	for _, match := range matches {
		seen[match.Home.CountryCode] = match.Home
		seen[match.Away.CountryCode] = match.Away
	}
	out := []worldcupTeam{}
	for _, team := range seen {
		team.Score = nil
		out = append(out, team)
	}
	s.ok(w, r, out)
}

func (s *server) handleAdminWorldcupTeamCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainWorldcupTeams, "worldcup-team", nil, "teamId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminWorldcupMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := s.worldcupMatches(r.Context())
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	local, _, ok := s.listStoredRecords(w, r, domainWorldcupMatches, 100)
	if !ok {
		return
	}
	out := make([]any, 0, len(matches)+len(local))
	for _, match := range matches {
		out = append(out, match)
	}
	for _, match := range local {
		out = append(out, match)
	}
	s.ok(w, r, out)
}

func (s *server) handleAdminWorldcupMatchCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainWorldcupMatches, "worldcup-match", nil, "matchId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminWorldcupMatchUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateStoredRecord(w, r, domainWorldcupMatches, r.PathValue("matchId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminWorldcupLineupsPut(w http.ResponseWriter, r *http.Request) {
	item, ok := s.putStoredRecord(w, r, domainWorldcupLineups, r.PathValue("matchId"), map[string]any{"matchId": r.PathValue("matchId")})
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminWorldcupStatsPut(w http.ResponseWriter, r *http.Request) {
	item, ok := s.putStoredRecord(w, r, domainWorldcupStats, r.PathValue("matchId"), map[string]any{"matchId": r.PathValue("matchId")})
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminPredictionSettle(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, "prediction_settlements", "prediction-settlement", map[string]any{"matchId": r.PathValue("matchId"), "settledAt": time.Now().UTC().Format(time.RFC3339)}, "settlementId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminWorldcupPredictions(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainWorldcupPredictions, 100, recordFilter{Field: "matchId", Value: r.URL.Query().Get("matchId")})
}

func (s *server) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainAuditLogs, 100)
}

func (s *server) handleAdminSystemHealth(w http.ResponseWriter, r *http.Request) {
	footballStatus := "not_configured"
	if s.football.apiKey != "" {
		footballStatus = "configured"
	}
	databaseStatus := "ok"
	if err := s.store.health(r.Context()); err != nil {
		databaseStatus = "unavailable"
	}
	s.ok(w, r, map[string]any{"api": "ok", "database": databaseStatus, "payments": "not_configured", "apiFootball": footballStatus, "checkedAt": time.Now().Format(time.RFC3339)})
}

func (s *server) handleAdminSystemJobs(w http.ResponseWriter, r *http.Request) {
	s.respondRecordList(w, r, domainSystemJobs, 100)
}

func (s *server) handleAdminIncidentCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createStoredRecord(w, r, domainIncidents, "incident", nil, "incidentId")
	if ok {
		s.created(w, r, item)
	}
}

type githubOAuthClient struct {
	clientID     string
	clientSecret string
	redirectURI  string
	scopes       string
	authURL      string
	tokenURL     string
	apiBaseURL   string
	http         *http.Client
}

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
	Description string `json:"error_description,omitempty"`
}

type githubUserResponse struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
}

type githubEmailResponse struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility"`
}

type githubRepositoryResponse struct {
	FullName string `json:"full_name"`
	Archived bool   `json:"archived"`
	Disabled bool   `json:"disabled"`
	Fork     bool   `json:"fork"`
}

type githubRepositoryCommitResponse struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubAPIError struct {
	Path   string
	Status int
	Body   string
}

func (e githubAPIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("GitHub API %s returned status %d", e.Path, e.Status)
	}
	return fmt.Sprintf("GitHub API %s returned status %d: %s", e.Path, e.Status, e.Body)
}

func newGitHubOAuthClientFromEnv() *githubOAuthClient {
	return &githubOAuthClient{
		clientID:     env("GITHUB_OAUTH_CLIENT_ID", ""),
		clientSecret: env("GITHUB_OAUTH_CLIENT_SECRET", ""),
		redirectURI:  env("GITHUB_OAUTH_REDIRECT_URI", env("PUBLIC_BASE_URL", "http://localhost:8080")+"/api/auth/github/callback"),
		scopes:       env("GITHUB_OAUTH_SCOPES", "read:user user:email repo"),
		authURL:      env("GITHUB_OAUTH_AUTHORIZE_URL", "https://github.com/login/oauth/authorize"),
		tokenURL:     env("GITHUB_OAUTH_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		apiBaseURL:   strings.TrimRight(env("GITHUB_API_BASE_URL", "https://api.github.com"), "/"),
		http:         &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *githubOAuthClient) Configured() bool {
	return c.clientID != "" && c.clientSecret != "" && c.redirectURI != ""
}

func (c *githubOAuthClient) AuthorizeURL(state string) string {
	values := url.Values{}
	values.Set("client_id", c.clientID)
	values.Set("redirect_uri", c.redirectURI)
	values.Set("scope", c.scopes)
	values.Set("state", state)
	values.Set("allow_signup", "true")
	return c.authURL + "?" + values.Encode()
}

func (c *githubOAuthClient) Exchange(ctx context.Context, code string) (githubTokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", c.clientID)
	values.Set("client_secret", c.clientSecret)
	values.Set("code", code)
	values.Set("redirect_uri", c.redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return githubTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return githubTokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubTokenResponse{}, fmt.Errorf("GitHub token endpoint returned status %d", resp.StatusCode)
	}

	token := githubTokenResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return githubTokenResponse{}, err
	}
	if token.Error != "" {
		return githubTokenResponse{}, fmt.Errorf("%s: %s", token.Error, token.Description)
	}
	if token.AccessToken == "" {
		return githubTokenResponse{}, errors.New("GitHub access token이 비어 있습니다")
	}
	return token, nil
}

func (c *githubOAuthClient) User(ctx context.Context, accessToken string) (githubUserResponse, error) {
	user := githubUserResponse{}
	if err := c.githubGet(ctx, accessToken, "/user", &user); err != nil {
		return githubUserResponse{}, err
	}
	if user.ID == 0 || user.Login == "" {
		return githubUserResponse{}, errors.New("GitHub 사용자 정보를 읽을 수 없습니다")
	}
	return user, nil
}

func (c *githubOAuthClient) PrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	emails := []githubEmailResponse{}
	if err := c.githubGet(ctx, accessToken, "/user/emails", &emails); err != nil {
		return "", err
	}
	for _, item := range emails {
		if item.Primary && item.Verified {
			return item.Email, nil
		}
	}
	for _, item := range emails {
		if item.Verified {
			return item.Email, nil
		}
	}
	return "", nil
}

func (c *githubOAuthClient) Commits(ctx context.Context, accessToken, login string, from, to time.Time) ([]githubCommitItem, error) {
	if accessToken == "" {
		return nil, errors.New("GitHub access token is empty")
	}
	if login == "" {
		return nil, errors.New("GitHub login is empty")
	}
	maxPages := queryIntValue(env("GITHUB_COMMIT_MAX_PAGES", "5"), 5)
	if maxPages < 1 {
		maxPages = 1
	}
	repositories, err := c.UserRepositories(ctx, accessToken, login)
	if err != nil {
		return nil, err
	}
	all := []githubCommitItem{}
	for _, repository := range repositories {
		commits, err := c.RepositoryCommits(ctx, accessToken, login, repository, from, to, maxPages)
		if err != nil {
			slog.Warn("skip github repository commits", "repository", repository, "error", err)
			continue
		}
		all = append(all, commits...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].OccurredAt.After(all[j].OccurredAt)
	})
	return all, nil
}

func (c *githubOAuthClient) UserRepositories(ctx context.Context, accessToken, login string) ([]string, error) {
	maxPages := queryIntValue(env("GITHUB_REPOSITORY_MAX_PAGES", "10"), 10)
	if maxPages < 1 {
		maxPages = 1
	}
	repositories := []string{}
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		values := url.Values{}
		values.Set("affiliation", "owner,collaborator,organization_member")
		values.Set("visibility", "all")
		values.Set("sort", "updated")
		values.Set("direction", "desc")
		values.Set("per_page", "100")
		values.Set("page", strconv.Itoa(page))
		response := []githubRepositoryResponse{}
		if err := c.githubGet(ctx, accessToken, "/user/repos?"+values.Encode(), &response); err != nil {
			return nil, err
		}
		for _, repo := range response {
			if repo.FullName == "" || repo.Archived || repo.Disabled || seen[repo.FullName] {
				continue
			}
			seen[repo.FullName] = true
			repositories = append(repositories, repo.FullName)
		}
		if len(response) < 100 {
			break
		}
	}
	sort.Strings(repositories)
	return repositories, nil
}

func (c *githubOAuthClient) RepositoryCommits(ctx context.Context, accessToken, login, repository string, from, to time.Time, maxPages int) ([]githubCommitItem, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository %q", repository)
	}
	toEnd := to.AddDate(0, 0, 1).Add(-time.Nanosecond)
	out := []githubCommitItem{}
	for page := 1; page <= maxPages; page++ {
		values := url.Values{}
		values.Set("author", login)
		values.Set("since", from.Format(time.RFC3339))
		values.Set("until", toEnd.Format(time.RFC3339))
		values.Set("per_page", "100")
		values.Set("page", strconv.Itoa(page))

		path := fmt.Sprintf("/repos/%s/%s/commits?%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), values.Encode())
		response := []githubRepositoryCommitResponse{}
		if err := c.githubGet(ctx, accessToken, path, &response); err != nil {
			var apiErr githubAPIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
				break
			}
			return nil, err
		}
		for _, item := range response {
			title := strings.TrimSpace(strings.Split(item.Commit.Message, "\n")[0])
			authorLogin := ""
			if item.Author != nil {
				authorLogin = item.Author.Login
			}
			out = append(out, githubCommitItem{
				SHA:          item.SHA,
				Repository:   repository,
				Message:      item.Commit.Message,
				Title:        title,
				AuthorName:   item.Commit.Author.Name,
				AuthorEmail:  item.Commit.Author.Email,
				AuthorLogin:  authorLogin,
				OccurredAt:   item.Commit.Author.Date,
				HTMLURL:      item.HTMLURL,
				RewardPoints: 10,
			})
		}
		if len(response) < 100 {
			break
		}
	}
	return out, nil
}

func (c *githubOAuthClient) githubGet(ctx context.Context, accessToken, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-GitHub-Api-Version", env("GITHUB_API_VERSION", "2022-11-28"))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return githubAPIError{Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

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
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("api-football status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
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
		Home:   worldcupTeam{ID: strconv.Itoa(f.Teams.Home.ID), CountryCode: countryCode(f.Teams.Home.Name), Name: koreanTeamName(f.Teams.Home.Name), Logo: f.Teams.Home.Logo, Score: f.Goals.Home},
		Away:   worldcupTeam{ID: strconv.Itoa(f.Teams.Away.ID), CountryCode: countryCode(f.Teams.Away.Name), Name: koreanTeamName(f.Teams.Away.Name), Logo: f.Teams.Away.Logo, Score: f.Goals.Away},
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
	if err != nil {
		return defaultValue
	}
	return value
}

func queryIntValue(raw string, defaultValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultValue
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
