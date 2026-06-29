package server

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *postgresStore) capturePaymentIntent(ctx context.Context, seller authUser, intentID string, req paymentCaptureRequest) (map[string]any, bool, error) {
	var item map[string]any
	var created bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		intent, found, err := s.getResourceTx(ctx, tx, resourcePaymentIntents, intentID, true)
		if err != nil {
			return err
		}
		if !found {
			return errPaymentIntentNotFound
		}
		if seller.BoothID != "" && stringValue(intent["boothId"]) != seller.BoothID {
			return errPaymentIntentNotFound
		}
		status := strings.ToLower(firstNonEmpty(stringValue(intent["status"]), "requires_capture"))
		if status == "captured" {
			existingPaymentID := firstNonEmpty(stringValue(intent["paymentId"]), req.PaymentID, ledgerID("payment", intentID))
			payment, ok, getErr := s.getResourceTx(ctx, tx, resourcePayments, existingPaymentID, true)
			if getErr != nil {
				return getErr
			}
			if !ok {
				return errPaymentIntentClosed
			}
			item = payment
			created = false
			return nil
		}
		if status != "requires_capture" && status != "requires-capture" {
			return errPaymentIntentClosed
		}
		intentRecord, err := paymentIntentRecordFromMap(intentID, intent)
		if err != nil {
			return err
		}
		paymentID := req.PaymentID
		if paymentID == "" {
			paymentID = firstNonEmpty(intentRecord.PaymentID, ledgerID("payment", intentID))
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if barcodeID := intentRecord.BarcodeID; barcodeID != "" {
			barcode, ok, barcodeErr := s.getResourceTx(ctx, tx, resourcePayBarcodes, barcodeID, true)
			if barcodeErr != nil {
				return barcodeErr
			}
			if !ok || !payBarcodeActive(barcode) {
				return errPaymentIntentClosed
			}
			barcode["status"] = "used"
			barcode["usedAt"] = now
			if _, err := s.putResourceTx(ctx, tx, resourcePayBarcodes, barcodeID, barcode); err != nil {
				return err
			}
		}
		payment := cloneMap(req.Extras)
		payment["paymentIntentId"] = intentID
		payment["orderId"] = intentRecord.OrderID
		payment["boothId"] = intentRecord.BoothID
		payment["customerId"] = intentRecord.CustomerID
		payment["userId"] = intentRecord.CustomerID
		payment["sellerId"] = seller.ID
		payment["status"] = "captured"
		payment["currency"] = intentRecord.Currency
		payment["amount"] = amount(intentRecord.Currency, intentRecord.Amount)
		payment["capturedAt"] = now

		item, created, err = s.createResourceTx(ctx, tx, resourcePayments, paymentID, payment)
		if err != nil {
			return err
		}
		if !created {
			existing, _, getErr := s.getResourceTx(ctx, tx, resourcePayments, paymentID, true)
			if getErr != nil {
				return getErr
			}
			if !paymentMatches(existing, intentID, intentRecord.CustomerID, intentRecord.BoothID, intentRecord.Currency, intentRecord.Amount) {
				return errLedgerIdempotencyConflict
			}
			item = existing
			return nil
		}

		ledgerExtras := map[string]any{
			"paymentId":       paymentID,
			"paymentIntentId": intentID,
			"orderId":         intentRecord.OrderID,
			"boothId":         intentRecord.BoothID,
			"referenceType":   "payment",
			"referenceId":     paymentID,
			"description":     "대마페이 결제",
		}
		if _, err = s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(authUser{ID: intentRecord.CustomerID}, ledgerID("payment-capture", paymentID), "payment-capture", "expense", intentRecord.Currency, intentRecord.Amount, ledgerExtras)); err != nil {
			return err
		}

		intent["status"] = "captured"
		intent["paymentId"] = paymentID
		intent["capturedAt"] = now
		if _, err := s.putResourceTx(ctx, tx, resourcePaymentIntents, intentID, intent); err != nil {
			return err
		}
		if intentRecord.OrderID != "" {
			if order, ok, err := s.getResourceTx(ctx, tx, resourceOrders, intentRecord.OrderID, true); err != nil {
				return err
			} else if ok {
				order["status"] = "paid"
				order["paymentId"] = paymentID
				order["paidAt"] = now
				if _, err := s.putResourceTx(ctx, tx, resourceOrders, intentRecord.OrderID, order); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return item, created, nil
}

func (s *postgresStore) cancelPaymentIntent(ctx context.Context, seller authUser, intentID string, req paymentCancelRequest) (map[string]any, bool, error) {
	var item map[string]any
	var changed bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		intent, found, err := s.getResourceTx(ctx, tx, resourcePaymentIntents, intentID, true)
		if err != nil {
			return err
		}
		if !found {
			return errPaymentIntentNotFound
		}
		if seller.BoothID != "" && stringValue(intent["boothId"]) != seller.BoothID {
			return errPaymentIntentNotFound
		}
		status := strings.ToLower(firstNonEmpty(stringValue(intent["status"]), "requires_capture"))
		if status == "captured" {
			return errPaymentIntentClosed
		}
		if status == "cancelled" || status == "canceled" || status == "expired" {
			item = intent
			changed = false
			return nil
		}
		for key, value := range req.Extras {
			intent[key] = value
		}
		intent["status"] = "cancelled"
		intent["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
		item, err = s.putResourceTx(ctx, tx, resourcePaymentIntents, intentID, intent)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return item, changed, nil
}

func (s *postgresStore) refundPayment(ctx context.Context, seller authUser, paymentID string, req paymentRefundRequest) (map[string]any, bool, error) {
	var item map[string]any
	var created bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		payment, found, err := s.getResourceTx(ctx, tx, resourcePayments, paymentID, true)
		if err != nil {
			return err
		}
		if !found {
			return errPaymentNotFound
		}
		if seller.BoothID != "" && stringValue(payment["boothId"]) != seller.BoothID {
			return errPaymentNotFound
		}
		if refundAlreadyRecorded(payment, req.RefundLedgerID) {
			item = payment
			created = false
			return nil
		}
		status := strings.ToLower(firstNonEmpty(stringValue(payment["status"]), "captured"))
		if status == "refunded" || status == "voided" {
			return errPaymentClosed
		}
		paymentRecord, err := paymentRecordFromMap(paymentID, payment)
		if err != nil {
			return err
		}
		refundedAmount := refundedPaymentAmount(payment)
		remainingAmount := paymentRecord.Amount - refundedAmount
		if remainingAmount <= 0 {
			return errPaymentClosed
		}
		amountToRefund := req.RequestedAmount
		if amountToRefund <= 0 {
			amountToRefund = remainingAmount
		}
		if amountToRefund > remainingAmount {
			return errPaymentRefundExceeded
		}
		now := time.Now().UTC().Format(time.RFC3339)
		ledgerExtras := map[string]any{
			"paymentId":       paymentID,
			"paymentIntentId": paymentRecord.PaymentIntentID,
			"orderId":         paymentRecord.OrderID,
			"boothId":         paymentRecord.BoothID,
			"refundId":        req.RefundLedgerID,
			"referenceType":   "payment-refund",
			"referenceId":     req.RefundLedgerID,
			"description":     "대마페이 결제 환불",
		}
		if _, err = s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(authUser{ID: paymentRecord.CustomerID}, req.RefundLedgerID, "payment-refund", "income", paymentRecord.Currency, amountToRefund, ledgerExtras)); err != nil {
			return err
		}
		for key, value := range req.Extras {
			payment[key] = value
		}
		totalRefunded := refundedAmount + amountToRefund
		status = "partially_refunded"
		if totalRefunded == paymentRecord.Amount {
			status = "refunded"
		}
		payment["status"] = status
		payment["refundedAmount"] = amount(paymentRecord.Currency, totalRefunded)
		payment["refundedAt"] = now
		payment["refunds"] = appendRefundRecord(payment["refunds"], map[string]any{
			"id":        req.RefundLedgerID,
			"amount":    amount(paymentRecord.Currency, amountToRefund),
			"createdAt": now,
		})
		item, err = s.putResourceTx(ctx, tx, resourcePayments, paymentID, payment)
		if err != nil {
			return err
		}
		if orderID := stringValue(payment["orderId"]); orderID != "" && status == "refunded" {
			if order, ok, err := s.getResourceTx(ctx, tx, resourceOrders, orderID, true); err != nil {
				return err
			} else if ok {
				order["status"] = "refunded"
				order["refundedAt"] = now
				if _, err := s.putResourceTx(ctx, tx, resourceOrders, orderID, order); err != nil {
					return err
				}
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return item, created, nil
}

func paymentMatches(payment map[string]any, intentID, customerID, boothID, currency string, value int) bool {
	existingCurrency, ok := normalizedCurrency(firstNonEmpty(stringValue(payment["currency"]), currency))
	if !ok || existingCurrency != currency {
		return false
	}
	existingAmount, ok := strictAmountValue(payment)
	return ok &&
		existingAmount == value &&
		stringValue(payment["paymentIntentId"]) == intentID &&
		firstNonEmpty(stringValue(payment["customerId"]), stringValue(payment["userId"])) == customerID &&
		stringValue(payment["boothId"]) == boothID
}

func refundedPaymentAmount(payment map[string]any) int {
	if amountMap, ok := payment["refundedAmount"].(map[string]any); ok {
		if n, ok := positiveIntegerValue(amountMap["value"]); ok {
			return n
		}
	}
	if n, ok := positiveIntegerValue(payment["refundedAmount"]); ok {
		return n
	}
	return 0
}

func refundAlreadyRecorded(payment map[string]any, refundID string) bool {
	if refundID == "" {
		return false
	}
	refunds, ok := payment["refunds"].([]any)
	if !ok {
		return false
	}
	for _, refund := range refunds {
		item, ok := refund.(map[string]any)
		if ok && stringValue(item["id"]) == refundID {
			return true
		}
	}
	return false
}

func appendRefundRecord(existing any, record map[string]any) []any {
	refunds, _ := existing.([]any)
	out := make([]any, 0, len(refunds)+1)
	out = append(out, refunds...)
	out = append(out, record)
	return out
}
