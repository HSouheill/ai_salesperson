package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/billing"
	"github.com/hussein/ai-salesperson/internal/channels"
	"github.com/hussein/ai-salesperson/internal/config"
	"github.com/hussein/ai-salesperson/internal/errtrack"
	"github.com/hussein/ai-salesperson/internal/httpapi"
	"github.com/hussein/ai-salesperson/internal/inbound"
	"github.com/hussein/ai-salesperson/internal/integrations"
	"github.com/hussein/ai-salesperson/internal/jobs"
	"github.com/hussein/ai-salesperson/internal/queue"
	"github.com/hussein/ai-salesperson/internal/ratelimit"
	"github.com/hussein/ai-salesperson/internal/sales"
	"github.com/hussein/ai-salesperson/internal/scheduler"
	"github.com/hussein/ai-salesperson/internal/secrets"
	"github.com/hussein/ai-salesperson/internal/sources"
	"github.com/hussein/ai-salesperson/internal/store"
	"github.com/hussein/ai-salesperson/internal/webfetch"
)

func main() {
	if runAdmin(os.Args[1:]) {
		return
	}
	cfg := config.Load()
	errtrack.Init(cfg.SentryDSN, cfg.Env, cfg.Release)
	defer errtrack.Flush()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Tenant credentials are encrypted at rest. A real database needs a real key.
	var box *secrets.Box
	if cfg.SecretsKey != "" {
		b, err := secrets.New(cfg.SecretsKey)
		if err != nil {
			log.Fatal(err)
		}
		box = b
	} else if cfg.DatabaseURL != "" {
		log.Fatal("SECRETS_KEY is required when DATABASE_URL is set (generate one with `openssl rand -hex 32`)")
	} else {
		box = secrets.FromPassphrase(cfg.JWTSecret)
	}

	var st store.Store
	if cfg.DatabaseURL == "" {
		log.Println("WARNING: DATABASE_URL not set; using the in-memory store (data is lost on restart)")
		st = store.NewMemory()
	} else {
		pg, err := store.NewPostgres(ctx, cfg.DatabaseURL, box)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		st = pg
	}
	defer st.Close()

	provider, err := ai.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.AIProvider == "mock" {
		log.Println("WARNING: using the mock AI provider; set ANTHROPIC_API_KEY or OPENAI_API_KEY for real output")
	}
	if cfg.DevLogChannels {
		log.Println("WARNING: DEV_LOG_CHANNELS is on; unconfigured channels only log and nothing is delivered")
	}

	var q *queue.Queue
	if cfg.RedisURL != "" {
		if q, err = queue.NewRedis(ctx, cfg.RedisURL); err != nil {
			log.Fatalf("queue: %v", err)
		}
	} else {
		log.Println("WARNING: REDIS_URL not set; using the in-process job queue (jobs are lost on restart, single instance only)")
		q = queue.NewMemory(1000)
	}

	web := webfetch.New(cfg.AllowPrivateNet)
	svc := sales.New(st, provider,
		channels.Tenant{AllowPrivate: cfg.AllowPrivateNet, DevLog: cfg.DevLogChannels},
		web, sources.Factory{Contact: cfg.PublicURL}, integrations.New(cfg.AllowPrivateNet), cfg.PublicURL)
	poller := &inbound.Poller{Store: st, Handler: svc, AllowPrivate: cfg.AllowPrivateNet}
	runner := &jobs.Runner{Q: q, Sales: svc, Store: st, Poller: poller}
	runner.Register()
	q.Start(ctx, cfg.Workers)

	if cfg.SchedulerInterval > 0 {
		go (&scheduler.Scheduler{Store: st, Jobs: runner, Interval: cfg.SchedulerInterval}).Run(ctx)
		log.Printf("scheduler running every %s", cfg.SchedulerInterval)
	}

	stripe := &billing.Stripe{Key: cfg.StripeKey, WebhookSecret: cfg.StripeWebhookSecret, Prices: cfg.StripePrices}
	if !stripe.Configured() {
		log.Println("billing: STRIPE_SECRET_KEY not set; plans cannot be purchased")
	}

	rl, err := ratelimit.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("rate limiter: %v", err)
	}
	defer rl.Close()
	if cfg.AdminToken == "" {
		log.Println("WARNING: ADMIN_TOKEN not set; the operator admin API is disabled")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(cfg, st, svc, runner, q, poller, stripe, rl),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute, // AI calls are slow
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("AI Salesperson listening on %s (ai=%s)", cfg.Addr, cfg.AIProvider)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	q.Close()
}
