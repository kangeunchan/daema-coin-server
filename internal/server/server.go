package server

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
	"golang.org/x/crypto/bcrypt"
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
	store      *postgresStore
	football   *footballClient
	githubAuth *githubOAuthClient
}

func Run(ctx context.Context) error {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("load .env", "error", err)
	}

	store, err := openPostgresStore(ctx, env("DATABASE_URL", "postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.close()

	s := &server{
		store:      store,
		football:   newFootballClientFromEnv(),
		githubAuth: newGitHubOAuthClientFromEnv(),
	}
	if err := s.ensureBootstrapAccounts(ctx); err != nil {
		return fmt.Errorf("bootstrap internal accounts: %w", err)
	}
	if envBool("PREDICTION_SETTLEMENT_WORKER_ENABLED", true) {
		go s.runPredictionSettlementWorker(ctx, envDuration("PREDICTION_SETTLEMENT_INTERVAL", time.Minute))
	}

	port := env("PORT", "8080")
	addr := ":" + port

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: envDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	slog.Info("starting daema coin server", "addr", addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), envDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second))
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
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
	mux.HandleFunc("POST /api/auth/admin/login", s.handleAdminLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/github/app/setup", s.handleGitHubAppSetup)
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
	mux.HandleFunc("DELETE /api/customer/worldcup/matches/{matchId}/predictions", s.handlePredictionCancel)
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
	mux.HandleFunc("GET /api/admin/accounts", s.handleAdminAccounts)
	mux.HandleFunc("POST /api/admin/accounts", s.handleAdminAccountCreate)
	mux.HandleFunc("PATCH /api/admin/accounts/{accountId}", s.handleAdminAccountUpdate)
	mux.HandleFunc("POST /api/admin/accounts/{accountId}/reset-password", s.handleAdminAccountResetPassword)
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

	return corsMiddleware(s.authzMiddleware(mux))
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

const (
	roleCustomer = "customer"
	roleAdmin    = "admin"
	roleBooth    = "booth"
)

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
	AccountID string   `json:"accountId,omitempty"`
	BoothID   string   `json:"boothId,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Roles     []string `json:"roles"`
}

type internalAccount struct {
	ID                  string `json:"id"`
	LoginID             string `json:"loginId"`
	PasswordHash        string `json:"passwordHash"`
	Role                string `json:"role"`
	Status              string `json:"status"`
	DisplayName         string `json:"displayName,omitempty"`
	BoothID             string `json:"boothId,omitempty"`
	ForcePasswordChange bool   `json:"forcePasswordChange"`
	CreatedBy           string `json:"createdBy,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
	LastLoginAt         string `json:"lastLoginAt,omitempty"`
}

type internalAccountInput struct {
	LoginID             string
	Password            string
	Role                string
	DisplayName         string
	BoothID             string
	ForcePasswordChange bool
	CreatedBy           string
}

func (s *server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if !s.githubAuth.Configured() {
		s.fail(w, r, http.StatusServiceUnavailable, "GITHUB_OAUTH_NOT_CONFIGURED", "GitHub OAuth 환경변수가 설정되지 않았습니다.", map[string]any{"required": []string{"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET", "GITHUB_OAUTH_REDIRECT_URI"}})
		return
	}

	state := randomToken()
	role := roleCustomer
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
	if installURL, shouldInstall := s.githubAppInstallRedirectURL(r.Context(), session, redirectURL); shouldInstall {
		http.Redirect(w, r, installURL, http.StatusFound)
		return
	}
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
	if found, err := s.store.customerProfileExists(r.Context(), session.User.ID); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "학생 프로필을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	} else if !found {
		s.ok(w, r, map[string]any{"status": "profile_required"})
		return
	}
	if err := s.grantSignupBonus(r.Context(), session.User); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "가입 보상을 지급하지 못했습니다.", map[string]any{"cause": err.Error()})
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
	profile, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	profile["userId"] = session.User.ID
	profile["githubLogin"] = session.User.Login
	profile["githubId"] = session.User.GitHubID
	item, err := s.store.saveCustomerProfile(r.Context(), session.User, profile)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "학생 프로필을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if err := s.grantSignupBonus(r.Context(), session.User); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "가입 보상을 지급하지 못했습니다.", map[string]any{"cause": err.Error()})
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

	stateItem, hasState, err := s.store.oauthState(ctx, state)
	if err != nil {
		return authSession{}, "", err
	}
	if hasState {
		_ = s.store.deleteOAuthState(ctx, state)
	}
	if !hasState {
		return authSession{}, "", errors.New("state가 없거나 이미 사용되었습니다")
	}
	if time.Now().After(stateItem.ExpiresAt) {
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

	role := roleCustomer
	_ = roleOverride
	user := authUser{
		ID:        "github-" + strconv.FormatInt(githubUser.ID, 10),
		GitHubID:  githubUser.ID,
		Login:     githubUser.Login,
		Name:      githubUser.Name,
		Email:     githubUser.Email,
		AvatarURL: githubUser.AvatarURL,
		HTMLURL:   githubUser.HTMLURL,
		Provider:  "github",
		Roles:     rolesForGitHubUser(role, githubUser.Email, githubUser.Login),
	}
	if !containsString(user.Roles, role) {
		return authSession{}, "", fmt.Errorf("%s 권한으로 로그인할 수 없는 GitHub 계정입니다", role)
	}

	session := authSession{Token: randomToken(), GitHubAccessToken: githubToken.AccessToken, User: user, Role: role, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := s.store.saveSession(ctx, session); err != nil {
		return authSession{}, "", err
	}

	return session, stateItem.RedirectAfter, nil
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
	http.SetCookie(w, sessionCookie("", time.Time{}, -1))
	s.ok(w, r, map[string]any{"loggedOut": true})
}

func (s *server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	s.handleInternalLogin(w, r, roleAdmin)
}

func (s *server) handleInternalLogin(w http.ResponseWriter, r *http.Request, requiredRole string) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	loginID := firstNonEmpty(stringValue(body["loginId"]), stringValue(body["username"]), stringValue(body["login"]))
	password := stringValue(body["password"])
	if loginID == "" || password == "" {
		s.fail(w, r, http.StatusBadRequest, "INVALID_CREDENTIALS", "계정 ID와 비밀번호가 필요합니다.", nil)
		return
	}

	account, found, err := s.store.internalAccountByLogin(r.Context(), loginID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "계정을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !found || !checkPassword(account.PasswordHash, password) {
		s.fail(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "계정 ID 또는 비밀번호가 올바르지 않습니다.", nil)
		return
	}
	if account.Status != "active" {
		s.fail(w, r, http.StatusForbidden, "ACCOUNT_DISABLED", "비활성화된 계정입니다.", nil)
		return
	}
	if normalizeInternalRole(account.Role) != requiredRole {
		s.fail(w, r, http.StatusForbidden, "ROLE_NOT_ALLOWED", "해당 로그인 화면에서 사용할 수 없는 계정입니다.", nil)
		return
	}

	account.LastLoginAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := s.store.saveInternalAccount(r.Context(), account); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "로그인 상태를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}

	user := authUser{
		ID:        account.ID,
		Login:     account.LoginID,
		Name:      account.DisplayName,
		AccountID: account.ID,
		BoothID:   account.BoothID,
		Provider:  "internal",
		Roles:     []string{normalizeInternalRole(account.Role)},
	}
	session := authSession{Token: randomToken(), User: user, Role: normalizeInternalRole(account.Role), ExpiresAt: time.Now().Add(envDuration("INTERNAL_SESSION_TTL", 12*time.Hour))}
	if err := s.store.saveSession(r.Context(), session); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "세션을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	setSessionCookie(w, session)
	s.created(w, r, map[string]any{
		"accessToken": session.Token,
		"tokenType":   "Bearer",
		"expiresAt":   session.ExpiresAt.Format(time.RFC3339),
		"user":        session.User,
		"role":        session.Role,
		"account":     sanitizeInternalAccount(account),
	})
}

func (s *server) ensureBootstrapAccounts(ctx context.Context) error {
	adminLogin := env("BOOTSTRAP_ADMIN_LOGIN", "")
	adminPassword := env("BOOTSTRAP_ADMIN_PASSWORD", "")
	if adminLogin == "" && adminPassword == "" {
		return nil
	}
	if adminLogin == "" || adminPassword == "" {
		return errors.New("BOOTSTRAP_ADMIN_LOGIN and BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if _, found, err := s.store.internalAccountByLogin(ctx, adminLogin); err != nil {
		return err
	} else if found {
		return nil
	}
	_, err := s.createInternalAccount(ctx, internalAccountInput{LoginID: adminLogin, Password: adminPassword, Role: roleAdmin, DisplayName: "Bootstrap Admin", ForcePasswordChange: true, CreatedBy: "bootstrap"})
	return err
}

func (s *server) createInternalAccount(ctx context.Context, input internalAccountInput) (internalAccount, error) {
	loginID := strings.TrimSpace(input.LoginID)
	if loginID == "" {
		return internalAccount{}, errors.New("loginId is required")
	}
	if len(input.Password) < 10 {
		return internalAccount{}, errors.New("password must be at least 10 characters")
	}
	role := normalizeInternalRole(input.Role)
	if role == roleBooth && strings.TrimSpace(input.BoothID) == "" {
		return internalAccount{}, errors.New("boothId is required for booth accounts")
	}
	if _, found, err := s.store.internalAccountByLogin(ctx, loginID); err != nil {
		return internalAccount{}, err
	} else if found {
		return internalAccount{}, errors.New("account already exists")
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return internalAccount{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	account := internalAccount{
		ID:                  internalAccountID(loginID),
		LoginID:             loginID,
		PasswordHash:        passwordHash,
		Role:                role,
		Status:              "active",
		DisplayName:         strings.TrimSpace(input.DisplayName),
		BoothID:             strings.TrimSpace(input.BoothID),
		ForcePasswordChange: input.ForcePasswordChange,
		CreatedBy:           input.CreatedBy,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	created, err := s.store.saveInternalAccount(ctx, account)
	if err != nil {
		return internalAccount{}, err
	}
	if !created {
		return internalAccount{}, errors.New("account already exists")
	}
	return account, nil
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
	http.SetCookie(w, sessionCookie(session.Token, session.ExpiresAt, 0))
}

func sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	cookie := &http.Cookie{
		Name:     "daema_session",
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: sessionCookieSameSite(),
		Secure:   sessionCookieSecure(),
	}
	if domain := env("SESSION_COOKIE_DOMAIN", ""); domain != "" {
		cookie.Domain = domain
	}
	return cookie
}

func sessionCookieSameSite() http.SameSite {
	switch strings.ToLower(env("SESSION_COOKIE_SAMESITE", "lax")) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}

func sessionCookieSecure() bool {
	value := strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE"))
	if value != "" {
		return envBool("SESSION_COOKIE_SECURE", false)
	}
	return strings.HasPrefix(env("GITHUB_OAUTH_REDIRECT_URI", ""), "https://") || strings.HasPrefix(env("PUBLIC_BASE_URL", ""), "https://")
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (s *server) authzMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/admin/"):
			if _, ok := s.requireRole(w, r, roleAdmin); !ok {
				return
			}
		case strings.HasPrefix(path, "/api/seller/"):
			session, ok := s.requireRole(w, r, roleBooth)
			if !ok {
				return
			}
			if !s.requireBoothScope(w, r, session) {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) requireBoothScope(w http.ResponseWriter, r *http.Request, session authSession) bool {
	boothID := boothIDFromSellerPath(r.URL.Path)
	if boothID == "" {
		return true
	}
	if session.User.BoothID == "" || session.User.BoothID != boothID {
		s.fail(w, r, http.StatusForbidden, "BOOTH_SCOPE_REQUIRED", "해당 부스에 접근할 권한이 없습니다.", map[string]any{"boothId": boothID})
		return false
	}
	return true
}

func boothIDFromSellerPath(path string) string {
	const prefix = "/api/seller/booths/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *server) requireRole(w http.ResponseWriter, r *http.Request, role string) (authSession, bool) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return authSession{}, false
	}
	if !sessionHasRole(session, role) {
		code := "FORBIDDEN"
		message := "요청한 리소스에 접근할 권한이 없습니다."
		if role == roleAdmin {
			code = "ADMIN_ROLE_REQUIRED"
			message = "관리자 권한이 필요합니다."
		}
		if role == roleBooth {
			code = "BOOTH_ROLE_REQUIRED"
			message = "부스 계정 권한이 필요합니다."
		}
		s.fail(w, r, http.StatusForbidden, code, message, nil)
		return authSession{}, false
	}
	return session, true
}

func sessionHasRole(session authSession, role string) bool {
	for _, item := range session.User.Roles {
		if item == role {
			return true
		}
		if role == roleBooth && item == "seller" {
			return true
		}
	}
	return false
}

func normalizeInternalRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case roleAdmin:
		return roleAdmin
	case roleBooth, "seller", "booth_owner", "booth_staff":
		return roleBooth
	default:
		return roleBooth
	}
}

func rolesForGitHubUser(requestedRole, email, login string) []string {
	_, _, _ = requestedRole, email, login
	return []string{roleCustomer}
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

func sessionTokenHashID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "session-" + hex.EncodeToString(sum[:])
}

func internalAccountID(loginID string) string {
	normalized := strings.ToLower(strings.TrimSpace(loginID))
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", "@", "_at_", ".", "_")
	return "account-" + replacer.Replace(normalized)
}

func hashPassword(password string) (string, error) {
	raw, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func checkPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func sanitizeInternalAccount(account internalAccount) map[string]any {
	data, err := mapFromStruct(account)
	if err != nil {
		return map[string]any{"id": account.ID, "loginId": account.LoginID, "role": account.Role, "status": account.Status}
	}
	delete(data, "passwordHash")
	return data
}

const (
	initialSignupDMC     = 40000
	initialSignupPoints  = 10000
	predictionCurrency   = "POINT"
	commitRewardPoints   = 500
	commitRewardLimit    = 10
	commitRewardCurrency = "POINT"
)

const (
	resourceNavigation            = "navigation"
	resourceCustomerProfiles      = "customer_profiles"
	resourceNotifications         = "notifications"
	resourceSearchSuggestions     = "search_suggestions"
	resourceSearchDocuments       = "search_documents"
	resourceNotices               = "notices"
	resourceWalletBalances        = "wallet_balances"
	resourceBenefits              = "benefits"
	resourceBenefitClaims         = "benefit_claims"
	resourceShortcuts             = "shortcuts"
	resourcePromotions            = "promotions"
	resourceLedgerTransactions    = "ledger_transactions"
	resourceUserRankings          = "rankings_user"
	resourceBoothRankings         = "rankings_booth"
	resourceFestivalBanners       = "festival_banners"
	resourcePayBarcodes           = "pay_barcodes"
	resourceBoothCategories       = "booth_categories"
	resourceBoothBanners          = "booth_banners"
	resourceBooths                = "booths"
	resourceProducts              = "products"
	resourceProductViews          = "product_views"
	resourceBoothCheckins         = "booth_checkins"
	resourceCartItems             = "cart_items"
	resourceOrders                = "orders"
	resourceFavorites             = "favorites"
	resourceInquiries             = "inquiries"
	resourceShares                = "shares"
	resourceWorldcupPredictions   = "worldcup_predictions"
	resourcePredictionSettlements = "prediction_settlements"
	resourceFeatures              = "features"
	resourceStaff                 = "staff"
	resourceInventory             = "inventory"
	resourcePurchaseLimits        = "purchase_limits"
	resourcePickupVouchers        = "pickup_vouchers"
	resourcePaymentIntents        = "payment_intents"
	resourcePayments              = "payments"
	resourceVisits                = "visits"
	resourceSettlements           = "settlements"
	resourceExports               = "exports"
	resourceFestivals             = "festivals"
	resourceMaps                  = "maps"
	resourceUsers                 = "users"
	resourceUserImports           = "user_imports"
	resourceRoles                 = "roles"
	resourceRoleAssignments       = "role_assignments"
	resourceWalletAdjustments     = "wallet_adjustments"
	resourceLedgerExports         = "ledger_exports"
	resourceRewardRules           = "reward_rules"
	resourceUploads               = "uploads"
	resourceWorldcupTeams         = "worldcup_teams"
	resourceWorldcupMatches       = "worldcup_matches"
	resourceWorldcupLineups       = "worldcup_lineups"
	resourceWorldcupStats         = "worldcup_stats"
	resourceAuditLogs             = "audit_logs"
	resourceSystemJobs            = "system_jobs"
	resourceIncidents             = "incidents"
	resourceRankingRules          = "ranking_rules"
	resourceGitHubCommits         = "github_commits"
	resourceGitHubInstallations   = "github_installations"
	resourceInternalAccounts      = "auth.internal_accounts"
	resourceAnalyticsImpressions  = "analytics_impressions"
	resourceInquiryReplies        = "inquiry_replies"
)

type resourceFilter struct {
	Field string
	Value string
}

func (s *server) listResources(w http.ResponseWriter, r *http.Request, domain string, defaultLimit int, filters ...resourceFilter) ([]map[string]any, int, bool) {
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
	sortResources(items)
	items = filterResources(items, r.URL.Query().Get("q"), filters...)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, limit, true
}

func (s *server) respondResourceList(w http.ResponseWriter, r *http.Request, domain string, defaultLimit int, filters ...resourceFilter) {
	items, limit, ok := s.listResources(w, r, domain, defaultLimit, filters...)
	if !ok {
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func (s *server) createResource(w http.ResponseWriter, r *http.Request, domain, prefix string, extras map[string]any, idCandidates ...string) (map[string]any, bool) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return nil, false
	}
	for key, value := range extras {
		body[key] = value
	}
	id := resourceID(body, prefix, idCandidates...)
	item, err := s.store.put(r.Context(), domain, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 저장하지 못했습니다.", map[string]any{"domain": domain, "cause": err.Error()})
		return nil, false
	}
	return item, true
}

func (s *server) updateResource(w http.ResponseWriter, r *http.Request, domain, id string, extras map[string]any) (map[string]any, bool) {
	body, ok := s.requestPayload(w, r)
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

func (s *server) putResource(w http.ResponseWriter, r *http.Request, domain, id string, extras map[string]any) (map[string]any, bool) {
	body, ok := s.requestPayload(w, r)
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

func (s *server) deleteResource(w http.ResponseWriter, r *http.Request, domain, id string) bool {
	if err := s.store.delete(r.Context(), domain, id); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 삭제하지 못했습니다.", map[string]any{"domain": domain, "id": id, "cause": err.Error()})
		return false
	}
	return true
}

func (s *server) requestPayload(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
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

func resourceID(data map[string]any, prefix string, candidates ...string) string {
	candidates = append([]string{"id"}, candidates...)
	for _, key := range candidates {
		if value := stringValue(data[key]); value != "" {
			return value
		}
	}
	return generatedID(prefix)
}

func filterResources(items []map[string]any, query string, filters ...resourceFilter) []map[string]any {
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
		if matched && query != "" && !resourceContains(item, query) {
			matched = false
		}
		if matched {
			out = append(out, item)
		}
	}
	return out
}

func resourceContains(value any, query string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(strings.ToLower(v), query)
	case map[string]any:
		for _, child := range v {
			if resourceContains(child, query) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if resourceContains(child, query) {
				return true
			}
		}
	default:
		return strings.Contains(strings.ToLower(fmt.Sprint(v)), query)
	}
	return false
}

func sortResources(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		left, leftOK := numericValue(items[i]["sortOrder"])
		right, rightOK := numericValue(items[j]["sortOrder"])
		if leftOK && rightOK {
			return left < right
		}
		return leftOK && !rightOK
	})
}

func firstItem(items []map[string]any) any {
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

func boolValue(value any, defaultValue bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return defaultValue
		}
	default:
		return defaultValue
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

func amountValue(item map[string]any) int {
	fields := []string{"totalAmount", "amount", "price", "unitAmount"}
	for _, field := range fields {
		if value, ok := item[field]; ok {
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

func walletBalanceID(userID, currency string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "wallet-" + replacer.Replace(userID) + "-" + strings.ToUpper(currency)
}

func ledgerID(parts ...string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			clean = append(clean, replacer.Replace(part))
		}
	}
	return strings.Join(clean, "-")
}

func walletCurrencyLabel(currency string) string {
	switch strings.ToUpper(currency) {
	case "POINT":
		return "대마포인트"
	default:
		return "대마코인"
	}
}

func (s *server) walletBalance(ctx context.Context, userID, currency string) (int, error) {
	return s.store.walletBalance(ctx, userID, currency)
}

func (s *server) createLedgerAndAdjustWallet(ctx context.Context, user authUser, id, txType, direction, currency string, value int, extras map[string]any) (bool, error) {
	return s.store.createLedgerAndAdjustWallet(ctx, user, id, txType, direction, currency, value, extras)
}

func (s *server) grantSignupBonus(ctx context.Context, user authUser) error {
	if _, err := s.createLedgerAndAdjustWallet(ctx, user, ledgerID("signup-bonus", "DMC", user.ID), "signup-bonus", "income", "DMC", initialSignupDMC, map[string]any{"description": "회원가입 대마코인 지급"}); err != nil {
		return err
	}
	_, err := s.createLedgerAndAdjustWallet(ctx, user, ledgerID("signup-bonus", "POINT", user.ID), "signup-bonus", "income", "POINT", initialSignupPoints, map[string]any{"description": "회원가입 대마포인트 지급"})
	return err
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
	s.respondResourceList(w, r, resourceNavigation, 50)
}

func (s *server) handleNotificationSummary(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceNotifications, 20)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"unreadCount": countUnread(items), "latest": firstItem(items)})
}

func (s *server) handleCartSummary(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceCartItems, 500, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
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
	s.respondResourceList(w, r, resourceSearchSuggestions, 20)
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	s.respondResourceList(w, r, resourceSearchDocuments, 50, resourceFilter{Field: "scope", Value: scope})
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	notices, _, ok := s.listResources(w, r, resourceNotices, 20)
	if !ok {
		return
	}
	wallet, err := s.store.walletBalances(r.Context(), s.currentUserID(r), 20)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "지갑 잔액을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	shortcuts, _, ok := s.listResources(w, r, resourceShortcuts, 50)
	if !ok {
		return
	}
	promotions, _, ok := s.listResources(w, r, resourcePromotions, 20, resourceFilter{Field: "placement", Value: "home"})
	if !ok {
		return
	}
	transactions, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), 6)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "최근 거래내역을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	userRanking, _, ok := s.listResources(w, r, resourceUserRankings, 10)
	if !ok {
		return
	}
	boothRanking, _, ok := s.listResources(w, r, resourceBoothRankings, 10)
	if !ok {
		return
	}
	banners, _, ok := s.listResources(w, r, resourceFestivalBanners, 10)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{
		"notice":             firstItem(notices),
		"wallet":             map[string]any{"balances": wallet},
		"shortcuts":          shortcuts,
		"promotions":         promotions,
		"recentTransactions": transactions,
		"rankings":           map[string]any{"userPoint": userRanking, "boothPoint": boothRanking},
		"festivalBanner":     firstItem(banners),
	})
}

func (s *server) handleNoticeHighlight(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceNotices, 20)
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleWalletBalances(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.walletBalances(r.Context(), s.currentUserID(r), queryInt(r, "limit", 20))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "지갑 잔액을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"balances": items})
}

func (s *server) handleInterestBenefit(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceBenefits, 10, resourceFilter{Field: "type", Value: "interest"})
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleClaimBenefit(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceBenefitClaims, "benefit-claim", map[string]any{"benefitId": r.PathValue("benefitId"), "userId": s.currentUserID(r)}, "claimId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceShortcuts, 50)
}

func (s *server) handlePromotions(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourcePromotions, 50, resourceFilter{Field: "placement", Value: r.URL.Query().Get("placement")})
}

func (s *server) handleLedgerRecent(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), queryInt(r, "limit", 6))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "최근 거래내역을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: queryInt(r, "limit", 6), HasMore: false})
}

func (s *server) handleRankings(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("type") {
	case "booth":
		s.respondResourceList(w, r, resourceBoothRankings, 20)
	case "user":
		rankings, err := s.userPointRankings(r.Context(), 20)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "개인 랭킹을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		if len(rankings) > 0 {
			s.ok(w, r, rankings)
			return
		}
		s.respondResourceList(w, r, resourceUserRankings, 20)
	default:
		users, err := s.userPointRankings(r.Context(), 20)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "개인 랭킹을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		if len(users) == 0 {
			var ok bool
			users, _, ok = s.listResources(w, r, resourceUserRankings, 20)
			if !ok {
				return
			}
		}
		booths, _, ok := s.listResources(w, r, resourceBoothRankings, 20)
		if !ok {
			return
		}
		s.ok(w, r, map[string]any{"userPoint": users, "boothPoint": booths})
	}
}

func (s *server) userPointRankings(ctx context.Context, limit int) ([]map[string]any, error) {
	wallets, err := s.store.walletBalances(ctx, "", 10000)
	if err != nil {
		return nil, err
	}
	profiles, err := s.store.customerProfiles(ctx, 10000)
	if err != nil {
		return nil, err
	}
	profileByUserID := map[string]map[string]any{}
	for _, profile := range profiles {
		userID := stringValue(profile["userId"])
		if userID == "" {
			continue
		}
		profileByUserID[userID] = profile
	}
	items := make([]map[string]any, 0, len(wallets))
	for _, wallet := range wallets {
		if strings.ToUpper(stringValue(wallet["currency"])) != "POINT" {
			continue
		}
		userID := stringValue(wallet["userId"])
		if userID == "" {
			continue
		}
		points := amountValue(wallet)
		if points <= 0 {
			if n, ok := numericValue(wallet["balance"]); ok {
				points = int(n)
			}
		}
		if points <= 0 {
			continue
		}
		profile := profileByUserID[userID]
		name := envDefault(
			stringValue(profile["name"]),
			envDefault(stringValue(profile["displayName"]), envDefault(stringValue(profile["githubLogin"]), "사용자")),
		)
		items = append(items, map[string]any{
			"userId":      userID,
			"githubLogin": firstNonEmpty(stringValue(wallet["githubLogin"]), stringValue(profile["githubLogin"])),
			"name":        name,
			"points":      amount("POINT", points),
			"score":       points,
			"totalPoint":  points,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return amountValue(map[string]any{"amount": items[i]["points"]}) > amountValue(map[string]any{"amount": items[j]["points"]})
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	for i := range items {
		items[i]["rank"] = i + 1
	}
	return items, nil
}

func (s *server) handleFestivalBanner(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceFestivalBanners, 10)
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleCreateBarcode(w http.ResponseWriter, r *http.Request) {
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	item, ok := s.createResource(w, r, resourcePayBarcodes, "barcode", map[string]any{"code": randomToken()[:24], "userId": s.currentUserID(r), "expiresAt": expiresAt}, "barcodeId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleBoothCategories(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBoothCategories, 100)
}

func (s *server) handleBoothBanners(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBoothBanners, 50)
}

func (s *server) handleBoothHome(w http.ResponseWriter, r *http.Request) {
	categories, _, ok := s.listResources(w, r, resourceBoothCategories, 100)
	if !ok {
		return
	}
	banners, _, ok := s.listResources(w, r, resourceBoothBanners, 20)
	if !ok {
		return
	}
	products, _, ok := s.listResources(w, r, resourceProducts, 50)
	if !ok {
		return
	}
	booths, _, ok := s.listResources(w, r, resourceBooths, 50)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"categories": categories, "banners": banners, "products": products, "booths": booths})
}

func (s *server) handleBoothProducts(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceProducts, 100,
		resourceFilter{Field: "boothId", Value: r.PathValue("boothId")},
		resourceFilter{Field: "categoryId", Value: r.URL.Query().Get("categoryId")},
		resourceFilter{Field: "status", Value: r.URL.Query().Get("status")},
	)
}

func (s *server) handleBoothProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("productId")
	item, found, err := s.store.get(r.Context(), resourceProducts, id)
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
	item, ok := s.createResource(w, r, resourceProductViews, "product-view", map[string]any{"productId": r.PathValue("productId"), "userId": s.currentUserID(r)}, "viewId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleCustomerBooth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	item, found, err := s.store.get(r.Context(), resourceBooths, id)
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
	item, ok := s.createResource(w, r, resourceBoothCheckins, "checkin", map[string]any{"boothId": r.PathValue("boothId"), "userId": s.currentUserID(r)}, "checkinId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleBoothRankings(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBoothRankings, 20)
}

func (s *server) handleCart(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceCartItems, 100, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) handleCartItem(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceCartItems, "cart-item", map[string]any{"userId": s.currentUserID(r)}, "cartItemId", "productId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleOrderPreview(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	productID := stringValue(body["productId"])
	if productID == "" {
		s.fail(w, r, http.StatusBadRequest, "PRODUCT_ID_REQUIRED", "productId가 필요합니다.", nil)
		return
	}
	product, found, err := s.store.get(r.Context(), resourceProducts, productID)
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
	item, ok := s.createResource(w, r, resourceOrders, "order", map[string]any{"userId": s.currentUserID(r)}, "orderId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleOrderDetail(w http.ResponseWriter, r *http.Request) {
	id := envDefault(r.PathValue("orderId"), r.PathValue("settlementId"))
	item, found, err := s.store.get(r.Context(), resourceOrders, id)
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
	item, ok := s.createResource(w, r, resourceFavorites, "favorite", map[string]any{"userId": s.currentUserID(r)}, "favoriteId", "targetId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("targetId")
	_ = s.deleteResource(w, r, resourceFavorites, targetID)
	items, _, ok := s.listResources(w, r, resourceFavorites, 100, resourceFilter{Field: "targetId", Value: targetID}, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	for _, item := range items {
		_ = s.store.delete(r.Context(), resourceFavorites, stringValue(item["id"]))
	}
	s.ok(w, r, map[string]any{"deleted": true, "targetId": targetID})
}

func (s *server) handleInquiries(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceInquiries, 100,
		resourceFilter{Field: "userId", Value: s.currentUserID(r)},
		resourceFilter{Field: "boothId", Value: r.PathValue("boothId")},
	)
}

func (s *server) handleInquiryCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceInquiries, "inquiry", map[string]any{"userId": s.currentUserID(r)}, "inquiryId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleShare(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceShares, "share", map[string]any{"userId": s.currentUserID(r)}, "shareId")
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
	rewards := map[string]int{}
	for _, commit := range commits {
		day := commit.OccurredAt.In(appLocation()).Format("2006-01-02")
		counts[day]++
		rewards[day] += commit.RewardPoints
	}
	items := []map[string]any{}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		count := counts[key]
		level := commitLevel(count)
		items = append(items, map[string]any{"date": key, "count": count, "level": level, "rewardedPoints": rewards[key]})
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
	installed, _ := s.githubAppInstalledForUser(r.Context(), session.User)
	s.ok(w, r, map[string]any{
		"configured": installURL != "",
		"installUrl": installURL,
		"installed":  installed,
		"login":      session.User.Login,
	})
}

func (s *server) handleGitHubAppSetup(w http.ResponseWriter, r *http.Request) {
	redirectURL := env("AUTH_SUCCESS_REDIRECT_URL", env("PUBLIC_BASE_URL", "http://localhost:5173")+"/login")
	session, ok := s.sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, appendQuery(redirectURL, map[string]string{"login": "required", "githubApp": "setup"}), http.StatusFound)
		return
	}
	installationID := strings.TrimSpace(r.URL.Query().Get("installation_id"))
	setupAction := strings.TrimSpace(r.URL.Query().Get("setup_action"))
	if installationID != "" {
		_, _ = s.store.put(r.Context(), resourceGitHubInstallations, "user-"+session.User.ID, map[string]any{
			"installationId": installationID,
			"setupAction":    setupAction,
			"userId":         session.User.ID,
			"githubLogin":    session.User.Login,
			"githubId":       session.User.GitHubID,
			"connectedAt":    time.Now().UTC().Format(time.RFC3339),
		})
	}
	http.Redirect(w, r, appendQuery(redirectURL, map[string]string{"login": "success", "role": session.Role, "githubApp": "installed"}), http.StatusFound)
}

func (s *server) githubAppInstallRedirectURL(ctx context.Context, session authSession, redirectAfter string) (string, bool) {
	if session.Role != "customer" || !envBool("GITHUB_APP_INSTALL_ON_LOGIN", true) {
		return "", false
	}
	installURL := env("GITHUB_APP_INSTALL_URL", "")
	if installURL == "" {
		return "", false
	}
	installed, err := s.githubAppInstalledForUser(ctx, session.User)
	if err != nil {
		slog.Warn("check github app installation", "user_id", session.User.ID, "error", err)
	}
	if installed {
		return "", false
	}
	return appendQuery(installURL, map[string]string{"state": randomToken(), "redirect_after": redirectAfter}), true
}

func (s *server) githubAppInstalledForUser(ctx context.Context, user authUser) (bool, error) {
	if user.ID == "" {
		return false, nil
	}
	if _, ok, err := s.store.get(ctx, resourceGitHubInstallations, "user-"+user.ID); err != nil || ok {
		return ok, err
	}
	items, err := s.store.list(ctx, resourceGitHubInstallations, 1000)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		action := strings.ToLower(stringValue(item["action"]))
		if action == "deleted" || action == "suspend" {
			continue
		}
		if stringValue(item["githubLogin"]) == user.Login || stringValue(item["accountLogin"]) == user.Login || stringValue(item["senderLogin"]) == user.Login {
			return true, nil
		}
		if account, _ := item["account"].(map[string]any); stringValue(account["login"]) == user.Login {
			return true, nil
		}
		if sender, _ := item["sender"].(map[string]any); stringValue(sender["login"]) == user.Login {
			return true, nil
		}
	}
	return false, nil
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
	if account, _ := payload["account"].(map[string]any); account != nil {
		payload["accountLogin"] = stringValue(account["login"])
	}
	if sender, _ := payload["sender"].(map[string]any); sender != nil {
		payload["senderLogin"] = stringValue(sender["login"])
	}
	_, err := s.store.put(ctx, resourceGitHubInstallations, installationID, payload)
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
		rewardPoints, rewardUser, err := s.githubCommitReward(ctx, authorLogin, occurredAt)
		if err != nil {
			return count, err
		}
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
			RewardPoints: rewardPoints,
		}
		data, err := mapFromStruct(item)
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
		commitEventID := githubCommitID(payload.Repository.FullName, commit.ID)
		_, created, err := s.store.create(ctx, resourceGitHubCommits, commitEventID, data)
		if err != nil {
			return count, err
		}
		if !created {
			continue
		}
		if rewardPoints > 0 && rewardUser.ID != "" {
			_, err = s.createLedgerAndAdjustWallet(ctx, rewardUser, ledgerID("github-commit-reward", rewardUser.ID, payload.Repository.FullName, commit.ID), "github-commit-reward", "income", commitRewardCurrency, rewardPoints, map[string]any{
				"description": "GitHub 커밋 리워드",
				"commitSha":   commit.ID,
				"repository":  payload.Repository.FullName,
				"htmlUrl":     commit.URL,
				"occurredAt":  occurredAt.UTC().Format(time.RFC3339),
			})
			if err != nil {
				return count, err
			}
		}
		count++
	}
	return count, nil
}

func (s *server) githubCommitReward(ctx context.Context, authorLogin string, occurredAt time.Time) (int, authUser, error) {
	user, ok, err := s.authUserForGitHubLogin(ctx, authorLogin)
	if err != nil || !ok {
		return 0, authUser{}, err
	}
	rewardedCount, err := s.rewardedGitHubCommitCountForDay(ctx, authorLogin, occurredAt)
	if err != nil {
		return 0, authUser{}, err
	}
	if rewardedCount >= commitRewardLimit {
		return 0, user, nil
	}
	return commitRewardPoints, user, nil
}

func (s *server) authUserForGitHubLogin(ctx context.Context, login string) (authUser, bool, error) {
	return s.store.authUserByGitHubLogin(ctx, login)
}

func (s *server) rewardedGitHubCommitCountForDay(ctx context.Context, login string, occurredAt time.Time) (int, error) {
	items, err := s.store.list(ctx, resourceGitHubCommits, 10000)
	if err != nil {
		return 0, err
	}
	targetDay := occurredAt.In(appLocation()).Format("2006-01-02")
	count := 0
	for _, item := range items {
		if !strings.EqualFold(stringValue(item["authorLogin"]), login) {
			continue
		}
		if amountValue(map[string]any{"amount": item["rewardedPoints"]}) <= 0 {
			continue
		}
		commit := githubCommitItem{}
		if err := decodeMap(item, &commit); err != nil {
			return 0, err
		}
		if commit.OccurredAt.In(appLocation()).Format("2006-01-02") == targetDay {
			count++
		}
	}
	return count, nil
}

func (s *server) listStoredGitHubCommits(ctx context.Context, login string, from, to time.Time) ([]githubCommitItem, error) {
	items, err := s.store.list(ctx, resourceGitHubCommits, 10000)
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
		if err := decodeMap(item, &commit); err != nil {
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

func githubCommitID(repository, sha string) string {
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
	rewards := map[string]int{}
	labels := map[string]string{}
	now := time.Now().In(appLocation())
	for _, commit := range commits {
		period, label := commitPeriod(commit.OccurredAt, groupBy)
		counts[period]++
		rewards[period] += commit.RewardPoints
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
		out = append(out, map[string]any{"period": period, "label": labels[period], "commitCount": count, "rewardedPoints": rewards[period], "current": period == currentPeriod})
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
	items, _, ok := s.listResources(w, r, resourceWorldcupPredictions, 1000, resourceFilter{Field: "matchId", Value: matchID})
	if !ok {
		return
	}
	counts := map[string]int{"home": 0, "draw": 0, "away": 0}
	myPrediction := ""
	myStakeAmount := 0
	userID := s.currentUserID(r)
	for _, item := range items {
		pick := stringValue(item["pick"])
		if _, ok := counts[pick]; ok {
			counts[pick]++
		}
		if userID != "" && stringValue(item["userId"]) == userID {
			myPrediction = pick
			myStakeAmount = predictionStakeAmount(item)
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
		"matchId":       matchID,
		"homePercent":   percent(counts["home"]),
		"drawPercent":   percent(counts["draw"]),
		"awayPercent":   percent(counts["away"]),
		"totalCount":    total,
		"canPredict":    myPrediction == "" && (!matchStatusKnown || match.Status == "scheduled"),
		"canCancel":     myPrediction != "" && (!matchStatusKnown || match.Status == "scheduled"),
		"myPrediction":  nil,
		"myStakeAmount": nil,
	}
	if matchStatusKnown {
		data["matchStatus"] = match.Status
		data["matchStatusLabel"] = match.StatusLabel
	}
	if myPrediction != "" {
		data["myPrediction"] = myPrediction
		data["myStakeAmount"] = myStakeAmount
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
	body, ok := s.requestPayload(w, r)
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
	stakeAmount, ok := requiredPredictionStakeAmount(body)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "INVALID_PREDICTION_STAKE", "stakeAmount는 필수이며 1 이상 정수여야 합니다.", map[string]any{"stakeAmount": body["stakeAmount"]})
		return
	}
	pointBalance, err := s.walletBalance(r.Context(), session.User.ID, predictionCurrency)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "대마포인트 잔액을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if pointBalance < stakeAmount {
		s.fail(w, r, http.StatusBadRequest, "INSUFFICIENT_POINT_BALANCE", "대마포인트 잔액이 부족합니다.", map[string]any{"balance": pointBalance, "stakeAmount": stakeAmount})
		return
	}
	body["matchId"] = matchID
	body["userId"] = session.User.ID
	body["githubLogin"] = session.User.Login
	body["stakeAmount"] = stakeAmount
	body["currency"] = predictionCurrency
	id := predictionID(matchID, session.User.ID)
	item, created, err := s.store.create(r.Context(), resourceWorldcupPredictions, id, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !created {
		existing, _, _ := s.store.get(r.Context(), resourceWorldcupPredictions, id)
		s.fail(w, r, http.StatusConflict, "PREDICTION_ALREADY_EXISTS", "이미 이 경기에 투표했습니다.", map[string]any{"matchId": matchID, "prediction": existing})
		return
	}
	ledgerCreated, err := s.createLedgerAndAdjustWallet(r.Context(), session.User, ledgerID("prediction-stake", matchID, session.User.ID, strconv.FormatInt(time.Now().UTC().UnixNano(), 10)), "worldcup-prediction-stake", "expense", predictionCurrency, stakeAmount, map[string]any{
		"matchId":     matchID,
		"pick":        pick,
		"stakeAmount": stakeAmount,
		"description": "월드컵 승부예측 참여",
	})
	if err != nil {
		_ = s.store.delete(r.Context(), resourceWorldcupPredictions, id)
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측 포인트 차감에 실패했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !ledgerCreated {
		_ = s.store.delete(r.Context(), resourceWorldcupPredictions, id)
		s.fail(w, r, http.StatusConflict, "PREDICTION_STAKE_ALREADY_PROCESSED", "승부예측 포인트 차감 내역이 이미 존재합니다.", map[string]any{"matchId": matchID})
		return
	}
	s.created(w, r, item)
}

func (s *server) handlePredictionCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	matchID := r.PathValue("matchId")
	match, matchStatusKnown, err := s.worldcupMatchByID(r.Context(), matchID)
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	if matchStatusKnown && match.Status != "scheduled" {
		s.fail(w, r, http.StatusConflict, "PREDICTION_CANCEL_CLOSED", "이미 시작했거나 종료된 경기는 투표를 취소할 수 없습니다.", map[string]any{"matchId": matchID, "status": match.Status, "statusLabel": match.StatusLabel})
		return
	}
	id := predictionID(matchID, session.User.ID)
	existing, found, err := s.store.deleteReturning(r.Context(), resourceWorldcupPredictions, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측을 취소하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "PREDICTION_NOT_FOUND", "취소할 승부예측이 없습니다.", map[string]any{"matchId": matchID})
		return
	}
	stakeAmount := predictionStakeAmount(existing)
	if stakeAmount <= 0 {
		s.fail(w, r, http.StatusConflict, "PREDICTION_CANCEL_FAILED", "환급할 투표 금액을 확인할 수 없습니다.", map[string]any{"matchId": matchID})
		return
	}
	_, err = s.createLedgerAndAdjustWallet(r.Context(), session.User, ledgerID("prediction-cancel", matchID, session.User.ID), "worldcup-prediction-cancel", "income", predictionCurrency, stakeAmount, map[string]any{
		"matchId":     matchID,
		"pick":        stringValue(existing["pick"]),
		"stakeAmount": stakeAmount,
		"description": "월드컵 승부예측 취소 환급",
	})
	if err != nil {
		_, _, _ = s.store.create(r.Context(), resourceWorldcupPredictions, id, existing)
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측 취소 환급에 실패했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{
		"matchId":      matchID,
		"cancelled":    true,
		"refundAmount": stakeAmount,
		"currency":     predictionCurrency,
	})
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

func predictionID(matchID, userID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "prediction-" + replacer.Replace(matchID) + "-" + replacer.Replace(userID)
}

func predictionSettlementID(matchID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "prediction-settlement-" + replacer.Replace(matchID)
}

var (
	errPredictionAlreadySettled    = errors.New("prediction already settled")
	errPredictionNoPredictions     = errors.New("no predictions to settle")
	errPredictionResultUnavailable = errors.New("prediction result is unavailable")
)

func predictionStakeAmount(item map[string]any) int {
	if n, ok := numericValue(item["stakeAmount"]); ok {
		return int(math.Round(n))
	}
	if n, ok := numericValue(item["stake"]); ok {
		return int(math.Round(n))
	}
	return 100
}

func requiredPredictionStakeAmount(item map[string]any) (int, bool) {
	n, ok := numericValue(item["stakeAmount"])
	if !ok || math.Trunc(n) != n {
		return 0, false
	}
	amount := int(n)
	return amount, amount > 0
}

func (s *server) predictionWinningPick(ctx context.Context, matchID string, body map[string]any) (string, error) {
	for _, key := range []string{"winningPick", "result", "pick"} {
		pick := stringValue(body[key])
		if pick == "home" || pick == "draw" || pick == "away" {
			return pick, nil
		}
	}
	match, ok, err := s.worldcupMatchByID(ctx, matchID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("match not found")
	}
	return predictionWinningPickFromMatch(match)
}

func predictionWinningPickFromMatch(match worldcupMatch) (string, error) {
	if match.Home.Score == nil || match.Away.Score == nil {
		return "", errPredictionResultUnavailable
	}
	if *match.Home.Score > *match.Away.Score {
		return "home", nil
	}
	if *match.Home.Score < *match.Away.Score {
		return "away", nil
	}
	return "draw", nil
}

func settlePredictions(matchID, winningPick string, predictions []map[string]any) (map[string]any, []map[string]any, error) {
	if winningPick != "home" && winningPick != "draw" && winningPick != "away" {
		return nil, nil, fmt.Errorf("invalid winning pick %q", winningPick)
	}
	type participant struct {
		prediction map[string]any
		pick       string
		stake      int
		userID     string
		login      string
	}
	participants := []participant{}
	totalPool := 0
	winnerStakeTotal := 0
	loserRefundTotal := 0
	for _, prediction := range predictions {
		pick := stringValue(prediction["pick"])
		stake := predictionStakeAmount(prediction)
		if stake <= 0 || (pick != "home" && pick != "draw" && pick != "away") {
			continue
		}
		item := participant{prediction: prediction, pick: pick, stake: stake, userID: stringValue(prediction["userId"]), login: stringValue(prediction["githubLogin"])}
		participants = append(participants, item)
		totalPool += stake
		if pick == winningPick {
			winnerStakeTotal += stake
		} else {
			loserRefundTotal += loserRefundAmount(stake)
		}
	}
	if len(participants) == 0 {
		return nil, nil, errPredictionNoPredictions
	}
	winnerPayoutPool := totalPool - loserRefundTotal
	winnerPayouts := map[string]int{}
	allocatedWinnerPayout := 0
	winners := []participant{}
	for _, participant := range participants {
		if participant.pick == winningPick {
			winners = append(winners, participant)
		}
	}
	if winnerStakeTotal > 0 {
		sort.SliceStable(winners, func(i, j int) bool {
			if winners[i].stake == winners[j].stake {
				return winners[i].userID < winners[j].userID
			}
			return winners[i].stake > winners[j].stake
		})
		for _, winner := range winners {
			payout := winnerPayoutPool * winner.stake / winnerStakeTotal
			winnerPayouts[winner.userID] = payout
			allocatedWinnerPayout += payout
		}
		for i := 0; allocatedWinnerPayout < winnerPayoutPool && len(winners) > 0; i++ {
			winner := winners[i%len(winners)]
			winnerPayouts[winner.userID]++
			allocatedWinnerPayout++
		}
	}
	ledgerEntries := []map[string]any{}
	resultRows := []map[string]any{}
	for _, participant := range participants {
		payout := 0
		outcome := "lost"
		if participant.pick == winningPick {
			outcome = "won"
			payout = winnerPayouts[participant.userID]
		} else {
			payout = loserRefundAmount(participant.stake)
		}
		row := map[string]any{
			"userId":      participant.userID,
			"githubLogin": participant.login,
			"pick":        participant.pick,
			"stakeAmount": participant.stake,
			"outcome":     outcome,
			"payout":      payout,
		}
		resultRows = append(resultRows, row)
		if payout > 0 && participant.userID != "" {
			ledgerID := predictionSettlementID(matchID) + "-" + participant.userID
			ledgerEntries = append(ledgerEntries, map[string]any{
				"id":          ledgerID,
				"userId":      participant.userID,
				"githubLogin": participant.login,
				"type":        "worldcup-prediction-settlement",
				"matchId":     matchID,
				"pick":        participant.pick,
				"winningPick": winningPick,
				"outcome":     outcome,
				"stakeAmount": participant.stake,
				"direction":   "income",
				"amount":      amount(predictionCurrency, payout),
				"occurredAt":  time.Now().UTC().Format(time.RFC3339),
				"description": "월드컵 승부예측 정산",
			})
		}
	}
	settlement := map[string]any{
		"matchId":             matchID,
		"winningPick":         winningPick,
		"totalPool":           totalPool,
		"winnerStakeTotal":    winnerStakeTotal,
		"loserRefundTotal":    loserRefundTotal,
		"winnerPayoutPool":    winnerPayoutPool,
		"participantCount":    len(participants),
		"winnerCount":         len(winners),
		"loserRefundRate":     0.1,
		"results":             resultRows,
		"ledgerEntryCount":    len(ledgerEntries),
		"allocatedPointTotal": allocatedWinnerPayout + loserRefundTotal,
	}
	return settlement, ledgerEntries, nil
}

type predictionSettlementResult struct {
	Settlement    map[string]any
	LedgerEntries []map[string]any
	Created       bool
}

func (s *server) settleWorldcupPrediction(ctx context.Context, matchID, winningPick, source, note string) (predictionSettlementResult, error) {
	settlementID := predictionSettlementID(matchID)
	if existing, ok, err := s.store.get(ctx, resourcePredictionSettlements, settlementID); err != nil {
		return predictionSettlementResult{}, err
	} else if ok {
		return predictionSettlementResult{Settlement: existing, Created: false}, errPredictionAlreadySettled
	}

	predictions, err := s.store.list(ctx, resourceWorldcupPredictions, 10000)
	if err != nil {
		return predictionSettlementResult{}, err
	}
	predictions = filterResources(predictions, "", resourceFilter{Field: "matchId", Value: matchID})

	settlement, ledgerEntries, err := settlePredictions(matchID, winningPick, predictions)
	if err != nil {
		return predictionSettlementResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	settlement["id"] = settlementID
	settlement["settledAt"] = now
	settlement["source"] = source
	if note != "" {
		settlement["note"] = note
	}

	for _, ledger := range ledgerEntries {
		user := authUser{ID: stringValue(ledger["userId"]), Login: stringValue(ledger["githubLogin"])}
		if user.ID != "" {
			if _, err := s.createLedgerAndAdjustWallet(ctx, user, stringValue(ledger["id"]), stringValue(ledger["type"]), stringValue(ledger["direction"]), predictionCurrency, amountValue(ledger), ledger); err != nil {
				return predictionSettlementResult{}, err
			}
		}
	}

	item, created, err := s.store.create(ctx, resourcePredictionSettlements, settlementID, settlement)
	if err != nil {
		return predictionSettlementResult{}, err
	}
	if !created {
		existing, _, _ := s.store.get(ctx, resourcePredictionSettlements, settlementID)
		return predictionSettlementResult{Settlement: existing, Created: false}, errPredictionAlreadySettled
	}
	return predictionSettlementResult{Settlement: item, LedgerEntries: ledgerEntries, Created: true}, nil
}

func (s *server) runPredictionSettlementWorker(ctx context.Context, interval time.Duration) {
	slog.Info("starting worldcup prediction settlement worker", "interval", interval.String())
	s.runPredictionSettlementCycle(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPredictionSettlementCycle(ctx)
		}
	}
}

func (s *server) runPredictionSettlementCycle(ctx context.Context) {
	startedAt := time.Now().UTC()
	summary := map[string]any{
		"id":        "worldcup-prediction-settlement",
		"name":      "World Cup prediction settlement",
		"type":      "worker",
		"status":    "running",
		"startedAt": startedAt.Format(time.RFC3339),
	}

	matches, err := s.worldcupMatches(ctx)
	if err != nil {
		summary["status"] = "failed"
		summary["error"] = err.Error()
		summary["finishedAt"] = time.Now().UTC().Format(time.RFC3339)
		_, _ = s.store.put(ctx, resourceSystemJobs, "worldcup-prediction-settlement", summary)
		slog.Warn("worldcup prediction settlement worker failed to load matches", "error", err)
		return
	}

	checked := 0
	eligible := 0
	settled := 0
	alreadySettled := 0
	skipped := 0
	failed := 0

	for _, match := range matches {
		checked++
		if match.Status != "finished" {
			continue
		}
		eligible++
		winningPick, err := predictionWinningPickFromMatch(match)
		if err != nil {
			skipped++
			slog.Warn("skip worldcup prediction settlement without result", "match_id", match.ID, "error", err)
			continue
		}
		result, err := s.settleWorldcupPrediction(ctx, match.ID, winningPick, "worker", "경기 종료 자동 정산")
		if errors.Is(err, errPredictionAlreadySettled) {
			alreadySettled++
			continue
		}
		if errors.Is(err, errPredictionNoPredictions) {
			skipped++
			continue
		}
		if err != nil {
			failed++
			slog.Error("worldcup prediction settlement failed", "match_id", match.ID, "error", err)
			continue
		}
		if result.Created {
			settled++
			slog.Info("worldcup prediction settled", "match_id", match.ID, "winning_pick", winningPick)
		}
	}

	status := "ok"
	if failed > 0 {
		status = "partial_failure"
	}
	summary["status"] = status
	summary["checkedCount"] = checked
	summary["eligibleCount"] = eligible
	summary["settledCount"] = settled
	summary["alreadySettledCount"] = alreadySettled
	summary["skippedCount"] = skipped
	summary["failedCount"] = failed
	summary["finishedAt"] = time.Now().UTC().Format(time.RFC3339)
	summary["durationMs"] = time.Since(startedAt).Milliseconds()
	if _, err := s.store.put(ctx, resourceSystemJobs, "worldcup-prediction-settlement", summary); err != nil {
		slog.Warn("store worldcup prediction settlement job status", "error", err)
	}
}

func loserRefundAmount(stake int) int {
	if stake <= 0 {
		return 0
	}
	return int(math.Round(float64(stake) * 0.1))
}

func (s *server) handleLedgerCalendar(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), 1000)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 달력 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
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
	limit := queryInt(r, "limit", 50)
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), limit)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 내역을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func (s *server) handleLedgerAnalysis(w http.ResponseWriter, r *http.Request) {
	month := envDefault(r.URL.Query().Get("month"), time.Now().In(appLocation()).Format("2006-01"))
	items, err := s.store.ledgerTransactions(r.Context(), s.currentUserID(r), 1000)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 분석 데이터를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
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
	s.respondResourceList(w, r, resourceFeatures, 100)
}

func (s *server) handleAccepted(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/customer/analytics/impressions" {
		item, ok := s.createResource(w, r, resourceAnalyticsImpressions, "impression", map[string]any{"userId": s.currentUserID(r)}, "impressionId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/status") {
		item, ok := s.updateResource(w, r, resourceBooths, r.PathValue("boothId"), nil)
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.Contains(path, "/staff") {
		if r.Method == http.MethodPatch {
			item, ok := s.updateResource(w, r, resourceStaff, r.PathValue("staffId"), map[string]any{"boothId": r.PathValue("boothId")})
			if ok {
				s.ok(w, r, item)
			}
			return
		}
		item, ok := s.createResource(w, r, resourceStaff, "staff", map[string]any{"boothId": r.PathValue("boothId")}, "staffId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/products") {
		item, ok := s.createResource(w, r, resourceProducts, "product", map[string]any{"boothId": r.PathValue("boothId"), "sellerId": s.currentUserID(r)}, "productId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/status") {
		item, ok := s.updateResource(w, r, resourceProducts, r.PathValue("productId"), nil)
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/images") {
		item, ok := s.createResource(w, r, resourceUploads, "upload", map[string]any{"productId": r.PathValue("productId")}, "uploadId", "imageId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/products/") && strings.HasSuffix(path, "/inventory/adjustments") {
		item, ok := s.createResource(w, r, resourceInventory, "inventory", map[string]any{"productId": r.PathValue("productId")}, "adjustmentId", "inventoryId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/orders/") {
		item, ok := s.updateResource(w, r, resourceOrders, r.PathValue("orderId"), map[string]any{"sellerAction": strings.TrimPrefix(strings.TrimPrefix(path, "/api/seller/orders/"+r.PathValue("orderId")), "/")})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pickup-vouchers/") && strings.HasSuffix(path, "/redeem") {
		item, ok := s.updateResource(w, r, resourcePickupVouchers, r.PathValue("voucherId"), map[string]any{"redeemedAt": time.Now().UTC().Format(time.RFC3339)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pay/payment-intents/") {
		item, ok := s.updateResource(w, r, resourcePaymentIntents, r.PathValue("intentId"), map[string]any{"paymentAction": strings.TrimPrefix(strings.TrimPrefix(path, "/api/seller/pay/payment-intents/"+r.PathValue("intentId")), "/")})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/pay/payments/") && strings.HasSuffix(path, "/refund") {
		item, ok := s.updateResource(w, r, resourcePayments, r.PathValue("paymentId"), map[string]any{"refundedAt": time.Now().UTC().Format(time.RFC3339)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/inquiries/") && strings.HasSuffix(path, "/replies") {
		item, ok := s.createResource(w, r, resourceInquiryReplies, "reply", map[string]any{"inquiryId": r.PathValue("inquiryId"), "sellerId": s.currentUserID(r)}, "replyId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if strings.HasPrefix(path, "/api/seller/booths/") && strings.HasSuffix(path, "/notices") {
		item, ok := s.createResource(w, r, resourceNotices, "notice", map[string]any{"boothId": r.PathValue("boothId")}, "noticeId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	if path == "/api/admin/ranking-rules" {
		item, ok := s.createResource(w, r, resourceRankingRules, "ranking-rule", nil, "ruleId")
		if ok {
			s.created(w, r, item)
		}
		return
	}
	domain := "actions_" + strings.NewReplacer("/", "_", "-", "_").Replace(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/"))
	item, ok := s.createResource(w, r, domain, "action", map[string]any{"method": r.Method, "path": r.URL.Path}, "actionId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleSellerLogin(w http.ResponseWriter, r *http.Request) {
	s.handleInternalLogin(w, r, roleBooth)
}

func (s *server) handleSellerMe(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.sessionFromRequest(r); ok && sessionHasRole(session, roleBooth) {
		s.ok(w, r, map[string]any{"id": session.User.ID, "displayName": envDefault(session.User.Name, session.User.Login), "loginId": session.User.Login, "boothId": session.User.BoothID, "roles": session.User.Roles})
		return
	}
	if _, ok := s.sessionFromRequest(r); !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	s.fail(w, r, http.StatusForbidden, "BOOTH_ROLE_REQUIRED", "부스 계정 권한이 필요합니다.", nil)
}

func (s *server) handleSellerBooths(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBooths, 100, resourceFilter{Field: "sellerId", Value: s.currentUserID(r)})
}

func (s *server) handleSellerBooth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	if r.Method == http.MethodPatch {
		item, ok := s.updateResource(w, r, resourceBooths, id, map[string]any{"sellerId": s.currentUserID(r)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	item, found, err := s.store.get(r.Context(), resourceBooths, id)
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
	s.respondResourceList(w, r, resourceStaff, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSellerProducts(w http.ResponseWriter, r *http.Request) {
	s.handleBoothProducts(w, r)
}

func (s *server) handleSellerProduct(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		item, ok := s.updateResource(w, r, resourceProducts, r.PathValue("productId"), map[string]any{"sellerId": s.currentUserID(r)})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	s.handleBoothProduct(w, r)
}

func (s *server) handleInventory(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceInventory, 100, resourceFilter{Field: "productId", Value: r.PathValue("productId")})
}

func (s *server) handlePurchaseLimits(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productId")
	if r.Method == http.MethodPatch {
		item, ok := s.putResource(w, r, resourcePurchaseLimits, productID, map[string]any{"productId": productID})
		if ok {
			s.ok(w, r, item)
		}
		return
	}
	item, found, err := s.store.get(r.Context(), resourcePurchaseLimits, productID)
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
	s.respondResourceList(w, r, resourceOrders, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleVoucherVerify(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	code := envDefault(stringValue(body["voucherId"]), stringValue(body["code"]))
	items, _, ok := s.listResources(w, r, resourcePickupVouchers, 1000)
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
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	code := envDefault(stringValue(body["barcode"]), stringValue(body["code"]))
	items, _, ok := s.listResources(w, r, resourcePayBarcodes, 1000)
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
	item, ok := s.createResource(w, r, resourcePaymentIntents, "payment-intent", map[string]any{"sellerId": s.currentUserID(r)}, "intentId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleSellerPayments(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourcePayments, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleVisitVerify(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceVisits, "visit", map[string]any{"boothId": r.PathValue("boothId")}, "visitId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleVisits(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceVisits, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleBoothRanking(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceBoothRankings, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleSellerDashboard(w http.ResponseWriter, r *http.Request) {
	boothID := r.PathValue("boothId")
	products, _, ok := s.listResources(w, r, resourceProducts, 1000, resourceFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	orders, _, ok := s.listResources(w, r, resourceOrders, 1000, resourceFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	payments, _, ok := s.listResources(w, r, resourcePayments, 1000, resourceFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	visits, _, ok := s.listResources(w, r, resourceVisits, 1000, resourceFilter{Field: "boothId", Value: boothID})
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
	s.respondResourceList(w, r, resourceSettlements, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSettlement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("settlementId")
	item, found, err := s.store.get(r.Context(), resourceSettlements, id)
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
	payments, _, ok := s.listResources(w, r, resourcePayments, 1000, resourceFilter{Field: "boothId", Value: boothID})
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
	products, _, ok := s.listResources(w, r, resourceProducts, 1000, resourceFilter{Field: "boothId", Value: boothID})
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"boothId": boothID, "products": products})
}

func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceExports, "export", map[string]any{"boothId": r.PathValue("boothId"), "sellerId": s.currentUserID(r)}, "exportId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.internalAccounts(r.Context(), 1000)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "계정 목록을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, account := range items {
		out = append(out, sanitizeInternalAccount(account))
	}
	s.okPage(w, r, out, &pagination{Limit: 1000, HasMore: false})
}

func (s *server) handleAdminAccountCreate(w http.ResponseWriter, r *http.Request) {
	session, _ := s.sessionFromRequest(r)
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	account, err := s.createInternalAccount(r.Context(), internalAccountInput{
		LoginID:             firstNonEmpty(stringValue(body["loginId"]), stringValue(body["username"]), stringValue(body["login"])),
		Password:            stringValue(body["password"]),
		Role:                stringValue(body["role"]),
		DisplayName:         stringValue(body["displayName"]),
		BoothID:             stringValue(body["boothId"]),
		ForcePasswordChange: boolValue(body["forcePasswordChange"], true),
		CreatedBy:           session.User.ID,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		s.fail(w, r, status, "ACCOUNT_CREATE_FAILED", "계정을 생성하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.created(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminAccountUpdate(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountId")
	account, found, err := s.store.internalAccount(r.Context(), accountID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "계정을 읽지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "계정을 찾을 수 없습니다.", map[string]any{"accountId": accountID})
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	if value := strings.TrimSpace(stringValue(body["displayName"])); value != "" {
		account.DisplayName = value
	}
	if value := strings.TrimSpace(stringValue(body["status"])); value != "" {
		switch value {
		case "active", "disabled", "locked":
			account.Status = value
		default:
			s.fail(w, r, http.StatusBadRequest, "INVALID_ACCOUNT_STATUS", "계정 상태가 올바르지 않습니다.", map[string]any{"status": value})
			return
		}
	}
	if value := strings.TrimSpace(stringValue(body["boothId"])); value != "" {
		account.BoothID = value
	}
	if value, ok := body["forcePasswordChange"]; ok {
		account.ForcePasswordChange = boolValue(value, account.ForcePasswordChange)
	}
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := s.store.saveInternalAccount(r.Context(), account); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "계정을 저장하지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		return
	}
	s.ok(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminAccountResetPassword(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountId")
	account, found, err := s.store.internalAccount(r.Context(), accountID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "계정을 읽지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "계정을 찾을 수 없습니다.", map[string]any{"accountId": accountID})
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	password := stringValue(body["password"])
	if len(password) < 10 {
		s.fail(w, r, http.StatusBadRequest, "INVALID_PASSWORD", "비밀번호는 10자 이상이어야 합니다.", nil)
		return
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "PASSWORD_HASH_FAILED", "비밀번호를 처리하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	account.PasswordHash = passwordHash
	account.ForcePasswordChange = true
	account.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := s.store.saveInternalAccount(r.Context(), account); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "비밀번호를 저장하지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		return
	}
	s.ok(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	festivals, _, ok := s.listResources(w, r, resourceFestivals, 1000)
	if !ok {
		return
	}
	booths, _, ok := s.listResources(w, r, resourceBooths, 1000)
	if !ok {
		return
	}
	users, _, ok := s.listResources(w, r, resourceUsers, 1000)
	if !ok {
		return
	}
	orders, _, ok := s.listResources(w, r, resourceOrders, 1000)
	if !ok {
		return
	}
	s.ok(w, r, map[string]any{"festivalCount": len(festivals), "boothCount": len(booths), "userCount": len(users), "orderCount": len(orders)})
}

func (s *server) handleAdminFestivals(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceFestivals, 100)
}

func (s *server) handleAdminFestivalCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceFestivals, "festival", nil, "festivalId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminFestivalUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceFestivals, r.PathValue("festivalId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminBooths(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBooths, 100, resourceFilter{Field: "festivalId", Value: r.URL.Query().Get("festivalId")})
}

func (s *server) handleAdminBoothCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceBooths, "booth", nil, "boothId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminBoothUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceBooths, r.PathValue("boothId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminBoothCategories(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBoothCategories, 100)
}

func (s *server) handleAdminBoothCategoryCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceBoothCategories, "category", nil, "categoryId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminBoothCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceBoothCategories, r.PathValue("categoryId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminMapCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceMaps, "map", nil, "mapId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceUsers, 100)
}

func (s *server) handleAdminUsersImport(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	importJob, err := s.store.put(r.Context(), resourceUserImports, resourceID(body, "user-import", "importId"), body)
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
			if _, err := s.store.put(r.Context(), resourceUsers, resourceID(user, "user", "userId", "studentId", "email"), user); err != nil {
				s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "사용자를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
				return
			}
			imported++
		}
	}
	importJob["importedCount"] = imported
	s.created(w, r, importJob)
}

func (s *server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userId")
	item, found, err := s.store.get(r.Context(), resourceUsers, id)
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
	item, ok := s.updateResource(w, r, resourceUsers, r.PathValue("userId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceRoles, 100)
}

func (s *server) handleAdminRoleAssignmentCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceRoleAssignments, "role-assignment", nil, "assignmentId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminRoleAssignmentDelete(w http.ResponseWriter, r *http.Request) {
	if s.deleteResource(w, r, resourceRoleAssignments, r.PathValue("assignmentId")) {
		s.ok(w, r, map[string]any{"deleted": true, "assignmentId": r.PathValue("assignmentId")})
	}
}

func (s *server) handleAdminWallets(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.walletBalances(r.Context(), r.URL.Query().Get("userId"), queryInt(r, "limit", 100))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "지갑 잔액을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: queryInt(r, "limit", 100), HasMore: false})
}

func (s *server) handleAdminWalletAdjustment(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	userID := stringValue(body["userId"])
	currency := envDefault(strings.ToUpper(stringValue(body["currency"])), "POINT")
	value := amountValue(body)
	if userID == "" || value <= 0 {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WALLET_ADJUSTMENT", "userId와 1 이상의 amount가 필요합니다.", map[string]any{"userId": userID, "amount": body["amount"]})
		return
	}
	direction := stringValue(body["direction"])
	if direction != "expense" {
		direction = "income"
	}
	id := resourceID(body, "wallet-adjustment", "adjustmentId", "ledgerId", "id")
	txType := envDefault(stringValue(body["type"]), "admin-adjustment")
	body["adminUserId"] = s.currentUserID(r)
	created, err := s.createLedgerAndAdjustWallet(r.Context(), authUser{ID: userID, Login: stringValue(body["githubLogin"])}, id, txType, direction, currency, value, body)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "지갑 조정 내역을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if !created {
		s.fail(w, r, http.StatusConflict, "WALLET_ADJUSTMENT_ALREADY_EXISTS", "이미 처리된 지갑 조정입니다.", map[string]any{"id": id})
		return
	}
	body["id"] = id
	body["type"] = txType
	body["direction"] = direction
	body["currency"] = currency
	body["amount"] = amount(currency, value)
	body["occurredAt"] = time.Now().UTC().Format(time.RFC3339)
	s.created(w, r, body)
}

func (s *server) handleAdminLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ledgerTransactions(r.Context(), r.URL.Query().Get("userId"), queryInt(r, "limit", 100))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "원장 내역을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: queryInt(r, "limit", 100), HasMore: false})
}

func (s *server) handleAdminLedgerExports(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceLedgerExports, 100)
}

func (s *server) handleAdminRewardRuleCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceRewardRules, "reward-rule", nil, "ruleId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminRewardRuleUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceRewardRules, r.PathValue("ruleId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminNotices(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceNotices, 100)
}

func (s *server) handleAdminNoticeCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceNotices, "notice", nil, "noticeId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminNoticeUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceNotices, r.PathValue("noticeId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminPromotions(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourcePromotions, 100, resourceFilter{Field: "placement", Value: r.URL.Query().Get("placement")})
}

func (s *server) handleAdminPromotionCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourcePromotions, "promotion", nil, "promotionId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminPromotionUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourcePromotions, r.PathValue("promotionId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminNotificationSend(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceNotifications, "notification", map[string]any{"sentAt": time.Now().UTC().Format(time.RFC3339)}, "notificationId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceUploads, "upload", nil, "uploadId", "fileId")
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
	item, ok := s.createResource(w, r, resourceWorldcupTeams, "worldcup-team", nil, "teamId")
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
	local, _, ok := s.listResources(w, r, resourceWorldcupMatches, 100)
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
	item, ok := s.createResource(w, r, resourceWorldcupMatches, "worldcup-match", nil, "matchId")
	if ok {
		s.created(w, r, item)
	}
}

func (s *server) handleAdminWorldcupMatchUpdate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.updateResource(w, r, resourceWorldcupMatches, r.PathValue("matchId"), nil)
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminWorldcupLineupsPut(w http.ResponseWriter, r *http.Request) {
	item, ok := s.putResource(w, r, resourceWorldcupLineups, r.PathValue("matchId"), map[string]any{"matchId": r.PathValue("matchId")})
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminWorldcupStatsPut(w http.ResponseWriter, r *http.Request) {
	item, ok := s.putResource(w, r, resourceWorldcupStats, r.PathValue("matchId"), map[string]any{"matchId": r.PathValue("matchId")})
	if ok {
		s.ok(w, r, item)
	}
}

func (s *server) handleAdminPredictionSettle(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	winningPick, err := s.predictionWinningPick(r.Context(), matchID, body)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_PREDICTION_RESULT", "승부예측 결과를 결정할 수 없습니다.", map[string]any{"cause": err.Error()})
		return
	}
	result, err := s.settleWorldcupPrediction(r.Context(), matchID, winningPick, "admin", stringValue(body["note"]))
	if errors.Is(err, errPredictionAlreadySettled) {
		s.fail(w, r, http.StatusConflict, "PREDICTION_ALREADY_SETTLED", "이미 정산된 경기입니다.", map[string]any{"matchId": matchID, "settlement": result.Settlement})
		return
	}
	if errors.Is(err, errPredictionNoPredictions) {
		s.fail(w, r, http.StatusBadRequest, "PREDICTION_SETTLEMENT_FAILED", "승부예측 정산에 실패했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측 정산을 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	result.Settlement["ledgerEntries"] = result.LedgerEntries
	s.created(w, r, result.Settlement)
}

func (s *server) handleAdminWorldcupPredictions(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceWorldcupPredictions, 100, resourceFilter{Field: "matchId", Value: r.URL.Query().Get("matchId")})
}

func (s *server) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceAuditLogs, 100)
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
	s.respondResourceList(w, r, resourceSystemJobs, 100)
}

func (s *server) handleAdminIncidentCreate(w http.ResponseWriter, r *http.Request) {
	item, ok := s.createResource(w, r, resourceIncidents, "incident", nil, "incidentId")
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
				RewardPoints: commitRewardPoints,
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
