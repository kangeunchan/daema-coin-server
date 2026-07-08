package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

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
	var body adminAccountCreateRequest
	if !s.decodeStrictJSON(w, r, &body) {
		return
	}
	account, err := s.createInternalAccount(r.Context(), body.input(session.User.ID))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errAccountAlreadyExists) {
			status = http.StatusConflict
		}
		s.fail(w, r, status, "ACCOUNT_CREATE_FAILED", "계정을 생성하지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	s.created(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminAccountUpdate(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountId")
	var body adminAccountUpdateRequest
	if !s.decodeStrictJSON(w, r, &body) {
		return
	}
	payload := body.mapPayload()
	account, err := s.adminAccounts().Update(r.Context(), accountID, payload)
	if err != nil {
		switch {
		case errors.Is(err, errAccountNotFound):
			s.fail(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "계정을 찾을 수 없습니다.", map[string]any{"accountId": accountID})
		case errors.Is(err, errInvalidAccountStatus):
			s.fail(w, r, http.StatusBadRequest, "INVALID_ACCOUNT_STATUS", "계정 상태가 올바르지 않습니다.", map[string]any{"status": payload["status"]})
		default:
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "계정을 저장하지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		}
		return
	}
	s.ok(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminAccountResetPassword(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountId")
	var body adminAccountResetPasswordRequest
	if !s.decodeStrictJSON(w, r, &body) {
		return
	}
	account, err := s.adminAccounts().ResetPassword(r.Context(), accountID, body.Password)
	if err != nil {
		switch {
		case errors.Is(err, errAccountNotFound):
			s.fail(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "계정을 찾을 수 없습니다.", map[string]any{"accountId": accountID})
		case errors.Is(err, errInvalidAccountPass):
			s.fail(w, r, http.StatusBadRequest, "INVALID_PASSWORD", "비밀번호는 10자 이상이어야 합니다.", nil)
		default:
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "비밀번호를 저장하지 못했습니다.", map[string]any{"accountId": accountID, "cause": err.Error()})
		}
		return
	}
	s.ok(w, r, sanitizeInternalAccount(account))
}

func (s *server) handleAdminResourceCreate(w http.ResponseWriter, r *http.Request, domain string, create func(context.Context, map[string]any) (map[string]any, error)) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := create(r.Context(), body)
	if err != nil {
		s.failResourceCommand(w, r, domain, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleAdminResourceUpdate(w http.ResponseWriter, r *http.Request, domain, id string, update func(context.Context, map[string]any) (map[string]any, error)) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := update(r.Context(), body)
	if err != nil {
		s.failResourceCommand(w, r, domain, id, "update", err)
		return
	}
	s.ok(w, r, item)
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
	s.handleAdminResourceCreate(w, r, resourceFestivals, s.adminResources().CreateFestival)
}

func (s *server) handleAdminFestivalUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("festivalId")
	s.handleAdminResourceUpdate(w, r, resourceFestivals, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateFestival(ctx, id, body)
	})
}

func (s *server) handleAdminBooths(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBooths, 100, resourceFilter{Field: "festivalId", Value: r.URL.Query().Get("festivalId")})
}

func (s *server) handleAdminBoothCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceBooths, s.adminResources().CreateBooth)
}

func (s *server) handleAdminBoothUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	s.handleAdminResourceUpdate(w, r, resourceBooths, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateBooth(ctx, id, body)
	})
}

func (s *server) handleAdminBoothCategories(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceBoothCategories, 100)
}

func (s *server) handleAdminBoothCategoryCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceBoothCategories, s.adminResources().CreateBoothCategory)
}

func (s *server) handleAdminBoothCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("categoryId")
	s.handleAdminResourceUpdate(w, r, resourceBoothCategories, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateBoothCategory(ctx, id, body)
	})
}

func (s *server) handleAdminMapCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceMaps, s.adminResources().CreateMap)
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
	id := r.PathValue("userId")
	s.handleAdminResourceUpdate(w, r, resourceUsers, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateUser(ctx, id, body)
	})
}

func (s *server) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceRoles, 100)
}

func (s *server) handleAdminRoleAssignmentCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceRoleAssignments, s.adminResources().CreateRoleAssignment)
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
	currency, currencyOK := normalizedCurrency(firstNonEmpty(stringValue(body["currency"]), "POINT"))
	value, amountOK := strictAmountValue(body)
	if userID == "" {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WALLET_ADJUSTMENT", "userId와 1 이상의 amount가 필요합니다.", map[string]any{"userId": userID, "amount": body["amount"]})
		return
	}
	if !amountOK {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WALLET_ADJUSTMENT_AMOUNT", "지갑 조정 금액은 1 이상의 정수여야 합니다.", map[string]any{"amount": body["amount"]})
		return
	}
	if !currencyOK {
		s.fail(w, r, http.StatusBadRequest, "INVALID_WALLET_CURRENCY", "currency는 DMC 또는 POINT여야 합니다.", map[string]any{"currency": body["currency"]})
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
		if errors.Is(err, errInsufficientWalletBalance) {
			s.fail(w, r, http.StatusBadRequest, "INSUFFICIENT_WALLET_BALANCE", "지갑 잔액이 부족합니다.", map[string]any{"userId": userID, "currency": currency, "amount": value})
			return
		}
		if errors.Is(err, errLedgerIdempotencyConflict) {
			s.fail(w, r, http.StatusConflict, "WALLET_ADJUSTMENT_IDEMPOTENCY_CONFLICT", "지갑 조정 idempotency 정보가 기존 원장과 일치하지 않습니다.", map[string]any{"id": id})
			return
		}
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
	s.handleAdminResourceCreate(w, r, resourceRewardRules, s.adminResources().CreateRewardRule)
}

func (s *server) handleAdminRewardRuleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("ruleId")
	s.handleAdminResourceUpdate(w, r, resourceRewardRules, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateRewardRule(ctx, id, body)
	})
}

func (s *server) handleAdminNotices(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceNotices, 100)
}

func (s *server) handleAdminNoticeCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceNotices, s.adminResources().CreateNotice)
}

func (s *server) handleAdminNoticeUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("noticeId")
	s.handleAdminResourceUpdate(w, r, resourceNotices, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateNotice(ctx, id, body)
	})
}

func (s *server) handleAdminPromotions(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourcePromotions, 100, resourceFilter{Field: "placement", Value: r.URL.Query().Get("placement")})
}

func (s *server) handleAdminPromotionCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourcePromotions, s.adminResources().CreatePromotion)
}

func (s *server) handleAdminPromotionUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("promotionId")
	s.handleAdminResourceUpdate(w, r, resourcePromotions, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdatePromotion(ctx, id, body)
	})
}

func (s *server) handleAdminNotificationSend(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceNotifications, s.adminResources().CreateNotification)
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
		team.Winner = nil
		out = append(out, team)
	}
	s.ok(w, r, out)
}

func (s *server) handleAdminWorldcupTeamCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceWorldcupTeams, s.adminResources().CreateWorldcupTeam)
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
	s.handleAdminResourceCreate(w, r, resourceWorldcupMatches, s.adminResources().CreateWorldcupMatch)
}

func (s *server) handleAdminWorldcupMatchUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("matchId")
	s.handleAdminResourceUpdate(w, r, resourceWorldcupMatches, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().UpdateWorldcupMatch(ctx, id, body)
	})
}

func (s *server) handleAdminWorldcupLineupsPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("matchId")
	s.handleAdminResourceUpdate(w, r, resourceWorldcupLineups, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().PutWorldcupLineup(ctx, id, body)
	})
}

func (s *server) handleAdminWorldcupStatsPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("matchId")
	s.handleAdminResourceUpdate(w, r, resourceWorldcupStats, id, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		return s.adminResources().PutWorldcupStats(ctx, id, body)
	})
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
	if s.football.Configured() {
		footballStatus = "configured"
	}
	databaseStatus := "ok"
	if err := s.store.health(r.Context()); err != nil {
		databaseStatus = "unavailable"
	}
	footballCacheStatus := "ok"
	if err := s.footballCache.health(r.Context()); err != nil {
		footballCacheStatus = "unavailable"
	}
	s.ok(w, r, map[string]any{"api": "ok", "database": databaseStatus, "payments": "not_configured", "apiFootball": footballStatus, "apiFootballCache": footballCacheStatus, "checkedAt": time.Now().Format(time.RFC3339)})
}

func (s *server) handleAdminSystemJobs(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceSystemJobs, 100)
}

func (s *server) handleAdminIncidentCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceIncidents, s.adminResources().CreateIncident)
}

func (s *server) handleAdminRankingRuleCreate(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCreate(w, r, resourceRankingRules, s.adminResources().CreateRankingRule)
}
