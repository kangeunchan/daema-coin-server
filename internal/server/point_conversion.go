package server

import (
	"context"
	"log/slog"
	"time"
)

const pointConversionJobID = "point-to-dmc-conversion-2026-07-10"

func (s *server) runPointConversionWorker(ctx context.Context, interval time.Duration) {
	slog.Info("starting point conversion worker", "interval", interval.String(), "conversion_at", pointConversionAtKST.Format(time.RFC3339))
	s.runPointConversionCycle(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPointConversionCycle(ctx)
		}
	}
}

func (s *server) runPointConversionCycle(ctx context.Context) {
	now := time.Now()
	if !pointConversionDueAt(now) {
		return
	}
	summary, err := s.store.convertPointBalancesToDMC(ctx, now)
	if err != nil {
		slog.Error("point conversion failed", "error", err)
		return
	}
	if boolValue(summary["alreadyCompleted"], false) {
		return
	}
	if stringValue(summary["status"]) == "completed" {
		slog.Info("point conversion completed",
			"converted_customer_count", summary["convertedCustomerCount"],
			"converted_amount", summary["convertedAmount"],
		)
	}
}
