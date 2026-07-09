package server

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

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
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	boothID := strings.TrimSpace(session.User.BoothID)
	if boothID == "" {
		s.okPage(w, r, []map[string]any{}, &pagination{Limit: 100, HasMore: false})
		return
	}
	item, found, err := s.store.get(r.Context(), resourceBooths, boothID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "부스를 읽지 못했습니다.", map[string]any{"boothId": boothID, "cause": err.Error()})
		return
	}
	if found {
		s.okPage(w, r, []map[string]any{item}, &pagination{Limit: 100, HasMore: false})
		return
	}
	s.okPage(w, r, []map[string]any{{
		"id":       boothID,
		"boothId":  boothID,
		"name":     envDefault(session.User.Name, boothID),
		"sellerId": session.User.ID,
		"status":   "active",
	}}, &pagination{Limit: 100, HasMore: false})
}

func (s *server) handleSellerBooth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("boothId")
	if r.Method == http.MethodPatch {
		body, ok := s.requestMap(w, r)
		if !ok {
			return
		}
		item, err := s.sellers().UpdateBooth(r.Context(), id, s.currentUserID(r), body)
		if err != nil {
			s.failResourceCommand(w, r, resourceBooths, id, "update", err)
			return
		}
		s.ok(w, r, item)
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

func (s *server) handleSellerBoothStatus(w http.ResponseWriter, r *http.Request) {
	boothID := r.PathValue("boothId")
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().UpdateBoothStatus(r.Context(), boothID, body)
	if err != nil {
		s.failResourceCommand(w, r, resourceBooths, boothID, "update", err)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSellerStaff(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceStaff, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSellerStaffCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateStaff(r.Context(), r.PathValue("boothId"), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceStaff, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleSellerStaffUpdate(w http.ResponseWriter, r *http.Request) {
	staffID := r.PathValue("staffId")
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().UpdateStaff(r.Context(), r.PathValue("boothId"), staffID, body)
	if err != nil {
		s.failResourceCommand(w, r, resourceStaff, staffID, "update", err)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSellerProducts(w http.ResponseWriter, r *http.Request) {
	s.handleBoothProducts(w, r)
}

func (s *server) handleSellerProductCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateProduct(r.Context(), r.PathValue("boothId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceProducts, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleSellerProduct(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	productID := r.PathValue("productId")
	product, found, err := s.store.get(r.Context(), resourceProducts, productID)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "상품을 읽지 못했습니다.", map[string]any{"productId": productID, "cause": err.Error()})
		return
	}
	if !found {
		s.fail(w, r, http.StatusNotFound, "PRODUCT_NOT_FOUND", "상품을 찾을 수 없습니다.", map[string]any{"productId": productID})
		return
	}
	if !resourceBelongsToBooth(product, session.User.BoothID) {
		s.fail(w, r, http.StatusForbidden, "BOOTH_SCOPE_REQUIRED", "해당 상품에 접근할 권한이 없습니다.", map[string]any{"productId": productID})
		return
	}
	if r.Method == http.MethodDelete {
		if !s.deleteResource(w, r, resourceProducts, productID) {
			return
		}
		s.ok(w, r, map[string]any{"deleted": true, "productId": productID})
		return
	}
	if r.Method == http.MethodPatch {
		body, ok := s.requestMap(w, r)
		if !ok {
			return
		}
		item, err := s.sellers().UpdateProduct(r.Context(), productID, session.User.BoothID, s.currentUserID(r), body)
		if err != nil {
			s.failResourceCommand(w, r, resourceProducts, productID, "update", err)
			return
		}
		s.ok(w, r, item)
		return
	}
	s.ok(w, r, product)
}

func (s *server) handleSellerProductStatus(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("productId")
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().UpdateProductStatus(r.Context(), productID, body)
	if err != nil {
		s.failResourceCommand(w, r, resourceProducts, productID, "update", err)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleSellerProductImageCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateProductImage(r.Context(), r.PathValue("productId"), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceUploads, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleInventory(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceInventory, 100, resourceFilter{Field: "productId", Value: r.PathValue("productId")})
}

func (s *server) handleInventoryAdjustmentCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateInventoryAdjustment(r.Context(), r.PathValue("productId"), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceInventory, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleSellerOrders(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourceOrders, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleSellerOrderStatus(w http.ResponseWriter, r *http.Request) {
	s.handleSellerOrderAction(w, r)
}

func (s *server) handleSellerOrderCancel(w http.ResponseWriter, r *http.Request) {
	s.handleSellerOrderAction(w, r)
}

func (s *server) handleSellerOrderRefund(w http.ResponseWriter, r *http.Request) {
	s.handleSellerOrderAction(w, r)
}

func (s *server) handleSellerOrderAction(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("orderId")
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	extras := sellerOrderActionExtras(r)
	if orderCompletionStatus(body) {
		extras["completedAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	item, err := s.sellers().UpdateOrderAction(r.Context(), orderID, body, extras)
	if err != nil {
		s.failResourceCommand(w, r, resourceOrders, orderID, "update", err)
		return
	}
	if orderCompletionStatus(body) {
		s.enqueueOrderCompletionNotification(item, body)
	}
	s.ok(w, r, item)
}

func (s *server) handleVoucherVerify(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	code := strings.TrimSpace(envDefault(stringValue(body["voucherId"]), stringValue(body["code"])))
	if code == "" {
		s.fail(w, r, http.StatusBadRequest, "VOUCHER_CODE_REQUIRED", "voucherId 또는 code가 필요합니다.", nil)
		return
	}
	if item, found, err := s.store.get(r.Context(), resourcePickupVouchers, code); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "픽업 바우처를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	} else if found {
		s.ok(w, r, item)
		return
	}
	items, err := s.store.listFiltered(r.Context(), resourcePickupVouchers, []resourceFilter{{Field: "code", Value: code}}, 1)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "픽업 바우처를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if len(items) > 0 {
		s.ok(w, r, items[0])
		return
	}
	s.fail(w, r, http.StatusNotFound, "VOUCHER_NOT_FOUND", "픽업 바우처를 찾을 수 없습니다.", nil)
}

func (s *server) handlePickupVoucherRedeem(w http.ResponseWriter, r *http.Request) {
	voucherID := r.PathValue("voucherId")
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().RedeemPickupVoucher(r.Context(), voucherID, body)
	if err != nil {
		s.failResourceCommand(w, r, resourcePickupVouchers, voucherID, "update", err)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handleBarcodeLookup(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	code := payBarcodeCode(firstNonEmpty(stringValue(body["barcode"]), stringValue(body["code"])))
	if code == "" {
		s.fail(w, r, http.StatusBadRequest, "BARCODE_REQUIRED", "대마페이 QR이 필요합니다.", nil)
		return
	}
	payments := s.payments()
	item, found, err := payments.PayBarcodeByCode(r.Context(), code)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_READ_FAILED", "결제 바코드를 읽지 못했습니다.", map[string]any{"cause": err.Error()})
		return
	}
	if found {
		s.ok(w, r, item)
		return
	}
	s.fail(w, r, http.StatusNotFound, "BARCODE_NOT_FOUND", "대마페이 QR을 찾을 수 없습니다.", nil)
}

func (s *server) handlePaymentIntent(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	item, created, err := s.payments().CreateIntent(r.Context(), session.User, body)
	if err != nil {
		s.handlePaymentIntentError(w, r, body, err)
		return
	}
	if !created {
		s.ok(w, r, item)
		return
	}
	s.created(w, r, item)
}

func (s *server) handlePaymentIntentError(w http.ResponseWriter, r *http.Request, body map[string]any, err error) {
	switch {
	case errors.Is(err, errInvalidPaymentAmount):
		s.fail(w, r, http.StatusBadRequest, "INVALID_PAYMENT_AMOUNT", "결제 금액은 1 이상의 정수여야 합니다.", map[string]any{"amount": body["amount"]})
	case errors.Is(err, errInvalidPaymentCurrency):
		s.fail(w, r, http.StatusBadRequest, "INVALID_PAYMENT_CURRENCY", "currency는 DMC 또는 POINT여야 합니다.", map[string]any{"currency": body["currency"]})
	case errors.Is(err, errPayBarcodeNotActive):
		barcodeCode := payBarcodeCode(firstNonEmpty(stringValue(body["barcodeValue"]), stringValue(body["barcode"]), stringValue(body["code"])))
		s.fail(w, r, http.StatusConflict, "PAY_BARCODE_NOT_ACTIVE", "사용할 수 없는 결제 바코드입니다.", map[string]any{"barcode": barcodeCode})
	case errors.Is(err, errPayBarcodeNotFound):
		barcodeCode := payBarcodeCode(firstNonEmpty(stringValue(body["barcodeValue"]), stringValue(body["barcode"]), stringValue(body["code"])))
		s.fail(w, r, http.StatusNotFound, "PAY_BARCODE_NOT_FOUND", "대마페이 QR을 찾을 수 없습니다.", map[string]any{"barcode": barcodeCode})
	case errors.Is(err, errPaymentCustomerRequired):
		s.fail(w, r, http.StatusBadRequest, "PAYMENT_CUSTOMER_REQUIRED", "유효한 대마페이 QR이 필요합니다.", nil)
	case errors.Is(err, errPaymentIntentIdempotencyConflict):
		id := resourceID(body, "payment-intent", "intentId", "idempotencyKey")
		s.fail(w, r, http.StatusConflict, "PAYMENT_INTENT_IDEMPOTENCY_CONFLICT", "결제 intent idempotency 정보가 기존 요청과 일치하지 않습니다.", map[string]any{"intentId": id})
	default:
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "결제 intent를 저장하지 못했습니다.", map[string]any{"cause": err.Error()})
	}
}

func (s *server) handlePaymentIntentCapture(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	intentID := r.PathValue("intentId")
	req := newPaymentCaptureRequest(body)
	item, created, err := s.payments().CaptureIntent(r.Context(), session.User, intentID, req)
	if err != nil {
		s.handlePaymentCaptureError(w, r, intentID, err)
		return
	}
	if created {
		s.created(w, r, item)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handlePaymentCaptureError(w http.ResponseWriter, r *http.Request, intentID string, err error) {
	switch {
	case errors.Is(err, errPaymentIntentNotFound):
		s.fail(w, r, http.StatusNotFound, "PAYMENT_INTENT_NOT_FOUND", "결제 intent를 찾을 수 없습니다.", map[string]any{"intentId": intentID})
	case errors.Is(err, errPaymentIntentClosed):
		s.fail(w, r, http.StatusConflict, "PAYMENT_INTENT_CLOSED", "결제 intent를 승인할 수 없는 상태입니다.", map[string]any{"intentId": intentID})
	case errors.Is(err, errInsufficientWalletBalance):
		s.fail(w, r, http.StatusBadRequest, "INSUFFICIENT_DMC_BALANCE", "대마코인 잔액이 부족합니다.", map[string]any{"intentId": intentID})
	case errors.Is(err, errLedgerIdempotencyConflict):
		s.fail(w, r, http.StatusConflict, "PAYMENT_CAPTURE_IDEMPOTENCY_CONFLICT", "결제 승인 idempotency 정보가 기존 원장과 일치하지 않습니다.", map[string]any{"intentId": intentID})
	default:
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "결제 승인에 실패했습니다.", map[string]any{"cause": err.Error()})
	}
}

func (s *server) handlePaymentIntentCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	intentID := r.PathValue("intentId")
	req := newPaymentCancelRequest(body)
	item, changed, err := s.payments().CancelIntent(r.Context(), session.User, intentID, req)
	if err != nil {
		s.handlePaymentCancelError(w, r, intentID, err)
		return
	}
	if changed {
		s.ok(w, r, item)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handlePaymentCancelError(w http.ResponseWriter, r *http.Request, intentID string, err error) {
	if errors.Is(err, errPaymentIntentNotFound) {
		s.fail(w, r, http.StatusNotFound, "PAYMENT_INTENT_NOT_FOUND", "결제 intent를 찾을 수 없습니다.", map[string]any{"intentId": intentID})
		return
	}
	if errors.Is(err, errPaymentIntentClosed) {
		s.fail(w, r, http.StatusConflict, "PAYMENT_INTENT_ALREADY_CAPTURED", "이미 승인된 결제 intent는 취소할 수 없습니다.", map[string]any{"intentId": intentID})
		return
	}
	s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "결제 intent 취소에 실패했습니다.", map[string]any{"cause": err.Error()})
}

func (s *server) handlePaymentRefund(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	paymentID := r.PathValue("paymentId")
	requestedAmount := 0
	if requestHasAmount(body) {
		var amountOK bool
		requestedAmount, amountOK = strictAmountValue(body)
		if !amountOK {
			s.fail(w, r, http.StatusBadRequest, "INVALID_REFUND_AMOUNT", "환불 금액은 1 이상의 정수여야 합니다.", map[string]any{"amount": body["amount"]})
			return
		}
	}
	req := newPaymentRefundRequest(paymentID, body, requestedAmount)
	item, created, err := s.payments().Refund(r.Context(), session.User, paymentID, req)
	if err != nil {
		s.handlePaymentRefundError(w, r, paymentID, err)
		return
	}
	if created {
		s.created(w, r, item)
		return
	}
	s.ok(w, r, item)
}

func (s *server) handlePaymentRefundError(w http.ResponseWriter, r *http.Request, paymentID string, err error) {
	switch {
	case errors.Is(err, errRefundIdempotencyRequired):
		s.fail(w, r, http.StatusBadRequest, "REFUND_IDEMPOTENCY_KEY_REQUIRED", "부분 환불에는 refundId 또는 idempotencyKey가 필요합니다.", nil)
	case errors.Is(err, errPaymentNotFound):
		s.fail(w, r, http.StatusNotFound, "PAYMENT_NOT_FOUND", "결제를 찾을 수 없습니다.", map[string]any{"paymentId": paymentID})
	case errors.Is(err, errPaymentClosed):
		s.fail(w, r, http.StatusConflict, "PAYMENT_CLOSED", "환불할 수 없는 결제 상태입니다.", map[string]any{"paymentId": paymentID})
	case errors.Is(err, errPaymentRefundExceeded):
		s.fail(w, r, http.StatusBadRequest, "REFUND_AMOUNT_EXCEEDED", "환불 금액이 남은 결제 금액을 초과합니다.", map[string]any{"paymentId": paymentID})
	case errors.Is(err, errLedgerIdempotencyConflict):
		s.fail(w, r, http.StatusConflict, "PAYMENT_REFUND_IDEMPOTENCY_CONFLICT", "환불 idempotency 정보가 기존 원장과 일치하지 않습니다.", map[string]any{"paymentId": paymentID})
	default:
		s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "결제 환불에 실패했습니다.", map[string]any{"cause": err.Error()})
	}
}

func paymentIntentRequestMatches(intent map[string]any, customerID, boothID, currency string, value int) bool {
	existingCurrency, ok := normalizedCurrency(firstNonEmpty(stringValue(intent["currency"]), currency))
	if !ok || existingCurrency != currency {
		return false
	}
	existingAmount, ok := strictAmountValue(intent)
	return ok &&
		existingAmount == value &&
		firstNonEmpty(stringValue(intent["customerId"]), stringValue(intent["userId"])) == customerID &&
		stringValue(intent["boothId"]) == boothID
}

func requestHasAmount(body map[string]any) bool {
	for _, field := range []string{"totalAmount", "amount", "price", "unitAmount"} {
		if _, ok := body[field]; ok {
			return true
		}
	}
	return false
}

func payBarcodeCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= len("DAEMA-PAY:") && strings.EqualFold(value[:len("DAEMA-PAY:")], "DAEMA-PAY:") {
		value = value[len("DAEMA-PAY:"):]
	}
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		value = parts[len(parts)-1]
	}
	value = strings.TrimSpace(value)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		default:
			return -1
		}
	}, value)
}

func payBarcodeActive(item map[string]any) bool {
	status := strings.ToLower(firstNonEmpty(stringValue(item["status"]), "active"))
	if status != "active" {
		return false
	}
	expiresAt := stringValue(item["expiresAt"])
	if expiresAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return time.Now().Before(parsed)
}

func (s *server) handleSellerPayments(w http.ResponseWriter, r *http.Request) {
	s.respondResourceList(w, r, resourcePayments, 100, resourceFilter{Field: "boothId", Value: r.PathValue("boothId")})
}

func (s *server) handleVisitVerify(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateVisit(r.Context(), r.PathValue("boothId"), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceVisits, "", "create", err)
		return
	}
	s.created(w, r, item)
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

func (s *server) handleInquiryReplyCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateInquiryReply(r.Context(), r.PathValue("inquiryId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceInquiryReplies, "", "create", err)
		return
	}
	s.created(w, r, item)
}

func (s *server) handleSellerNoticeCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateNotice(r.Context(), r.PathValue("boothId"), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceNotices, "", "create", err)
		return
	}
	s.created(w, r, item)
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
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "부스 계정 로그인이 필요합니다.", nil)
		return
	}
	if !resourceBelongsToBooth(item, session.User.BoothID) {
		s.fail(w, r, http.StatusForbidden, "BOOTH_SCOPE_REQUIRED", "해당 정산에 접근할 권한이 없습니다.", map[string]any{"settlementId": id})
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
	body, ok := s.requestMap(w, r)
	if !ok {
		return
	}
	item, err := s.sellers().CreateExport(r.Context(), r.PathValue("boothId"), s.currentUserID(r), body)
	if err != nil {
		s.failResourceCommand(w, r, resourceExports, "", "create", err)
		return
	}
	s.created(w, r, item)
}
