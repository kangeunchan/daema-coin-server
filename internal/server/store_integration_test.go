package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
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
	studentNo := integrationStudentNo()
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
		"studentNo":  studentNo,
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

func TestPostgresStoreRejectsDuplicateStudentNumber(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	studentNo := integrationStudentNo()
	firstUser := authUser{ID: "test-student-first-" + suffix, Login: "first-" + suffix}
	secondUser := authUser{ID: "test-student-second-" + suffix, Login: "second-" + suffix}

	firstProfile, err := store.saveCustomerProfile(ctx, firstUser, map[string]any{
		"name":      "First Student",
		"studentNo": "  " + studentNo + "  ",
	})
	if err != nil {
		t.Fatalf("save first student profile: %v", err)
	}
	if firstProfile["studentNo"] != studentNo {
		t.Fatalf("normalized student number = %q, want %q", firstProfile["studentNo"], studentNo)
	}
	if _, err := store.saveCustomerProfile(ctx, firstUser, map[string]any{
		"name":      "First Student Updated",
		"studentNo": studentNo,
	}); err != nil {
		t.Fatalf("same customer could not update their profile: %v", err)
	}
	if _, err := store.saveCustomerProfile(ctx, secondUser, map[string]any{
		"name":      "Second Student",
		"studentNo": studentNo,
	}); !errors.Is(err, errDuplicateStudentNo) {
		t.Fatalf("duplicate student number error = %v, want errDuplicateStudentNo", err)
	}
	if found, err := store.customerProfileExists(ctx, secondUser.ID); err != nil || found {
		t.Fatalf("duplicate customer profile exists = %v, err=%v; want false", found, err)
	}
}

func TestGitHubOAuthReusesExistingCustomerIdentity(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	githubID := time.Now().UnixNano()
	existingUser := authUser{
		ID:        "test-legacy-github-" + suffix,
		GitHubID:  githubID,
		Login:     "legacy-login-" + suffix,
		Name:      "Legacy Student",
		Provider:  "github",
		Roles:     []string{roleCustomer},
		AvatarURL: "https://example.com/legacy.png",
		HTMLURL:   "https://github.com/legacy-login-" + suffix,
	}
	if _, err := store.saveCustomerProfile(ctx, existingUser, map[string]any{
		"name":      existingUser.Name,
		"studentNo": integrationStudentNo(),
	}); err != nil {
		t.Fatalf("save existing customer profile: %v", err)
	}

	oauthUser := authUser{
		ID:        fmt.Sprintf("github-%d", githubID),
		GitHubID:  githubID,
		Login:     "renamed-login-" + suffix,
		Name:      "GitHub Display Name",
		Provider:  "github",
		Roles:     []string{roleCustomer},
		AvatarURL: "https://example.com/current.png",
		HTMLURL:   "https://github.com/renamed-login-" + suffix,
	}
	server := &server{store: store}
	session := authSession{
		Token:     "test-github-session-" + suffix,
		User:      oauthUser,
		Role:      roleCustomer,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	mappedSession, err := server.sessionWithExistingGitHubCustomer(ctx, session)
	if err != nil {
		t.Fatalf("sessionWithExistingGitHubCustomer failed: %v", err)
	}
	if mappedSession.User.ID != existingUser.ID {
		t.Fatalf("mapped user id = %q, want %q", mappedSession.User.ID, existingUser.ID)
	}
	if mappedSession.User.Login != oauthUser.Login {
		t.Fatalf("mapped login = %q, want current OAuth login %q", mappedSession.User.Login, oauthUser.Login)
	}

	resolvedUser, found, err := store.authUserByGitHubIdentity(ctx, githubID, oauthUser.Login)
	if err != nil {
		t.Fatalf("authUserByGitHubIdentity failed: %v", err)
	}
	if !found || resolvedUser.ID != existingUser.ID || resolvedUser.Login != oauthUser.Login {
		t.Fatalf("resolved user = %#v, found=%v; want existing id and current login", resolvedUser, found)
	}
}

func TestPostgresStoreLedgerIdempotencyAndConcurrentDebit(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := createIntegrationCustomer(t, store, ctx, "test-ledger-money-"+suffix)

	if _, err := store.createLedgerAndAdjustWallet(ctx, user, "test-credit-"+suffix, "test-credit", "income", "POINT", 1000, map[string]any{"referenceType": "test", "referenceId": suffix}); err != nil {
		t.Fatalf("initial credit failed: %v", err)
	}
	created, err := store.createLedgerAndAdjustWallet(ctx, user, "test-credit-"+suffix, "test-credit", "income", "POINT", 1000, map[string]any{"referenceType": "test", "referenceId": suffix})
	if err != nil {
		t.Fatalf("idempotent duplicate failed: %v", err)
	}
	if created {
		t.Fatal("idempotent duplicate should not create a second ledger row")
	}
	if _, err := store.createLedgerAndAdjustWallet(ctx, user, "test-credit-"+suffix, "test-credit", "income", "POINT", 999, map[string]any{"referenceType": "test", "referenceId": suffix}); !errors.Is(err, errLedgerIdempotencyConflict) {
		t.Fatalf("conflicting duplicate error = %v, want errLedgerIdempotencyConflict", err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 1000 {
		t.Fatalf("balance after idempotency checks = %d, err=%v; want 1000", balance, err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	createdCount := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := store.createLedgerAndAdjustWallet(ctx, user, ledgerID("test-concurrent-debit", suffix, string(rune('a'+i))), "test-debit", "expense", "POINT", 700, nil)
			errs <- err
			createdCount <- created
		}()
	}
	wg.Wait()
	close(errs)
	close(createdCount)

	successes := 0
	for created := range createdCount {
		if created {
			successes++
		}
	}
	for err := range errs {
		if err != nil && !errors.Is(err, errInsufficientWalletBalance) {
			t.Fatalf("concurrent debit unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent debit successes = %d, want 1", successes)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 300 {
		t.Fatalf("balance after concurrent debit = %d, err=%v; want 300", balance, err)
	}
}

func TestLegacySignupBonusMigrationPreservesIdempotency(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := createIntegrationCustomer(t, store, ctx, "test-legacy-signup-"+suffix)
	id := ledgerID("signup-bonus", "POINT", user.ID)

	created, err := store.createLedgerAndAdjustWallet(ctx, user, id, "signup-bonus", "income", "POINT", initialSignupPoints, map[string]any{
		"referenceType": "signup-bonus",
		"description":   "회원가입 대마포인트 지급",
	})
	if err != nil || !created {
		t.Fatalf("create legacy signup bonus: created=%v err=%v", created, err)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE ledger_transactions
SET idempotency_key = $2
WHERE id = $1`, id, "legacy-resources:ledger_transactions:"+id); err != nil {
		t.Fatalf("mark signup bonus as migrated legacy ledger: %v", err)
	}

	migration, err := migrationFiles.ReadFile("migrations/0006_normalize_legacy_signup_bonus_ledgers.sql")
	if err != nil {
		t.Fatalf("read signup bonus compatibility migration: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("apply signup bonus compatibility migration: %v", err)
	}

	created, err = store.createLedgerAndAdjustWallet(ctx, user, id, "signup-bonus", "income", "POINT", initialSignupPoints, map[string]any{
		"description": "회원가입 대마포인트 지급",
	})
	if err != nil {
		t.Fatalf("replay migrated signup bonus: %v", err)
	}
	if created {
		t.Fatal("replayed signup bonus created a duplicate ledger")
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != initialSignupPoints {
		t.Fatalf("signup balance after replay = %d, err=%v; want %d", balance, err, initialSignupPoints)
	}
}

func TestGitHubPushRewardsUseServerReceivedDate(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := authUser{
		ID:       "test-github-reward-" + suffix,
		GitHubID: time.Now().UnixNano(),
		Login:    "test-github-login-" + suffix,
		Name:     "Test GitHub Reward",
		Provider: "github",
		Roles:    []string{roleCustomer},
	}
	if _, err := store.saveCustomerProfile(ctx, user, map[string]any{"name": user.Name, "studentNo": integrationStudentNo()}); err != nil {
		t.Fatalf("save GitHub reward customer: %v", err)
	}

	commits := make([]map[string]any, 0, commitRewardLimit+1)
	for i := 0; i < commitRewardLimit+1; i++ {
		commits = append(commits, map[string]any{
			"id":        fmt.Sprintf("test-sha-%s-%02d", suffix, i),
			"message":   "test commit",
			"timestamp": time.Date(2000, time.January, i+1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
			"url":       "https://github.com/test/repository/commit/test",
			"distinct":  true,
			"author":    map[string]any{"username": user.Login},
		})
	}
	payload, err := json.Marshal(map[string]any{
		"repository": map[string]any{"id": 1, "full_name": "test/repository-" + suffix},
		"sender":     map[string]any{"login": user.Login},
		"commits":    commits,
	})
	if err != nil {
		t.Fatalf("marshal GitHub push payload: %v", err)
	}

	receivedAt := time.Date(2026, time.June, 29, 4, 0, 0, 0, time.UTC)
	srv := &server{store: store}
	stored, err := srv.storeGitHubPushEventAt(ctx, "test-delivery-"+suffix, payload, receivedAt)
	if err != nil {
		t.Fatalf("store GitHub push event: %v", err)
	}
	if stored != commitRewardLimit+1 {
		t.Fatalf("stored commits = %d, want %d", stored, commitRewardLimit+1)
	}
	if balance, err := store.walletBalance(ctx, user.ID, commitRewardCurrency); err != nil || balance != commitRewardLimit*commitRewardPoints {
		t.Fatalf("GitHub reward balance = %d, err=%v; want %d", balance, err, commitRewardLimit*commitRewardPoints)
	}

	items, err := store.listFiltered(ctx, resourceGitHubCommits, []resourceFilter{{Field: "authorLogin", Value: user.Login}}, commitRewardLimit+1)
	if err != nil {
		t.Fatalf("list stored GitHub commits: %v", err)
	}
	for _, storedCommit := range items {
		commit := githubCommitItem{}
		if err := decodeMap(storedCommit, &commit); err != nil {
			t.Fatalf("decode stored GitHub commit: %v", err)
		}
		if !commit.OccurredAt.Equal(receivedAt) {
			t.Fatalf("stored occurredAt = %s, want server receivedAt %s", commit.OccurredAt, receivedAt)
		}
		if commit.CommitTimestamp == nil || !commit.CommitTimestamp.Before(receivedAt) {
			t.Fatalf("commit timestamp was not preserved separately: %#v", commit.CommitTimestamp)
		}
	}
}

func TestGitHubCommitStreakRewardsAtSevenAndFourteenDays(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := authUser{
		ID:       "test-github-streak-" + suffix,
		GitHubID: time.Now().UnixNano(),
		Login:    "test-github-streak-login-" + suffix,
		Name:     "Test GitHub Streak Reward",
		Provider: "github",
		Roles:    []string{roleCustomer},
	}
	if _, err := store.saveCustomerProfile(ctx, user, map[string]any{"name": user.Name, "studentNo": integrationStudentNo()}); err != nil {
		t.Fatalf("save GitHub streak customer: %v", err)
	}

	srv := &server{store: store}
	start := time.Date(2026, time.June, 18, 12, 0, 0, 0, appLocation())
	var lastPayload []byte
	for day := 0; day < 14; day++ {
		commits := make([]map[string]any, 0, commitDailyGoal)
		for index := 0; index < commitDailyGoal; index++ {
			commits = append(commits, map[string]any{
				"id":        fmt.Sprintf("streak-sha-%s-%02d-%02d", suffix, day, index),
				"message":   "streak test commit",
				"timestamp": start.AddDate(0, 0, day).Add(time.Duration(index) * time.Minute).Format(time.RFC3339),
				"url":       "https://github.com/test/streak/commit/test",
				"distinct":  true,
				"author":    map[string]any{"username": user.Login},
			})
		}
		payload, err := json.Marshal(map[string]any{
			"repository": map[string]any{"id": day + 1, "full_name": fmt.Sprintf("test/streak-%s-%02d", suffix, day)},
			"sender":     map[string]any{"login": user.Login},
			"commits":    commits,
		})
		if err != nil {
			t.Fatalf("marshal streak payload day %d: %v", day, err)
		}
		lastPayload = payload
		stored, err := srv.storeGitHubPushEventAt(ctx, fmt.Sprintf("streak-delivery-%s-%02d", suffix, day), payload, start.AddDate(0, 0, day).UTC())
		if err != nil {
			t.Fatalf("store streak payload day %d: %v", day, err)
		}
		if stored != commitDailyGoal {
			t.Fatalf("stored commits on day %d = %d, want %d", day, stored, commitDailyGoal)
		}
	}

	wantBalance := 14*commitDailyGoal*commitRewardPoints + 5_000 + 15_000
	if balance, err := store.walletBalance(ctx, user.ID, commitStreakCurrency); err != nil || balance != wantBalance {
		t.Fatalf("streak reward balance = %d, err=%v; want %d", balance, err, wantBalance)
	}
	total, err := store.ledgerIncomeTotalByType(ctx, user.ID, commitStreakType)
	if err != nil || total != 20_000 {
		t.Fatalf("streak reward total = %d, err=%v; want 20000", total, err)
	}
	summary, err := srv.commitRewardSummary(ctx, user, user.Login, start.AddDate(0, 0, 13))
	if err != nil {
		t.Fatalf("commit reward summary: %v", err)
	}
	if summary.CurrentStreakDays != 14 || summary.LongestStreakDays != 14 || summary.TotalRewardAmount != 20_000 {
		t.Fatalf("commit reward summary = %#v", summary)
	}
	for _, milestone := range summary.Milestones {
		if milestone.Status != "paid" || milestone.PaidAt == "" {
			t.Fatalf("milestone was not paid: %#v", milestone)
		}
	}

	stored, err := srv.storeGitHubPushEventAt(ctx, "streak-delivery-replay-"+suffix, lastPayload, start.AddDate(0, 0, 13).UTC())
	if err != nil || stored != 0 {
		t.Fatalf("replay stored=%d err=%v, want 0 and nil", stored, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, commitStreakCurrency); err != nil || balance != wantBalance {
		t.Fatalf("balance after replay = %d, err=%v; want %d", balance, err, wantBalance)
	}
}

func TestLegacyGitHubCommitMigrationUsesDatabaseCreatedAt(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	id := "test-legacy-github-commit-" + suffix
	commitTimestamp := time.Date(2000, time.January, 1, 12, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, time.June, 29, 4, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{
		"id":             id,
		"sha":            "test-sha-" + suffix,
		"authorLogin":    "test-login-" + suffix,
		"occurredAt":     commitTimestamp.Format(time.RFC3339),
		"rewardedPoints": commitRewardPoints,
	})
	if err != nil {
		t.Fatalf("marshal legacy GitHub commit: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO github_commit_events(id, payload, created_at, updated_at)
VALUES($1, $2, $3, $3)`, id, string(payload), createdAt); err != nil {
		t.Fatalf("insert legacy GitHub commit: %v", err)
	}

	migration, err := migrationFiles.ReadFile("migrations/0007_use_server_time_for_github_commits.sql")
	if err != nil {
		t.Fatalf("read GitHub server time migration: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("apply GitHub server time migration: %v", err)
	}

	item, found, err := store.get(ctx, resourceGitHubCommits, id)
	if err != nil || !found {
		t.Fatalf("load migrated GitHub commit: found=%v err=%v", found, err)
	}
	commit := githubCommitItem{}
	if err := decodeMap(item, &commit); err != nil {
		t.Fatalf("decode migrated GitHub commit: %v", err)
	}
	if !commit.OccurredAt.Equal(createdAt) {
		t.Fatalf("migrated occurredAt = %s, want database createdAt %s", commit.OccurredAt, createdAt)
	}
	if commit.CommitTimestamp == nil || !commit.CommitTimestamp.Equal(commitTimestamp) {
		t.Fatalf("migrated commitTimestamp = %v, want %s", commit.CommitTimestamp, commitTimestamp)
	}
}

func TestPostgresStorePredictionStakeCancelAndInsufficientBalanceAreAtomic(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := createIntegrationCustomer(t, store, ctx, "test-prediction-money-"+suffix)

	if _, err := store.createLedgerAndAdjustWallet(ctx, user, "test-prediction-credit-"+suffix, "test-credit", "income", "POINT", 500, nil); err != nil {
		t.Fatalf("initial credit failed: %v", err)
	}
	matchID := "test-match-" + suffix
	predictionKey := predictionID(matchID, user.ID)
	stakeLedgerID := ledgerID("prediction-stake", matchID, user.ID, suffix)
	prediction := map[string]any{
		"matchId":       matchID,
		"userId":        user.ID,
		"githubLogin":   user.Login,
		"pick":          "home",
		"stakeAmount":   200,
		"currency":      predictionCurrency,
		"stakeLedgerId": stakeLedgerID,
	}
	item, created, err := store.createWorldcupPredictionWithStake(ctx, user, predictionKey, predictionStakeRequest{Prediction: prediction, StakeLedgerID: stakeLedgerID, StakeAmount: 200, LedgerExtras: map[string]any{"matchId": matchID, "stakeLedgerId": stakeLedgerID}})
	if err != nil || !created {
		t.Fatalf("createWorldcupPredictionWithStake = created %v, err %v, item %#v", created, err, item)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 300 {
		t.Fatalf("balance after prediction stake = %d, err=%v; want 300", balance, err)
	}
	if _, created, err := store.createWorldcupPredictionWithStake(ctx, user, predictionKey, predictionStakeRequest{Prediction: prediction, StakeLedgerID: stakeLedgerID, StakeAmount: 200, LedgerExtras: map[string]any{"matchId": matchID, "stakeLedgerId": stakeLedgerID}}); err != nil || created {
		t.Fatalf("duplicate prediction = created %v, err %v; want existing without error", created, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 300 {
		t.Fatalf("balance after duplicate prediction = %d, err=%v; want 300", balance, err)
	}

	refundLedgerID := predictionCancelLedgerID(matchID, user.ID, prediction)
	if _, cancelled, err := store.cancelWorldcupPredictionWithRefund(ctx, user, predictionKey, predictionCancelRequest{RefundLedgerID: refundLedgerID, StakeAmount: 200, LedgerExtras: map[string]any{"matchId": matchID, "stakeLedgerId": stakeLedgerID}}); err != nil || !cancelled {
		t.Fatalf("cancelWorldcupPredictionWithRefund = cancelled %v, err %v", cancelled, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 500 {
		t.Fatalf("balance after prediction cancel = %d, err=%v; want 500", balance, err)
	}
	if _, cancelled, err := store.cancelWorldcupPredictionWithRefund(ctx, user, predictionKey, predictionCancelRequest{RefundLedgerID: refundLedgerID, StakeAmount: 200, LedgerExtras: map[string]any{"matchId": matchID}}); err != nil || cancelled {
		t.Fatalf("duplicate cancel = cancelled %v, err %v; want not found without refund", cancelled, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 500 {
		t.Fatalf("balance after duplicate cancel = %d, err=%v; want 500", balance, err)
	}

	tooLargeID := predictionID(matchID+"-too-large", user.ID)
	_, created, err = store.createWorldcupPredictionWithStake(ctx, user, tooLargeID, predictionStakeRequest{Prediction: map[string]any{
		"matchId":       matchID + "-too-large",
		"userId":        user.ID,
		"pick":          "away",
		"stakeAmount":   600,
		"currency":      predictionCurrency,
		"stakeLedgerId": ledgerID("prediction-stake", matchID, user.ID, "too-large"),
	}, StakeLedgerID: ledgerID("prediction-stake", matchID, user.ID, "too-large"), StakeAmount: 600, LedgerExtras: map[string]any{"matchId": matchID + "-too-large"}})
	if !errors.Is(err, errInsufficientWalletBalance) || created {
		t.Fatalf("insufficient prediction = created %v, err %v; want insufficient balance", created, err)
	}
	if _, found, err := store.get(ctx, resourceWorldcupPredictions, tooLargeID); err != nil || found {
		t.Fatalf("insufficient prediction stored = %v, err=%v; want absent", found, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "POINT"); err != nil || balance != 500 {
		t.Fatalf("balance after insufficient prediction = %d, err=%v; want 500", balance, err)
	}
}

func TestPostgresStorePredictionSettlementIsIdempotent(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	users := []authUser{
		createIntegrationCustomer(t, store, ctx, "test-settlement-u1-"+suffix),
		createIntegrationCustomer(t, store, ctx, "test-settlement-u2-"+suffix),
		createIntegrationCustomer(t, store, ctx, "test-settlement-u3-"+suffix),
	}
	matchID := "test-settlement-match-" + suffix
	predictions := []map[string]any{
		{"matchId": matchID, "userId": users[0].ID, "githubLogin": users[0].Login, "pick": "home", "stakeAmount": 100},
		{"matchId": matchID, "userId": users[1].ID, "githubLogin": users[1].Login, "pick": "home", "stakeAmount": 50},
		{"matchId": matchID, "userId": users[2].ID, "githubLogin": users[2].Login, "pick": "away", "stakeAmount": 50},
	}
	for i, prediction := range predictions {
		if _, created, err := store.create(ctx, resourceWorldcupPredictions, predictionID(matchID, stringValue(prediction["userId"])), prediction); err != nil || !created {
			t.Fatalf("seed prediction %d = created %v, err %v", i, created, err)
		}
	}
	srv := &server{store: store}
	result, err := srv.settleWorldcupPrediction(ctx, matchID, "home", "admin", "test settlement")
	if err != nil || !result.Created {
		t.Fatalf("settleWorldcupPrediction = created %v, err %v", result.Created, err)
	}
	wantBalances := map[string]int{}
	for _, entry := range result.LedgerEntries {
		wantBalances[stringValue(entry["userId"])] += amountValue(entry)
	}
	for userID, want := range wantBalances {
		if balance, err := store.walletBalance(ctx, userID, "POINT"); err != nil || balance != want {
			t.Fatalf("settlement balance[%s] = %d, err=%v; want %d", userID, balance, err, want)
		}
	}
	if _, err := srv.settleWorldcupPrediction(ctx, matchID, "home", "admin", "retry"); !errors.Is(err, errPredictionAlreadySettled) {
		t.Fatalf("duplicate settlement error = %v, want errPredictionAlreadySettled", err)
	}
	for userID, want := range wantBalances {
		if balance, err := store.walletBalance(ctx, userID, "POINT"); err != nil || balance != want {
			t.Fatalf("balance after duplicate settlement[%s] = %d, err=%v; want %d", userID, balance, err, want)
		}
	}
}

func TestPostgresStorePaymentCaptureAndRefundAreIdempotent(t *testing.T) {
	store, ctx := openIntegrationStore(t)
	suffix := randomToken()[:12]
	user := createIntegrationCustomer(t, store, ctx, "test-payment-money-"+suffix)
	seller := authUser{ID: "seller-" + suffix, BoothID: "booth-" + suffix}

	if _, err := store.createLedgerAndAdjustWallet(ctx, user, "test-payment-credit-"+suffix, "test-credit", "income", "DMC", 1000, nil); err != nil {
		t.Fatalf("initial DMC credit failed: %v", err)
	}
	intentID := "test-payment-intent-" + suffix
	if _, created, err := store.create(ctx, resourcePaymentIntents, intentID, map[string]any{
		"boothId":    seller.BoothID,
		"customerId": user.ID,
		"userId":     user.ID,
		"status":     "requires_capture",
		"currency":   "DMC",
		"amount":     amount("DMC", 600),
	}); err != nil || !created {
		t.Fatalf("seed payment intent = created %v, err %v", created, err)
	}

	paymentID := "test-payment-" + suffix
	if _, created, err := store.capturePaymentIntent(ctx, seller, intentID, paymentCaptureRequest{PaymentID: paymentID}); err != nil || !created {
		t.Fatalf("capturePaymentIntent = created %v, err %v", created, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "DMC"); err != nil || balance != 400 {
		t.Fatalf("balance after capture = %d, err=%v; want 400", balance, err)
	}
	if _, created, err := store.capturePaymentIntent(ctx, seller, intentID, paymentCaptureRequest{PaymentID: paymentID}); err != nil || created {
		t.Fatalf("duplicate capture = created %v, err %v; want idempotent existing", created, err)
	}
	if payment, created, err := store.capturePaymentIntent(ctx, seller, intentID, paymentCaptureRequest{PaymentID: ""}); err != nil || created || stringValue(payment["id"]) != paymentID {
		t.Fatalf("duplicate capture without payment id = payment %#v, created %v, err %v; want existing custom payment", payment, created, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "DMC"); err != nil || balance != 400 {
		t.Fatalf("balance after duplicate capture = %d, err=%v; want 400", balance, err)
	}

	refundID := "test-payment-refund-" + suffix
	if _, created, err := store.refundPayment(ctx, seller, paymentID, paymentRefundRequest{RefundLedgerID: refundID, RequestedAmount: 0}); err != nil || !created {
		t.Fatalf("refundPayment = created %v, err %v", created, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "DMC"); err != nil || balance != 1000 {
		t.Fatalf("balance after refund = %d, err=%v; want 1000", balance, err)
	}
	if _, created, err := store.refundPayment(ctx, seller, paymentID, paymentRefundRequest{RefundLedgerID: refundID, RequestedAmount: 0}); err != nil || created {
		t.Fatalf("duplicate refund = created %v, err %v; want idempotent existing", created, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "DMC"); err != nil || balance != 1000 {
		t.Fatalf("balance after duplicate refund = %d, err=%v; want 1000", balance, err)
	}
	if _, _, err := store.refundPayment(ctx, seller, paymentID, paymentRefundRequest{RefundLedgerID: refundID + "-again", RequestedAmount: 1}); !errors.Is(err, errPaymentClosed) {
		t.Fatalf("over refund after full refund error = %v, want errPaymentClosed", err)
	}

	tooLargeIntentID := "test-payment-intent-too-large-" + suffix
	if _, created, err := store.create(ctx, resourcePaymentIntents, tooLargeIntentID, map[string]any{
		"boothId":    seller.BoothID,
		"customerId": user.ID,
		"userId":     user.ID,
		"status":     "requires_capture",
		"currency":   "DMC",
		"amount":     amount("DMC", 2000),
	}); err != nil || !created {
		t.Fatalf("seed too large payment intent = created %v, err %v", created, err)
	}
	tooLargePaymentID := "test-payment-too-large-" + suffix
	if _, created, err := store.capturePaymentIntent(ctx, seller, tooLargeIntentID, paymentCaptureRequest{PaymentID: tooLargePaymentID}); !errors.Is(err, errInsufficientWalletBalance) || created {
		t.Fatalf("too large capture = created %v, err %v; want insufficient balance", created, err)
	}
	if _, found, err := store.get(ctx, resourcePayments, tooLargePaymentID); err != nil || found {
		t.Fatalf("too large payment stored = %v, err=%v; want absent", found, err)
	}
	if balance, err := store.walletBalance(ctx, user.ID, "DMC"); err != nil || balance != 1000 {
		t.Fatalf("balance after too large capture = %d, err=%v; want 1000", balance, err)
	}
}

func openIntegrationStore(t *testing.T) (*postgresStore, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := openPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("openPostgresStore failed: %v", err)
	}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Fatalf("close store failed: %v", err)
		}
	})
	return store, ctx
}

func createIntegrationCustomer(t *testing.T, store *postgresStore, ctx context.Context, id string) authUser {
	t.Helper()
	user := authUser{
		ID:       id,
		Login:    id,
		Name:     "Test Customer " + id,
		Provider: "github",
		Roles:    []string{roleCustomer},
	}
	if _, err := store.saveCustomerProfile(ctx, user, map[string]any{"name": user.Name, "studentNo": integrationStudentNo()}); err != nil {
		t.Fatalf("saveCustomerProfile(%s) failed: %v", id, err)
	}
	return user
}

func integrationStudentNo() string {
	return fmt.Sprintf("%012d", time.Now().UnixNano()%1_000_000_000_000)
}
