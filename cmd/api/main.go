package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/snap-crew/tripaw-server/internal/ai"
	"github.com/snap-crew/tripaw-server/internal/auth"
	"github.com/snap-crew/tripaw-server/internal/config"
	"github.com/snap-crew/tripaw-server/internal/db"
	"github.com/snap-crew/tripaw-server/internal/image"
	"github.com/snap-crew/tripaw-server/internal/oauth"
	"github.com/snap-crew/tripaw-server/internal/pet"
	"github.com/snap-crew/tripaw-server/internal/place"
	"github.com/snap-crew/tripaw-server/internal/router"
	"github.com/snap-crew/tripaw-server/internal/terms"
	"github.com/snap-crew/tripaw-server/internal/token"
	"github.com/snap-crew/tripaw-server/internal/trip"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("서버 기동 실패", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	setupLogger(cfg)

	if err := cfg.RequireAuth(); err != nil {
		return err
	}
	if cfg.Kakao.AppID == 0 {
		slog.Warn("KAKAO_APP_ID 가 비어 있어 카카오 토큰의 출처를 확인하지 않습니다. " +
			"운영 환경에서는 반드시 설정하세요")
	}
	if cfg.GeminiKey == "" {
		slog.Warn("GEMINI_API_KEY 가 비어 있어 AI 루트 채우기가 랭킹 순서로만 동작합니다")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	appleClient, err := oauth.NewAppleClient(
		cfg.Apple.ClientID, cfg.Apple.TeamID, cfg.Apple.KeyID,
		cfg.Apple.PrivateKey, cfg.Apple.RedirectURI,
	)
	if err != nil {
		return err
	}

	tokens := token.NewManager(cfg.JWT.Secret, cfg.JWT.AccessExpiry, cfg.JWT.RefreshExpiry)

	authService := auth.NewService(
		auth.NewRepository(pool),
		tokens,
		appleClient,
		oauth.NewKakaoClient(cfg.Kakao.AppID),
	)

	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	handler := router.New(tokens,
		auth.NewHandler(authService, cfg.TestLogin),
		terms.NewHandler(terms.NewService(terms.NewRepository(pool))),
		image.NewHandler(image.NewService(image.NewRepository(pool))),
		pet.NewHandler(pet.NewService(pet.NewRepository(pool))),
		place.NewHandler(place.NewService(place.NewRepository(pool))),
		trip.NewHandler(trip.NewService(
			trip.NewRepository(pool), ai.New(cfg.GeminiKey, cfg.GeminiModel))),
	)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,

		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go cleanupExpiredTokens(ctx, authService)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("서버 시작", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("종료 신호를 받았습니다. 처리 중인 요청을 기다립니다")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	slog.Info("서버를 종료했습니다")
	return nil
}

func setupLogger(cfg *config.Config) {
	var h slog.Handler
	if cfg.IsProduction() {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}

func cleanupExpiredTokens(ctx context.Context, svc *auth.Service) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := svc.CleanupExpiredTokens(ctx)
			if err != nil {
				slog.Error("만료 토큰 정리 실패", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("만료된 리프레시 토큰 정리", "deleted", n)
			}
		}
	}
}
