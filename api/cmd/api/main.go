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

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/malinanu/youtubeclipping/api/internal/auth"
	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/db"
	"github.com/malinanu/youtubeclipping/api/internal/events"
	"github.com/malinanu/youtubeclipping/api/internal/httpapi"
	"github.com/malinanu/youtubeclipping/api/internal/media"
	"github.com/malinanu/youtubeclipping/api/internal/store"
	"github.com/malinanu/youtubeclipping/api/internal/tasks"
	"github.com/malinanu/youtubeclipping/api/internal/ytdlp"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateAPI(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(pool); err != nil {
		return err
	}

	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB}
	aq := asynq.NewClient(redisOpt)
	defer aq.Close()
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	defer rdb.Close()

	layout := media.Layout{Root: cfg.DataDir}
	if err := layout.Ensure(layout.TmpDir()); err != nil {
		return err
	}

	srv := &httpapi.Server{
		Cfg:   cfg,
		Store: store.New(pool),
		Auth:  auth.New(cfg.AdminEmail, cfg.AdminPasswordHash, cfg.SessionSecret, cfg.SessionTTL, cfg.CookieSecure),
		Enq:   &tasks.Enqueuer{C: aq},
		Bus:   events.NewBus(rdb),
		Redis: rdb,
		YtDlp: &ytdlp.Client{Bin: cfg.YtDlpBin, CookiesFile: cfg.CookiesFile, Proxy: cfg.YtDlpProxy,
			POTURL: cfg.YtDlpPOTURL, UpdateCmd: cfg.YtDlpUpdate, ExtraArgs: cfg.YtDlpExtra},
		Layout: layout,
		Log:    log,
	}
	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shutdown)
}
