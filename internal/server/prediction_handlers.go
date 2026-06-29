package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *server) handlePredictionSummary(w http.ResponseWriter, r *http.Request) {
	matchID := r.PathValue("matchId")
	match, matchStatusKnown, err := s.worldcupMatchByID(r.Context(), matchID)
	if err != nil {
		s.failFootball(w, r, err)
		return
	}
	items, _, ok := s.listResources(w, r, resourceWorldcupPredictions, 1000, resourceFilter{Field: "matchId", Value: matchID})
	if !ok {
		return
	}
	counts := map[string]int{"home": 0, "draw": 0, "away": 0}
	myPrediction := ""
	myStakeAmount := 0
	totalStakeAmount := 0
	userID := s.currentUserID(r)
	for _, item := range items {
		pick := stringValue(item["pick"])
		if _, ok := counts[pick]; ok {
			counts[pick]++
			totalStakeAmount += predictionStakeAmount(item)
		}
		if userID != "" && stringValue(item["userId"]) == userID {
			myPrediction = pick
			myStakeAmount = predictionStakeAmount(item)
		}
	}
	total := counts["home"] + counts["draw"] + counts["away"]
	percent := func(count int) int {
		if total == 0 {
			return 0
		}
		return int(math.Round(float64(count) / float64(total) * 100))
	}
	data := map[string]any{
		"matchId":          matchID,
		"homePercent":      percent(counts["home"]),
		"drawPercent":      percent(counts["draw"]),
		"awayPercent":      percent(counts["away"]),
		"totalCount":       total,
		"totalStakeAmount": totalStakeAmount,
		"canPredict":       myPrediction == "" && (!matchStatusKnown || match.Status == "scheduled"),
		"canCancel":        myPrediction != "" && (!matchStatusKnown || match.Status == "scheduled"),
		"myPrediction":     nil,
		"myStakeAmount":    nil,
	}
	if matchStatusKnown {
		data["matchStatus"] = match.Status
		data["matchStatusLabel"] = match.StatusLabel
	}
	if myPrediction != "" {
		data["myPrediction"] = myPrediction
		data["myStakeAmount"] = myStakeAmount
	}
	s.ok(w, r, data)
}

func (s *server) handlePredictionCreate(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	matchID := r.PathValue("matchId")
	body, ok := s.requestPayload(w, r)
	if !ok {
		return
	}
	result, err := s.predictions().Create(r.Context(), session.User, matchID, body)
	if err != nil {
		if !validPredictionPick(stringValue(body["pick"])) {
			s.fail(w, r, http.StatusBadRequest, "INVALID_PREDICTION_PICK", "pick은 home, draw, away 중 하나여야 합니다.", map[string]any{"pick": stringValue(body["pick"])})
			return
		}
		switch {
		case errors.Is(err, errPredictionInvalidInput):
			s.fail(w, r, http.StatusBadRequest, "INVALID_PREDICTION_STAKE", "stakeAmount는 필수이며 1 이상 정수여야 합니다.", map[string]any{"stakeAmount": body["stakeAmount"]})
			return
		case errors.Is(err, errPredictionClosed):
			s.fail(w, r, http.StatusConflict, "PREDICTION_CLOSED", "이미 시작했거나 종료된 경기는 투표할 수 없습니다.", map[string]any{"matchId": matchID})
			return
		case errors.Is(err, errPredictionInsufficientFund), errors.Is(err, errInsufficientWalletBalance):
			s.fail(w, r, http.StatusBadRequest, "INSUFFICIENT_POINT_BALANCE", "대마포인트 잔액이 부족합니다.", map[string]any{"stakeAmount": result.StakeAmount})
			return
		case errors.Is(err, errLedgerIdempotencyConflict):
			s.fail(w, r, http.StatusConflict, "PREDICTION_STAKE_IDEMPOTENCY_CONFLICT", "승부예측 차감 idempotency 정보가 기존 원장과 일치하지 않습니다.", map[string]any{"matchId": matchID})
			return
		default:
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측 포인트 차감에 실패했습니다.", map[string]any{"cause": err.Error()})
			return
		}
	}
	if !result.Created {
		s.fail(w, r, http.StatusConflict, "PREDICTION_ALREADY_EXISTS", "이미 이 경기에 투표했습니다.", map[string]any{"matchId": matchID, "prediction": result.Item})
		return
	}
	s.created(w, r, result.Item)
}

func (s *server) handlePredictionCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "GitHub 로그인이 필요합니다.", nil)
		return
	}
	matchID := r.PathValue("matchId")
	result, err := s.predictions().Cancel(r.Context(), session.User, matchID)
	if err != nil {
		switch {
		case errors.Is(err, errPredictionCancelClosed):
			s.fail(w, r, http.StatusConflict, "PREDICTION_CANCEL_CLOSED", "이미 시작했거나 종료된 경기는 투표를 취소할 수 없습니다.", map[string]any{"matchId": matchID})
			return
		case errors.Is(err, errPredictionNotFound):
			s.fail(w, r, http.StatusNotFound, "PREDICTION_NOT_FOUND", "취소할 승부예측이 없습니다.", map[string]any{"matchId": matchID})
			return
		case errors.Is(err, errPredictionCancelInvalid):
			s.fail(w, r, http.StatusConflict, "PREDICTION_CANCEL_FAILED", "환급할 투표 금액을 확인할 수 없습니다.", map[string]any{"matchId": matchID})
			return
		case errors.Is(err, errLedgerIdempotencyConflict):
			s.fail(w, r, http.StatusConflict, "PREDICTION_CANCEL_IDEMPOTENCY_CONFLICT", "승부예측 환급 idempotency 정보가 기존 원장과 일치하지 않습니다.", map[string]any{"matchId": matchID})
			return
		default:
			s.fail(w, r, http.StatusInternalServerError, "DATABASE_WRITE_FAILED", "승부예측 취소 환급에 실패했습니다.", map[string]any{"cause": err.Error()})
			return
		}
	}
	if !result.Cancelled {
		s.fail(w, r, http.StatusNotFound, "PREDICTION_NOT_FOUND", "취소할 승부예측이 없습니다.", map[string]any{"matchId": matchID})
		return
	}
	s.ok(w, r, map[string]any{
		"matchId":      matchID,
		"cancelled":    true,
		"refundAmount": result.RefundAmount,
		"currency":     predictionCurrency,
	})
}

func predictionID(matchID, userID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "prediction-" + replacer.Replace(matchID) + "-" + replacer.Replace(userID)
}

func predictionCancelLedgerID(matchID, userID string, prediction map[string]any) string {
	if stakeLedgerID := stringValue(prediction["stakeLedgerId"]); stakeLedgerID != "" {
		return ledgerID("prediction-cancel", stakeLedgerID)
	}
	return ledgerID("prediction-cancel", matchID, userID, firstNonEmpty(stringValue(prediction["createdAt"]), stringValue(prediction["id"])))
}

func predictionSettlementID(matchID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return "prediction-settlement-" + replacer.Replace(matchID)
}

var (
	errPredictionAlreadySettled    = errors.New("prediction already settled")
	errPredictionNoPredictions     = errors.New("no predictions to settle")
	errPredictionResultUnavailable = errors.New("prediction result is unavailable")
)

func predictionStakeAmount(item map[string]any) int {
	if n, ok := numericValue(item["stakeAmount"]); ok {
		return int(math.Round(n))
	}
	if n, ok := numericValue(item["stake"]); ok {
		return int(math.Round(n))
	}
	return 100
}

func requiredPredictionStakeAmount(item map[string]any) (int, bool) {
	n, ok := numericValue(item["stakeAmount"])
	if !ok || math.Trunc(n) != n {
		return 0, false
	}
	amount := int(n)
	return amount, amount > 0
}

func (s *server) predictionWinningPick(ctx context.Context, matchID string, body map[string]any) (string, error) {
	for _, key := range []string{"winningPick", "result", "pick"} {
		pick := stringValue(body[key])
		if pick == "home" || pick == "draw" || pick == "away" {
			return pick, nil
		}
	}
	match, ok, err := s.worldcupMatchByID(ctx, matchID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("match not found")
	}
	return predictionWinningPickFromMatch(match)
}

func predictionWinningPickFromMatch(match worldcupMatch) (string, error) {
	if match.Home.Score == nil || match.Away.Score == nil {
		return "", errPredictionResultUnavailable
	}
	if *match.Home.Score > *match.Away.Score {
		return "home", nil
	}
	if *match.Home.Score < *match.Away.Score {
		return "away", nil
	}
	return "draw", nil
}

func settlePredictions(matchID, winningPick string, predictions []map[string]any) (map[string]any, []map[string]any, error) {
	if !validPredictionPick(winningPick) {
		return nil, nil, fmt.Errorf("invalid winning pick %q", winningPick)
	}
	participants := []predictionRecord{}
	totalPool := 0
	winnerStakeTotal := 0
	loserRefundTotal := 0
	for _, prediction := range predictions {
		item, err := predictionRecordFromMap(prediction)
		if err != nil {
			continue
		}
		participants = append(participants, item)
		totalPool += item.StakeAmount
		if item.Pick == winningPick {
			winnerStakeTotal += item.StakeAmount
		} else {
			loserRefundTotal += loserRefundAmount(item.StakeAmount)
		}
	}
	if len(participants) == 0 {
		return nil, nil, errPredictionNoPredictions
	}
	winnerPayoutPool := totalPool - loserRefundTotal
	winnerPayouts := map[string]int{}
	allocatedWinnerPayout := 0
	winners := []predictionRecord{}
	for _, participant := range participants {
		if participant.Pick == winningPick {
			winners = append(winners, participant)
		}
	}
	if winnerStakeTotal > 0 {
		sort.SliceStable(winners, func(i, j int) bool {
			if winners[i].StakeAmount == winners[j].StakeAmount {
				return winners[i].UserID < winners[j].UserID
			}
			return winners[i].StakeAmount > winners[j].StakeAmount
		})
		for _, winner := range winners {
			payout := winnerPayoutPool * winner.StakeAmount / winnerStakeTotal
			winnerPayouts[winner.UserID] = payout
			allocatedWinnerPayout += payout
		}
		for i := 0; allocatedWinnerPayout < winnerPayoutPool && len(winners) > 0; i++ {
			winner := winners[i%len(winners)]
			winnerPayouts[winner.UserID]++
			allocatedWinnerPayout++
		}
	}
	ledgerEntries := []map[string]any{}
	resultRows := []map[string]any{}
	for _, participant := range participants {
		payout := 0
		outcome := "lost"
		if participant.Pick == winningPick {
			outcome = "won"
			payout = winnerPayouts[participant.UserID]
		} else {
			payout = loserRefundAmount(participant.StakeAmount)
		}
		row := map[string]any{
			"userId":      participant.UserID,
			"githubLogin": participant.GitHubLogin,
			"pick":        participant.Pick,
			"stakeAmount": participant.StakeAmount,
			"outcome":     outcome,
			"payout":      payout,
		}
		resultRows = append(resultRows, row)
		if payout > 0 && participant.UserID != "" {
			ledgerID := predictionSettlementID(matchID) + "-" + participant.UserID
			ledgerEntries = append(ledgerEntries, map[string]any{
				"id":          ledgerID,
				"userId":      participant.UserID,
				"githubLogin": participant.GitHubLogin,
				"type":        "worldcup-prediction-settlement",
				"matchId":     matchID,
				"pick":        participant.Pick,
				"winningPick": winningPick,
				"outcome":     outcome,
				"stakeAmount": participant.StakeAmount,
				"direction":   "income",
				"amount":      amount(predictionCurrency, payout),
				"occurredAt":  time.Now().UTC().Format(time.RFC3339),
				"description": "월드컵 승부예측 정산",
			})
		}
	}
	settlement := map[string]any{
		"matchId":             matchID,
		"winningPick":         winningPick,
		"totalPool":           totalPool,
		"winnerStakeTotal":    winnerStakeTotal,
		"loserRefundTotal":    loserRefundTotal,
		"winnerPayoutPool":    winnerPayoutPool,
		"participantCount":    len(participants),
		"winnerCount":         len(winners),
		"loserRefundRate":     0.1,
		"results":             resultRows,
		"ledgerEntryCount":    len(ledgerEntries),
		"allocatedPointTotal": allocatedWinnerPayout + loserRefundTotal,
	}
	return settlement, ledgerEntries, nil
}

type predictionSettlementResult struct {
	Settlement    map[string]any
	LedgerEntries []map[string]any
	Created       bool
}

func (s *server) settleWorldcupPrediction(ctx context.Context, matchID, winningPick, source, note string) (predictionSettlementResult, error) {
	return s.predictions().Settle(ctx, matchID, winningPick, source, note)
}

func (s *server) runPredictionSettlementWorker(ctx context.Context, interval time.Duration) {
	slog.Info("starting worldcup prediction settlement worker", "interval", interval.String())
	s.runPredictionSettlementCycle(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPredictionSettlementCycle(ctx)
		}
	}
}

func (s *server) runPredictionSettlementCycle(ctx context.Context) {
	startedAt := time.Now().UTC()
	summary := map[string]any{
		"id":        "worldcup-prediction-settlement",
		"name":      "World Cup prediction settlement",
		"type":      "worker",
		"status":    "running",
		"startedAt": startedAt.Format(time.RFC3339),
	}

	matches, err := s.worldcupMatches(ctx)
	if err != nil {
		summary["status"] = "failed"
		summary["error"] = err.Error()
		summary["finishedAt"] = time.Now().UTC().Format(time.RFC3339)
		_, _ = s.store.put(ctx, resourceSystemJobs, "worldcup-prediction-settlement", summary)
		slog.Warn("worldcup prediction settlement worker failed to load matches", "error", err)
		return
	}

	checked := 0
	eligible := 0
	settled := 0
	alreadySettled := 0
	skipped := 0
	failed := 0

	for _, match := range matches {
		checked++
		if match.Status != "finished" {
			continue
		}
		eligible++
		winningPick, err := predictionWinningPickFromMatch(match)
		if err != nil {
			skipped++
			slog.Warn("skip worldcup prediction settlement without result", "match_id", match.ID, "error", err)
			continue
		}
		result, err := s.settleWorldcupPrediction(ctx, match.ID, winningPick, "worker", "경기 종료 자동 정산")
		if errors.Is(err, errPredictionAlreadySettled) {
			alreadySettled++
			continue
		}
		if errors.Is(err, errPredictionNoPredictions) {
			skipped++
			continue
		}
		if err != nil {
			failed++
			slog.Error("worldcup prediction settlement failed", "match_id", match.ID, "error", err)
			continue
		}
		if result.Created {
			settled++
			slog.Info("worldcup prediction settled", "match_id", match.ID, "winning_pick", winningPick)
		}
	}

	status := "ok"
	if failed > 0 {
		status = "partial_failure"
	}
	summary["status"] = status
	summary["checkedCount"] = checked
	summary["eligibleCount"] = eligible
	summary["settledCount"] = settled
	summary["alreadySettledCount"] = alreadySettled
	summary["skippedCount"] = skipped
	summary["failedCount"] = failed
	summary["finishedAt"] = time.Now().UTC().Format(time.RFC3339)
	summary["durationMs"] = time.Since(startedAt).Milliseconds()
	if _, err := s.store.put(ctx, resourceSystemJobs, "worldcup-prediction-settlement", summary); err != nil {
		slog.Warn("store worldcup prediction settlement job status", "error", err)
	}
}

func loserRefundAmount(stake int) int {
	if stake <= 0 {
		return 0
	}
	return int(math.Round(float64(stake) * 0.1))
}
