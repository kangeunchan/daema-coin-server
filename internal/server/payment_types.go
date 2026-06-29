package server

import (
	"errors"
	"fmt"
	"strings"
)

type paymentCaptureRequest struct {
	PaymentID string
	Extras    map[string]any
}

func newPaymentCaptureRequest(body map[string]any) paymentCaptureRequest {
	return paymentCaptureRequest{
		PaymentID: firstNonEmpty(stringValue(body["paymentId"]), stringValue(body["idempotencyKey"])),
		Extras:    cloneMap(body),
	}
}

type paymentCancelRequest struct {
	Extras map[string]any
}

func newPaymentCancelRequest(body map[string]any) paymentCancelRequest {
	return paymentCancelRequest{Extras: cloneMap(body)}
}

type paymentRefundRequest struct {
	RefundLedgerID  string
	RequestedAmount int
	Extras          map[string]any
}

func newPaymentRefundRequest(paymentID string, body map[string]any, requestedAmount int) paymentRefundRequest {
	refundID := firstNonEmpty(stringValue(body["refundId"]), stringValue(body["idempotencyKey"]), stringValue(body["ledgerId"]))
	if refundID == "" && requestedAmount <= 0 {
		refundID = ledgerID("payment-refund", paymentID, "full")
	}
	return paymentRefundRequest{
		RefundLedgerID:  refundID,
		RequestedAmount: requestedAmount,
		Extras:          cloneMap(body),
	}
}

type paymentIntentRecord struct {
	ID         string
	PaymentID  string
	BoothID    string
	CustomerID string
	Status     string
	Currency   string
	Amount     int
	BarcodeID  string
	OrderID    string
	Raw        map[string]any
}

func paymentIntentRecordFromMap(id string, item map[string]any) (paymentIntentRecord, error) {
	currency, ok := normalizedCurrency(firstNonEmpty(stringValue(item["currency"]), "DMC"))
	if !ok {
		return paymentIntentRecord{}, fmt.Errorf("unsupported payment currency %q", item["currency"])
	}
	value, ok := strictAmountValue(item)
	if !ok {
		return paymentIntentRecord{}, errors.New("payment intent amount must be a positive integer")
	}
	customerID := firstNonEmpty(stringValue(item["customerId"]), stringValue(item["userId"]))
	if customerID == "" {
		return paymentIntentRecord{}, errors.New("payment intent customer id is required")
	}
	return paymentIntentRecord{
		ID:         id,
		PaymentID:  stringValue(item["paymentId"]),
		BoothID:    stringValue(item["boothId"]),
		CustomerID: customerID,
		Status:     strings.ToLower(firstNonEmpty(stringValue(item["status"]), "requires_capture")),
		Currency:   currency,
		Amount:     value,
		BarcodeID:  stringValue(item["barcodeId"]),
		OrderID:    stringValue(item["orderId"]),
		Raw:        item,
	}, nil
}

type paymentRecord struct {
	ID              string
	PaymentIntentID string
	BoothID         string
	CustomerID      string
	Status          string
	Currency        string
	Amount          int
	OrderID         string
	Raw             map[string]any
}

func paymentRecordFromMap(id string, item map[string]any) (paymentRecord, error) {
	currency, ok := normalizedCurrency(firstNonEmpty(stringValue(item["currency"]), "DMC"))
	if !ok {
		return paymentRecord{}, fmt.Errorf("unsupported payment currency %q", item["currency"])
	}
	value, ok := strictAmountValue(item)
	if !ok {
		return paymentRecord{}, errors.New("payment amount must be a positive integer")
	}
	customerID := firstNonEmpty(stringValue(item["customerId"]), stringValue(item["userId"]))
	if customerID == "" {
		return paymentRecord{}, errors.New("payment customer id is required")
	}
	return paymentRecord{
		ID:              id,
		PaymentIntentID: stringValue(item["paymentIntentId"]),
		BoothID:         stringValue(item["boothId"]),
		CustomerID:      customerID,
		Status:          strings.ToLower(firstNonEmpty(stringValue(item["status"]), "captured")),
		Currency:        currency,
		Amount:          value,
		OrderID:         stringValue(item["orderId"]),
		Raw:             item,
	}, nil
}
