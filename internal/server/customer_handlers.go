package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	teacherInfiniteWalletBalance   = 999_999_999
	excludedUserRankingGitHubLogin = "kangeunchan"
)

var boothRankingsLaunchAtKST = time.Date(2026, time.July, 8, 0, 0, 0, 0, time.FixedZone("KST", 9*60*60))

func (s *server) walletBalance(ctx context.Context, userID, currency string) (int, error) {
	return s.store.walletBalance(ctx, userID, currency)
}

func (s *server) createLedgerAndAdjustWallet(ctx context.Context, user authUser, id, txType, direction, currency string, value int, extras map[string]any) (bool, error) {
	return s.store.createLedgerAndAdjustWalletRequest(ctx, newWalletLedgerRequest(user, id, txType, direction, currency, value, extras))
}

func (s *server) grantSignupBonus(ctx context.Context, user authUser) error {
	if err := s.grantSignupBonusCurrency(ctx, user, "DMC", initialSignupDMC, "회원가입 대마코인 지급"); err != nil {
		return err
	}
	return s.grantSignupBonusCurrency(ctx, user, "POINT", initialSignupPoints, "회원가입 대마포인트 지급")
}

func (s *server) grantSignupBonusCurrency(ctx context.Context, user authUser, currency string, amount int, description string) error {
	_, err := s.createLedgerAndAdjustWallet(ctx, user, ledgerID("signup-bonus", currency, user.ID), "signup-bonus", "income", currency, amount, map[string]any{"description": description})
	if errors.Is(err, errLedgerIdempotencyConflict) {
		slog.Warn("signup bonus ledger already exists with legacy idempotency shape",
			"user_id", user.ID,
			"currency", currency,
			"ledger_id", ledgerID("signup-bonus", currency, user.ID),
		)
		return nil
	}
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
	items, _, ok := s.listResources(w, r, resourceNotifications, 200)
	if !ok {
		return
	}
	items = filterCustomerNotifications(items, s.currentUserID(r))
	if len(items) > 20 {
		items = items[:20]
	}
	s.ok(w, r, map[string]any{"unreadCount": countUnread(items), "latest": firstItem(items)})
}

func filterCustomerNotifications(items []map[string]any, userID string) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		targetUserID := firstNonEmpty(
			stringValue(item["userId"]),
			stringValue(item["customerId"]),
			stringValue(item["recipientId"]),
		)
		if targetUserID == "" || targetUserID == userID {
			out = append(out, item)
		}
	}
	return out
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
	wallet, err := s.customerWalletBalances(r, 20)
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
	boothRanking := []map[string]any{}
	if boothRankingsVisibleNow() {
		var ok bool
		boothRanking, _, ok = s.listResources(w, r, resourceBoothRankings, 10)
		if !ok {
			return
		}
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
	items, err := s.customerWalletBalances(r, queryInt(r, "limit", 20))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "지갑 잔액을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"balances": items})
}

func (s *server) customerWalletBalances(r *http.Request, limit int) ([]map[string]any, error) {
	if session, ok := s.sessionFromRequest(r); ok && sessionHasRole(session, roleTeacher) {
		return infiniteTeacherWalletBalances(session.User.ID), nil
	}
	return s.store.walletBalances(r.Context(), s.currentUserID(r), limit)
}

func infiniteTeacherWalletBalances(userID string) []map[string]any {
	return []map[string]any{
		infiniteTeacherWalletBalance(userID, "DMC"),
		infiniteTeacherWalletBalance(userID, "POINT"),
	}
}

func infiniteTeacherWalletBalance(userID, currency string) map[string]any {
	return map[string]any{
		"id":       walletBalanceID(userID, currency),
		"userId":   userID,
		"currency": currency,
		"label":    walletCurrencyLabel(currency),
		"name":     walletCurrencyLabel(currency),
		"balance":  teacherInfiniteWalletBalance,
		"amount":   amount(currency, teacherInfiniteWalletBalance),
		"infinite": true,
	}
}

func (s *server) handleInterestBenefit(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceBenefits, 10, resourceFilter{Field: "type", Value: "interest"})
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleClaimBenefit(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().ClaimBenefit(r.Context(), r.PathValue("benefitId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceBenefitClaims, "", "create", err)
		return
	}
	s.created(w, r, item)
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
		if !boothRankingsVisibleNow() {
			s.okPage(w, r, []map[string]any{}, &pagination{Limit: 20, HasMore: false})
			return
		}
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
		items, _, ok := s.listResources(w, r, resourceUserRankings, 21)
		if !ok {
			return
		}
		s.okPage(w, r, filterUserRankings(items, 20), &pagination{Limit: 20, HasMore: false})
	default:
		users, err := s.userPointRankings(r.Context(), 20)
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "개인 랭킹을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
			return
		}
		if len(users) == 0 {
			var ok bool
			users, _, ok = s.listResources(w, r, resourceUserRankings, 21)
			if !ok {
				return
			}
			users = filterUserRankings(users, 20)
		}
		booths := []map[string]any{}
		if boothRankingsVisibleNow() {
			var ok bool
			booths, _, ok = s.listResources(w, r, resourceBoothRankings, 20)
			if !ok {
				return
			}
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
	excludedUserIDs, err := s.excludedUserRankingAccountIDs(ctx)
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
		if excludedUserIDs[userID] {
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
		item := map[string]any{
			"userId":      userID,
			"githubLogin": firstNonEmpty(stringValue(wallet["githubLogin"]), stringValue(profile["githubLogin"])),
			"name":        name,
			"points":      amount("POINT", points),
			"score":       points,
			"totalPoint":  points,
		}
		if avatarURL := stringValue(profile["avatarUrl"]); avatarURL != "" {
			item["avatarUrl"] = avatarURL
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return amountValue(map[string]any{"amount": items[i]["points"]}) > amountValue(map[string]any{"amount": items[j]["points"]})
	})
	return filterUserRankings(items, limit), nil
}

func (s *server) excludedUserRankingAccountIDs(ctx context.Context) (map[string]bool, error) {
	accounts, err := s.store.internalAccounts(ctx, 10000)
	if err != nil {
		return nil, err
	}
	excluded := map[string]bool{}
	for _, account := range accounts {
		switch normalizeInternalRole(account.Role) {
		case roleBooth, roleTeacher:
			excluded[account.ID] = true
		}
	}
	return excluded, nil
}

func boothRankingsVisibleNow() bool {
	return !time.Now().Before(boothRankingsLaunchAtKST)
}

func filterUserRankings(items []map[string]any, limit int) []map[string]any {
	return filterUserRankingsByUserIDs(items, limit, nil)
}

func filterUserRankingsByUserIDs(items []map[string]any, limit int, excludedUserIDs map[string]bool) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if excludedUserIDs[stringValue(item["userId"])] {
			continue
		}
		login := firstNonEmpty(stringValue(item["githubLogin"]), stringValue(item["login"]))
		if strings.EqualFold(strings.TrimSpace(login), excludedUserRankingGitHubLogin) {
			continue
		}
		item["rank"] = len(filtered) + 1
		filtered = append(filtered, item)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func (s *server) handleFestivalBanner(w http.ResponseWriter, r *http.Request) {
	items, _, ok := s.listResources(w, r, resourceFestivalBanners, 10)
	if !ok {
		return
	}
	s.ok(w, r, firstItem(items))
}

func (s *server) handleCreateBarcode(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreatePayBarcode(r.Context(), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourcePayBarcodes, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	products, _, ok := s.listResources(w, r, resourceProducts, 1000)
	if !ok {
		return
	}
	if err := s.decorateProductsWithViewCounts(r.Context(), products); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품 조회수를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	booths, _, ok := s.listResources(w, r, resourceBooths, 50)
	if !ok {
		return
	}
	if err := s.decorateBoothDisplayNames(r.Context(), booths, products); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스 이름을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.ok(w, r, map[string]any{"categories": categories, "banners": banners, "products": products, "booths": booths})
}

func (s *server) handleBoothProducts(w http.ResponseWriter, r *http.Request) {
	items, limit, ok := s.listResources(w, r, resourceProducts, 1000,
		resourceFilter{Field: "boothId", Value: r.PathValue("boothId")},
		resourceFilter{Field: "categoryId", Value: r.URL.Query().Get("categoryId")},
		resourceFilter{Field: "status", Value: r.URL.Query().Get("status")},
	)
	if !ok {
		return
	}
	if err := s.decorateProductsWithViewCounts(r.Context(), items); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품 조회수를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if err := s.decorateBoothDisplayNames(r.Context(), nil, items); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스 이름을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
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
	if err := s.decorateProductsWithViewCounts(r.Context(), []map[string]any{item}); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품 조회수를 읽지 못했습니다.", map[string]any{"productId": id, "cause": err.Error()})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleProductView(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	productID := r.PathValue("productId")
	item, err := s.customers().CreateProductView(r.Context(), productID, s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceProductViews, "", "create", err)
		return
	}
	if counts, err := s.productViewCounts(r.Context(), []string{productID}); err == nil {
		count := counts[productID]
		item["viewCount"] = count
		item["meta"] = productViewMeta(count)
	}
	s.created(w, r, item)
}

func (s *server) decorateProductsWithViewCounts(ctx context.Context, products []map[string]any) error {
	productIDs := make([]string, 0, len(products))
	for _, product := range products {
		if id := resourceID(product, "productId"); id != "" {
			productIDs = append(productIDs, id)
		}
	}
	counts, err := s.productViewCounts(ctx, productIDs)
	if err != nil {
		return err
	}
	for _, product := range products {
		id := resourceID(product, "productId")
		count := counts[id]
		product["viewCount"] = count
		product["meta"] = productViewMeta(count)
	}
	return nil
}

func (s *server) decorateBoothDisplayNames(ctx context.Context, booths []map[string]any, products []map[string]any) error {
	namesByBoothID := map[string]string{}
	for _, booth := range booths {
		id := stringValue(booth["id"])
		if id == "" {
			continue
		}
		if name := firstNonEmpty(stringValue(booth["displayName"]), stringValue(booth["title"]), stringValue(booth["name"])); name != "" && name != id {
			namesByBoothID[id] = name
		}
	}

	accounts, err := s.store.internalAccounts(ctx, 10000)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		boothID := strings.TrimSpace(account.BoothID)
		if boothID == "" || namesByBoothID[boothID] != "" {
			continue
		}
		if name := strings.TrimSpace(account.DisplayName); name != "" && name != boothID {
			namesByBoothID[boothID] = name
		}
	}

	for _, booth := range booths {
		id := stringValue(booth["id"])
		if name := namesByBoothID[id]; name != "" {
			booth["displayName"] = name
			booth["boothName"] = name
		}
	}
	for _, product := range products {
		boothID := stringValue(product["boothId"])
		if name := namesByBoothID[boothID]; name != "" {
			product["displayName"] = name
			product["boothName"] = name
		}
	}
	return nil
}

func (s *server) productViewCounts(ctx context.Context, productIDs []string) (map[string]int, error) {
	uniqueIDs := make([]string, 0, len(productIDs))
	seen := map[string]bool{}
	for _, productID := range productIDs {
		productID = strings.TrimSpace(productID)
		if productID == "" || seen[productID] {
			continue
		}
		seen[productID] = true
		uniqueIDs = append(uniqueIDs, productID)
	}
	counts := make(map[string]int, len(uniqueIDs))
	for _, productID := range uniqueIDs {
		counts[productID] = 0
	}
	if len(uniqueIDs) == 0 {
		return counts, nil
	}

	args := make([]any, len(uniqueIDs))
	placeholders := make([]string, len(uniqueIDs))
	for index, productID := range uniqueIDs {
		args[index] = productID
		placeholders[index] = fmt.Sprintf("$%d", index+1)
	}
	rows, err := s.store.db.QueryContext(ctx, fmt.Sprintf(`
SELECT payload->>'productId', COUNT(*)
FROM product_view_events
WHERE payload->>'productId' IN (%s)
GROUP BY payload->>'productId'
`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var productID string
		var count int
		if err := rows.Scan(&productID, &count); err != nil {
			return nil, err
		}
		counts[productID] = count
	}
	return counts, rows.Err()
}

func productViewMeta(count int) string {
	return "조회 " + number(count) + "회"
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
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CheckInBooth(r.Context(), r.PathValue("boothId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceBoothCheckins, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleBoothRankings(w http.ResponseWriter, r *http.Request) {
	if !boothRankingsVisibleNow() {
		s.okPage(w, r, []map[string]any{}, &pagination{Limit: 20, HasMore: false})
		return
	}
	s.respondResourceList(w, r, resourceBoothRankings, 20)
}

func (s *server) handleAnalyticsImpressionCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreateAnalyticsImpression(r.Context(), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceAnalyticsImpressions, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleCart(w http.ResponseWriter, r *http.Request) {
	items, limit, ok := s.listResources(w, r, resourceCartItems, 100, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
	if !ok {
		return
	}
	if err := s.decorateBoothDisplayNames(r.Context(), nil, items); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스 이름을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.okPage(w, r, items, &pagination{Limit: limit, HasMore: false})
}

func (s *server) handleCartItem(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	productID := strings.TrimSpace(stringValue(body["productId"]))
	if productID == "" {
		s.fail(w, r, http.StatusBadRequest, "PRODUCT_ID_REQUIRED", "productId가 필요합니다.", nil)
		return
	}
	if !optionalPositiveQuantity(body) {
		s.fail(w, r, http.StatusBadRequest, "INVALID_QUANTITY", "quantity는 1 이상의 정수여야 합니다.", map[string]any{"quantity": body["quantity"]})
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
	userID := s.currentUserID(r)
	quantity := 1
	if n, ok := numericValue(body["quantity"]); ok {
		quantity = int(n)
	}
	body["cartItemId"] = firstNonEmpty(stringValue(body["cartItemId"]), ledgerID("cart-item", userID, productID))
	body["productId"] = productID
	body["quantity"] = quantity
	body["title"] = firstNonEmpty(stringValue(product["title"]), stringValue(product["name"]), productID)
	body["name"] = body["title"]
	body["boothId"] = stringValue(product["boothId"])
	body["imageUrl"] = firstNonEmpty(stringValue(product["imageUrl"]), stringValue(product["imageSrc"]), stringValue(product["thumbnail"]))
	body["thumbnail"] = body["imageUrl"]
	body["unitAmount"] = amount("DMC", productUnitAmount(product))
	body["price"] = body["unitAmount"]
	if err := s.decorateBoothDisplayNames(r.Context(), nil, []map[string]any{body}); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스 이름을 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	item, err := s.customers().CreateCartItem(r.Context(), userID, body)
	if err != nil {
		if s.failCustomerMutationValidation(w, r, err, body) {
			return
		}
		s.failResourceCommand(w, r, resourceCartItems, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleCartItemDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("cartItemId"))
	if id == "" {
		s.fail(w, r, http.StatusBadRequest, "CART_ITEM_ID_REQUIRED", "cartItemId가 필요합니다.", nil)
		return
	}
	item, found, err := s.store.get(r.Context(), resourceCartItems, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "장바구니 항목을 읽지 못했습니다.", map[string]any{"cartItemId": id, "cause": err.Error()})
		return
	}
	if !found || stringValue(item["userId"]) != s.currentUserID(r) {
		s.fail(w, r, http.StatusNotFound, "CART_ITEM_NOT_FOUND", "장바구니 항목을 찾을 수 없습니다.", map[string]any{"cartItemId": id})
		return
	}
	if !s.deleteResource(w, r, resourceCartItems, id) {
		return
	}
	s.ok(w, r, map[string]any{"deleted": true, "cartItemId": id})
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
	if !optionalPositiveQuantity(body) {
		s.fail(w, r, http.StatusBadRequest, "INVALID_QUANTITY", "quantity는 1 이상의 정수여야 합니다.", map[string]any{"quantity": body["quantity"]})
		return
	}
	if n, ok := numericValue(body["quantity"]); ok {
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
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return
	}
	item, err := s.createPaidCustomerOrder(r.Context(), session.User, body)
	if err != nil {
		if s.failCustomerMutationValidation(w, r, err, body) {
			return
		}
		s.failResourceCommand(w, r, resourceOrders, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleOrders(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceOrders, 100, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) createPaidCustomerOrder(ctx context.Context, user authUser, body map[string]any) (map[string]any, error) {
	productID := strings.TrimSpace(stringValue(body["productId"]))
	if productID == "" {
		return nil, errCustomerProductRequired
	}
	if !optionalPositiveQuantity(body) {
		return nil, errCustomerQuantityInvalid
	}
	quantity := 1
	if n, ok := numericValue(body["quantity"]); ok {
		quantity = int(n)
	}

	orderID := firstNonEmpty(stringValue(body["orderId"]), resourceID(body, "order", "orderId"))
	var order map[string]any
	err := s.store.withSerializableTx(ctx, func(tx *sql.Tx) error {
		product, found, err := s.store.getResourceTx(ctx, tx, resourceProducts, productID, true)
		if err != nil {
			return err
		}
		if !found {
			return errCustomerProductNotFound
		}
		status := strings.ToLower(strings.TrimSpace(stringValue(product["status"])))
		if status != "" && status != "active" && status != "판매 중" {
			return errCustomerProductClosed
		}
		stock, hasStock := numericValue(firstNonEmptyAny(product["stock"], product["stockQuantity"]))
		if hasStock && int(stock) < quantity {
			return errCustomerStockShortage
		}

		unitAmount := productUnitAmount(product)
		if unitAmount <= 0 {
			return errCustomerProductClosed
		}
		totalAmount := unitAmount * quantity
		now := time.Now().UTC().Format(time.RFC3339)
		productName := firstNonEmpty(stringValue(product["title"]), stringValue(product["name"]), productID)
		boothID := stringValue(product["boothId"])

		orderPayload := cloneMap(body)
		orderPayload["productId"] = productID
		orderPayload["productName"] = productName
		orderPayload["item"] = productName
		orderPayload["items"] = []map[string]any{{"productId": productID, "name": productName, "quantity": quantity, "unitAmount": amount("DMC", unitAmount)}}
		orderPayload["quantity"] = quantity
		orderPayload["boothId"] = boothID
		orderPayload["customerId"] = user.ID
		orderPayload["userId"] = user.ID
		orderPayload["customerName"] = firstNonEmpty(user.Name, user.Login, user.ID)
		orderPayload["name"] = firstNonEmpty(user.Name, user.Login, user.ID)
		orderPayload["currency"] = "DMC"
		orderPayload["unitAmount"] = amount("DMC", unitAmount)
		orderPayload["totalAmount"] = amount("DMC", totalAmount)
		orderPayload["amount"] = amount("DMC", totalAmount)
		orderPayload["status"] = "paid"
		orderPayload["paidAt"] = now

		createdOrder, created, err := s.store.createResourceTx(ctx, tx, resourceOrders, orderID, orderPayload)
		if err != nil {
			return err
		}
		if !created {
			existing, found, err := s.store.getResourceTx(ctx, tx, resourceOrders, orderID, true)
			if err != nil {
				return err
			}
			if !found || stringValue(existing["userId"]) != user.ID || stringValue(existing["productId"]) != productID {
				return errLedgerIdempotencyConflict
			}
			order = existing
			return nil
		}

		ledgerExtras := map[string]any{
			"orderId":       orderID,
			"productId":     productID,
			"boothId":       boothID,
			"referenceType": "order",
			"referenceId":   orderID,
			"description":   "부스 상품 구매",
		}
		if _, err := s.store.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(user, ledgerID("booth-order", orderID), "booth-order", "expense", "DMC", totalAmount, ledgerExtras)); err != nil {
			return err
		}

		if hasStock {
			nextStock := int(stock) - quantity
			product["stock"] = nextStock
			product["stockQuantity"] = nextStock
			if nextStock <= 0 {
				product["status"] = "sold_out"
			}
			if _, err := s.store.putResourceTx(ctx, tx, resourceProducts, productID, product); err != nil {
				return err
			}
		}

		order = createdOrder
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

func productUnitAmount(product map[string]any) int {
	for _, field := range []string{"salePrice", "price", "amount", "unitAmount"} {
		if value, ok := product[field]; ok {
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

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		if stringValue(value) != "" {
			return value
		}
	}
	return nil
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
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.", nil)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/customer/") && !resourceBelongsToUser(item, session.User.ID) {
		s.fail(w, r, http.StatusNotFound, "ORDER_NOT_FOUND", "주문을 찾을 수 없습니다.", map[string]any{"orderId": id})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/seller/") && !resourceBelongsToBooth(item, session.User.BoothID) {
		s.fail(w, r, http.StatusForbidden, "BOOTH_SCOPE_REQUIRED", "해당 주문에 접근할 권한이 없습니다.", map[string]any{"orderId": id})
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	userID := s.currentUserID(r)
	targetID := firstNonEmpty(stringValue(body["targetId"]), stringValue(body["productId"]))
	if targetID == "" {
		s.fail(w, r, http.StatusBadRequest, "TARGET_ID_REQUIRED", "targetId가 필요합니다.", nil)
		return
	}
	body["favoriteId"] = firstNonEmpty(stringValue(body["favoriteId"]), ledgerID("favorite", userID, targetID))
	body["targetId"] = targetID
	body["targetType"] = firstNonEmpty(stringValue(body["targetType"]), "product")
	item, err := s.customers().CreateFavorite(r.Context(), userID, body)
	if err != nil {
		s.failResourceCommand(w, r, resourceFavorites, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceFavorites, 100,
		resourceFilter{Field: "userId", Value: s.currentUserID(r)},
		resourceFilter{Field: "targetId", Value: r.URL.Query().Get("targetId")},
	)
}

func (s *server) handleFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("targetId")
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
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreateInquiry(r.Context(), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceInquiries, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleShare(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreateShare(r.Context(), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceShares, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) failCustomerMutationValidation(w http.ResponseWriter, r *http.Request, err error, body map[string]any) bool {
	switch {
	case errors.Is(err, errCustomerProductRequired):
		s.fail(w, r, http.StatusBadRequest, "PRODUCT_ID_REQUIRED", "productId가 필요합니다.", nil)
	case errors.Is(err, errCustomerOrderRequired):
		s.fail(w, r, http.StatusBadRequest, "ORDER_ITEMS_REQUIRED", "productId 또는 주문 상품 목록이 필요합니다.", nil)
	case errors.Is(err, errCustomerQuantityInvalid):
		s.fail(w, r, http.StatusBadRequest, "INVALID_QUANTITY", "quantity는 1 이상의 정수여야 합니다.", map[string]any{"quantity": body["quantity"]})
	case errors.Is(err, errCustomerProductNotFound):
		s.fail(w, r, http.StatusNotFound, "PRODUCT_NOT_FOUND", "상품을 찾을 수 없습니다.", map[string]any{"productId": body["productId"]})
	case errors.Is(err, errCustomerProductClosed):
		s.fail(w, r, http.StatusBadRequest, "PRODUCT_NOT_AVAILABLE", "구매할 수 없는 상품입니다.", map[string]any{"productId": body["productId"]})
	case errors.Is(err, errCustomerStockShortage):
		s.fail(w, r, http.StatusConflict, "PRODUCT_STOCK_SHORTAGE", "상품 재고가 부족합니다.", map[string]any{"productId": body["productId"]})
	case errors.Is(err, errInsufficientWalletBalance):
		s.fail(w, r, http.StatusPaymentRequired, "INSUFFICIENT_BALANCE", "DMC 잔액이 부족합니다.", nil)
	default:
		return false
	}
	return true
}
