package server

import (
	"context"
	"time"
)

type sellerService struct {
	resources resourceCommandService
}

func (s *server) sellers() sellerService {
	return sellerService{resources: s.resourceCommands()}
}

func (svc sellerService) UpdateBooth(ctx context.Context, boothID, sellerID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceBooths, ID: boothID, Body: body, Extras: map[string]any{"sellerId": sellerID}})
}

func (svc sellerService) UpdateBoothStatus(ctx context.Context, boothID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceBooths, ID: boothID, Body: body})
}

func (svc sellerService) CreateStaff(ctx context.Context, boothID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceStaff, Prefix: "staff", Body: body, Extras: map[string]any{"boothId": boothID}, IDCandidates: []string{"staffId"}})
}

func (svc sellerService) UpdateStaff(ctx context.Context, boothID, staffID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceStaff, ID: staffID, Body: body, Extras: map[string]any{"boothId": boothID}})
}

func (svc sellerService) CreateProduct(ctx context.Context, boothID, sellerID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceProducts, Prefix: "product", Body: body, Extras: map[string]any{"boothId": boothID, "sellerId": sellerID}, IDCandidates: []string{"productId"}})
}

func (svc sellerService) UpdateProduct(ctx context.Context, productID, boothID, sellerID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceProducts, ID: productID, Body: body, Extras: map[string]any{"sellerId": sellerID, "boothId": boothID}})
}

func (svc sellerService) UpdateProductStatus(ctx context.Context, productID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceProducts, ID: productID, Body: body})
}

func (svc sellerService) CreateProductImage(ctx context.Context, productID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceUploads, Prefix: "upload", Body: body, Extras: map[string]any{"productId": productID}, IDCandidates: []string{"uploadId", "imageId"}})
}

func (svc sellerService) CreateInventoryAdjustment(ctx context.Context, productID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceInventory, Prefix: "inventory", Body: body, Extras: map[string]any{"productId": productID}, IDCandidates: []string{"adjustmentId", "inventoryId"}})
}

func (svc sellerService) UpdateOrderAction(ctx context.Context, orderID string, body, extras map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{Resource: resourceOrders, ID: orderID, Body: body, Extras: extras})
}

func (svc sellerService) RedeemPickupVoucher(ctx context.Context, voucherID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Patch(ctx, patchResourceCommand{
		Resource: resourcePickupVouchers,
		ID:       voucherID,
		Body:     body,
		Extras:   map[string]any{"redeemedAt": time.Now().UTC().Format(time.RFC3339)},
	})
}

func (svc sellerService) CreateVisit(ctx context.Context, boothID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceVisits, Prefix: "visit", Body: body, Extras: map[string]any{"boothId": boothID}, IDCandidates: []string{"visitId"}})
}

func (svc sellerService) CreateInquiryReply(ctx context.Context, inquiryID, sellerID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceInquiryReplies, Prefix: "reply", Body: body, Extras: map[string]any{"inquiryId": inquiryID, "sellerId": sellerID}, IDCandidates: []string{"replyId"}})
}

func (svc sellerService) CreateNotice(ctx context.Context, boothID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceNotices, Prefix: "notice", Body: body, Extras: map[string]any{"boothId": boothID}, IDCandidates: []string{"noticeId"}})
}

func (svc sellerService) CreateExport(ctx context.Context, boothID, sellerID string, body map[string]any) (map[string]any, error) {
	return svc.resources.Create(ctx, createResourceCommand{Resource: resourceExports, Prefix: "export", Body: body, Extras: map[string]any{"boothId": boothID, "sellerId": sellerID}, IDCandidates: []string{"exportId"}})
}
