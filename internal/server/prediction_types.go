package server

import (
	"errors"
	"fmt"
)

type predictionCreateRequest struct {
	Pick        string
	StakeAmount int
	Extras      map[string]any
}

func newPredictionCreateRequest(body map[string]any) (predictionCreateRequest, error) {
	pick := stringValue(body["pick"])
	if !validPredictionPick(pick) {
		return predictionCreateRequest{}, fmt.Errorf("invalid prediction pick %q", pick)
	}
	stakeAmount, ok := requiredPredictionStakeAmount(body)
	if !ok {
		return predictionCreateRequest{}, errors.New("prediction stakeAmount must be a positive integer")
	}
	return predictionCreateRequest{
		Pick:        pick,
		StakeAmount: stakeAmount,
		Extras:      cloneMap(body),
	}, nil
}

func (r predictionCreateRequest) predictionMap(matchID string, user authUser, stakeLedgerID string) map[string]any {
	item := cloneMap(r.Extras)
	item["matchId"] = matchID
	item["userId"] = user.ID
	item["githubLogin"] = user.Login
	item["pick"] = r.Pick
	item["stakeAmount"] = r.StakeAmount
	item["currency"] = predictionCurrency
	item["stakeLedgerId"] = stakeLedgerID
	return item
}

type predictionStakeRequest struct {
	Prediction    map[string]any
	StakeLedgerID string
	StakeAmount   int
	LedgerExtras  map[string]any
}

func newPredictionStakeRequest(matchID string, user authUser, req predictionCreateRequest, stakeLedgerID string) predictionStakeRequest {
	return predictionStakeRequest{
		Prediction:    req.predictionMap(matchID, user, stakeLedgerID),
		StakeLedgerID: stakeLedgerID,
		StakeAmount:   req.StakeAmount,
		LedgerExtras: map[string]any{
			"matchId":       matchID,
			"pick":          req.Pick,
			"stakeAmount":   req.StakeAmount,
			"stakeLedgerId": stakeLedgerID,
			"description":   "월드컵 승부예측 참여",
		},
	}
}

type predictionCancelRequest struct {
	RefundLedgerID string
	StakeAmount    int
	LedgerExtras   map[string]any
}

func newPredictionCancelRequest(matchID, userID string, prediction map[string]any) (predictionCancelRequest, error) {
	record, err := predictionRecordFromMap(prediction)
	if err != nil {
		return predictionCancelRequest{}, err
	}
	refundLedgerID := predictionCancelLedgerID(matchID, userID, prediction)
	return predictionCancelRequest{
		RefundLedgerID: refundLedgerID,
		StakeAmount:    record.StakeAmount,
		LedgerExtras: map[string]any{
			"matchId":        matchID,
			"pick":           record.Pick,
			"stakeAmount":    record.StakeAmount,
			"stakeLedgerId":  record.StakeLedgerID,
			"refundLedgerId": refundLedgerID,
			"description":    "월드컵 승부예측 취소 환급",
		},
	}, nil
}

type predictionRecord struct {
	ID            string
	MatchID       string
	UserID        string
	GitHubLogin   string
	Pick          string
	StakeAmount   int
	StakeLedgerID string
	Raw           map[string]any
}

func predictionRecordFromMap(item map[string]any) (predictionRecord, error) {
	pick := stringValue(item["pick"])
	if !validPredictionPick(pick) {
		return predictionRecord{}, fmt.Errorf("invalid prediction pick %q", pick)
	}
	stake := predictionStakeAmount(item)
	if stake <= 0 {
		return predictionRecord{}, errors.New("prediction stakeAmount must be positive")
	}
	return predictionRecord{
		ID:            stringValue(item["id"]),
		MatchID:       stringValue(item["matchId"]),
		UserID:        stringValue(item["userId"]),
		GitHubLogin:   stringValue(item["githubLogin"]),
		Pick:          pick,
		StakeAmount:   stake,
		StakeLedgerID: stringValue(item["stakeLedgerId"]),
		Raw:           item,
	}, nil
}

func validPredictionPick(pick string) bool {
	return pick == "home" || pick == "draw" || pick == "away"
}
