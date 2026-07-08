package server

import (
	"context"
	"errors"
	"time"
)

var (
	errCustomerProductRequired = errors.New("productId is required")
	errCustomerOrderRequired   = errors.New("order product or items are required")
	errCustomerQuantityInvalid = errors.New("quantity must be a positive integer")
	errCustomerProductNotFound = errors.New("product not found")
	errCustomerProductClosed   = errors.New("product is not available")
	errCustomerStockShortage   = errors.New("product stock is not enough")
)

type customerService struct {
	resources resourceCommandService
}

func (s *server) customers() customerService {
	return customerService{resources: s.resourceCommands()}
}

func (svc customerService) ClaimBenefit(ctx context.Context, benefitID, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceBenefitClaims, Prefix: "benefit-claim", Body: body, Extras: map[string]any{"benefitId": benefitID, "userId": userID}, IDCandidates: []string{"claimId"}})
}

func (svc customerService) CreatePayBarcode(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourcePayBarcodes, Prefix: "barcode", Body: body, Extras: map[string]any{"code": randomToken()[:24], "userId": userID, "expiresAt": expiresAt}, IDCandidates: []string{"barcodeId"}})
}

func (svc customerService) CreateProductView(ctx context.Context, productID, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceProductViews, Prefix: "product-view", Body: body, Extras: map[string]any{"productId": productID, "userId": userID}, IDCandidates: []string{"viewId"}})
}

func (svc customerService) CheckInBooth(ctx context.Context, boothID, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceBoothCheckins, Prefix: "checkin", Body: body, Extras: map[string]any{"boothId": boothID, "userId": userID}, IDCandidates: []string{"checkinId"}})
}

func (svc customerService) CreateAnalyticsImpression(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceAnalyticsImpressions, Prefix: "impression", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"impressionId"}})
}

func (svc customerService) CreateCartItem(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	if stringValue(body["productId"]) == "" {
		return nil, errCustomerProductRequired
	}
	if !optionalPositiveQuantity(body) {
		return nil, errCustomerQuantityInvalid
	}
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceCartItems, Prefix: "cart-item", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"cartItemId", "productId"}})
}

func (svc customerService) CreateOrder(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	if stringValue(body["productId"]) == "" && !nonEmptyArray(body["items"]) && !nonEmptyArray(body["cartItems"]) {
		return nil, errCustomerOrderRequired
	}
	if !optionalPositiveQuantity(body) {
		return nil, errCustomerQuantityInvalid
	}
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceOrders, Prefix: "order", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"orderId"}})
}

func (svc customerService) CreateFavorite(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceFavorites, Prefix: "favorite", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"favoriteId", "targetId"}})
}

func (svc customerService) CreateInquiry(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceInquiries, Prefix: "inquiry", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"inquiryId"}})
}

func (svc customerService) CreateShare(ctx context.Context, userID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceShares, Prefix: "share", Body: body, Extras: map[string]any{"userId": userID}, IDCandidates: []string{"shareId"}})
}

func optionalPositiveQuantity(body map[string]any) bool {
	value, ok := body["quantity"]
	if !ok {
		return true
	}
	_, amountOK := positiveIntegerValue(value)
	return amountOK
}

func nonEmptyArray(value any) bool {
	items, ok := value.([]any)
	return ok && len(items) > 0
}
