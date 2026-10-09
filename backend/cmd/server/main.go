// Command server runs any combination of roles against one Postgres:
//
//	api        HTTP ingest edge + read APIs + admin actions
//	workers    inbox processing (scales horizontally, SKIP LOCKED)
//	dispatch   queued → dispatched and outbox delivery to the robot
//	poller     upstream API poller (leader-elected, one active)
//	scheduler  releases scheduled orders (leader-elected, one active)
//
// Example: server --roles=api        (stateless HTTP replicas)
//
//	server --roles=workers,dispatch,poller,scheduler
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // meal-slot time zones work in a scratch image

	"github.com/jj0629/lab37-homework/backend/internal/config"
	"github.com/jj0629/lab37-homework/backend/internal/dispatch"
	"github.com/jj0629/lab37-homework/backend/internal/healthcheck"
	"github.com/jj0629/lab37-homework/backend/internal/httpapi"
	"github.com/jj0629/lab37-homework/backend/internal/ingest"
	"github.com/jj0629/lab37-homework/backend/internal/leader"
	"github.com/jj0629/lab37-homework/backend/internal/poller"
	"github.com/jj0629/lab37-homework/backend/internal/scheduler"
	"github.com/jj0629/lab37-homework/backend/internal/store"
)

func main() {
	healthcheck.RunIfRequested()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	roles := flag.String("roles", "", "comma-separated roles (overrides ROLES env)")
	flag.Parse()
	if *roles != "" {
		os.Setenv("ROLES", *roles)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.Default().With("instance", cfg.InstanceID))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL, 10+cfg.WorkerConcurrency*2)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	var active []string
	for r := range cfg.Roles {
		active = append(active, r)
	}
	slog.Info("starting", "roles", strings.Join(active, ","))

	var wg sync.WaitGroup
	run := func(name string, fn func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(ctx)
			slog.Info("stopped", "component", name)
		}()
	}

	if cfg.Has("workers") {
		w := &ingest.Worker{DB: db, Cfg: cfg}
		run("workers", func(ctx context.Context) { w.Run(ctx, cfg.WorkerConcurrency) })
	}
	if cfg.Has("dispatch") {
		d := &dispatch.Dispatcher{DB: db}
		s := &dispatch.Sender{DB: db, RobotURL: cfg.RobotURL, MaxAttempts: cfg.MaxAttempts}
		run("dispatcher", d.Run)
		run("outbox-sender", s.Run)
	}
	if cfg.Has("poller") {
		p := &poller.Poller{DB: db, Cfg: cfg, Instance: cfg.InstanceID}
		run("poller", func(ctx context.Context) {
			leader.Run(ctx, db, "poller", leader.KeyPoller, cfg.InstanceID, p.Run)
		})
	}
	if cfg.Has("scheduler") {
		s := &scheduler.Scheduler{DB: db, LeadMinutes: cfg.LeadMinutes}
		run("scheduler", func(ctx context.Context) {
			leader.Run(ctx, db, "scheduler", leader.KeyScheduler, cfg.InstanceID, s.Run)
		})
	}

	// Every process serves HTTP: the full API with the api role, otherwise
	// just health checks for the orchestrator.
	var h http.Handler
	if cfg.Has("api") {
		h = (&httpapi.Server{DB: db, Cfg: cfg}).Handler()
	} else {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }) //nolint:errcheck
		h = mux
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	wg.Wait()
}
