package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
)

const excludedUserRankingGitHubLogin = "kangeunchan"

func (s *server) walletBalance(ctx context.Context, userID, currency string) (int, error) {
	return s.store.walletBalance(ctx, userID, currency)
}

func (s *server) createLedgerAndAdjustWallet(ctx context.Context, user authUser, id, txType, direction, currency string, value int, extras map[string]any) (bool, error) {
	return s.store.createLedgerAndAdjustWalletRequest(ctx, newWalletLedgerRequest(user, id, txType, direction, currency, value, extras))
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

func filterUserRankings(items []map[string]any, limit int) []map[string]any {
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
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
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreateProductView(r.Context(), r.PathValue("productId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceProductViews, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	s.respondResourceList(w, r, resourceCartItems, 100, resourceFilter{Field: "userId", Value: s.currentUserID(r)})
}

func (s *server) handleCartItem(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.customers().CreateCartItem(r.Context(), s.currentUserID(r), body)
	if err != nil {
		if s.failCustomerMutationValidation(w, r, err, body) {
			return
		}
		s.failResourceCommand(w, r, resourceCartItems, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	item, err := s.customers().CreateOrder(r.Context(), s.currentUserID(r), body)
	if err != nil {
		if s.failCustomerMutationValidation(w, r, err, body) {
			return
		}
		s.failResourceCommand(w, r, resourceOrders, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	item, err := s.customers().CreateFavorite(r.Context(), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceFavorites, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	default:
		return false
	}
	return true
}
