package server

import "testing"

func TestStrictAmountValueRejectsUnsafeAmounts(t *testing.T) {
	valid := []map[string]any{
		{"amount": 1},
		{"amount": int64(2)},
		{"amount": map[string]any{"value": 3}},
		{"totalAmount": "4000"},
	}
	for _, item := range valid {
		if got, ok := strictAmountValue(item); !ok || got <= 0 {
			t.Fatalf("strictAmountValue(%#v) = %d, %v; want positive integer", item, got, ok)
		}
	}

	invalid := []map[string]any{
		{},
		{"amount": 0},
		{"amount": -1},
		{"amount": 1.5},
		{"amount": "1.5"},
		{"amount": "abc"},
		{"amount": map[string]any{"value": 1.5}},
	}
	for _, item := range invalid {
		if got, ok := strictAmountValue(item); ok {
			t.Fatalf("strictAmountValue(%#v) = %d, true; want rejection", item, got)
		}
	}
}

func TestSettlePredictionsAllocatesPoolWithoutRoundingLoss(t *testing.T) {
	settlement, ledgerEntries, err := settlePredictions("match-money", "home", []map[string]any{
		{"id": "p1", "userId": "u1", "githubLogin": "u1", "pick": "home", "stakeAmount": 101},
		{"id": "p2", "userId": "u2", "githubLogin": "u2", "pick": "home", "stakeAmount": 99},
		{"id": "p3", "userId": "u3", "githubLogin": "u3", "pick": "away", "stakeAmount": 45},
		{"id": "p4", "userId": "u4", "githubLogin": "u4", "pick": "draw", "stakeAmount": 5},
	})
	if err != nil {
		t.Fatalf("settlePredictions failed: %v", err)
	}
	if settlement["totalPool"] != 250 {
		t.Fatalf("totalPool = %v, want 250", settlement["totalPool"])
	}
	if settlement["allocatedPointTotal"] != 250 {
		t.Fatalf("allocatedPointTotal = %v, want 250", settlement["allocatedPointTotal"])
	}

	payoutByUser := map[string]int{}
	for _, entry := range ledgerEntries {
		payoutByUser[stringValue(entry["userId"])] += amountValue(entry)
	}
	want := map[string]int{"u1": 124, "u2": 120, "u3": 5, "u4": 1}
	for userID, wantPayout := range want {
		if got := payoutByUser[userID]; got != wantPayout {
			t.Fatalf("payout[%s] = %d, want %d; entries=%#v", userID, got, wantPayout, ledgerEntries)
		}
	}
}

func TestPredictionTypedRequestsPreserveExtrasAndCanonicalFields(t *testing.T) {
	req, err := newPredictionCreateRequest(map[string]any{
		"pick":        "home",
		"stakeAmount": 250,
		"clientNote":  "front row",
	})
	if err != nil {
		t.Fatalf("newPredictionCreateRequest failed: %v", err)
	}
	if req.Pick != "home" || req.StakeAmount != 250 {
		t.Fatalf("prediction request = %#v, want pick home and stake 250", req)
	}
	if req.Extras["clientNote"] != "front row" {
		t.Fatalf("prediction extras = %#v, want clientNote preserved", req.Extras)
	}

	user := authUser{ID: "user-1", Login: "octo"}
	stake := newPredictionStakeRequest("match-1", user, req, "stake-ledger-1")
	if stake.StakeLedgerID != "stake-ledger-1" || stake.StakeAmount != 250 {
		t.Fatalf("stake request = %#v, want explicit ledger and amount", stake)
	}
	if stake.Prediction["matchId"] != "match-1" || stake.Prediction["userId"] != "user-1" || stake.Prediction["githubLogin"] != "octo" {
		t.Fatalf("stake prediction = %#v, want canonical identity fields", stake.Prediction)
	}
	if stake.Prediction["currency"] != predictionCurrency {
		t.Fatalf("stake currency = %v, want %s", stake.Prediction["currency"], predictionCurrency)
	}
}

func TestPredictionTypedRequestsRejectInvalidInput(t *testing.T) {
	if _, err := newPredictionCreateRequest(map[string]any{"pick": "left", "stakeAmount": 100}); err == nil {
		t.Fatal("invalid pick was accepted")
	}
	if _, err := newPredictionCreateRequest(map[string]any{"pick": "home", "stakeAmount": 0}); err == nil {
		t.Fatal("invalid stake was accepted")
	}
	if _, err := newPredictionCreateRequest(map[string]any{"pick": "home", "stakeAmount": 1.5}); err == nil {
		t.Fatal("fractional stake was accepted")
	}
}

func TestPredictionRecordValidatesSettlementFields(t *testing.T) {
	record, err := predictionRecordFromMap(map[string]any{
		"id":            "prediction-1",
		"matchId":       "match-1",
		"userId":        "user-1",
		"githubLogin":   "octo",
		"pick":          "away",
		"stakeAmount":   120,
		"stakeLedgerId": "stake-1",
	})
	if err != nil {
		t.Fatalf("predictionRecordFromMap failed: %v", err)
	}
	if record.Pick != "away" || record.StakeAmount != 120 || record.UserID != "user-1" || record.StakeLedgerID != "stake-1" {
		t.Fatalf("prediction record = %#v, want normalized prediction fields", record)
	}

	if _, err := predictionRecordFromMap(map[string]any{"pick": "bad", "stakeAmount": 100}); err == nil {
		t.Fatal("invalid record pick was accepted")
	}
	if _, err := predictionRecordFromMap(map[string]any{"pick": "home", "stakeAmount": 0}); err == nil {
		t.Fatal("invalid record stake was accepted")
	}
}

func TestSettlePredictionsSkipsInvalidRows(t *testing.T) {
	settlement, ledgerEntries, err := settlePredictions("match-money", "home", []map[string]any{
		{"id": "p1", "userId": "u1", "githubLogin": "u1", "pick": "home", "stakeAmount": 100},
		{"id": "bad-pick", "userId": "u2", "githubLogin": "u2", "pick": "side", "stakeAmount": 100},
		{"id": "bad-stake", "userId": "u3", "githubLogin": "u3", "pick": "away", "stakeAmount": 0},
	})
	if err != nil {
		t.Fatalf("settlePredictions failed: %v", err)
	}
	if settlement["participantCount"] != 1 {
		t.Fatalf("participantCount = %v, want 1", settlement["participantCount"])
	}
	if len(ledgerEntries) != 1 || stringValue(ledgerEntries[0]["userId"]) != "u1" {
		t.Fatalf("ledgerEntries = %#v, want only valid user", ledgerEntries)
	}
}

func TestPaymentIntentRequestMatchesStrictly(t *testing.T) {
	intent := map[string]any{
		"customerId": "customer-1",
		"boothId":    "booth-1",
		"currency":   "DMC",
		"amount":     amount("DMC", 700),
	}
	if !paymentIntentRequestMatches(intent, "customer-1", "booth-1", "DMC", 700) {
		t.Fatal("matching payment intent was rejected")
	}
	if paymentIntentRequestMatches(intent, "customer-1", "booth-1", "DMC", 701) {
		t.Fatal("different amount was accepted")
	}
	if paymentIntentRequestMatches(intent, "customer-2", "booth-1", "DMC", 700) {
		t.Fatal("different customer was accepted")
	}
	if paymentIntentRequestMatches(intent, "customer-1", "booth-2", "DMC", 700) {
		t.Fatal("different booth was accepted")
	}
}

func TestPaymentTypedRequestsPreserveExtrasAndIdempotencyKeys(t *testing.T) {
	capture := newPaymentCaptureRequest(map[string]any{
		"idempotencyKey": "payment-1",
		"operatorNote":   "counter checkout",
	})
	if capture.PaymentID != "payment-1" {
		t.Fatalf("capture PaymentID = %q, want payment-1", capture.PaymentID)
	}
	if capture.Extras["operatorNote"] != "counter checkout" {
		t.Fatalf("capture extras = %#v, want operatorNote preserved", capture.Extras)
	}

	cancel := newPaymentCancelRequest(map[string]any{"reason": "customer_left"})
	if cancel.Extras["reason"] != "customer_left" {
		t.Fatalf("cancel extras = %#v, want reason preserved", cancel.Extras)
	}

	fullRefund := newPaymentRefundRequest("payment-1", nil, 0)
	if fullRefund.RefundLedgerID != ledgerID("payment-refund", "payment-1", "full") {
		t.Fatalf("full refund id = %q, want generated full refund id", fullRefund.RefundLedgerID)
	}

	partialRefund := newPaymentRefundRequest("payment-1", map[string]any{
		"refundId": "refund-1",
		"reason":   "partial",
	}, 300)
	if partialRefund.RefundLedgerID != "refund-1" || partialRefund.RequestedAmount != 300 {
		t.Fatalf("partial refund = %#v, want explicit id and amount", partialRefund)
	}
	if partialRefund.Extras["reason"] != "partial" {
		t.Fatalf("partial refund extras = %#v, want reason preserved", partialRefund.Extras)
	}

	missingPartialRefundID := newPaymentRefundRequest("payment-1", nil, 300)
	if missingPartialRefundID.RefundLedgerID != "" {
		t.Fatalf("missing partial refund id = %q, want empty id for handler validation", missingPartialRefundID.RefundLedgerID)
	}
}

func TestPaymentRecordsValidateRequiredMoneyFields(t *testing.T) {
	intent, err := paymentIntentRecordFromMap("intent-1", map[string]any{
		"paymentId":  "payment-1",
		"boothId":    "booth-1",
		"customerId": "customer-1",
		"status":     "requires_capture",
		"currency":   "dmc",
		"amount":     amount("DMC", 900),
		"barcodeId":  "barcode-1",
		"orderId":    "order-1",
	})
	if err != nil {
		t.Fatalf("paymentIntentRecordFromMap failed: %v", err)
	}
	if intent.Currency != "DMC" || intent.Amount != 900 || intent.CustomerID != "customer-1" || intent.OrderID != "order-1" {
		t.Fatalf("intent record = %#v, want normalized money and identifiers", intent)
	}

	if _, err := paymentIntentRecordFromMap("intent-2", map[string]any{
		"customerId": "customer-1",
		"currency":   "BTC",
		"amount":     amount("BTC", 1),
	}); err == nil {
		t.Fatal("unsupported intent currency was accepted")
	}
	if _, err := paymentIntentRecordFromMap("intent-3", map[string]any{
		"customerId": "customer-1",
		"currency":   "DMC",
		"amount":     0,
	}); err == nil {
		t.Fatal("invalid intent amount was accepted")
	}
	if _, err := paymentIntentRecordFromMap("intent-4", map[string]any{
		"currency": "DMC",
		"amount":   1,
	}); err == nil {
		t.Fatal("missing intent customer was accepted")
	}

	payment, err := paymentRecordFromMap("payment-1", map[string]any{
		"paymentIntentId": "intent-1",
		"boothId":         "booth-1",
		"userId":          "customer-1",
		"status":          "captured",
		"currency":        "point",
		"amount":          amount("POINT", 100),
	})
	if err != nil {
		t.Fatalf("paymentRecordFromMap failed: %v", err)
	}
	if payment.Currency != "POINT" || payment.Amount != 100 || payment.CustomerID != "customer-1" || payment.PaymentIntentID != "intent-1" {
		t.Fatalf("payment record = %#v, want normalized money and identifiers", payment)
	}
}

func TestWalletLedgerRequestCopiesExtras(t *testing.T) {
	extras := map[string]any{"referenceType": "payment", "referenceId": "payment-1"}
	req := newWalletLedgerRequest(authUser{ID: "user-1", Login: "octo"}, "ledger-1", "payment-capture", "expense", "DMC", 100, extras)
	extras["referenceId"] = "changed"

	if req.User.ID != "user-1" || req.ID != "ledger-1" || req.Type != "payment-capture" || req.Direction != "expense" || req.Currency != "DMC" || req.Amount != 100 {
		t.Fatalf("wallet ledger request = %#v, want canonical ledger fields", req)
	}
	if req.Extras["referenceId"] != "payment-1" {
		t.Fatalf("wallet ledger extras = %#v, want copied metadata", req.Extras)
	}
}
