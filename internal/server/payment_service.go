package server

import (
	"context"
	"errors"
)

var (
	errInvalidPaymentAmount             = errors.New("payment amount must be a positive integer")
	errInvalidPaymentCurrency           = errors.New("payment currency is invalid")
	errPaymentCustomerRequired          = errors.New("payment customer is required")
	errPaymentCustomerNotFound          = errors.New("payment customer not found")
	errPayBarcodeNotActive              = errors.New("pay barcode is not active")
	errPaymentIntentIdempotencyConflict = errors.New("payment intent idempotency conflict")
	errRefundIdempotencyRequired        = errors.New("refund idempotency key is required")
)

type paymentService struct {
	store paymentServiceStore
}

func (s *server) payments() paymentService {
	return paymentService{store: s.store}
}

type paymentServiceStore interface {
	create(ctx context.Context, resource, id string, data map[string]any) (map[string]any, bool, error)
	get(ctx context.Context, resource, id string) (map[string]any, bool, error)
	listFiltered(ctx context.Context, resource string, filters []resourceFilter, limit int) ([]map[string]any, error)
	customerByPaymentIdentifier(ctx context.Context, identifier string) (map[string]any, bool, error)
	capturePaymentIntent(ctx context.Context, seller authUser, intentID string, req paymentCaptureRequest) (map[string]any, bool, error)
	cancelPaymentIntent(ctx context.Context, seller authUser, intentID string, req paymentCancelRequest) (map[string]any, bool, error)
	refundPayment(ctx context.Context, seller authUser, paymentID string, req paymentRefundRequest) (map[string]any, bool, error)
}

func (svc paymentService) CreateIntent(ctx context.Context, seller authUser, body map[string]any) (map[string]any, bool, error) {
	value, ok := strictAmountValue(body)
	if !ok {
		return nil, false, errInvalidPaymentAmount
	}
	currency, ok := normalizedCurrency(firstNonEmpty(stringValue(body["currency"]), "DMC"))
	if !ok {
		return nil, false, errInvalidPaymentCurrency
	}
	customerID := firstNonEmpty(stringValue(body["customerId"]), stringValue(body["userId"]))
	intent := cloneMap(body)
	barcodeCode := payBarcodeCode(firstNonEmpty(stringValue(body["barcodeValue"]), stringValue(body["barcode"]), stringValue(body["code"])))
	if barcodeCode != "" {
		barcode, found, err := svc.payBarcodeByCode(ctx, barcodeCode)
		if err != nil {
			return nil, false, err
		}
		if found {
			if !payBarcodeActive(barcode) {
				return nil, false, errPayBarcodeNotActive
			}
			customerID = firstNonEmpty(customerID, stringValue(barcode["userId"]), stringValue(barcode["customerId"]))
			intent["barcodeId"] = stringValue(barcode["id"])
			intent["barcodeCode"] = stringValue(barcode["code"])
		} else {
			customer, found, err := svc.customerByPaymentIdentifier(ctx, barcodeCode)
			if err != nil {
				return nil, false, err
			}
			if !found {
				return nil, false, errPaymentCustomerNotFound
			}
			customerID = firstNonEmpty(customerID, stringValue(customer["userId"]), stringValue(customer["id"]))
			intent["customerLookupCode"] = barcodeCode
		}
	}
	if customerID == "" {
		return nil, false, errPaymentCustomerRequired
	}
	id := resourceID(intent, "payment-intent", "intentId", "idempotencyKey")
	intent["sellerId"] = seller.ID
	intent["boothId"] = seller.BoothID
	intent["customerId"] = customerID
	intent["userId"] = customerID
	intent["status"] = "requires_capture"
	intent["currency"] = currency
	intent["amount"] = amount(currency, value)
	item, created, err := svc.store.create(ctx, resourcePaymentIntents, id, intent)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, _, err := svc.store.get(ctx, resourcePaymentIntents, id)
		if err != nil {
			return nil, false, err
		}
		if !paymentIntentRequestMatches(existing, customerID, seller.BoothID, currency, value) {
			return nil, false, errPaymentIntentIdempotencyConflict
		}
		return existing, false, nil
	}
	return item, true, nil
}

func (svc paymentService) CaptureIntent(ctx context.Context, seller authUser, intentID string, req paymentCaptureRequest) (map[string]any, bool, error) {
	return svc.store.capturePaymentIntent(ctx, seller, intentID, req)
}

func (svc paymentService) CancelIntent(ctx context.Context, seller authUser, intentID string, req paymentCancelRequest) (map[string]any, bool, error) {
	return svc.store.cancelPaymentIntent(ctx, seller, intentID, req)
}

func (svc paymentService) Refund(ctx context.Context, seller authUser, paymentID string, req paymentRefundRequest) (map[string]any, bool, error) {
	if req.RefundLedgerID == "" {
		return nil, false, errRefundIdempotencyRequired
	}
	return svc.store.refundPayment(ctx, seller, paymentID, req)
}

func (svc paymentService) PayBarcodeByCode(ctx context.Context, code string) (map[string]any, bool, error) {
	return svc.payBarcodeByCode(ctx, code)
}

func (svc paymentService) CustomerByPaymentIdentifier(ctx context.Context, identifier string) (map[string]any, bool, error) {
	return svc.customerByPaymentIdentifier(ctx, identifier)
}

func (svc paymentService) customerByPaymentIdentifier(ctx context.Context, identifier string) (map[string]any, bool, error) {
	identifier = payBarcodeCode(identifier)
	if identifier == "" {
		return nil, false, nil
	}
	return svc.store.customerByPaymentIdentifier(ctx, identifier)
}

func (svc paymentService) payBarcodeByCode(ctx context.Context, code string) (map[string]any, bool, error) {
	code = payBarcodeCode(code)
	if code == "" {
		return nil, false, nil
	}
	if item, found, err := svc.store.get(ctx, resourcePayBarcodes, code); err != nil || found {
		return item, found, err
	}
	items, err := svc.store.listFiltered(ctx, resourcePayBarcodes, []resourceFilter{{Field: "code", Value: code}}, 1)
	if err != nil {
		return nil, false, err
	}
	if len(items) == 0 {
		return nil, false, nil
	}
	return items[0], true, nil
}
