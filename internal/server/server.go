package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type server struct {
	store         *postgresStore
	football      *footballClient
	footballCache *redisFootballCache
	fcm           *fcmClient
	githubAuth    *githubOAuthClient
}

func Run(ctx context.Context) error {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("load .env", "error", err)
	}
	configureLogging()

	store, err := openPostgresStore(ctx, env("DATABASE_URL", "postgres://daema:daema@localhost:5432/daema_coin?sslmode=disable"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.close()

	footballCache, err := openRedisFootballCache(ctx)
	if err != nil {
		return fmt.Errorf("open football cache: %w", err)
	}
	defer footballCache.close()

	s := &server{
		store:         store,
		football:      newFootballClientFromEnv(),
		footballCache: footballCache,
		fcm:           newFCMClientFromEnv(),
		githubAuth:    newGitHubOAuthClientFromEnv(),
	}
	if err := s.ensureBootstrapAccounts(ctx); err != nil {
		return fmt.Errorf("bootstrap internal accounts: %w", err)
	}
	if envBool("API_FOOTBALL_CACHE_WORKER_ENABLED", true) {
		go s.runFootballCacheWorker(ctx, envDuration("API_FOOTBALL_CACHE_REFRESH_INTERVAL", time.Minute))
	}
	if envBool("PREDICTION_SETTLEMENT_WORKER_ENABLED", true) {
		go s.runPredictionSettlementWorker(ctx, envDuration("PREDICTION_SETTLEMENT_INTERVAL", time.Minute))
	}
	if envBool("POINT_CONVERSION_WORKER_ENABLED", true) {
		go s.runPointConversionWorker(ctx, envDuration("POINT_CONVERSION_INTERVAL", time.Minute))
	}

	port := env("PORT", "8080")
	addr := ":" + port

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: envDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	slog.Info("starting daema coin server", "addr", addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), envDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second))
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	}
}
