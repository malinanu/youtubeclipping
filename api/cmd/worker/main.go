package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/db"
	"github.com/malinanu/youtubeclipping/api/internal/events"
	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
	"github.com/malinanu/youtubeclipping/api/internal/heavy"
	"github.com/malinanu/youtubeclipping/api/internal/media"
	"github.com/malinanu/youtubeclipping/api/internal/store"
	"github.com/malinanu/youtubeclipping/api/internal/tasks"
	"github.com/malinanu/youtubeclipping/api/internal/whisper"
	"github.com/malinanu/youtubeclipping/api/internal/ytdlp"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
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

	warnRedisEviction(ctx, rdb, log)

	whisper.BundledDir = cfg.WhisperBundle
	layout := media.Layout{Root: cfg.DataDir}
	if err := layout.Ensure(layout.TmpDir()); err != nil {
		return err
	}
	// One gate shared by whisper.cpp and ffmpeg encodes: running both at once
	// on a small VM is the usual cause of OOM kills.
	heavyGate := heavy.New(cfg.HeavyJobs)
	gate := func(ctx context.Context) (func(), error) {
		return heavyGate.Acquire(ctx, func() { log.Info("waiting for another heavy job to finish") })
	}
	h := &tasks.Handlers{
		Gate:  gate,
		Cfg:   cfg,
		Store: store.New(pool),
		Bus:   events.NewBus(rdb),
		Redis: rdb,
		Enq:   &tasks.Enqueuer{C: aq},
		YtDlp: &ytdlp.Client{Bin: cfg.YtDlpBin, CookiesFile: cfg.CookiesFile, Proxy: cfg.YtDlpProxy,
			POTURL: cfg.YtDlpPOTURL, UpdateCmd: cfg.YtDlpUpdate, ExtraArgs: cfg.YtDlpExtra},
		FF:     &ffmpeg.Runner{FFmpeg: cfg.FFmpegBin, FFprobe: cfg.FFprobeBin, Threads: cfg.FFmpegThreads, Gate: gate},
		Layout: layout,
		Log:    log,
	}
	go h.PreloadModel(ctx)

	mux := asynq.NewServeMux()
	h.Register(mux)

	// One asynq server per queue gives each job class its own concurrency cap,
	// so a long transcription never blocks renders and vice versa.
	queues := []struct {
		name string
		conc int
	}{
		{tasks.QueueIngest, cfg.IngestConcurrency},
		{tasks.QueueTranscribe, cfg.TranscribeConcurrency},
		{tasks.QueueRender, cfg.RenderConcurrency},
		{tasks.QueueDefault, 2},
	}
	var servers []*asynq.Server
	for _, q := range queues {
		srv := asynq.NewServer(redisOpt, asynq.Config{
			Concurrency:     max(q.conc, 1),
			Queues:          map[string]int{q.name: 1},
			ShutdownTimeout: 20e9,
			Logger:          &asynqLogger{log.With("queue", q.name)},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
				log.Error("task failed", "type", t.Type(), "err", err)
			}),
		})
		if err := srv.Start(mux); err != nil {
			return err
		}
		servers = append(servers, srv)
	}

	sched := asynq.NewScheduler(redisOpt, nil)
	if _, err := sched.Register("17 3 * * *", asynq.NewTask(tasks.TypeCleanup, nil), asynq.Queue(tasks.QueueDefault)); err != nil {
		return err
	}
	if _, err := sched.Register("43 4 * * *", asynq.NewTask(tasks.TypeYtDlpUp, nil), asynq.Queue(tasks.QueueDefault)); err != nil {
		return err
	}
	if err := sched.Start(); err != nil {
		return err
	}
	log.Info("worker started", "ytdlp", h.YtDlp.Version(ctx))

	<-ctx.Done()
	sched.Shutdown()
	for _, s := range servers {
		s.Shutdown()
	}
	return nil
}

// warnRedisEviction flags shared Redis instances whose eviction policy could
// silently drop queued jobs. CONFIG may be disabled on managed Redis; ignore errors.
func warnRedisEviction(ctx context.Context, rdb *redis.Client, log *slog.Logger) {
	res, err := rdb.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		return
	}
	if p, ok := res["maxmemory-policy"]; ok && p != "noeviction" {
		log.Warn("redis maxmemory-policy is not noeviction; queued jobs may be evicted under memory pressure",
			"policy", p, "fix", "redis-cli CONFIG SET maxmemory-policy noeviction (and persist it in redis.conf)")
	}
}

type asynqLogger struct{ l *slog.Logger }

func (a *asynqLogger) Debug(args ...any) {}
func (a *asynqLogger) Info(args ...any)  { a.l.Info("asynq", "msg", args) }
func (a *asynqLogger) Warn(args ...any)  { a.l.Warn("asynq", "msg", args) }
func (a *asynqLogger) Error(args ...any) { a.l.Error("asynq", "msg", args) }
func (a *asynqLogger) Fatal(args ...any) { a.l.Error("asynq fatal", "msg", args); os.Exit(1) }
