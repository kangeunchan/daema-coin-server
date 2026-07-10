package server

import (
	"context"
	"errors"
	"strconv"
	"time"
)

var (
	errPredictionInvalidInput     = errors.New("prediction input is invalid")
	errPredictionClosed           = errors.New("prediction is closed")
	errPredictionCancelClosed     = errors.New("prediction cancel is closed")
	errPredictionNotFound         = errors.New("prediction not found")
	errPredictionCancelInvalid    = errors.New("prediction cancel is invalid")
	errPredictionInsufficientFund = errors.New("prediction balance is insufficient")
)

type predictionService struct {
	matches predictionMatchReader
	wallet  predictionWalletReader
	store   predictionStore
	now     func() time.Time
}

func (s *server) predictions() predictionService {
	return predictionService{matches: s, wallet: s, store: s.store, now: time.Now}
}

type predictionMatchReader interface {
	worldcupMatchByID(ctx context.Context, id string) (worldcupMatch, bool, error)
}

type predictionWalletReader interface {
	walletBalance(ctx context.Context, userID, currency string) (int, error)
}

type predictionStore interface {
	get(ctx context.Context, resource, id string) (map[string]any, bool, error)
	listFiltered(ctx context.Context, resource string, filters []resourceFilter, limit int) ([]map[string]any, error)
	createWorldcupPredictionWithStake(ctx context.Context, user authUser, predictionID string, req predictionStakeRequest) (map[string]any, bool, error)
	cancelWorldcupPredictionWithRefund(ctx context.Context, user authUser, predictionID string, req predictionCancelRequest) (map[string]any, bool, error)
	createPredictionSettlementWithLedger(ctx context.Context, settlementID string, settlement map[string]any, ledgerEntries []map[string]any) (map[string]any, bool, error)
}

type predictionCreateResult struct {
	Item        map[string]any
	Created     bool
	StakeAmount int
}

func (svc predictionService) Create(ctx context.Context, user authUser, matchID string, body map[string]any) (predictionCreateResult, error) {
	now := svc.currentTime()
	req, err := newPredictionCreateRequest(body)
	if err != nil {
		return predictionCreateResult{}, errPredictionInvalidInput
	}
	if !worldcupPredictionOpenAt(now) {
		return predictionCreateResult{StakeAmount: req.StakeAmount}, errPredictionClosed
	}
	match, matchStatusKnown, err := svc.matches.worldcupMatchByID(ctx, matchID)
	if err != nil {
		return predictionCreateResult{}, err
	}
	if matchStatusKnown && match.Status != "scheduled" {
		return predictionCreateResult{StakeAmount: req.StakeAmount}, errPredictionClosed
	}
	pointBalance := teacherInfiniteWalletBalance
	if !authUserHasRole(user, roleTeacher) {
		var err error
		pointBalance, err = svc.wallet.walletBalance(ctx, user.ID, predictionCurrency)
		if err != nil {
			return predictionCreateResult{StakeAmount: req.StakeAmount}, err
		}
	}
	if pointBalance < req.StakeAmount {
		return predictionCreateResult{StakeAmount: req.StakeAmount}, errPredictionInsufficientFund
	}
	stakeLedgerID := ledgerID("prediction-stake", matchID, user.ID, strconv.FormatInt(now.UTC().UnixNano(), 10))
	id := predictionID(matchID, user.ID)
	stakeReq := newPredictionStakeRequest(matchID, user, req, stakeLedgerID)
	item, created, err := svc.store.createWorldcupPredictionWithStake(ctx, user, id, stakeReq)
	if err != nil {
		return predictionCreateResult{StakeAmount: req.StakeAmount}, err
	}
	return predictionCreateResult{Item: item, Created: created, StakeAmount: req.StakeAmount}, nil
}

type predictionCancelResult struct {
	Cancelled    bool
	RefundAmount int
}

func (svc predictionService) Cancel(ctx context.Context, user authUser, matchID string) (predictionCancelResult, error) {
	if !worldcupPredictionOpenAt(svc.currentTime()) {
		return predictionCancelResult{}, errPredictionCancelClosed
	}
	match, matchStatusKnown, err := svc.matches.worldcupMatchByID(ctx, matchID)
	if err != nil {
		return predictionCancelResult{}, err
	}
	if matchStatusKnown && match.Status != "scheduled" {
		return predictionCancelResult{}, errPredictionCancelClosed
	}
	id := predictionID(matchID, user.ID)
	existing, found, err := svc.store.get(ctx, resourceWorldcupPredictions, id)
	if err != nil {
		return predictionCancelResult{}, err
	}
	if !found {
		return predictionCancelResult{}, errPredictionNotFound
	}
	cancelReq, err := newPredictionCancelRequest(matchID, user.ID, existing)
	if err != nil {
		return predictionCancelResult{}, errPredictionCancelInvalid
	}
	_, cancelled, err := svc.store.cancelWorldcupPredictionWithRefund(ctx, user, id, cancelReq)
	if err != nil {
		return predictionCancelResult{RefundAmount: cancelReq.StakeAmount}, err
	}
	if !cancelled {
		return predictionCancelResult{RefundAmount: cancelReq.StakeAmount}, errPredictionNotFound
	}
	return predictionCancelResult{Cancelled: true, RefundAmount: cancelReq.StakeAmount}, nil
}

func (svc predictionService) currentTime() time.Time {
	if svc.now != nil {
		return svc.now()
	}
	return time.Now()
}

func (svc predictionService) Settle(ctx context.Context, matchID, winningPick, source, note string) (predictionSettlementResult, error) {
	settlementID := predictionSettlementID(matchID)
	if existing, ok, err := svc.store.get(ctx, resourcePredictionSettlements, settlementID); err != nil {
		return predictionSettlementResult{}, err
	} else if ok {
		return predictionSettlementResult{Settlement: existing, Created: false}, errPredictionAlreadySettled
	}

	predictions, err := svc.store.listFiltered(ctx, resourceWorldcupPredictions, []resourceFilter{{Field: "matchId", Value: matchID}}, 10000)
	if err != nil {
		return predictionSettlementResult{}, err
	}

	settlement, ledgerEntries, err := settlePredictions(matchID, winningPick, predictions)
	if err != nil {
		return predictionSettlementResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	settlement["id"] = settlementID
	settlement["settledAt"] = now
	settlement["source"] = source
	if note != "" {
		settlement["note"] = note
	}

	item, created, err := svc.store.createPredictionSettlementWithLedger(ctx, settlementID, settlement, ledgerEntries)
	if err != nil {
		return predictionSettlementResult{}, err
	}
	if !created {
		return predictionSettlementResult{Settlement: item, Created: false}, errPredictionAlreadySettled
	}
	return predictionSettlementResult{Settlement: item, LedgerEntries: ledgerEntries, Created: true}, nil
}
