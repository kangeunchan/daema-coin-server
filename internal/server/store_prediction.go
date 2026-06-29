package server

import (
	"context"
	"database/sql"
)

func (s *postgresStore) createWorldcupPredictionWithStake(ctx context.Context, user authUser, predictionID string, req predictionStakeRequest) (map[string]any, bool, error) {
	var item map[string]any
	var created bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		var err error
		item, created, err = s.createResourceTx(ctx, tx, resourceWorldcupPredictions, predictionID, req.Prediction)
		if err != nil {
			return err
		}
		if !created {
			existing, _, getErr := s.getResourceTx(ctx, tx, resourceWorldcupPredictions, predictionID, true)
			if getErr != nil {
				return getErr
			}
			item = existing
			return nil
		}

		ledgerCreated, err := s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(user, req.StakeLedgerID, "worldcup-prediction-stake", "expense", predictionCurrency, req.StakeAmount, req.LedgerExtras))
		if err != nil {
			return err
		}
		if !ledgerCreated {
			return errLedgerIdempotencyConflict
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return item, created, nil
}

func (s *postgresStore) cancelWorldcupPredictionWithRefund(ctx context.Context, user authUser, predictionID string, req predictionCancelRequest) (map[string]any, bool, error) {
	var existing map[string]any
	var found bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		var err error
		existing, found, err = s.deleteReturningResourceTx(ctx, tx, resourceWorldcupPredictions, predictionID)
		if err != nil || !found {
			return err
		}
		ledgerCreated, err := s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(user, req.RefundLedgerID, "worldcup-prediction-cancel", "income", predictionCurrency, req.StakeAmount, req.LedgerExtras))
		if err != nil {
			return err
		}
		if !ledgerCreated {
			return errLedgerIdempotencyConflict
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return existing, found, nil
}

func (s *postgresStore) createPredictionSettlementWithLedger(ctx context.Context, settlementID string, settlement map[string]any, ledgerEntries []map[string]any) (map[string]any, bool, error) {
	var item map[string]any
	var created bool
	err := s.withSerializableTx(ctx, func(tx *sql.Tx) error {
		var err error
		item, created, err = s.createResourceTx(ctx, tx, resourcePredictionSettlements, settlementID, settlement)
		if err != nil {
			return err
		}
		if !created {
			existing, _, getErr := s.getResourceTx(ctx, tx, resourcePredictionSettlements, settlementID, true)
			if getErr != nil {
				return getErr
			}
			item = existing
			return nil
		}

		for _, ledger := range ledgerEntries {
			user := authUser{ID: stringValue(ledger["userId"]), Login: stringValue(ledger["githubLogin"])}
			if user.ID == "" {
				continue
			}
			if _, err := s.createLedgerAndAdjustWalletRequestTx(ctx, tx, newWalletLedgerRequest(user, stringValue(ledger["id"]), stringValue(ledger["type"]), stringValue(ledger["direction"]), predictionCurrency, amountValue(ledger), ledger)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return item, created, nil
}
