package server

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPostgresStoreNormalizedAuthAndWallet(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}

	ctx := context.Background()
	store, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("openPostgresStore failed: %v", err)
	}
	defer store.close()

	suffix := randomToken()[:12]
	user := authUser{
		ID:        "test-customer-" + suffix,
		GitHubID:  9000000001,
		Login:     "test-login-" + suffix,
		Name:      "Test Customer",
		Provider:  "github",
		Roles:     []string{roleCustomer},
		AvatarURL: "https://example.com/avatar.png",
		HTMLURL:   "https://github.com/test-login-" + suffix,
	}

	if _, err := store.saveCustomerProfile(ctx, user, map[string]any{
		"name":       "Test Customer",
		"schoolName": "Daema High",
		"studentNo":  "3101",
		"grade":      "3",
		"classNo":    "1",
	}); err != nil {
		t.Fatalf("saveCustomerProfile failed: %v", err)
	}

	found, err := store.customerProfileExists(ctx, user.ID)
	if err != nil {
		t.Fatalf("customerProfileExists failed: %v", err)
	}
	if !found {
		t.Fatal("customer profile was not saved")
	}

	session := authSession{
		Token:             "test-token-" + suffix,
		GitHubAccessToken: "github-token-" + suffix,
		User:              user,
		Role:              roleCustomer,
		ExpiresAt:         time.Now().Add(time.Hour),
	}
	if err := store.saveSession(ctx, session); err != nil {
		t.Fatalf("saveSession failed: %v", err)
	}
	loaded, ok, err := store.session(ctx, session.Token)
	if err != nil {
		t.Fatalf("session failed: %v", err)
	}
	if !ok || loaded.User.ID != user.ID || loaded.GitHubAccessToken != session.GitHubAccessToken {
		t.Fatalf("loaded session = %#v, ok=%v", loaded, ok)
	}

	state := oauthState{Value: "state-" + suffix, Role: roleCustomer, RedirectAfter: "/after", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.saveOAuthState(ctx, state); err != nil {
		t.Fatalf("saveOAuthState failed: %v", err)
	}
	if loadedState, ok, err := store.oauthState(ctx, state.Value); err != nil || !ok || loadedState.RedirectAfter != "/after" {
		t.Fatalf("oauthState = %#v, ok=%v, err=%v", loadedState, ok, err)
	}
	if err := store.deleteOAuthState(ctx, state.Value); err != nil {
		t.Fatalf("deleteOAuthState failed: %v", err)
	}
	if _, ok, err := store.oauthState(ctx, state.Value); err != nil || ok {
		t.Fatalf("oauthState after delete ok=%v err=%v", ok, err)
	}

	ledgerID := "test-ledger-" + suffix
	created, err := store.createLedgerAndAdjustWallet(ctx, user, ledgerID, "test-credit", "income", "POINT", 100, map[string]any{"description": "test credit"})
	if err != nil {
		t.Fatalf("createLedgerAndAdjustWallet income failed: %v", err)
	}
	if !created {
		t.Fatal("first ledger insert should be created")
	}
	created, err = store.createLedgerAndAdjustWallet(ctx, user, ledgerID, "test-credit", "income", "POINT", 100, map[string]any{"description": "duplicate"})
	if err != nil {
		t.Fatalf("duplicate createLedgerAndAdjustWallet failed: %v", err)
	}
	if created {
		t.Fatal("duplicate ledger insert should not be created")
	}
	balance, err := store.walletBalance(ctx, user.ID, "POINT")
	if err != nil {
		t.Fatalf("walletBalance failed: %v", err)
	}
	if balance != 100 {
		t.Fatalf("balance = %d, want 100", balance)
	}

	if _, err := store.createLedgerAndAdjustWallet(ctx, user, "test-ledger-overdraft-"+suffix, "test-debit", "expense", "POINT", 101, nil); !errors.Is(err, errInsufficientWalletBalance) {
		t.Fatalf("overdraft error = %v, want errInsufficientWalletBalance", err)
	}

	transactions, err := store.ledgerTransactions(ctx, user.ID, 10)
	if err != nil {
		t.Fatalf("ledgerTransactions failed: %v", err)
	}
	if len(transactions) != 1 || transactions[0]["id"] != ledgerID {
		t.Fatalf("transactions = %#v", transactions)
	}
}
