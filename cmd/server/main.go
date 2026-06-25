package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"daema-coin-server/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
