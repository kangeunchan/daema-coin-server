package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakePaymentStore struct {
	items map[string]map[string]map[string]any
}

func newFakePaymentStore() *fakePaymentStore {
	return &fakePaymentStore{items: map[string]map[string]map[string]any{}}
}

func (f *fakePaymentStore) create(_ context.Context, resource, id string, data map[string]any) (map[string]any, bool, error) {
	if f.items[resource] == nil {
		f.items[resource] = map[string]map[string]any{}
	}
	if existing, ok := f.items[resource][id]; ok {
		return cloneMap(existing), false, nil
	}
	item := cloneMap(data)
	item["id"] = id
	f.items[resource][id] = item
	return cloneMap(item), true, nil
}

func (f *fakePaymentStore) get(_ context.Context, resource, id string) (map[string]any, bool, error) {
	item, ok := f.items[resource][id]
	if !ok {
		return nil, false, nil
	}
	return cloneMap(item), true, nil
}

func (f *fakePaymentStore) listFiltered(_ context.Context, resource string, filters []resourceFilter, limit int) ([]map[string]any, error) {
	items := []map[string]any{}
	for _, item := range f.items[resource] {
		matches := true
		for _, filter := range filters {
			if item[filter.Field] != filter.Value {
				matches = false
				break
			}
		}
		if matches {
			items = append(items, cloneMap(item))
			if limit > 0 && len(items) >= limit {
				break
			}
		}
	}
	return items, nil
}

func (f *fakePaymentStore) capturePaymentIntent(context.Context, authUser, string, paymentCaptureRequest) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakePaymentStore) cancelPaymentIntent(context.Context, authUser, string, paymentCancelRequest) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakePaymentStore) refundPayment(context.Context, authUser, string, paymentRefundRequest) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func TestPaymentServiceCreateIntentUsesActiveBarcodeCustomer(t *testing.T) {
	store := newFakePaymentStore()
	store.items[resourcePayBarcodes] = map[string]map[string]any{
		"pay-1": {
			"id":        "pay-1",
			"code":      "PAY-001",
			"userId":    "customer-1",
			"status":    "active",
			"expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	svc := paymentService{store: store}

	item, created, err := svc.CreateIntent(context.Background(), authUser{ID: "seller-1", BoothID: "booth-1"}, map[string]any{
		"idempotencyKey": "intent-1",
		"barcode":        "DAEMA-PAY:PAY-001",
		"amount":         900,
	})
	if err != nil {
		t.Fatalf("CreateIntent failed: %v", err)
	}
	if !created {
		t.Fatal("CreateIntent returned existing item, want created")
	}
	if item["customerId"] != "customer-1" || item["boothId"] != "booth-1" || item["status"] != "requires_capture" {
		t.Fatalf("created intent = %#v, want canonical customer, booth, status", item)
	}
	if item["amount"].(map[string]any)["value"] != 900 {
		t.Fatalf("created intent amount = %#v, want value 900", item["amount"])
	}
}

func TestPaymentServiceCreateIntentRejectsIdempotencyConflict(t *testing.T) {
	store := newFakePaymentStore()
	store.items[resourcePaymentIntents] = map[string]map[string]any{
		"intent-1": {
			"id":         "intent-1",
			"customerId": "customer-1",
			"boothId":    "booth-1",
			"currency":   "DMC",
			"amount":     amount("DMC", 500),
		},
	}
	svc := paymentService{store: store}

	_, _, err := svc.CreateIntent(context.Background(), authUser{ID: "seller-1", BoothID: "booth-1"}, map[string]any{
		"idempotencyKey": "intent-1",
		"customerId":     "customer-1",
		"amount":         900,
	})
	if !errors.Is(err, errPaymentIntentIdempotencyConflict) {
		t.Fatalf("CreateIntent err = %v, want idempotency conflict", err)
	}
}

type fakeAdminAccountStore struct {
	accounts map[string]internalAccount
}

func (f *fakeAdminAccountStore) internalAccount(_ context.Context, id string) (internalAccount, bool, error) {
	account, ok := f.accounts[id]
	return account, ok, nil
}

func (f *fakeAdminAccountStore) internalAccountByLogin(_ context.Context, loginID string) (internalAccount, bool, error) {
	for _, account := range f.accounts {
		if account.LoginID == loginID {
			return account, true, nil
		}
	}
	return internalAccount{}, false, nil
}

func (f *fakeAdminAccountStore) saveInternalAccount(_ context.Context, account internalAccount) (bool, error) {
	if f.accounts == nil {
		f.accounts = map[string]internalAccount{}
	}
	_, exists := f.accounts[account.ID]
	f.accounts[account.ID] = account
	return !exists, nil
}

func TestAdminAccountServiceValidatesRoleSpecificFields(t *testing.T) {
	svc := adminAccountService{store: &fakeAdminAccountStore{}}
	_, err := svc.Create(context.Background(), internalAccountInput{
		LoginID:  "booth-login",
		Password: "long-enough-password",
		Role:     roleBooth,
	})
	if !errors.Is(err, errBoothAccountMissingID) {
		t.Fatalf("Create err = %v, want missing booth id", err)
	}
}

type fakePredictionDependencies struct {
	match        worldcupMatch
	matchFound   bool
	balance      int
	createdStake predictionStakeRequest
	items        map[string]map[string]any
}

func (f *fakePredictionDependencies) worldcupMatchByID(context.Context, string) (worldcupMatch, bool, error) {
	return f.match, f.matchFound, nil
}

func (f *fakePredictionDependencies) walletBalance(context.Context, string, string) (int, error) {
	return f.balance, nil
}

func (f *fakePredictionDependencies) get(_ context.Context, resource, id string) (map[string]any, bool, error) {
	item, ok := f.items[resource+":"+id]
	return cloneMap(item), ok, nil
}

func (f *fakePredictionDependencies) listFiltered(context.Context, string, []resourceFilter, int) ([]map[string]any, error) {
	return nil, nil
}

func (f *fakePredictionDependencies) createWorldcupPredictionWithStake(_ context.Context, _ authUser, predictionID string, req predictionStakeRequest) (map[string]any, bool, error) {
	f.createdStake = req
	item := cloneMap(req.Prediction)
	item["id"] = predictionID
	return item, true, nil
}

func (f *fakePredictionDependencies) cancelWorldcupPredictionWithRefund(context.Context, authUser, string, predictionCancelRequest) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (f *fakePredictionDependencies) createPredictionSettlementWithLedger(context.Context, string, map[string]any, []map[string]any) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func TestPredictionServiceCreateUsesTypedStakeRequest(t *testing.T) {
	deps := &fakePredictionDependencies{
		match:      worldcupMatch{ID: "match-1", Status: "scheduled"},
		matchFound: true,
		balance:    1000,
		items:      map[string]map[string]any{},
	}
	svc := predictionService{matches: deps, wallet: deps, store: deps}

	result, err := svc.Create(context.Background(), authUser{ID: "user-1", Login: "octo"}, "match-1", map[string]any{
		"pick":        "home",
		"stakeAmount": 300,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !result.Created || result.Item["matchId"] != "match-1" || result.Item["userId"] != "user-1" {
		t.Fatalf("Create result = %#v, want created prediction with canonical fields", result)
	}
	if deps.createdStake.StakeAmount != 300 || deps.createdStake.Prediction["currency"] != predictionCurrency {
		t.Fatalf("stake request = %#v, want typed amount and currency", deps.createdStake)
	}
}

type fakeResourceCommandStore struct {
	putCalls int
	items    map[string]map[string]any
}

func (f *fakeResourceCommandStore) put(_ context.Context, resource, id string, data map[string]any) (map[string]any, error) {
	f.putCalls++
	if f.items == nil {
		f.items = map[string]map[string]any{}
	}
	item := cloneMap(data)
	item["id"] = id
	f.items[resource+":"+id] = item
	return cloneMap(item), nil
}

func (f *fakeResourceCommandStore) patch(context.Context, string, string, map[string]any) (map[string]any, bool, error) {
	return nil, false, errors.New("not implemented")
}

func TestCustomerServiceValidatesCartAndOrderMutations(t *testing.T) {
	store := &fakeResourceCommandStore{}
	svc := customerService{resources: resourceCommandService{store: store}}

	if _, err := svc.CreateCartItem(context.Background(), "user-1", map[string]any{"quantity": 1}); !errors.Is(err, errCustomerProductRequired) {
		t.Fatalf("CreateCartItem missing product err = %v, want product required", err)
	}
	if store.putCalls != 0 {
		t.Fatalf("invalid cart item called store %d times, want 0", store.putCalls)
	}
	if _, err := svc.CreateCartItem(context.Background(), "user-1", map[string]any{"productId": "product-1", "quantity": 0}); !errors.Is(err, errCustomerQuantityInvalid) {
		t.Fatalf("CreateCartItem invalid quantity err = %v, want quantity invalid", err)
	}
	if _, err := svc.CreateOrder(context.Background(), "user-1", map[string]any{"quantity": 1}); !errors.Is(err, errCustomerOrderRequired) {
		t.Fatalf("CreateOrder missing items err = %v, want order required", err)
	}

	item, err := svc.CreateOrder(context.Background(), "user-1", map[string]any{"productId": "product-1", "quantity": 2})
	if err != nil {
		t.Fatalf("CreateOrder valid request failed: %v", err)
	}
	if item["userId"] != "user-1" || item["productId"] != "product-1" {
		t.Fatalf("order item = %#v, want canonical user and product", item)
	}
}
