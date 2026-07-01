package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	initialSignupDMC     = 40000
	initialSignupPoints  = 10000
	predictionCurrency   = "POINT"
	commitRewardPoints   = 500
	commitRewardLimit    = 10
	commitRewardCurrency = "POINT"
	commitDailyGoal      = 10
	commitStreakCurrency = "POINT"
	commitStreakType     = "github-commit-streak-reward"
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
	activeFilters := activeResourceFilters(filters)
	if len(activeFilters) > 0 || strings.TrimSpace(r.URL.Query().Get("q")) != "" {
		storeLimit = 1000
	}
	var items []map[string]any
	var err error
	if len(activeFilters) > 0 {
		items, err = s.store.listFiltered(r.Context(), domain, activeFilters, storeLimit)
	} else {
		items, err = s.store.list(r.Context(), domain, storeLimit)
	}
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "데이터를 읽지 못했습니다.", map[string]any{"domain": domain, "cause": err.Error()})
		return nil, limit, false
	}
	sortResources(items)
	items = filterResources(items, r.URL.Query().Get("q"))
	if len(items) > limit {
		items = items[:limit]
	}
	slog.Debug("resource list loaded",
		"request_id", requestID(r),
		"domain", domain,
		"limit", limit,
		"store_limit", storeLimit,
		"query", truncateLogString(r.URL.Query().Get("q")),
		"filters", resourceFiltersForLog(filters),
		"result_count", len(items),
	)
	return items, limit, true
}

func activeResourceFilters(filters []resourceFilter) []resourceFilter {
	out := make([]resourceFilter, 0, len(filters))
	for _, filter := range filters {
		if strings.TrimSpace(filter.Field) == "" || strings.TrimSpace(filter.Value) == "" {
			continue
		}
		out = append(out, filter)
	}
	return out
}

func (s *server) respondResourceList(w http.ResponseWriter, r *http.Request, domain string, defaultLimit int, filters ...resourceFilter) {
	items, limit, ok := s.listResources(w, r, domain, defaultLimit, filters...)
	if !ok {
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func sellerOrderActionExtras(r *http.Request) map[string]any {
	orderID := r.PathValue("orderId")
	prefix := "/api/seller/orders/" + orderID
	return map[string]any{"sellerAction": strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")}
}

func (s *server) requestMap(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	return s.requestPayload(w, r)
}

func (s *server) failResourceCommand(w http.ResponseWriter, r *http.Request, domain, id, action string, err error) {
	if errors.Is(err, errResourceNotFound) {
		s.fail(w, r, http.StatusNotFound, "RECORD_NOT_FOUND", "대상 데이터를 찾을 수 없습니다.", map[string]any{"domain": domain, "id": id})
		return
	}
	message := "데이터를 저장하지 못했습니다."
	if action == "update" {
		message = "데이터를 수정하지 못했습니다."
	}
	details := map[string]any{"domain": domain, "cause": err.Error()}
	if id != "" {
		details["id"] = id
	}
	s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", message, details)
}

func (s *server) deleteResource(w http.ResponseWriter, r *http.Request, domain, id string) bool {
	if err := s.store.delete(r.Context(), domain, id); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "데이터를 삭제하지 못했습니다.", map[string]any{"domain": domain, "id": id, "cause": err.Error()})
		return false
	}
	slog.Debug("resource deleted", "request_id", requestID(r), "domain", domain, "id", id)
	return true
}

func (s *server) requestPayload(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	if r.Body == nil {
		return map[string]any{}, true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes())
	defer r.Body.Close()
	var payload any
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, http.ErrBodyReadAfterClose) {
			return map[string]any{}, true
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "요청 본문이 너무 큽니다.", map[string]any{"limit": maxBytesErr.Limit})
			return nil, false
		}
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", map[string]any{"cause": err.Error()})
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != nil && !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "요청 본문이 너무 큽니다.", map[string]any{"limit": maxBytesErr.Limit})
			return nil, false
		}
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "요청 본문을 읽을 수 없습니다.", map[string]any{"cause": err.Error()})
		return nil, false
	} else if err == nil {
		s.fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "JSON 본문에는 하나의 값만 포함할 수 있습니다.", nil)
		return nil, false
	}
	if payload == nil {
		return map[string]any{}, true
	}
	if data, ok := normalizeJSON(payload).(map[string]any); ok {
		slog.Debug("http request payload decoded",
			"request_id", requestID(r),
			"method", r.Method,
			"path", r.URL.Path,
			"payload_keys", sortedMapKeys(data),
			"payload", sanitizeLogValue(data),
		)
		return data, true
	}
	data := map[string]any{"payload": normalizeJSON(payload)}
	slog.Debug("http request payload decoded",
		"request_id", requestID(r),
		"method", r.Method,
		"path", r.URL.Path,
		"payload_keys", sortedMapKeys(data),
		"payload", sanitizeLogValue(data),
	)
	return data, true
}

func sortedMapKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func resourceFiltersForLog(filters []resourceFilter) []map[string]string {
	out := make([]map[string]string, 0, len(filters))
	for _, filter := range filters {
		value := truncateLogString(filter.Value)
		if sensitiveLogKey(filter.Field) {
			value = "[REDACTED]"
		}
		out = append(out, map[string]string{
			"field": filter.Field,
			"value": value,
		})
	}
	return out
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

func resourceBelongsToUser(item map[string]any, userID string) bool {
	if userID == "" {
		return false
	}
	return firstNonEmpty(stringValue(item["userId"]), stringValue(item["customerId"])) == userID
}

func resourceBelongsToBooth(item map[string]any, boothID string) bool {
	if boothID == "" {
		return false
	}
	return stringValue(item["boothId"]) == boothID
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

func positiveIntegerValue(value any) (int, bool) {
	maxInt := int64(^uint(0) >> 1)
	switch v := value.(type) {
	case int:
		return v, v > 0
	case int64:
		if v <= 0 || v > maxInt {
			return 0, false
		}
		return int(v), true
	case float64:
		if v <= 0 || math.Trunc(v) != v || v > float64(maxInt) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		if err == nil && i > 0 && i <= maxInt {
			return int(i), true
		}
		return 0, false
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || i <= 0 || i > maxInt {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func strictAmountValue(item map[string]any) (int, bool) {
	fields := []string{"totalAmount", "amount", "price", "unitAmount"}
	for _, field := range fields {
		if value, ok := item[field]; ok {
			if amountMap, ok := value.(map[string]any); ok {
				if n, ok := positiveIntegerValue(amountMap["value"]); ok {
					return n, true
				}
				return 0, false
			}
			if n, ok := positiveIntegerValue(value); ok {
				return n, true
			}
			return 0, false
		}
	}
	return 0, false
}

func normalizedCurrency(currency string) (string, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	switch currency {
	case "DMC", "POINT":
		return currency, true
	default:
		return "", false
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
