package server

import "net/http"

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	routes := routeRegistrar{s: s, mux: mux}

	routes.public("GET /healthz", s.handleHealth)

	routes.public("GET /api/auth/github/login", s.handleGitHubLogin)
	routes.public("GET /api/auth/github/callback", s.handleGitHubCallback)
	routes.public("POST /api/auth/github/exchange", s.handleGitHubExchange)
	routes.public("POST /api/auth/github/session", s.handleGitHubSession)
	routes.public("GET /api/auth/me", s.handleAuthMe)
	routes.public("PUT /api/auth/me/student-profile", s.handleStudentProfile)
	routes.public("POST /api/auth/admin/login", s.handleAdminLogin)
	routes.public("POST /api/auth/teacher/login", s.handleTeacherLogin)
	routes.public("POST /api/auth/logout", s.handleAuthLogout)
	routes.public("GET /api/github/app/setup", s.handleGitHubAppSetup)
	routes.public("POST /api/github/webhooks", s.handleGitHubWebhook)
	routes.public("GET /api/files/{fileId}", s.handleFileDownload)

	routes.customer("GET /api/customer/me", s.handleCustomerMe)
	routes.customer("GET /api/customer/navigation", s.handleNavigation)
	routes.customer("GET /api/customer/notifications/summary", s.handleNotificationSummary)
	routes.customer("GET /api/customer/cart/summary", s.handleCartSummary)
	routes.public("GET /api/search/suggestions", s.handleSearchSuggestions)
	routes.public("GET /api/search", s.handleSearch)

	routes.customer("GET /api/customer/home", s.handleHome)
	routes.customer("GET /api/customer/notices/highlight", s.handleNoticeHighlight)
	routes.customer("GET /api/customer/wallet/balances", s.handleWalletBalances)
	routes.customer("GET /api/customer/benefits/interest", s.handleInterestBenefit)
	routes.customer("POST /api/customer/benefits/{benefitId}/claim", s.handleClaimBenefit)
	routes.customer("GET /api/customer/home/shortcuts", s.handleShortcuts)
	routes.customer("GET /api/customer/promotions", s.handlePromotions)
	routes.customer("GET /api/customer/ledger/recent", s.handleLedgerRecent)
	routes.customer("GET /api/customer/rankings", s.handleRankings)
	routes.customer("GET /api/customer/festival/banner", s.handleFestivalBanner)
	routes.customer("GET /api/customer/schedules/highlight", s.handleFestivalBanner)

	routes.customer("POST /api/customer/pay/barcodes", s.handleCreateBarcode)

	routes.customer("GET /api/customer/booth/categories", s.handleBoothCategories)
	routes.customer("GET /api/customer/booth/banners", s.handleBoothBanners)
	routes.customer("GET /api/customer/booth/home", s.handleBoothHome)
	routes.customer("GET /api/customer/booth/products", s.handleBoothProducts)
	routes.customer("GET /api/customer/booth/products/search", s.handleBoothProducts)
	routes.customer("GET /api/customer/booth/products/{productId}", s.handleBoothProduct)
	routes.customer("POST /api/customer/booth/products/{productId}/view", s.handleProductView)
	routes.customer("GET /api/customer/booths/{boothId}", s.handleCustomerBooth)
	routes.customer("POST /api/customer/booths/{boothId}/check-in", s.handleBoothCheckIn)
	routes.customer("GET /api/customer/booth-rankings", s.handleBoothRankings)
	routes.customer("POST /api/customer/analytics/impressions", s.handleAnalyticsImpressionCreate)
	routes.customer("GET /api/customer/cart", s.handleCart)
	routes.customer("POST /api/customer/cart/items", s.handleCartItem)
	routes.customer("POST /api/customer/orders/preview", s.handleOrderPreview)
	routes.customer("POST /api/customer/orders", s.handleOrderCreate)
	routes.customer("GET /api/customer/orders/{orderId}", s.handleOrderDetail)
	routes.customer("POST /api/customer/favorites", s.handleFavorite)
	routes.customer("DELETE /api/customer/favorites/{targetId}", s.handleFavoriteDelete)
	routes.customer("GET /api/customer/inquiries", s.handleInquiries)
	routes.customer("POST /api/customer/inquiries", s.handleInquiryCreate)
	routes.customer("POST /api/customer/shares", s.handleShare)

	routes.customer("GET /api/customer/points/commit-activity", s.handleCommitActivity)
	routes.customer("GET /api/customer/points/commit-stats", s.handleCommitStats)
	routes.customer("GET /api/customer/points/commit-transactions", s.handleCommitTransactions)
	routes.customer("GET /api/customer/points/commit-reward-summary", s.handleCommitRewardSummary)
	routes.customer("GET /api/customer/github/commits", s.handleGitHubCommits)
	routes.customer("GET /api/customer/github/commit-activity", s.handleCommitActivity)
	routes.customer("GET /api/customer/github/commit-stats", s.handleCommitStats)
	routes.customer("GET /api/customer/github/app-installation", s.handleGitHubAppInstallation)

	routes.customer("GET /api/customer/worldcup/match-days", s.handleWorldcupMatchDays)
	routes.customer("GET /api/customer/worldcup/matches", s.handleWorldcupMatches)
	routes.customer("GET /api/customer/worldcup/matches/{matchId}", s.handleWorldcupMatch)
	routes.customer("GET /api/customer/worldcup/matches/{matchId}/predictions/summary", s.handlePredictionSummary)
	routes.customer("POST /api/customer/worldcup/matches/{matchId}/predictions", s.handlePredictionCreate)
	routes.customer("DELETE /api/customer/worldcup/matches/{matchId}/predictions", s.handlePredictionCancel)
	routes.customer("GET /api/customer/worldcup/matches/{matchId}/stats", s.handleWorldcupStats)
	routes.customer("GET /api/customer/worldcup/matches/{matchId}/lineups", s.handleWorldcupLineups)

	routes.customer("GET /api/customer/ledger/calendar", s.handleLedgerCalendar)
	routes.customer("GET /api/customer/ledger/transactions", s.handleLedgerTransactions)
	routes.customer("GET /api/customer/ledger/analysis", s.handleLedgerAnalysis)
	routes.customer("GET /api/customer/features", s.handleFeatures)

	routes.public("POST /api/auth/seller/login", s.handleSellerLogin)
	routes.public("POST /api/auth/seller/logout", s.handleAuthLogout)
	routes.seller("GET /api/seller/me", s.handleSellerMe)
	routes.seller("GET /api/seller/booths", s.handleSellerBooths)
	routes.seller("GET /api/seller/booths/{boothId}", s.handleSellerBooth)
	routes.seller("PATCH /api/seller/booths/{boothId}", s.handleSellerBooth)
	routes.seller("PATCH /api/seller/booths/{boothId}/status", s.handleSellerBoothStatus)
	routes.seller("GET /api/seller/booths/{boothId}/staff", s.handleSellerStaff)
	routes.seller("POST /api/seller/booths/{boothId}/staff", s.handleSellerStaffCreate)
	routes.seller("PATCH /api/seller/booths/{boothId}/staff/{staffId}", s.handleSellerStaffUpdate)
	routes.seller("GET /api/seller/booths/{boothId}/products", s.handleSellerProducts)
	routes.seller("POST /api/seller/booths/{boothId}/products", s.handleSellerProductCreate)
	routes.seller("GET /api/seller/products/{productId}", s.handleSellerProduct)
	routes.seller("PATCH /api/seller/products/{productId}", s.handleSellerProduct)
	routes.seller("DELETE /api/seller/products/{productId}", s.handleSellerProduct)
	routes.seller("PATCH /api/seller/products/{productId}/status", s.handleSellerProductStatus)
	routes.seller("POST /api/seller/products/{productId}/images", s.handleSellerProductImageCreate)
	routes.seller("GET /api/seller/products/{productId}/inventory", s.handleInventory)
	routes.seller("POST /api/seller/products/{productId}/inventory/adjustments", s.handleInventoryAdjustmentCreate)
	routes.seller("GET /api/seller/booths/{boothId}/orders", s.handleSellerOrders)
	routes.seller("GET /api/seller/orders/{orderId}", s.handleOrderDetail)
	routes.seller("PATCH /api/seller/orders/{orderId}/status", s.handleSellerOrderStatus)
	routes.seller("POST /api/seller/orders/{orderId}/cancel", s.handleSellerOrderCancel)
	routes.seller("POST /api/seller/orders/{orderId}/refund", s.handleSellerOrderRefund)
	routes.seller("POST /api/seller/pickup-vouchers/verify", s.handleVoucherVerify)
	routes.seller("POST /api/seller/pickup-vouchers/{voucherId}/redeem", s.handlePickupVoucherRedeem)
	routes.seller("POST /api/seller/pay/barcodes/lookup", s.handleBarcodeLookup)
	routes.seller("POST /api/seller/pay/payment-intents", s.handlePaymentIntent)
	routes.seller("POST /api/seller/pay/payment-intents/{intentId}/capture", s.handlePaymentIntentCapture)
	routes.seller("POST /api/seller/pay/payment-intents/{intentId}/cancel", s.handlePaymentIntentCancel)
	routes.seller("POST /api/seller/pay/payments/{paymentId}/refund", s.handlePaymentRefund)
	routes.seller("GET /api/seller/booths/{boothId}/payments", s.handleSellerPayments)
	routes.seller("POST /api/seller/booths/{boothId}/visits/verify", s.handleVisitVerify)
	routes.seller("GET /api/seller/booths/{boothId}/visits", s.handleVisits)
	routes.seller("GET /api/seller/booths/{boothId}/ranking", s.handleBoothRanking)
	routes.seller("GET /api/seller/booths/{boothId}/inquiries", s.handleInquiries)
	routes.seller("POST /api/seller/inquiries/{inquiryId}/replies", s.handleInquiryReplyCreate)
	routes.seller("POST /api/seller/booths/{boothId}/notices", s.handleSellerNoticeCreate)
	routes.seller("GET /api/seller/booths/{boothId}/dashboard", s.handleSellerDashboard)
	routes.seller("GET /api/seller/booths/{boothId}/settlements", s.handleSettlements)
	routes.seller("GET /api/seller/settlements/{settlementId}", s.handleSettlement)
	routes.seller("GET /api/seller/booths/{boothId}/reports/sales", s.handleSalesReport)
	routes.seller("GET /api/seller/booths/{boothId}/reports/inventory", s.handleInventoryReport)
	routes.seller("POST /api/seller/booths/{boothId}/exports", s.handleExport)

	routes.admin("GET /api/admin/dashboard", s.handleAdminDashboard)
	routes.admin("GET /api/admin/accounts", s.handleAdminAccounts)
	routes.admin("POST /api/admin/accounts", s.handleAdminAccountCreate)
	routes.admin("PATCH /api/admin/accounts/{accountId}", s.handleAdminAccountUpdate)
	routes.admin("POST /api/admin/accounts/{accountId}/reset-password", s.handleAdminAccountResetPassword)
	routes.admin("GET /api/admin/festivals", s.handleAdminFestivals)
	routes.admin("POST /api/admin/festivals", s.handleAdminFestivalCreate)
	routes.admin("PATCH /api/admin/festivals/{festivalId}", s.handleAdminFestivalUpdate)
	routes.admin("GET /api/admin/booths", s.handleAdminBooths)
	routes.admin("POST /api/admin/booths", s.handleAdminBoothCreate)
	routes.admin("PATCH /api/admin/booths/{boothId}", s.handleAdminBoothUpdate)
	routes.admin("GET /api/admin/booth-categories", s.handleAdminBoothCategories)
	routes.admin("POST /api/admin/booth-categories", s.handleAdminBoothCategoryCreate)
	routes.admin("PATCH /api/admin/booth-categories/{categoryId}", s.handleAdminBoothCategoryUpdate)
	routes.admin("POST /api/admin/maps", s.handleAdminMapCreate)
	routes.admin("GET /api/admin/users", s.handleAdminUsers)
	routes.admin("POST /api/admin/users/import", s.handleAdminUsersImport)
	routes.admin("GET /api/admin/users/{userId}", s.handleAdminUser)
	routes.admin("PATCH /api/admin/users/{userId}", s.handleAdminUserUpdate)
	routes.admin("GET /api/admin/roles", s.handleAdminRoles)
	routes.admin("POST /api/admin/role-assignments", s.handleAdminRoleAssignmentCreate)
	routes.admin("DELETE /api/admin/role-assignments/{assignmentId}", s.handleAdminRoleAssignmentDelete)
	routes.admin("GET /api/admin/wallets", s.handleAdminWallets)
	routes.admin("POST /api/admin/wallets/adjustments", s.handleAdminWalletAdjustment)
	routes.admin("GET /api/admin/ledger/transactions", s.handleAdminLedgerTransactions)
	routes.admin("GET /api/admin/ledger/exports", s.handleAdminLedgerExports)
	routes.admin("POST /api/admin/reward-rules", s.handleAdminRewardRuleCreate)
	routes.admin("PATCH /api/admin/reward-rules/{ruleId}", s.handleAdminRewardRuleUpdate)
	routes.admin("GET /api/admin/notices", s.handleAdminNotices)
	routes.admin("POST /api/admin/notices", s.handleAdminNoticeCreate)
	routes.admin("PATCH /api/admin/notices/{noticeId}", s.handleAdminNoticeUpdate)
	routes.admin("GET /api/admin/promotions", s.handleAdminPromotions)
	routes.admin("POST /api/admin/promotions", s.handleAdminPromotionCreate)
	routes.admin("PATCH /api/admin/promotions/{promotionId}", s.handleAdminPromotionUpdate)
	routes.admin("POST /api/admin/notifications", s.handleAdminNotificationSend)
	routes.adminOrBooth("POST /api/files/uploads", s.handleFileUpload)
	routes.admin("GET /api/admin/worldcup/teams", s.handleAdminWorldcupTeams)
	routes.admin("POST /api/admin/worldcup/teams", s.handleAdminWorldcupTeamCreate)
	routes.admin("GET /api/admin/worldcup/matches", s.handleAdminWorldcupMatches)
	routes.admin("POST /api/admin/worldcup/matches", s.handleAdminWorldcupMatchCreate)
	routes.admin("PATCH /api/admin/worldcup/matches/{matchId}", s.handleAdminWorldcupMatchUpdate)
	routes.admin("PUT /api/admin/worldcup/matches/{matchId}/lineups", s.handleAdminWorldcupLineupsPut)
	routes.admin("PUT /api/admin/worldcup/matches/{matchId}/stats", s.handleAdminWorldcupStatsPut)
	routes.admin("POST /api/admin/worldcup/matches/{matchId}/predictions/settle", s.handleAdminPredictionSettle)
	routes.admin("GET /api/admin/worldcup/predictions", s.handleAdminWorldcupPredictions)
	routes.admin("GET /api/admin/audit-logs", s.handleAdminAuditLogs)
	routes.admin("GET /api/admin/system/health", s.handleAdminSystemHealth)
	routes.admin("GET /api/admin/system/jobs", s.handleAdminSystemJobs)
	routes.admin("POST /api/admin/incidents", s.handleAdminIncidentCreate)
	routes.admin("POST /api/admin/ranking-rules", s.handleAdminRankingRuleCreate)

	return requestIDMiddleware(loggingMiddleware(corsMiddleware(csrfMiddleware(s.authzMiddleware(mux)))))
}

type routeRegistrar struct {
	s   *server
	mux *http.ServeMux
}

func (r routeRegistrar) public(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, handler)
}

func (r routeRegistrar) customer(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.requireAnyRouteRole(handler, roleCustomer, roleTeacher))
}

func (r routeRegistrar) seller(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.requireSellerRoute(handler))
}

func (r routeRegistrar) admin(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.requireRouteRole(roleAdmin, handler))
}

func (r routeRegistrar) adminOrBooth(pattern string, handler http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.requireAnyRouteRole(handler, roleAdmin, roleBooth))
}

func (s *server) requireRouteRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.requireRole(w, r, role)
		if !ok {
			return
		}
		next(w, r.WithContext(contextWithAuthSession(r.Context(), session)))
	}
}

func (s *server) requireSellerRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.requireRole(w, r, roleBooth)
		if !ok {
			return
		}
		if !s.requireBoothScope(w, r, session) {
			return
		}
		next(w, r.WithContext(contextWithAuthSession(r.Context(), session)))
	}
}

func (s *server) requireAnyRouteRole(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.requireAnyRole(w, r, roles...)
		if !ok {
			return
		}
		next(w, r.WithContext(contextWithAuthSession(r.Context(), session)))
	}
}
